package cmd

import (
	"testing"

	"github.com/coolcake/cvkeharness/config"
)

func TestSetDefaultModelUpdatesBothFieldsAndApprovedModels(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider:       "openrouter",
		DefaultModel:   "old-model",
		Model:          "old-model",
		ApprovedModels: []string{"openrouter/old-model"},
	}

	setDefaultModel(cfg, "openrouter/auto")

	if cfg.DefaultModel != "openrouter/auto" {
		t.Fatalf("expected default model to update, got %q", cfg.DefaultModel)
	}
	if cfg.Model != "openrouter/auto" {
		t.Fatalf("expected legacy model field to stay in sync, got %q", cfg.Model)
	}

	found := false
	for _, item := range cfg.ApprovedModels {
		if item == "openrouter/openrouter/auto" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected approved models to include chosen default, got %#v", cfg.ApprovedModels)
	}
}

func TestEnsureDefaultApprovedAvoidsDuplicates(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider:       "openrouter",
		DefaultModel:   "anthropic/claude-sonnet-4.6",
		ApprovedModels: []string{"openrouter/anthropic/claude-sonnet-4.6"},
	}

	ensureDefaultApproved(cfg)

	if len(cfg.ApprovedModels) != 1 {
		t.Fatalf("expected no duplicate approved model entries, got %#v", cfg.ApprovedModels)
	}
}

func TestRootIncludesSettingsCommand(t *testing.T) {
	t.Parallel()

	cmd, _, err := rootCmd.Find([]string{"settings"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected settings command to be registered")
	}
	if cmd.Use != "settings" {
		t.Fatalf("expected settings command, got %q", cmd.Use)
	}
}
