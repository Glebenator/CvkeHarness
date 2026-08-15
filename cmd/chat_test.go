package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestExportCurrentChatWritesCompletedPersistedTurn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.db")
	store := state.Open(statePath)
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.StartChatSession(ctx, state.ChatSession{Provider: "openai", PinnedModel: "test-model"})
	if err != nil {
		t.Fatalf("StartChatSession returned error: %v", err)
	}
	if _, err := store.AppendChatTurn(ctx, sessionID, state.ChatTurn{
		UserInput:   "hello",
		FinalOutput: "world",
		TaskState:   state.TaskStateCompleted,
		Success:     true,
	}, nil, nil); err != nil {
		t.Fatalf("AppendChatTurn returned error: %v", err)
	}

	path, err := exportCurrentChat(ctx, store, &chatSessionState{sessionID: sessionID}, &config.Config{StateDBPath: statePath})
	if err != nil {
		t.Fatalf("exportCurrentChat returned error: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(body), "hello") || !strings.Contains(string(body), "world") {
		t.Fatalf("unexpected export:\n%s", body)
	}
}

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
