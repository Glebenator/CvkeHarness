package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestSetDefaultModelNormalizesProviderQualifiedOpenRouterIDs(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider: "openrouter",
	}

	setDefaultModel(cfg, "openrouter/google/gemma-4-31b-it:free")

	if cfg.DefaultModel != "google/gemma-4-31b-it:free" {
		t.Fatalf("expected default model to be normalized, got %q", cfg.DefaultModel)
	}
	if len(cfg.ApprovedModels) != 1 || cfg.ApprovedModels[0] != "openrouter/google/gemma-4-31b-it:free" {
		t.Fatalf("expected approved model to retain provider/model format, got %#v", cfg.ApprovedModels)
	}
}

func TestSetDefaultModelNormalizesProviderQualifiedOpenAIIDs(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider: "openai",
	}

	setDefaultModel(cfg, "openai/gpt-5.2-codex")

	if cfg.DefaultModel != "gpt-5.2-codex" {
		t.Fatalf("expected default model to be normalized, got %q", cfg.DefaultModel)
	}
	if len(cfg.ApprovedModels) != 1 || cfg.ApprovedModels[0] != "openai/gpt-5.2-codex" {
		t.Fatalf("expected approved model to retain provider/model format, got %#v", cfg.ApprovedModels)
	}
}

func TestSetDefaultModelNormalizesProviderQualifiedCodexIDs(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider: "codex",
	}

	setDefaultModel(cfg, "codex/gpt-5.1-codex-max")

	if cfg.DefaultModel != "gpt-5.1-codex-max" {
		t.Fatalf("expected default model to be normalized, got %q", cfg.DefaultModel)
	}
	if len(cfg.ApprovedModels) != 1 || cfg.ApprovedModels[0] != "codex/gpt-5.1-codex-max" {
		t.Fatalf("expected approved model to retain provider/model format, got %#v", cfg.ApprovedModels)
	}
}

func TestCloneConfigCopiesReferenceFields(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		APIKeys:         map[string]string{"openrouter": "secret"},
		AllowedCommands: []string{"echo"},
		ApprovedModels:  []string{"openrouter/model"},
		FavoriteModels:  []string{"openrouter/favorite"},
	}

	clone := cloneConfig(cfg)
	clone.APIKeys["openrouter"] = "updated"
	clone.AllowedCommands[0] = "pwd"
	clone.ApprovedModels[0] = "openrouter/other"
	clone.FavoriteModels[0] = "openrouter/else"

	if cfg.APIKeys["openrouter"] != "secret" {
		t.Fatalf("expected source API keys to remain unchanged, got %#v", cfg.APIKeys)
	}
	if cfg.AllowedCommands[0] != "echo" {
		t.Fatalf("expected source allowed commands to remain unchanged, got %#v", cfg.AllowedCommands)
	}
	if cfg.ApprovedModels[0] != "openrouter/model" {
		t.Fatalf("expected source approved models to remain unchanged, got %#v", cfg.ApprovedModels)
	}
	if cfg.FavoriteModels[0] != "openrouter/favorite" {
		t.Fatalf("expected source favorite models to remain unchanged, got %#v", cfg.FavoriteModels)
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

func TestSettingsMenuEntriesReflectCodexProvider(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider:       "codex",
		DefaultModel:   "gpt-5.1-codex-max",
		SafetyMode:     tools.SafetyModeUserConfirm,
		RoutingEnabled: false,
		RoutingMode:    "disabled",
		MaxTokens:      2048,
		MaxIterations:  10,
		LogLevel:       "warn",
		MemoryDir:      t.TempDir(),
	}

	entries := settingsMenuEntries(cfg, defaultSoulProfile())

	if entries[0].Label != "Provider" || entries[0].Description != "Codex via ChatGPT" {
		t.Fatalf("expected Codex provider summary, got %#v", entries[0])
	}
	if entries[1].Label != "Codex Login" {
		t.Fatalf("expected Codex login entry, got %#v", entries[1])
	}
}

func TestSafetyModelsFollowProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeCodexModelsCache(t, home, fmt.Sprintf(`{
		"fetched_at": %q,
		"models": [
			{
				"slug": "gpt-5.5",
				"display_name": "GPT-5.5",
				"description": "Strong model",
				"visibility": "list",
				"supported_in_api": true,
				"priority": 0
			}
		]
	}`, time.Now().UTC().Format(time.RFC3339Nano)))

	codexOptions := safetyModelsForProvider(&config.Config{Provider: "codex"})
	if codexOptions[0][0] != "gpt-5.5" {
		t.Fatalf("expected Codex safety model options, got %#v", codexOptions)
	}

	openAIOptions := safetyModelsForProvider(&config.Config{Provider: "openai"})
	if openAIOptions[0][0] != "gpt-5-nano" {
		t.Fatalf("expected OpenAI safety model options, got %#v", openAIOptions)
	}
}

func TestFetchCodexModelsUsesFreshCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	now := time.Date(2026, 4, 23, 14, 0, 0, 0, time.UTC)
	writeCodexModelsCache(t, home, `{
		"fetched_at": "2026-04-23T13:45:00Z",
		"client_version": "0.122.0",
		"models": [
			{
				"slug": "hidden-model",
				"display_name": "Hidden",
				"description": "Hidden model",
				"visibility": "hidden",
				"supported_in_api": true,
				"priority": 1
			},
			{
				"slug": "gpt-5.4-mini",
				"display_name": "GPT-5.4-Mini",
				"description": "Small model",
				"visibility": "list",
				"supported_in_api": true,
				"priority": 4
			},
			{
				"slug": "gpt-5.4",
				"display_name": "gpt-5.4",
				"description": "Strong model",
				"visibility": "list",
				"supported_in_api": true,
				"priority": 2
			}
		]
	}`)

	result := fetchCodexModels(now)

	if !result.isLive {
		t.Fatalf("expected fresh cache to be live, got %#v", result)
	}
	if result.source != "codex-cache" {
		t.Fatalf("expected codex cache source, got %q", result.source)
	}
	if len(result.items) < 3 {
		t.Fatalf("expected cached models plus custom entry, got %#v", result.items)
	}
	if result.items[0][0] != "gpt-5.4" || result.items[1][0] != "gpt-5.4-mini" {
		t.Fatalf("expected priority ordering, got %#v", result.items)
	}
}

func TestFetchCodexModelsMarksStaleCacheNotLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	now := time.Date(2026, 4, 23, 14, 0, 0, 0, time.UTC)
	writeCodexModelsCache(t, home, `{
		"fetched_at": "2026-04-22T01:00:00Z",
		"models": [
			{
				"slug": "gpt-5.4",
				"display_name": "gpt-5.4",
				"description": "Strong model",
				"visibility": "list",
				"supported_in_api": true,
				"priority": 2
			}
		]
	}`)

	result := fetchCodexModels(now)

	if result.isLive {
		t.Fatalf("expected stale cache to be non-live, got %#v", result)
	}
	if result.source != "codex-cache-stale" {
		t.Fatalf("expected stale codex cache marker, got %q", result.source)
	}
	if result.items[0][0] != "[ custom model ]" {
		t.Fatalf("expected stale cache models not to be listed, got %#v", result.items)
	}
}

func TestFetchCodexModelsFallsBackWhenCacheMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	result := fetchCodexModels(time.Date(2026, 4, 23, 14, 0, 0, 0, time.UTC))

	if result.isLive {
		t.Fatalf("expected missing cache fallback to be non-live, got %#v", result)
	}
	if result.source != "fallback" {
		t.Fatalf("expected fallback source, got %q", result.source)
	}
	if len(result.items) == 0 || result.items[0][0] != "[ custom model ]" {
		t.Fatalf("expected manual-only Codex fallback, got %#v", result.items)
	}
}

func writeCodexModelsCache(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func TestSettingsMenuEntriesReflectOpenAIProvider(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider:       "openai",
		APIKeys:        map[string]string{"openai": "sk-test-secret"},
		DefaultModel:   "gpt-5.2-codex",
		SafetyMode:     tools.SafetyModeUserConfirm,
		RoutingEnabled: false,
		RoutingMode:    "disabled",
		MaxTokens:      2048,
		MaxIterations:  10,
		LogLevel:       "warn",
		MemoryDir:      t.TempDir(),
	}

	entries := settingsMenuEntries(cfg, defaultSoulProfile())

	if entries[0].Label != "Provider" || entries[0].Description != "OpenAI" {
		t.Fatalf("expected OpenAI provider summary, got %#v", entries[0])
	}
	if entries[1].Label != "API Key" || entries[1].Description == "Not configured" {
		t.Fatalf("expected OpenAI credential entry, got %#v", entries[1])
	}
}

func TestSoulSummaryTracksGuidanceBootstrapState(t *testing.T) {
	t.Parallel()

	memoryDir := t.TempDir()
	profile := soulProfileByID("mentor")

	summary, editable := soulSummary(&config.Config{MemoryDir: memoryDir}, profile)
	if !editable {
		t.Fatal("expected missing guidance file to be editable")
	}
	if !strings.Contains(summary, "Mentor") {
		t.Fatalf("expected bootstrap summary to mention profile, got %q", summary)
	}

	if err := os.WriteFile(filepath.Join(memoryDir, "guidance.md"), []byte("# existing guidance"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	summary, editable = soulSummary(&config.Config{MemoryDir: memoryDir}, profile)
	if editable {
		t.Fatal("expected existing guidance file to be preserved")
	}
	if summary != "Existing guidance.md is preserved" {
		t.Fatalf("unexpected preserved guidance summary %q", summary)
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
