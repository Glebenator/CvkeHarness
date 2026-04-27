package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const daemonServiceName = "cvkeharness.service"

type daemonServiceOptions struct {
	System       bool
	User         string
	Interval     time.Duration
	EnableLinger bool
}

type daemonCommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type realDaemonCommandRunner struct{}

func (realDaemonCommandRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (realDaemonCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

var (
	daemonRuntimeGOOS                     = runtime.GOOS
	daemonRunner      daemonCommandRunner = realDaemonCommandRunner{}
	daemonExecutable                      = os.Executable
	daemonUserHomeDir                     = os.UserHomeDir
	daemonCurrentUser                     = user.Current
	daemonLookupUser                      = user.Lookup
)

func installDaemonService(ctx context.Context, opts daemonServiceOptions) (string, error) {
	mgr, err := newDaemonServiceManager(opts)
	if err != nil {
		return "", err
	}
	unit, err := mgr.renderUnit()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(mgr.unitPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(mgr.unitPath, []byte(unit), 0644); err != nil {
		return "", err
	}
	if _, err := mgr.systemctl(ctx, "daemon-reload"); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Installed %s\n", mgr.unitPath)
	if opts.System {
		fmt.Fprintf(&b, "Enable it with: sudo systemctl enable %s\n", daemonServiceName)
		fmt.Fprintf(&b, "Start it with: sudo systemctl start %s\n", daemonServiceName)
	} else {
		fmt.Fprintf(&b, "Enable it with: systemctl --user enable %s\n", daemonServiceName)
		fmt.Fprintf(&b, "Start it with: systemctl --user start %s\n", daemonServiceName)
		if opts.EnableLinger {
			if _, err := daemonRunner.Run(ctx, "sudo", "loginctl", "enable-linger", mgr.userName); err != nil {
				return b.String(), err
			}
			fmt.Fprintf(&b, "Enabled login linger for %s\n", mgr.userName)
		} else {
			b.WriteString(mgr.lingerAdvice(ctx))
		}
	}
	return b.String(), nil
}

func runDaemonServiceAction(ctx context.Context, action string, opts daemonServiceOptions) (string, error) {
	mgr, err := newDaemonServiceManager(opts)
	if err != nil {
		return "", err
	}
	switch action {
	case "start", "stop", "restart", "status":
		out, err := mgr.systemctl(ctx, action, daemonServiceName)
		if err != nil {
			return out, err
		}
		if strings.TrimSpace(out) == "" {
			return fmt.Sprintf("%s %s\n", action, daemonServiceName), nil
		}
		return out, nil
	case "uninstall":
		out, err := mgr.systemctl(ctx, "disable", "--now", daemonServiceName)
		if err != nil && !strings.Contains(strings.ToLower(out), "not loaded") {
			return out, err
		}
		if err := os.Remove(mgr.unitPath); err != nil && !os.IsNotExist(err) {
			return out, err
		}
		reloadOut, err := mgr.systemctl(ctx, "daemon-reload")
		if err != nil {
			return out + reloadOut, err
		}
		return out + reloadOut + fmt.Sprintf("Uninstalled %s\n", mgr.unitPath), nil
	default:
		return "", fmt.Errorf("unsupported daemon service action %q", action)
	}
}

type daemonServiceManager struct {
	opts       daemonServiceOptions
	binaryPath string
	unitPath   string
	homeDir    string
	userName   string
}

func newDaemonServiceManager(opts daemonServiceOptions) (daemonServiceManager, error) {
	if daemonRuntimeGOOS != "linux" {
		return daemonServiceManager{}, fmt.Errorf("systemd daemon services are only supported on Linux")
	}
	if _, err := daemonRunner.LookPath("systemctl"); err != nil {
		return daemonServiceManager{}, fmt.Errorf("systemctl not found: %w", err)
	}
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	bin, err := daemonExecutable()
	if err != nil {
		return daemonServiceManager{}, err
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return daemonServiceManager{}, err
	}

	if opts.System {
		if strings.TrimSpace(opts.User) == "" {
			return daemonServiceManager{}, fmt.Errorf("--user is required with --system")
		}
		u, err := daemonLookupUser(strings.TrimSpace(opts.User))
		if err != nil {
			return daemonServiceManager{}, err
		}
		return daemonServiceManager{
			opts:       opts,
			binaryPath: bin,
			unitPath:   filepath.Join("/etc/systemd/system", daemonServiceName),
			homeDir:    u.HomeDir,
			userName:   u.Username,
		}, nil
	}

	home, err := daemonUserHomeDir()
	if err != nil {
		return daemonServiceManager{}, err
	}
	u, err := daemonCurrentUser()
	if err != nil {
		return daemonServiceManager{}, err
	}
	return daemonServiceManager{
		opts:       opts,
		binaryPath: bin,
		unitPath:   filepath.Join(home, ".config", "systemd", "user", daemonServiceName),
		homeDir:    home,
		userName:   u.Username,
	}, nil
}

func (m daemonServiceManager) renderUnit() (string, error) {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=CvkeHarness Scheduler\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if m.opts.System {
		fmt.Fprintf(&b, "User=%s\n", m.userName)
		fmt.Fprintf(&b, "Environment=HOME=%s\n", systemdQuote(m.homeDir))
	}
	fmt.Fprintf(&b, "ExecStart=%s daemon --interval %s\n", systemdQuote(m.binaryPath), m.opts.Interval.String())
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n\n")
	b.WriteString("[Install]\n")
	if m.opts.System {
		b.WriteString("WantedBy=multi-user.target\n")
	} else {
		b.WriteString("WantedBy=default.target\n")
	}
	return b.String(), nil
}

func (m daemonServiceManager) systemctl(ctx context.Context, args ...string) (string, error) {
	fullArgs := args
	if !m.opts.System {
		fullArgs = append([]string{"--user"}, args...)
	}
	return daemonRunner.Run(ctx, "systemctl", fullArgs...)
}

func (m daemonServiceManager) lingerAdvice(ctx context.Context) string {
	if _, err := daemonRunner.LookPath("loginctl"); err != nil {
		return fmt.Sprintf("To keep the user service alive after logout, run: sudo loginctl enable-linger %s\n", m.userName)
	}
	out, err := daemonRunner.Run(ctx, "loginctl", "show-user", m.userName, "-p", "Linger", "--value")
	if err == nil && strings.TrimSpace(out) == "yes" {
		return ""
	}
	return fmt.Sprintf("To keep the user service alive after logout, run: sudo loginctl enable-linger %s\n", m.userName)
}

func systemdQuote(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\"'\\") {
		return strconv.Quote(value)
	}
	return value
}
