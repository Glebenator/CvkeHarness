package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/config"
)

func TestFirstRunCommandsRequireSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	for _, args := range [][]string{
		{}, {"run", "check status"}, {"console"}, {"tui"},
		{"console", "--view", "chat"}, {"daemon", "--once"}, {"settings"},
	} {
		rootCmd.SetArgs(args)
		_, err := rootCmd.ExecuteC()
		if err == nil || !strings.Contains(err.Error(), "cvkeharness setup") {
			t.Fatalf("%v: expected setup error, got %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".cvkeharness")); !os.IsNotExist(err) {
		t.Fatalf("blocked commands created state: %v", err)
	}
}

func TestFirstRunRejectsIncompleteAndMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, _ := config.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "{}", "provider: codex\n", "provider: openrouter\ndefault_model: sample\n", "invalid: ["} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := requireSetup(consoleCmd); err == nil || !strings.Contains(err.Error(), "cvkeharness setup") {
			t.Fatalf("%q accepted: %v", raw, err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != raw {
			t.Fatal("gate changed config")
		}
	}
}

func TestFirstRunLegacyLocalConfigDoesNotNeedCompletionFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := config.DefaultConfig()
	cfg.Provider = "lmstudio"
	cfg.DefaultModel = "local-model"
	cfg.SafetyModel = "local-model"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := requireSetup(consoleCmd); err != nil {
		t.Fatal(err)
	}
}

func TestFirstRunBootstrapAndRecoveryRemainAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, name := range []string{"setup", "recovery", "antigravity"} {
		c, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if err := requireSetup(c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
