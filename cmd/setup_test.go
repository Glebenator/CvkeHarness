package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/tools"
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

func TestCloneConfigCopiesReferenceFields(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		APIKeys:         map[string]string{"openrouter": "secret"},
		AllowedCommands: []string{"echo"},
		ApprovedModels:  []string{"openrouter/model"},
	}

	clone := cloneConfig(cfg)
	clone.APIKeys["openrouter"] = "updated"
	clone.AllowedCommands[0] = "pwd"
	clone.ApprovedModels[0] = "openrouter/other"

	if cfg.APIKeys["openrouter"] != "secret" {
		t.Fatalf("expected source API keys to remain unchanged, got %#v", cfg.APIKeys)
	}
	if cfg.AllowedCommands[0] != "echo" {
		t.Fatalf("expected source allowed commands to remain unchanged, got %#v", cfg.AllowedCommands)
	}
	if cfg.ApprovedModels[0] != "openrouter/model" {
		t.Fatalf("expected source approved models to remain unchanged, got %#v", cfg.ApprovedModels)
	}
}

func TestSettingsMenuEntriesReflectCurrentProvider(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider:       "lmstudio",
		BaseURL:        "http://localhost:1234/v1",
		DefaultModel:   "local-model",
		SafetyMode:     tools.SafetyModeUserConfirm,
		RoutingEnabled: false,
		RoutingMode:    "disabled",
		MaxTokens:      2048,
		MaxIterations:  10,
		LogLevel:       "warn",
		MemoryDir:      t.TempDir(),
	}

	entries := settingsMenuEntries(cfg, defaultSoulProfile())

	if len(entries) < 10 {
		t.Fatalf("expected settings menu entries, got %#v", entries)
	}
	if entries[0].Label != "Provider" || entries[0].Description != "LM Studio" {
		t.Fatalf("expected LM Studio provider summary, got %#v", entries[0])
	}
	if entries[1].Label != "Connection" || entries[1].Description != "http://localhost:1234/v1" {
		t.Fatalf("expected LM Studio connection entry, got %#v", entries[1])
	}
	if entries[3].Description != "Manual user confirmation" {
		t.Fatalf("expected manual approval summary, got %#v", entries[3])
	}
	if entries[4].Description != "Default model only" {
		t.Fatalf("expected routing summary, got %#v", entries[4])
	}
}

func TestSoulSummaryTracksBootstrapState(t *testing.T) {
	t.Parallel()

	memoryDir := t.TempDir()
	profile := soulProfileByID("mentor")

	summary, editable := soulSummary(&config.Config{MemoryDir: memoryDir}, profile)
	if !editable {
		t.Fatal("expected missing soul file to be editable")
	}
	if !strings.Contains(summary, "Mentor") {
		t.Fatalf("expected bootstrap summary to mention profile, got %q", summary)
	}

	if err := os.WriteFile(filepath.Join(memoryDir, "soul.md"), []byte("# existing soul"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	summary, editable = soulSummary(&config.Config{MemoryDir: memoryDir}, profile)
	if editable {
		t.Fatal("expected existing soul file to be preserved")
	}
	if summary != "Existing soul.md is preserved" {
		t.Fatalf("unexpected preserved soul summary %q", summary)
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

func TestRootIncludesChatCommand(t *testing.T) {
	t.Parallel()

	cmd, _, err := rootCmd.Find([]string{"chat"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected chat command to be registered")
	}
	if cmd.Use != "chat" {
		t.Fatalf("expected chat command, got %q", cmd.Use)
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
		"chat",
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

func TestParseChatSlashAction(t *testing.T) {
	t.Parallel()

	cases := map[string]chatSlashAction{
		"/help":  chatSlashHelp,
		"/clear": chatSlashClear,
		"/exit":  chatSlashExit,
		"hello":  chatSlashNone,
	}

	for input, want := range cases {
		if got := parseChatSlashAction(input); got != want {
			t.Fatalf("parseChatSlashAction(%q) = %q, want %q", input, got, want)
		}
	}
}
