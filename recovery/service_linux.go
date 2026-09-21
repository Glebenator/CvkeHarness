//go:build linux

package recovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func processStart(pid int) (string, int, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", 0, err
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", 0, fmt.Errorf("invalid process identity")
	}
	fields := strings.Fields(string(b)[end+1:])
	if len(fields) < 20 || fields[0] == "Z" {
		return "", 0, fmt.Errorf("process is unavailable")
	}
	parent, err := strconv.Atoi(fields[1])
	return fields[19], parent, err // /proc stat field 22, measured since boot
}

func currentServiceMaster(s NGINXService) (ServiceProcess, error) {
	d, err := parentFor(s.PIDFile)
	if err != nil {
		return ServiceProcess{}, err
	}
	defer d.Close()
	f, data, err := readRegular(d, filepath.Base(s.PIDFile), 32)
	if err != nil || !f.Exists || f.UID != uint32(os.Geteuid()) || f.Mode&0022 != 0 {
		return ServiceProcess{}, fmt.Errorf("service PID file is missing, unsafe or unreadable: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return ServiceProcess{}, fmt.Errorf("invalid service master PID")
	}
	start, _, err := processStart(pid)
	if err != nil {
		return ServiceProcess{}, err
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil || exe != s.Binary {
		return ServiceProcess{}, fmt.Errorf("PID does not run the configured executable")
	}
	command, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ServiceProcess{}, err
	}
	want := "nginx: master process " + s.Binary + " -p " + s.Prefix + " -c " + s.ConfigPath
	if strings.TrimRight(string(command), "\x00 ") != want {
		return ServiceProcess{}, fmt.Errorf("NGINX master must be started with exactly: binary -p prefix -c config_path")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ServiceProcess{}, err
	}
	return ServiceProcess{PID: pid, Start: start, Boot: strings.TrimSpace(string(boot))}, nil
}

func serviceWorkers(master ServiceProcess) (map[int]string, error) {
	start, _, err := processStart(master.PID)
	if err != nil || start != master.Start {
		return nil, fmt.Errorf("service master no longer matches")
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", master.PID, master.PID))
	if err != nil {
		return nil, err
	}
	workers := map[int]string{}
	for _, id := range strings.Fields(string(b)) {
		pid, err := strconv.Atoi(id)
		if err != nil {
			return nil, err
		}
		start, parent, err := processStart(pid)
		if err != nil || parent != master.PID {
			continue
		}
		cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err == nil && strings.TrimRight(string(cmd), "\x00 ") == "nginx: worker process" {
			workers[pid] = start
		}
	}
	return workers, nil
}

func signalService(p ServicePlan) error {
	// pidfd binds the signal to this process, avoiding PID-reuse races. Older
	// kernels that lack pidfd support fail closed rather than falling back.
	fd, err := unix.PidfdOpen(p.Master.PID, 0)
	if err != nil {
		return fmt.Errorf("service adapter requires Linux pidfd support: %w", err)
	}
	defer unix.Close(fd)
	if err = verifyServiceIdentity(p); err != nil {
		return err
	}
	return unix.PidfdSendSignal(fd, unix.SIGHUP, nil, 0)
}

func checkServiceCapability(p ServicePlan) error {
	fd, err := unix.PidfdOpen(p.Master.PID, 0)
	if err != nil {
		return fmt.Errorf("service adapter requires Linux pidfd support: %w", err)
	}
	defer unix.Close(fd)
	if err = verifyServiceIdentity(p); err != nil {
		return err
	}
	// Signal 0 performs only kernel existence/permission checks.
	if err = unix.PidfdSendSignal(fd, 0, nil, 0); err != nil {
		return fmt.Errorf("service pidfd signaling is unavailable: %w", err)
	}
	return nil
}
