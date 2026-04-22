package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestPromptModelApprovalRejectsByDefault(t *testing.T) {
	t.Parallel()

	approved, err := promptModelApprovalWithIO(strings.NewReader("\n"), &strings.Builder{}, core.NewModelRef("openrouter", "gpt-best"), "higher confidence for execution")
	if err != nil {
		t.Fatalf("promptModelApprovalWithIO returned unexpected error: %v", err)
	}
	if approved {
		t.Fatal("expected blank fallback input to keep the current approved model")
	}
}

func TestPromptModelApprovalAcceptsSecondOption(t *testing.T) {
	t.Parallel()

	approved, err := promptModelApprovalWithIO(strings.NewReader("2\n"), &strings.Builder{}, core.NewModelRef("openrouter", "gpt-best"), "higher confidence for execution")
	if err != nil {
		t.Fatalf("promptModelApprovalWithIO returned unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected choosing the second option to approve the recommended model")
	}
}

func TestSummarizeRunResultAggregatesPhasesAndTools(t *testing.T) {
	t.Parallel()

	startedAt := time.Now().Add(-3 * time.Second)
	finishedAt := startedAt.Add(2500 * time.Millisecond)
	summary := summarizeRunResult(agent.RunResult{
		Run: state.RunRecord{
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			Phases: []state.PhaseRecord{
				{
					Provider:          "openrouter",
					ActualModel:       "model-a",
					PromptTokens:      100,
					CompletionTokens:  25,
					TotalTokens:       125,
					CachedTokens:      10,
					CachedTokensKnown: true,
				},
				{
					Provider:         "openrouter",
					ActualModel:      "model-b",
					PromptTokens:     40,
					CompletionTokens: 15,
					TotalTokens:      55,
				},
			},
			Tools: []state.ToolOutcome{
				{ToolName: "shell_execute", Success: true},
				{ToolName: "memory_record_finding", Success: false},
			},
		},
	}, "")

	if summary.ExitReason != "Completed" {
		t.Fatalf("expected completed exit reason, got %q", summary.ExitReason)
	}
	if summary.Duration != 2500*time.Millisecond {
		t.Fatalf("expected duration to be preserved, got %s", summary.Duration)
	}
	if summary.TotalTokens != 180 || summary.PromptTokens != 140 || summary.CompletionTokens != 40 {
		t.Fatalf("unexpected token totals: %#v", summary)
	}
	if !summary.CachedTokensKnown || summary.CachedTokens != 10 {
		t.Fatalf("expected cached tokens, got %#v", summary)
	}
	if summary.ToolCalls != 2 || summary.SuccessfulTools != 1 || summary.FailedTools != 1 {
		t.Fatalf("unexpected tool counts: %#v", summary)
	}
	if len(summary.ModelsUsed) != 2 {
		t.Fatalf("expected 2 models, got %v", summary.ModelsUsed)
	}
}

func TestSummarizeRunResultUsesInterruptReason(t *testing.T) {
	t.Parallel()

	summary := summarizeRunResult(agent.RunResult{}, "interrupt")
	if summary.ExitReason != "Interrupted" {
		t.Fatalf("expected interrupted exit reason, got %q", summary.ExitReason)
	}
}
