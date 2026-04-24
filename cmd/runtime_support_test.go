package cmd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/config"
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
