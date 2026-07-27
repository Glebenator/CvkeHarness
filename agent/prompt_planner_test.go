package agent

import (
	"testing"

	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
)

func TestPromptPlannerKeepsStablePrefixAcrossCompatibleTurns(t *testing.T) {
	t.Parallel()

	retrieved := memory.RetrievalResult{
		BuiltInRules:       "rules",
		Guidance:           "compiled guidance",
		RuntimeHostSummary: "runtime host",
	}
	tools := []provider.ToolDef{{
		Type: "function",
		Function: provider.ToolFuncDef{
			Name: "shell_execute",
		},
	}}
	first := buildPromptPlan(retrieved, "", []provider.Message{{Role: "user", Content: "check disk"}}, tools)
	second := buildPromptPlan(retrieved, "", []provider.Message{{Role: "user", Content: "check cpu"}}, tools)

	if first.PrefixHash == "" || first.PrefixHash != second.PrefixHash {
		t.Fatalf("expected compatible turns to share stable prefix hash, first=%q second=%q", first.PrefixHash, second.PrefixHash)
	}
	if first.PromptHash == second.PromptHash {
		t.Fatalf("expected volatile turn content to change prompt hash, got %q", first.PromptHash)
	}
}
