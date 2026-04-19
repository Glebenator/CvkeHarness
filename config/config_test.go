package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigIncludesEchoInAllowedCommands(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

	for _, command := range cfg.AllowedCommands {
		if command == "echo" {
			return
		}
	}

	t.Fatalf("expected default allowed commands to include echo, got %#v", cfg.AllowedCommands)
}

func TestLoadConfigAddsEchoToExistingAllowlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".cvkeharness")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll returned unexpected error: %v", err)
	}

	data := []byte("provider: openrouter\nallowed_commands:\n  - ps\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("WriteFile returned unexpected error: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}

	foundEcho := false
	foundPS := false
	for _, command := range cfg.AllowedCommands {
		if command == "echo" {
			foundEcho = true
		}
		if command == "ps" {
			foundPS = true
		}
	}

	if !foundPS {
		t.Fatalf("expected existing allowed command to be preserved, got %#v", cfg.AllowedCommands)
	}
	if !foundEcho {
		t.Fatalf("expected loaded config to include echo, got %#v", cfg.AllowedCommands)
	}
}
