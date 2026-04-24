package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
)

func TestOpenGracefullyHandlesMissingPath(t *testing.T) {
	t.Parallel()

	store := Open("")
	if store.Available() {
		t.Fatal("expected unavailable store for empty path")
	}
	if store.Err() == nil {
		t.Fatal("expected initialization error for empty path")
	}
}

func TestOpenGracefullyHandlesCorruptSQLiteFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	store := Open(path)
	if store.Available() {
		t.Fatal("expected unavailable store for corrupt sqlite file")
	}
	if store.Err() == nil {
		t.Fatal("expected initialization error for corrupt sqlite file")
	}
}

func TestSaveAndListCommandApprovals(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	if !store.Available() {
		t.Fatalf("expected store to be available, got %v", store.Err())
	}

	if err := store.SaveCommandApproval(context.Background(), CommandApproval{
		Command:   "echo hello",
		Status:    "approved",
		Source:    "llm_judge",
		Rationale: "safe diagnostic command",
	}); err != nil {
		t.Fatalf("SaveCommandApproval returned error: %v", err)
	}

	approvals, err := store.ListCommandApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListCommandApprovals returned error: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected 1 command approval, got %d", len(approvals))
	}
	if approvals[0].Command != "echo hello" {
		t.Fatalf("expected persisted command approval, got %#v", approvals[0])
	}
}

func TestChatPersistenceUsesDedicatedTables(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	sessionID, err := store.StartChatSession(context.Background(), ChatSession{
		Provider:       "openrouter",
		PinnedModel:    "chat-model",
		RoutingEnabled: true,
	})
	if err != nil {
		t.Fatalf("StartChatSession returned error: %v", err)
	}

	_, err = store.AppendChatTurn(context.Background(), sessionID, ChatTurn{
		UserInput:      "hello",
		TaskClass:      core.TaskClassGeneral,
		RequestedModel: "chat-model",
		ActualModel:    "chat-model",
		Success:        true,
		LatencyMs:      25,
		PromptTokens:   10,
		TotalTokens:    20,
	}, []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	})
	if err != nil {
		t.Fatalf("AppendChatTurn returned error: %v", err)
	}

	if err := store.FinishChatSession(context.Background(), sessionID, timeNowForTest(), "user_exit"); err != nil {
		t.Fatalf("FinishChatSession returned error: %v", err)
	}

	var runs, sessions, turns, messages int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatalf("count runs returned error: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM chat_sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count chat_sessions returned error: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM chat_turns`).Scan(&turns); err != nil {
		t.Fatalf("count chat_turns returned error: %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM chat_messages`).Scan(&messages); err != nil {
		t.Fatalf("count chat_messages returned error: %v", err)
	}

	if runs != 0 {
		t.Fatalf("expected chat persistence to avoid runs table, got %d rows", runs)
	}
	if sessions != 1 || turns != 1 || messages != 2 {
		t.Fatalf("expected dedicated chat tables to be populated, got sessions=%d turns=%d messages=%d", sessions, turns, messages)
	}
}

func TestRecordChatPhaseStatsUsesChatPhaseOnly(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	err := store.RecordChatPhaseStats(context.Background(), core.TaskClassGeneral, PhaseRecord{
		Phase:          core.PhaseChat,
		Provider:       "openrouter",
		RequestedModel: "chat-model",
		ActualModel:    "chat-model",
		Success:        true,
		LatencyMs:      12,
	}, []ToolOutcome{
		{
			Phase:    core.PhaseChat,
			ToolName: "shell_execute",
			Toolset:  core.ToolsetKey([]string{"shell_execute"}),
			Success:  true,
		},
	})
	if err != nil {
		t.Fatalf("RecordChatPhaseStats returned error: %v", err)
	}

	chatStats, err := store.ListModelStats(context.Background(), core.PhaseChat, core.TaskClassGeneral, core.ToolsetKey([]string{"shell_execute"}))
	if err != nil {
		t.Fatalf("ListModelStats(chat) returned error: %v", err)
	}
	execStats, err := store.ListModelStats(context.Background(), core.PhaseExecution, core.TaskClassGeneral, core.ToolsetKey([]string{"shell_execute"}))
	if err != nil {
		t.Fatalf("ListModelStats(execution) returned error: %v", err)
	}

	if len(chatStats) != 1 {
		t.Fatalf("expected one chat stat row, got %d", len(chatStats))
	}
	if len(execStats) != 0 {
		t.Fatalf("expected no execution stat rows from chat recording, got %d", len(execStats))
	}
}

