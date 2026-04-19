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
