package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/config"
)

func TestSetDefaultModelUpdatesCanonicalFieldAndApprovedModels(t *testing.T) {
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

func TestRootIncludesCommandsCommand(t *testing.T) {
	t.Parallel()

	cmd, _, err := rootCmd.Find([]string{"commands"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected commands command to be registered")
	}
	if cmd.Use != "commands" {
		t.Fatalf("expected commands command, got %q", cmd.Use)
	}
}

func TestDefaultHelpListsRegisteredCommands(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs([]string{"--help"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	helpText := out.String() + errOut.String()
	expectedSnippets := []string{
		"Available Commands:",
		"commands",
		"memory",
		"models",
		"run",
		"setup",
		"settings",
	}
	for _, snippet := range expectedSnippets {
		if !strings.Contains(helpText, snippet) {
			t.Fatalf("expected help output to contain %q, got:\n%s", snippet, helpText)
		}
	}
}
