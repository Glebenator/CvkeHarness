package cmd

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeDaemonCommandRunner struct {
	paths   map[string]string
	outputs map[string]string
	calls   []string
}

func (r *fakeDaemonCommandRunner) LookPath(file string) (string, error) {
	if path, ok := r.paths[file]; ok {
		return path, nil
	}
	return "", fmt.Errorf("%s not found", file)
}

func (r *fakeDaemonCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	return r.outputs[call], nil
}

func withFakeDaemonService(t *testing.T, runner *fakeDaemonCommandRunner, home, exe string) {
	t.Helper()
	oldGOOS := daemonRuntimeGOOS
	oldRunner := daemonRunner
	oldExecutable := daemonExecutable
	oldHomeDir := daemonUserHomeDir
	oldCurrentUser := daemonCurrentUser
	oldLookupUser := daemonLookupUser
	daemonRuntimeGOOS = "linux"
	daemonRunner = runner
	daemonExecutable = func() (string, error) { return exe, nil }
	daemonUserHomeDir = func() (string, error) { return home, nil }
	daemonCurrentUser = func() (*user.User, error) {
		return &user.User{Username: "alice", HomeDir: home}, nil
	}
	daemonLookupUser = func(name string) (*user.User, error) {
		if name != "svcuser" {
			return nil, fmt.Errorf("unknown user")
		}
		return &user.User{Username: "svcuser", HomeDir: "/home/svcuser"}, nil
	}
	t.Cleanup(func() {
		daemonRuntimeGOOS = oldGOOS
		daemonRunner = oldRunner
		daemonExecutable = oldExecutable
		daemonUserHomeDir = oldHomeDir
		daemonCurrentUser = oldCurrentUser
		daemonLookupUser = oldLookupUser
	})
}

func TestRenderUserSystemdUnitAndPath(t *testing.T) {
	home := t.TempDir()
	runner := &fakeDaemonCommandRunner{paths: map[string]string{"systemctl": "/bin/systemctl"}}
	withFakeDaemonService(t, runner, home, "/usr/local/bin/cvkeharness")

	mgr, err := newDaemonServiceManager(daemonServiceOptions{Interval: 45 * time.Second})
	if err != nil {
		t.Fatalf("newDaemonServiceManager returned error: %v", err)
	}
	unit, err := mgr.renderUnit()
	if err != nil {
		t.Fatalf("renderUnit returned error: %v", err)
	}
	if mgr.unitPath != filepath.Join(home, ".config", "systemd", "user", daemonServiceName) {
		t.Fatalf("unexpected unit path: %s", mgr.unitPath)
	}
	for _, want := range []string{
		"Type=simple",
		"ExecStart=/usr/local/bin/cvkeharness daemon --interval 45s",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("expected unit to contain %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "User=") {
		t.Fatalf("user unit should not contain User=:\n%s", unit)
	}
}

func TestRenderSystemSystemdUnitRequiresUser(t *testing.T) {
	home := t.TempDir()
	runner := &fakeDaemonCommandRunner{paths: map[string]string{"systemctl": "/bin/systemctl"}}
	withFakeDaemonService(t, runner, home, "/usr/local/bin/cvkeharness")

	if _, err := newDaemonServiceManager(daemonServiceOptions{System: true}); err == nil {
		t.Fatal("expected --system without --user to fail")
	}
	mgr, err := newDaemonServiceManager(daemonServiceOptions{System: true, User: "svcuser", Interval: time.Minute})
	if err != nil {
		t.Fatalf("newDaemonServiceManager returned error: %v", err)
	}
	unit, err := mgr.renderUnit()
	if err != nil {
		t.Fatalf("renderUnit returned error: %v", err)
	}
	if mgr.unitPath != filepath.Join("/etc/systemd/system", daemonServiceName) {
		t.Fatalf("unexpected unit path: %s", mgr.unitPath)
	}
	for _, want := range []string{
		"User=svcuser",
		"Environment=HOME=/home/svcuser",
		"ExecStart=/usr/local/bin/cvkeharness daemon --interval 1m0s",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("expected unit to contain %q:\n%s", want, unit)
		}
	}
}

