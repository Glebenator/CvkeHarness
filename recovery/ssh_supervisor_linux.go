//go:build linux && (amd64 || arm64)

package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// SuperviseSSH owns one dedicated foreground sshd. An init manager must restart
// this supervisor and start it on boot. Its startup path reconciles a pending
// operation before launching the listener; it never relies on an SSH session,
// an in-memory timer from a prior process, or a provider to do the restoration.
func (e *Engine) SuperviseSSH(ctx context.Context, s SSHService) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("managed SSH supervisor requires root")
	}
	if err := s.validate(); err != nil {
		return err
	}
	if _, _, err := sshClock(); err != nil {
		return err
	}
	dir, err := privateDirectory(filepath.Join(e.assets, "ssh-guard-"+s.Name))
	if err != nil {
		return err
	}
	defer dir.Close()
	guardUnlock, err := lockDirectory(dir)
	if err != nil {
		return fmt.Errorf("another SSH supervisor owns this instance: %w", err)
	}
	defer guardUnlock()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	self, err := sshProcess(os.Getpid(), executable)
	if err != nil {
		return err
	}
	binary, _, err := sshFile(executable, 64<<20)
	if err != nil {
		return err
	}
	lease := SSHLease{Config: s, Guard: SSHGuardIdentity{Process: self, Executable: executable, Binary: binary}}
	unlock, err := e.locked()
	if err != nil {
		return err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	_, pendingRaw, err := e.store.RecoveryGuard(ctx, e.target, s.Name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var restoring *Operation
	if len(pendingRaw) != 0 {
		var ref SSHPending
		if err = json.Unmarshal(pendingRaw, &ref); err != nil {
			return fmt.Errorf("invalid persistent SSH deadline: %w", err)
		}
		op, err := e.Inspect(ctx, ref.ID)
		if err != nil {
			return err
		}
		if op.Digest != ref.Digest || op.Plan.SSH == nil || op.Plan.SSH.Config != s {
			return fmt.Errorf("pending SSH plan does not match this supervisor")
		}
		if op.Status == Committed || op.Status == Recovered {
			if err = e.store.ClearRecoveryGuard(ctx, e.target, s.Name, pendingRaw); err != nil {
				return err
			}
			pendingRaw = nil
		} else {
			if op.Status == RecoveryFailed {
				return fmt.Errorf("prior SSH restoration failed; resolve the recorded conflict and run recovery before restarting this supervisor")
			}
			op, err = e.rollbackSSH(ctx, op, nil)
			if err != nil {
				return err
			}
			restoring = &op
		}
	}
	if _, err = sshInputs(s); err != nil {
		return err
	}
	_, config, err := sshFile(s.ConfigPath, 64<<10)
	if err != nil {
		return err
	}
	port, err := parseManagedSSHConfig(s, config)
	if err != nil {
		return err
	}
	if err = validateSSHConfig(ctx, s, s.ConfigPath); err != nil {
		return err
	}
	// Linux's parent-death signal is bound to the creating thread. Pin this
	// goroutine for the lifetime of the child instead of assuming Go preserves
	// that thread while an arbitrary goroutine migrates between OS threads.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	command := exec.Command(s.Binary, "-D", "-e", "-f", s.ConfigPath)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if err = command.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			_ = command.Process.Kill()
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
			}
		}
	}()
	lease.Master, err = sshProcess(command.Process.Pid, s.Binary)
	if err != nil {
		return err
	}
	if err = sshPortReady(ctx, lease.Master, s, port); err != nil {
		return err
	}
	if restoring != nil {
		if port != restoring.Plan.SSH.OldPort {
			return fmt.Errorf("restored SSH listener port differs from original")
		}
		if err = e.save(ctx, restoring, Recovered, ""); err != nil {
			return err
		}
		if err = e.store.ClearRecoveryGuard(ctx, e.target, s.Name, pendingRaw); err != nil {
			return err
		}
	}
	heartbeat := func() error {
		_, now, err := sshClock()
		if err != nil {
			return err
		}
		lease.HeartbeatNS = now
		data, err := json.Marshal(lease)
		if err != nil {
			return err
		}
		return e.store.PutRecoveryGuard(ctx, e.target, s.Name, data)
	}
	if err = heartbeat(); err != nil {
		return err
	}
	unlock()
	locked = false
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			reaped = true
			return fmt.Errorf("managed SSH listener exited; init must restart supervisor for pending recovery: %v", err)
		case <-ticker.C:
			// Heartbeats only update the lease column. Take the executor
			// lock when a deadline actually needs reconciliation, avoiding
			// contention with ordinary file operations while SSH is idle.
			_, raw, err := e.store.RecoveryGuard(ctx, e.target, s.Name)
			if err != nil {
				return err
			}
			if len(raw) != 0 {
				var ref SSHPending
				if err = json.Unmarshal(raw, &ref); err != nil {
					return err
				}
				boot, now, err := sshClock()
				if err != nil {
					return err
				}
				if boot != ref.Boot || now >= ref.DeadlineNS {
					unlock, err := e.locked()
					if err == nil {
						err = e.sshGuardTick(ctx, &lease)
						unlock()
						if err != nil {
							return err
						}
					}
				}
			}
			if err = heartbeat(); err != nil {
				return err
			}
		}
	}
}

func (e *Engine) sshGuardTick(ctx context.Context, lease *SSHLease) error {
	_, raw, err := e.store.RecoveryGuard(ctx, e.target, lease.Config.Name)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var ref SSHPending
	if err = json.Unmarshal(raw, &ref); err != nil {
		return err
	}
	op, err := e.Inspect(ctx, ref.ID)
	if err != nil {
		return err
	}
	if op.Digest != ref.Digest || op.Plan.SSH == nil || op.Plan.SSH.Config != lease.Config {
		return fmt.Errorf("SSH watchdog pending binding changed")
	}
	if op.Status == Committed || op.Status == Recovered {
		return e.store.ClearRecoveryGuard(ctx, e.target, lease.Config.Name, raw)
	}
	// Failed automatic restoration is sticky. Do not keep repairing or
	// overwriting a conflicting writer every time the watchdog wakes up.
	if op.Status == RecoveryFailed {
		return nil
	}
	boot, now, err := sshClock()
	if err != nil {
		return err
	}
	if boot != ref.Boot || now >= ref.DeadlineNS {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_, err := e.rollbackSSH(recoveryCtx, op, lease)
		if err != nil {
			// The journal records the failure. Keep the existing listener and
			// watchdog alive for explicit operator recovery when possible.
			return nil
		}
	}
	return nil
}
