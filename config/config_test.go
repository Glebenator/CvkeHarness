package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/securitypolicy"
)

func TestDefaultConfigUsesReasonableSecurity(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Normalize()
	effective, err := cfg.EffectiveSecurity()
	if err != nil {
		t.Fatal(err)
	}
	if effective.Profile != securitypolicy.ProfileReasonable {
		t.Fatalf("profile = %q", effective.Profile)
	}
	if effective.Decision(securitypolicy.SettingFileDelete) != securitypolicy.DecisionAsk {
		t.Fatalf("reasonable delete policy = %q", effective.Decision(securitypolicy.SettingFileDelete))
	}
}

func TestLegacySecurityModesMigrate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode    string
		profile securitypolicy.Profile
	}{
		{"llm_judge", securitypolicy.ProfileReasonable},
		{"user_confirm", securitypolicy.ProfileReasonable},
		{"user_confirm_all", securitypolicy.ProfileExtraStrict},
		{"unrestricted", securitypolicy.ProfileYOLO},
	}
	for _, tc := range cases {
		cfg := &Config{SafetyMode: tc.mode, CapabilityPolicy: CapabilityPolicy{PythonScripts: "ask", AutonomousDiagnostics: "ask", NetworkProbes: "ask", InstallMissingTools: "ask"}}
		cfg.Normalize()
		if cfg.Security == nil || cfg.Security.Profile != tc.profile {
			t.Fatalf("mode %s migrated to %#v, want %s", tc.mode, cfg.Security, tc.profile)
		}
	}
}

func TestConfigCloneDeepCopiesSecurityOverrides(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	_ = cfg.Security.SetOverride(securitypolicy.SettingFileDelete, string(securitypolicy.DecisionDeny))
	clone := cfg.Clone()
	_ = clone.Security.SetOverride(securitypolicy.SettingFileDelete, string(securitypolicy.DecisionAllow))
	if cfg.Security.Overrides[securitypolicy.SettingFileDelete] != string(securitypolicy.DecisionDeny) {
		t.Fatal("clone mutated source security overrides")
	}
}

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

func TestDefaultConfigIncludesGuidedCapabilityPolicy(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Normalize()

	if cfg.SetupAgentMode != "guided" {
		t.Fatalf("expected guided setup agent mode, got %q", cfg.SetupAgentMode)
	}
	if cfg.CapabilityPolicy.PythonScripts != "ask" {
		t.Fatalf("expected python script policy to default to ask, got %#v", cfg.CapabilityPolicy)
	}
	if cfg.CapabilityPolicy.ScriptWriteDir == "" {
		t.Fatalf("expected script write directory default, got %#v", cfg.CapabilityPolicy)
	}
	if cfg.CapabilityPolicy.InstallMissingTools != "ask" {
		t.Fatalf("expected missing tool installs to default to ask, got %#v", cfg.CapabilityPolicy)
	}
	if cfg.WebSearch.Enabled {
		t.Fatal("expected web search to be disabled by default")
	}
	if cfg.WebSearch.Provider != "tavily" {
		t.Fatalf("expected Tavily provider default, got %#v", cfg.WebSearch)
	}
	if cfg.WebSearch.MaxResults != 5 || cfg.WebSearch.SearchDepth != "basic" || cfg.WebSearch.MaxFetchedChars != 12000 {
		t.Fatalf("unexpected web search defaults: %#v", cfg.WebSearch)
	}
}

func TestLoadConfigPreservesCapabilityPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".cvkeharness")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll returned unexpected error: %v", err)
	}

	data := []byte(`provider: openrouter
setup_agent_mode: guided
capability_policy:
  python_scripts: allow
  script_write_dir: /tmp/cvkeharness-scripts
  autonomous_diagnostics: ask
  network_probes: allow
  install_missing_tools: deny
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("WriteFile returned unexpected error: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if cfg.CapabilityPolicy.PythonScripts != "allow" {
		t.Fatalf("expected loaded policy, got %#v", cfg.CapabilityPolicy)
	}
	if cfg.CapabilityPolicy.InstallMissingTools != "deny" {
		t.Fatalf("expected install policy to be preserved, got %#v", cfg.CapabilityPolicy)
	}
}

func TestLoadConfigPreservesWebSearchConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".cvkeharness")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll returned unexpected error: %v", err)
	}

	data := []byte(`provider: openrouter
web_search:
  enabled: true
  provider: tavily
  max_results: 99
  search_depth: advanced
  max_fetched_chars: 99999
  allowed_domains:
    - Docs.Example.com
    - docs.example.com
  blocked_domains:
    - bad.example.com
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("WriteFile returned unexpected error: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if !cfg.WebSearch.Enabled || cfg.WebSearch.Provider != "tavily" || cfg.WebSearch.SearchDepth != "advanced" {
		t.Fatalf("unexpected web search config: %#v", cfg.WebSearch)
	}
	if cfg.WebSearch.MaxResults != 10 {
		t.Fatalf("expected max_results cap of 10, got %d", cfg.WebSearch.MaxResults)
	}
	if cfg.WebSearch.MaxFetchedChars != 30000 {
		t.Fatalf("expected max_fetched_chars cap of 30000, got %d", cfg.WebSearch.MaxFetchedChars)
	}
	if len(cfg.WebSearch.AllowedDomains) != 1 || cfg.WebSearch.AllowedDomains[0] != "docs.example.com" {
		t.Fatalf("expected normalized allowed domains, got %#v", cfg.WebSearch.AllowedDomains)
	}
	if len(cfg.WebSearch.BlockedDomains) != 1 || cfg.WebSearch.BlockedDomains[0] != "bad.example.com" {
		t.Fatalf("expected normalized blocked domains, got %#v", cfg.WebSearch.BlockedDomains)
	}
}

func TestTavilyAPIKeyUsesConfigBeforeEnv(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "env-key")

	cfg := DefaultConfig()
	if got := cfg.TavilyAPIKey(); got != "env-key" {
		t.Fatalf("expected env fallback, got %q", got)
	}

	cfg.SetAPIKey("tavily", "config-key")
	if got := cfg.TavilyAPIKey(); got != "config-key" {
		t.Fatalf("expected configured key to win, got %q", got)
	}
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
