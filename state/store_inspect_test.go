package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
)

func TestInspectionQueriesReturnRunsChatsAndCronAudits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatalf("expected store to be available, got %v", store.Err())
	}

	now := timeNowForTest()
	if err := store.RecordRun(ctx, RunRecord{
		StartedAt:          now.Add(-time.Minute),
		FinishedAt:         now,
		Provider:           "openrouter",
		Task:               "inspect service",
		TaskClass:          core.TaskClassDebugging,
		Success:            true,
		FinalOutput:        "service is healthy",
		VerificationStatus: "satisfied",
		Phases: []PhaseRecord{{
			Phase:          core.PhaseExecution,
			Provider:       "openrouter",
			RequestedModel: "model-a",
			ActualModel:    "model-a",
			Success:        true,
		}},
		Tools: []ToolOutcome{{
			Phase:     core.PhaseExecution,
			Provider:  "openrouter",
			Model:     "model-a",
			ToolName:  "shell_execute",
			Toolset:   "shell_execute",
			Arguments: `{"command":"systemctl status demo"}`,
			Command:   "systemctl status demo",
			Success:   true,
		}},
	}); err != nil {
		t.Fatalf("RecordRun returned error: %v", err)
	}

	sessionID, err := store.StartChatSession(ctx, ChatSession{
		StartedAt:      now,
		Provider:       "openrouter",
		PinnedModel:    "chat-model",
		RoutingEnabled: true,
	})
	if err != nil {
		t.Fatalf("StartChatSession returned error: %v", err)
	}
	if _, err := store.AppendChatTurn(ctx, sessionID, ChatTurn{
		UserInput:      "hello",
		TaskClass:      core.TaskClassGeneral,
		RequestedModel: "chat-model",
		ActualModel:    "chat-model",
		Success:        true,
		FinalOutput:    "hi",
	}, []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}, nil); err != nil {
		t.Fatalf("AppendChatTurn returned error: %v", err)
	}

	if err := store.RecordSystemCronAudit(ctx, SystemCronAudit{
		Action:     "add",
		Target:     "cron_1",
		Success:    true,
		OldSnippet: "",
		NewSnippet: "* * * * * echo hi\n",
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordSystemCronAudit returned error: %v", err)
	}

	runs, err := store.ListRecentRuns(ctx, 5)
	if err != nil {
		t.Fatalf("ListRecentRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].Task != "inspect service" || len(runs[0].Phases) != 1 || len(runs[0].Tools) != 1 {
		t.Fatalf("unexpected run inspection result: %#v", runs)
	}
	if runs[0].Tools[0].Command != "systemctl status demo" {
		t.Fatalf("expected tool command to round-trip, got %#v", runs[0].Tools[0])
	}

	chats, err := store.ListRecentChatSessions(ctx, 5)
	if err != nil {
		t.Fatalf("ListRecentChatSessions returned error: %v", err)
	}
	if len(chats) != 1 || chats[0].TurnCount != 1 {
		t.Fatalf("unexpected chat summaries: %#v", chats)
	}
	detail, err := store.GetChatSessionDetail(ctx, chats[0].ID)
	if err != nil {
		t.Fatalf("GetChatSessionDetail returned error: %v", err)
	}
	if len(detail.Turns) != 1 || len(detail.Messages) != 2 {
		t.Fatalf("unexpected chat detail: %#v", detail)
	}

	audits, err := store.ListSystemCronAudits(ctx, 5)
	if err != nil {
		t.Fatalf("ListSystemCronAudits returned error: %v", err)
	}
	if len(audits) != 1 || audits[0].Action != "add" || !audits[0].Success {
		t.Fatalf("unexpected cron audits: %#v", audits)
	}
}