func TestRecordRunTracksModelAliases(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	err := store.RecordRun(context.Background(), RunRecord{
		StartedAt:  timeNowForTest(),
		FinishedAt: timeNowForTest(),
		Provider:   "openrouter",
		Task:       "debug something",
		TaskClass:  core.TaskClassDebugging,
		Success:    true,
		Phases: []PhaseRecord{{
			Phase:          core.PhaseExecution,
			Provider:       "openrouter",
			RequestedModel: "shadow-model",
			ActualModel:    "vendor/revealed-model",
			Success:        true,
		}},
	})
	if err != nil {
		t.Fatalf("RecordRun returned error: %v", err)
	}

	aliases, err := store.ListModelAliases(context.Background())
	if err != nil {
		t.Fatalf("ListModelAliases returned error: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("expected one alias row, got %#v", aliases)
	}
	if aliases[0].RequestedModel != "shadow-model" || aliases[0].ActualModel != "vendor/revealed-model" {
		t.Fatalf("expected persisted alias mapping, got %#v", aliases[0])
	}
}

func TestAppendChatTurnTracksModelAliases(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	sessionID, err := store.StartChatSession(context.Background(), ChatSession{
		Provider:    "openrouter",
		PinnedModel: "shadow-model",
	})
	if err != nil {
		t.Fatalf("StartChatSession returned error: %v", err)
	}

	_, err = store.AppendChatTurn(context.Background(), sessionID, ChatTurn{
		UserInput:      "hello",
		TaskClass:      core.TaskClassGeneral,
		RequestedModel: "shadow-model",
		ActualModel:    "vendor/revealed-model",
		Success:        true,
	}, nil)
	if err != nil {
		t.Fatalf("AppendChatTurn returned error: %v", err)
	}

	aliases, err := store.ListModelAliases(context.Background())
	if err != nil {
		t.Fatalf("ListModelAliases returned error: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("expected one alias row, got %#v", aliases)
	}
	if aliases[0].Provider != "openrouter" {
		t.Fatalf("expected chat alias provider to be preserved, got %#v", aliases[0])
	}
}

func TestListRecentModelUsageIncludesRunsAndChat(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	if err := store.RecordRun(context.Background(), RunRecord{
		StartedAt:  time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 4, 20, 10, 5, 0, 0, time.UTC),
		Provider:   "openrouter",
		Task:       "run task",
		TaskClass:  core.TaskClassGeneral,
		Success:    true,
		Phases: []PhaseRecord{{
			Phase:          core.PhaseExecution,
			Provider:       "openrouter",
			RequestedModel: "google/gemma-4-31b-it:free",
			ActualModel:    "google/gemma-4-31b-it:free",
			Success:        true,
		}},
	}); err != nil {
		t.Fatalf("RecordRun returned error: %v", err)
	}

	sessionID, err := store.StartChatSession(context.Background(), ChatSession{
		Provider:    "openrouter",
		PinnedModel: "shadow-model",
		StartedAt:   time.Date(2026, 4, 21, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartChatSession returned error: %v", err)
	}

	_, err = store.AppendChatTurn(context.Background(), sessionID, ChatTurn{
		UserInput:      "chat task",
		TaskClass:      core.TaskClassGeneral,
		RequestedModel: "shadow-model",
		ActualModel:    "vendor/revealed-model",
		Success:        false,
		CreatedAt:      time.Date(2026, 4, 21, 9, 1, 0, 0, time.UTC),
	}, nil)
	if err != nil {
		t.Fatalf("AppendChatTurn returned error: %v", err)
	}

	items, err := store.ListRecentModelUsage(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecentModelUsage returned error: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected both run and chat usage rows, got %#v", items)
	}
	if items[0].RequestedModel != "shadow-model" {
		t.Fatalf("expected most recent model usage first, got %#v", items[0])
	}

	var sawRun bool
	var sawChatAlias bool
	for _, item := range items {
		if item.RequestedModel == "google/gemma-4-31b-it:free" && item.Successes == 1 {
			sawRun = true
		}
		if item.RequestedModel == "shadow-model" && strings.Contains(item.ActualModel, "revealed-model") {
			sawChatAlias = true
		}
	}
	if !sawRun || !sawChatAlias {
		t.Fatalf("expected recent usage to include run and aliased chat rows, got %#v", items)
	}
}

func timeNowForTest() time.Time {
	return time.Now().UTC()
}
