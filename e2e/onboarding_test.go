//go:build e2e && !windows

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/creack/pty"
	"gopkg.in/yaml.v3"
)

func TestFirstRunConsoleCannotCreateRuntimeState(t *testing.T) {
	for _, args := range [][]string{{"console"}, {"tui"}, {"console", "--view", "chat"}, {"daemon", "--once"}} {
		home := t.TempDir()
		output, err := runCLI(t, home, "", args...)
		if err == nil {
			t.Fatalf("%v succeeded: %s", args, output)
		}
		assertContains(t, output, "cvkeharness setup")
		if _, err := os.Stat(filepath.Join(home, ".cvkeharness")); !os.IsNotExist(err) {
			t.Fatalf("%v created runtime state", args)
		}
	}
}

func TestSettingsEntryPointsWorkBeforeProviderConfiguration(t *testing.T) {
	for _, entry := range []struct {
		name  string
		args  []string
		width uint16
	}{
		{"settings_command", []string{"settings"}, 80},
		{"console_settings", []string{"console", "--view", "settings"}, 120},
	} {
		t.Run(entry.name, func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, testBinaryPath, entry.args...)
			command.Env = envWith(userTestEnv(home), map[string]string{"CODEX_HOME": filepath.Join(home, ".codex")})
			terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: entry.width})
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			buffer := &lockedBuffer{}
			go func() { _, _ = io.Copy(buffer, terminal) }()
			if !waitForOutput(buffer, "Configuration only", 5*time.Second) {
				t.Fatalf("Settings blocked without a provider:\n%s", stripANSI(buffer.String()))
			}
			if _, err := terminal.Write([]byte("c")); err != nil {
				t.Fatal(err)
			}
			if !waitForOutput(buffer, "Save provider access once", 5*time.Second) {
				t.Fatal("Connections section was not reachable")
			}
			if _, err := terminal.Write([]byte("a")); err != nil {
				t.Fatal(err)
			}
			if !waitForOutput(buffer, "New connection", 5*time.Second) {
				t.Fatal("Connection editor was not reachable")
			}
			start := len(buffer.String())
			if _, err := terminal.Write([]byte("\x1b")); err != nil {
				t.Fatal(err)
			}
			if !waitForOutputAfter(buffer, start, "Save provider access once", 5*time.Second) {
				t.Fatal("cancelling the editor did not return to Connections")
			}
			if _, err := terminal.Write([]byte("q")); err != nil {
				t.Fatal(err)
			}
			if !waitForOutput(buffer, "Discard", 5*time.Second) {
				t.Fatal("unsaved Settings exit did not offer explicit discard")
			}
			if _, err := terminal.Write([]byte("d")); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"config.yaml", "state.db"} {
				if _, err := os.Stat(filepath.Join(home, ".cvkeharness", name)); !os.IsNotExist(err) {
					t.Fatalf("opening configuration-only Settings created %s", name)
				}
			}
		})
	}
}

func TestCodexOnboardingSavesBothModelsThroughPTY(t *testing.T) {
	for _, width := range []uint16{80, 100, 120} {
		t.Run(fmt.Sprintf("%d_columns", width), func(t *testing.T) {
			home := t.TempDir()
			authDir := filepath.Join(home, ".codex")
			if err := os.MkdirAll(authDir, 0700); err != nil {
				t.Fatal(err)
			}
			// Deliberately synthetic credentials; this journey never calls a provider.
			if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"tokens":{"access_token":"synthetic-e2e-token","account_id":"test-account"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			cache := map[string]any{"fetched_at": time.Now().UTC(), "models": []map[string]any{
				{"slug": "e2e-primary", "priority": 1, "visibility": "list", "supported_in_api": true},
				{"slug": "e2e-judge", "priority": 2, "visibility": "list", "supported_in_api": true},
			}}
			raw, _ := json.Marshal(cache)
			if err := os.WriteFile(filepath.Join(authDir, "models_cache.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, testBinaryPath, "setup")
			command.Env = envWith(userTestEnv(home), map[string]string{"CODEX_HOME": authDir})
			terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: width})
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			buffer := &lockedBuffer{}
			copyDone := make(chan struct{})
			go func() { _, _ = io.Copy(buffer, terminal); close(copyDone) }()
			step := func(input, want string) {
				t.Helper()
				start := len(buffer.String())
				if input == "" {
					start = 0
				}
				if input != "" {
					if _, err := terminal.Write([]byte(input)); err != nil {
						t.Fatal(err)
					}
				}
				if !waitForOutputAfter(buffer, start, want, 5*time.Second) {
					t.Fatalf("missing %q:\n%s", want, stripANSI(buffer.String()))
				}
			}
			step("", "Continue to Connect")
			step("\r", "Add connection")
			step("a", "New connection")
			step("\r", "Name")
			step("Test account\r", "Test account")
			step("\x1b[B\r", "Choose provider")
			step("\r", "Login file")
			step("\x1b[B\x1b[B\r", "e2e-primary")
			step("\r", "Choose a security profile")
			step("\r", "Choose Safety judge model")
			step("e2e-judge", "e2e-judge")
			step("\r", "Skip host scan")
			step("\r", "Continue to review")
			step("\r", "Safety judge: Test account")
			step("\r", "Configuration saved.")
			if _, err := terminal.Write([]byte("\r")); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil {
				t.Fatalf("setup failed: %v\n%s", err, stripANSI(buffer.String()))
			}
			_ = terminal.Close()
			<-copyDone
			raw, err = os.ReadFile(filepath.Join(home, ".cvkeharness", "config.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var cfg config.Config
			if err := yaml.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			primary, primaryErr := cfg.ResolveRole(config.RolePrimary)
			judge, judgeErr := cfg.ResolveRole(config.RoleSafetyJudge)
			if primaryErr != nil || judgeErr != nil || primary.Connection.Provider != "codex" || primary.Model != "e2e-primary" || judge.Model != "e2e-judge" || judge.ConnectionID != primary.ConnectionID {
				t.Fatalf("wrong saved models: %s", raw)
			}
			if _, err := os.Stat(filepath.Join(home, ".cvkeharness", "host_profile.json")); !os.IsNotExist(err) {
				t.Fatal("skipped scan created a host profile")
			}
			// A saved setup passes the command gate without another onboarding prompt.
			check := exec.CommandContext(ctx, testBinaryPath, "models", "favorites")
			check.Env = command.Env
			if out, err := check.CombinedOutput(); err != nil {
				t.Fatalf("saved setup rejected: %v\n%s", err, out)
			}
		})
	}
}
