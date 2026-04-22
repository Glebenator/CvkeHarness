package cmd

import (
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestChatSessionStatsSummaryUsesPinnedModelWhenNoTurns(t *testing.T) {
	t.Parallel()

	stats := newChatSessionStats(core.RoutingSelection{
		Requested: core.NewModelRef("openrouter", "gpt-pinned"),
	})
	stats.startedAt = time.Now().Add(-2 * time.Second)

	summary := stats.summary("user_exit")
	if summary.ExitReason != "Exited by user" {
		t.Fatalf("expected humanized exit reason, got %q", summary.ExitReason)
	}
	if len(summary.ModelsUsed) != 1 || summary.ModelsUsed[0] != "openrouter/gpt-pinned (pinned)" {
		t.Fatalf("expected pinned model fallback, got %v", summary.ModelsUsed)
	}
}

func TestChatSessionStatsRecordTurnAggregatesUsage(t *testing.T) {
	t.Parallel()

	stats := newChatSessionStats(core.RoutingSelection{
		Requested: core.NewModelRef("openrouter", "gpt-pinned"),
	})
	stats.startedAt = time.Now().Add(-3 * time.Second)

	stats.recordTurn(agent.ChatTurnResult{
		Phase: state.PhaseRecord{
			Provider:          "openrouter",
			ActualModel:       "model-a",
			PromptTokens:      100,
			CompletionTokens:  40,
			TotalTokens:       140,
			CachedTokens:      12,
			CachedTokensKnown: true,
		},
		Tools: []state.ToolOutcome{
			{ToolName: "shell_execute", Success: true},
			{ToolName: "memory_record_finding", Success: false},
		},
	})
	stats.recordTurn(agent.ChatTurnResult{
		Phase: state.PhaseRecord{
			Provider:         "openrouter",
			ActualModel:      "model-a",
			PromptTokens:     25,
			CompletionTokens: 10,
			TotalTokens:      35,
		},
		Tools: []state.ToolOutcome{
			{ToolName: "shell_execute", Success: true},
		},
	})

	summary := stats.summary("interrupt")
	if summary.TurnCount != 2 {
		t.Fatalf("expected 2 turns, got %d", summary.TurnCount)
	}
	if summary.TotalTokens != 175 || summary.PromptTokens != 125 || summary.CompletionTokens != 50 {
		t.Fatalf("unexpected token summary: %#v", summary)
	}
	if !summary.CachedTokensKnown || summary.CachedTokens != 12 {
		t.Fatalf("expected cached tokens to be tracked, got %#v", summary)
	}
	if summary.ToolCalls != 3 || summary.SuccessfulTools != 2 || summary.FailedTools != 1 {
		t.Fatalf("unexpected tool counts: %#v", summary)
	}
	if len(summary.ModelsUsed) != 1 || summary.ModelsUsed[0] != "openrouter/model-a x2" {
		t.Fatalf("expected aggregated model usage, got %v", summary.ModelsUsed)
	}
}
