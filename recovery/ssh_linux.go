//go:build linux && (amd64 || arm64)

package recovery

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func sshClock() (string, int64, error) {
	var clock unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &clock); err != nil {
		return "", 0, err
	}
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", 0, err
	}
	boot := strings.TrimSpace(string(b))
	if len(boot) != 36 {
		return "", 0, fmt.Errorf("invalid Linux boot identity")
	}
	return boot, clock.Nano(), nil
}

func sshTicksNow() (uint64, error) {
	// AT_CLKTCK comes from the kernel; do not assume USER_HZ or confuse
	// CLOCK_BOOTTIME nanoseconds with /proc process-start ticks.
	b, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 0, err
	}
	var frequency uint64
	for i := 0; i+16 <= len(b); i += 16 {
		if binary.LittleEndian.Uint64(b[i:i+8]) == 17 {
			frequency = binary.LittleEndian.Uint64(b[i+8 : i+16])
			break
		}
	}
	if frequency < 1 || frequency > 1000000 {
		return 0, fmt.Errorf("Linux process clock frequency unavailable")
	}
	_, ns, err := sshClock()
	if err != nil || ns < 0 {
		return 0, fmt.Errorf("Linux process clock unavailable")
	}
	// Divide before multiplying, keeping the remainder exact and bounded.
	return uint64(ns/int64(time.Second))*frequency + uint64(ns%int64(time.Second))*frequency/uint64(time.Second), nil
}

func sshProcess(pid int, executable string) (ServiceProcess, error) {
	start, _, err := processStart(pid)
	if err != nil {
		return ServiceProcess{}, err
	}
	path, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil || path != executable {
		return ServiceProcess{}, fmt.Errorf("managed SSH process executable mismatch")
	}
	boot, _, err := sshClock()
	return ServiceProcess{PID: pid, Start: start, Boot: boot}, err
}

func verifySSHProcess(expected ServiceProcess, executable string) error {
	got, err := sshProcess(expected.PID, executable)
	if err != nil || got != expected {
		return fmt.Errorf("managed SSH process stopped, restarted or changed identity")
	}
	return nil
}

func verifySSHGuard(expected SSHGuardIdentity) error {
	if err := verifySSHProcess(expected.Process, expected.Executable); err != nil {
		return err
	}
	f, _, err := sshFile(expected.Executable, 64<<20)
	if err != nil || !matches(f, expected.Binary, true) {
		return fmt.Errorf("SSH supervisor executable changed")
	}
	return nil
}

func sshSignal(expected ServiceProcess, executable string, signal unix.Signal) error {
	fd, err := unix.PidfdOpen(expected.PID, 0)
	if err != nil {
		return fmt.Errorf("SSH guard requires Linux pidfd support: %w", err)
	}
	defer unix.Close(fd)
	if err = verifySSHProcess(expected, executable); err != nil {
		return err
	}
	return unix.PidfdSendSignal(fd, signal, nil, 0)
}

func sshReload(expected ServiceProcess, executable string) error {
	return sshSignal(expected, executable, unix.SIGHUP)
}

func validateSSHConfig(ctx context.Context, s SSHService, path string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, s.Binary, "-t", "-f", path)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("managed SSH syntax/key validation failed: %w", err)
	}
	return nil
}

func sshSocketOwned(pid, port int, state string) (bool, error) {
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return false, err
	}
	if len(entries) > 1024 {
		return false, fmt.Errorf("SSH descriptor evidence exceeds bound")
	}
	inodes := map[string]bool{}
	for _, en := range entries {
		link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, en.Name()))
		if err == nil && strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	for _, table := range []string{"tcp", "tcp6"} {
		f, err := os.Open(fmt.Sprintf("/proc/%d/net/%s", pid, table))
		if err != nil {
			return false, err
		}
		data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if err != nil || len(data) > 1<<20 {
			return false, fmt.Errorf("SSH socket evidence unavailable or exceeds bound")
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != state || !inodes[fields[9]] {
				continue
			}
			_, p, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			value, err := strconv.ParseUint(p, 16, 16)
			if err == nil && int(value) == port {
				return true, nil
			}
		}
	}
	return false, nil
}

func sshPortReady(ctx context.Context, master ServiceProcess, s SSHService, port int) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	for {
		if err := verifySSHProcess(master, s.Binary); err != nil {
			return err
		}
		owned, err := sshSocketOwned(master.PID, port, "0A")
		if err != nil {
			return err
		}
		if owned {
			dialer := net.Dialer{Timeout: 500 * time.Millisecond}
			conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err == nil {
				_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				var banner [8]byte
				_, err = io.ReadFull(conn, banner[:])
				conn.Close()
				if err == nil && string(banner[:]) == "SSH-2.0-" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("managed SSH listener did not become ready: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func newSSHConnectionProof(p SSHPlan, afterTicks uint64) ([]ServiceProcess, error) {
	if err := verifySSHProcess(p.Master, p.Config.Binary); err != nil {
		return nil, err
	}
	pid := os.Getppid()
	var proof []ServiceProcess
	transport := false
	for depth := 0; depth < 32 && pid > 1; depth++ {
		if pid == p.Master.PID {
			if len(proof) == 0 || !transport {
				return nil, fmt.Errorf("confirmation requires a newly authenticated SSH transport to the proposed port")
			}
			return proof, nil
		}
		start, parent, err := processStart(pid)
		if err != nil {
			return nil, err
		}
		path, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			return nil, err
		}
		if path == p.Config.SessionBinary {
			ticks, err := strconv.ParseUint(start, 10, 64)
			if err != nil || ticks <= afterTicks {
				return nil, fmt.Errorf("existing SSH transport cannot confirm a new configuration; reconnect without multiplexing")
			}
			owned, err := sshSocketOwned(pid, p.NewPort, "01")
			if err != nil {
				return nil, err
			}
			transport = transport || owned
			proof = append(proof, ServiceProcess{PID: pid, Start: start, Boot: p.Master.Boot})
		}
		pid = parent
	}
	return nil, fmt.Errorf("confirmation process does not descend from the managed SSH listener")
}
