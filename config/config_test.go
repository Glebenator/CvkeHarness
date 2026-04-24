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

func TestNormalizeProviderModelIDStripsRedundantProviderPrefix(t *testing.T) {
	t.Parallel()

	got := NormalizeProviderModelID("openrouter", "openrouter/google/gemma-4-31b-it:free")
	if got != "google/gemma-4-31b-it:free" {
		t.Fatalf("expected provider prefix to be stripped, got %q", got)
	}
}

func TestNormalizeProviderModelIDPreservesOpenRouterSpecialAliases(t *testing.T) {
	t.Parallel()

	got := NormalizeProviderModelID("openrouter", "openrouter/auto")
	if got != "openrouter/auto" {
		t.Fatalf("expected OpenRouter alias to be preserved, got %q", got)
	}
}

func TestNormalizeProviderModelIDStripsOpenAIPrefix(t *testing.T) {
	t.Parallel()

	got := NormalizeProviderModelID("openai", "openai/gpt-5.2-codex")
	if got != "gpt-5.2-codex" {
		t.Fatalf("expected OpenAI provider prefix to be stripped, got %q", got)
	}
}

func TestNormalizeProviderModelIDStripsCodexPrefix(t *testing.T) {
	t.Parallel()

	got := NormalizeProviderModelID("codex", "codex/gpt-5.1-codex-max")
	if got != "gpt-5.1-codex-max" {
		t.Fatalf("expected Codex provider prefix to be stripped, got %q", got)
	}
}

func TestLoadConfigNormalizesProviderQualifiedDefaultAndSafetyModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".cvkeharness")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll returned unexpected error: %v", err)
	}

	data := []byte("provider: openrouter\ndefault_model: openrouter/google/gemma-4-31b-it:free\nsafety_model: openrouter/google/gemma-4-31b-it:free\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("WriteFile returned unexpected error: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}

	if cfg.DefaultModel != "google/gemma-4-31b-it:free" {
		t.Fatalf("expected default model to be normalized, got %q", cfg.DefaultModel)
	}
	if cfg.SafetyModel != "google/gemma-4-31b-it:free" {
		t.Fatalf("expected safety model to be normalized, got %q", cfg.SafetyModel)
	}
}

func TestLoadConfigDeduplicatesFavoriteModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".cvkeharness")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll returned unexpected error: %v", err)
	}

	data := []byte("provider: openrouter\nfavorite_models:\n  - openrouter/google/gemma-4-31b-it:free\n  - openrouter/google/gemma-4-31b-it:free\n  - \"  \"\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("WriteFile returned unexpected error: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}

	if len(cfg.FavoriteModels) != 1 || cfg.FavoriteModels[0] != "openrouter/google/gemma-4-31b-it:free" {
		t.Fatalf("expected favorite models to be deduplicated, got %#v", cfg.FavoriteModels)
	}
}
