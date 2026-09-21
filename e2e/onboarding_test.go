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
				if input != "" {
					if _, err := terminal.Write([]byte(input)); err != nil {
						t.Fatal(err)
					}
				}
				if !waitForOutput(buffer, want, 5*time.Second) {
					t.Fatalf("missing %q:\n%s", want, stripANSI(buffer.String()))
				}
			}
			step("", "Continue to Connect")
			step("\r", "openrouter")
			step("\x1b[A\r", "Found Codex login")
			step("\r", "e2e-primary")
			step("\r", "Choose a security profile")
			step("\r", "Judge Model")
			step("\x1b[B\r", "Skip host scan")
			step("\r", "Continue to review")
			step("\r", "Judge model: e2e-judge")
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
			if cfg.Provider != "codex" || cfg.DefaultModel != "e2e-primary" || cfg.SafetyModel != "e2e-judge" {
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
