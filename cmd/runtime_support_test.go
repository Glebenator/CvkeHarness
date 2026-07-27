package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/state"
)

func TestRoutingConfigIncludesPromptApprovedModelsFromState(t *testing.T) {
	t.Parallel()

	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	if err := store.SaveModelApproval(context.Background(), state.ModelApproval{
		Provider:  "openrouter",
		Model:     "gpt-best",
		Status:    "approved_once",
		Source:    "prompt",
		Rationale: "performed best for debugging",
	}); err != nil {
		t.Fatalf("SaveModelApproval returned error: %v", err)
	}

	cfg := &config.Config{
		Provider:       "openrouter",
		DefaultModel:   "default",
		ApprovedModels: []string{"openrouter/default"},
	}

	routingCfg := routingConfigFromConfig(cfg, store)
	approved := routingCfg.ApprovedSet()

	if _, ok := approved["openrouter/gpt-best"]; !ok {
		t.Fatalf("expected prompt-approved model to be available for routing, got %#v", approved)
	}
}

func TestRoutingConfigKeepsOpenAIApprovedModels(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider:       "openai",
		DefaultModel:   "gpt-5.2-codex",
		ApprovedModels: []string{"openai/gpt-5.2-codex"},
	}

	routingCfg := routingConfigFromConfig(cfg, nil)
	approved := routingCfg.ApprovedSet()

	if _, ok := approved["openai/gpt-5.2-codex"]; !ok {
		t.Fatalf("expected OpenAI approved model to be available for routing, got %#v", approved)
	}
}

func TestRoutingConfigKeepsCodexApprovedModels(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Provider:       "codex",
		DefaultModel:   "gpt-5.1-codex-max",
		ApprovedModels: []string{"codex/gpt-5.1-codex-max"},
	}

	routingCfg := routingConfigFromConfig(cfg, nil)
	approved := routingCfg.ApprovedSet()

	if _, ok := approved["codex/gpt-5.1-codex-max"]; !ok {
		t.Fatalf("expected Codex approved model to be available for routing, got %#v", approved)
	}
}

func TestDefaultRegistryFromConfigIncludesWebToolsWhenEnabled(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.WebSearch.Enabled = true
	cfg.SetAPIKey("tavily", "tvly-test-key")

	store := state.Open("")
	registry, err := defaultRegistryFromConfig(cfg, store, memory.NewManager(t.TempDir(), store), nil, nil, false)
	if err != nil {
		t.Fatalf("defaultRegistryFromConfig returned error: %v", err)
	}
	names := strings.Join(registry.Names(), ",")
	if !strings.Contains(names, "web_search") || !strings.Contains(names, "web_fetch") {
		t.Fatalf("expected web tools in shared runtime registry, got %v", registry.Names())
	}
}

func TestDefaultRegistryFromConfigErrorsWhenWebSearchEnabledWithoutCredentials(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.WebSearch.Enabled = true

	_, err := defaultRegistryFromConfig(cfg, state.Open(""), nil, nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "Tavily API key is missing") {
		t.Fatalf("expected missing Tavily key error, got %v", err)
	}
}