func TestInstallWritesUnitButDoesNotStartOrEnable(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "bin", "cvkeharness")
	runner := &fakeDaemonCommandRunner{
		paths: map[string]string{"systemctl": "/bin/systemctl", "loginctl": "/bin/loginctl"},
		outputs: map[string]string{
			"loginctl show-user alice -p Linger --value": "no\n",
		},
	}
	withFakeDaemonService(t, runner, home, exe)

	out, err := installDaemonService(context.Background(), daemonServiceOptions{Interval: 30 * time.Second})
	if err != nil {
		t.Fatalf("installDaemonService returned error: %v", err)
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", daemonServiceName)
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "ExecStart="+exe+" daemon --interval 30s") {
		t.Fatalf("unexpected unit content:\n%s", string(data))
	}
	wantCalls := []string{
		"systemctl --user daemon-reload",
		"loginctl show-user alice -p Linger --value",
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("unexpected calls:\n got %v\nwant %v", runner.calls, wantCalls)
	}
	if strings.Contains(strings.Join(runner.calls, "\n"), "start") || strings.Contains(strings.Join(runner.calls, "\n"), "enable "+daemonServiceName) {
		t.Fatalf("install should not start or enable service, calls=%v", runner.calls)
	}
	if !strings.Contains(out, "sudo loginctl enable-linger alice") {
		t.Fatalf("expected linger advice, got %q", out)
	}
}

func TestInstallEnableLingerInvokesLoginctl(t *testing.T) {
	home := t.TempDir()
	runner := &fakeDaemonCommandRunner{paths: map[string]string{"systemctl": "/bin/systemctl", "loginctl": "/bin/loginctl"}}
	withFakeDaemonService(t, runner, home, "/usr/local/bin/cvkeharness")

	if _, err := installDaemonService(context.Background(), daemonServiceOptions{EnableLinger: true}); err != nil {
		t.Fatalf("installDaemonService returned error: %v", err)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"sudo loginctl enable-linger alice",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("unexpected calls:\n got %v\nwant %v", runner.calls, want)
	}
}

func TestDaemonServiceActionsUseSystemctl(t *testing.T) {
	home := t.TempDir()
	actions := map[string][]string{
		"start":   {"systemctl --user start " + daemonServiceName},
		"stop":    {"systemctl --user stop " + daemonServiceName},
		"restart": {"systemctl --user restart " + daemonServiceName},
		"status":  {"systemctl --user status " + daemonServiceName},
	}
	for action, want := range actions {
		t.Run(action, func(t *testing.T) {
			runner := &fakeDaemonCommandRunner{paths: map[string]string{"systemctl": "/bin/systemctl"}}
			withFakeDaemonService(t, runner, home, "/usr/local/bin/cvkeharness")
			if _, err := runDaemonServiceAction(context.Background(), action, daemonServiceOptions{}); err != nil {
				t.Fatalf("runDaemonServiceAction returned error: %v", err)
			}
			if !reflect.DeepEqual(runner.calls, want) {
				t.Fatalf("unexpected calls:\n got %v\nwant %v", runner.calls, want)
			}
		})
	}
}

func TestDaemonServiceUninstallDisablesRemovesAndReloads(t *testing.T) {
	home := t.TempDir()
	unitPath := filepath.Join(home, ".config", "systemd", "user", daemonServiceName)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(unitPath, []byte("unit"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	runner := &fakeDaemonCommandRunner{paths: map[string]string{"systemctl": "/bin/systemctl"}}
	withFakeDaemonService(t, runner, home, "/usr/local/bin/cvkeharness")

	if _, err := runDaemonServiceAction(context.Background(), "uninstall", daemonServiceOptions{}); err != nil {
		t.Fatalf("runDaemonServiceAction returned error: %v", err)
	}
	want := []string{
		"systemctl --user disable --now " + daemonServiceName,
		"systemctl --user daemon-reload",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("unexpected calls:\n got %v\nwant %v", runner.calls, want)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("expected unit file removed, stat err=%v", err)
	}
}

func TestDaemonServiceUnsupportedPlatformAndMissingSystemctl(t *testing.T) {
	home := t.TempDir()
	runner := &fakeDaemonCommandRunner{paths: map[string]string{"systemctl": "/bin/systemctl"}}
	withFakeDaemonService(t, runner, home, "/usr/local/bin/cvkeharness")
	daemonRuntimeGOOS = "darwin"
	if _, err := newDaemonServiceManager(daemonServiceOptions{}); err == nil {
		t.Fatal("expected unsupported platform error")
	}

	daemonRuntimeGOOS = "linux"
	daemonRunner = &fakeDaemonCommandRunner{}
	if _, err := newDaemonServiceManager(daemonServiceOptions{}); err == nil {
		t.Fatal("expected missing systemctl error")
	}
}
