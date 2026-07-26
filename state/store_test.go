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

	now := time.Now().UTC()
	if err := store.SaveCommandApproval(context.Background(), CommandApproval{
		TargetID:       "target-1",
		Environment:    "staging",
		RemoteIdentity: "ops@target-1",
		Command:        "echo hello",
		Action:         "echo",
		Status:         ApprovalStatusApproved,
		Source:         "cli_policy",
		Rationale:      "operator-approved diagnostic command",
		PolicyVersion:  CommandApprovalPolicyVersion,
		ApprovedAt:     now,
		ExpiresAt:      now.Add(time.Hour),
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
	reusable, err := store.ListApprovedCommandApprovals(context.Background(), "target-1", "staging", "", now)
	if err != nil || len(reusable) != 1 {
		t.Fatalf("expected one scoped reusable approval, got %#v err=%v", reusable, err)
	}
}

func TestReusableApprovalsRejectOneOffWrongScopeAndExpiry(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	now := time.Now().UTC()
	base := CommandApproval{
		TargetID: "target-1", Environment: "production", RemoteIdentity: "ops@target-1", Command: "systemctl restart api",
		Action: "systemctl restart", Source: "cli_policy", Rationale: "operator approved",
		PolicyVersion: CommandApprovalPolicyVersion, ApprovedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	oneOff := base
	oneOff.Status = ApprovalStatusApprovedOnce
	if err := store.SaveCommandApproval(context.Background(), oneOff); err != nil {
		t.Fatalf("SaveCommandApproval one-off returned error: %v", err)
	}
	if got, err := store.ListApprovedCommandApprovals(context.Background(), "target-1", "production", "", now); err != nil || len(got) != 0 {
		t.Fatalf("approved_once must not be reusable, got %#v err=%v", got, err)
	}

	active := base
	active.Status = ApprovalStatusApproved
	if err := store.SaveCommandApproval(context.Background(), active); err != nil {
		t.Fatalf("SaveCommandApproval active returned error: %v", err)
	}
	if got, _ := store.ListApprovedCommandApprovals(context.Background(), "target-2", "production", "", now); len(got) != 0 {
		t.Fatalf("wrong target must not reuse approval, got %#v", got)
	}
	if got, _ := store.ListApprovedCommandApprovals(context.Background(), "target-1", "staging", "", now); len(got) != 0 {
		t.Fatalf("wrong environment must not reuse approval, got %#v", got)
	}
	if got, _ := store.ListApprovedCommandApprovals(context.Background(), "target-1", "production", "", now.Add(2*time.Hour)); len(got) != 0 {
		t.Fatalf("expired approval must not be reusable, got %#v", got)
	}
}

func TestInteractiveApprovalsRequireExactSession(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	now := time.Now().UTC()
	approval := CommandApproval{
		TargetID: "target-1", Environment: "production", RemoteIdentity: "ops@target-1",
		SessionID: "session-a", Command: "systemctl restart api", Action: "systemctl restart",
		Status: ApprovalStatusApproved, Source: "user_confirm", Rationale: "interactive confirmation",
		PolicyVersion: CommandApprovalPolicyVersion, ApprovedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveCommandApproval(context.Background(), approval); err != nil {
		t.Fatalf("SaveCommandApproval returned error: %v", err)
	}
	if got, _ := store.ListApprovedCommandApprovals(context.Background(), "target-1", "production", "session-a", now); len(got) != 1 {
		t.Fatalf("same session should reuse interactive approval, got %#v", got)
	}
	if got, _ := store.ListApprovedCommandApprovals(context.Background(), "target-1", "production", "session-b", now); len(got) != 0 {
		t.Fatalf("cross-session reuse must fail closed, got %#v", got)
	}
	if got, _ := store.ListApprovedCommandApprovals(context.Background(), "target-1", "production", "", now); len(got) != 0 {
		t.Fatalf("missing session must not reuse interactive approval, got %#v", got)
	}

	approval.SessionID = ""
	if err := store.SaveCommandApproval(context.Background(), approval); err == nil {
		t.Fatal("interactive approval without a session id must be rejected")
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
		UserInput:                   "hello",
		TaskClass:                   core.TaskClassGeneral,
		RequestedModel:              "chat-model",
		ActualModel:                 "chat-model",
		Success:                     true,
		LatencyMs:                   25,
		PromptTokens:                10,
		TotalTokens:                 20,
		FinalOutput:                 "hi there",
		VerificationStatus:          "satisfied",
		VerificationReason:          "answered greeting",
		VerificationMissingActions:  "",
		VerificationRepairTriggered: false,
	}, []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}, []ToolOutcome{{
		Phase:      core.PhaseChat,
		Provider:   "openrouter",
		Model:      "chat-model",
		ToolName:   "shell",
		Toolset:    "shell_execute",
		Arguments:  `{"command":"go test ./..."}`,
		Command:    "go test ./...",
		Success:    true,
		DurationMs: 25,
	}})
	if err != nil {
		t.Fatalf("AppendChatTurn returned error: %v", err)
	}

	if err := store.FinishChatSession(context.Background(), sessionID, timeNowForTest(), "user_exit"); err != nil {
		t.Fatalf("FinishChatSession returned error: %v", err)
	}

	var runs, sessions, turns, messages, tools int
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
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM chat_tool_outcomes`).Scan(&tools); err != nil {
		t.Fatalf("count chat_tool_outcomes returned error: %v", err)
	}

	if runs != 0 {
		t.Fatalf("expected chat persistence to avoid runs table, got %d rows", runs)
	}
	if sessions != 1 || turns != 1 || messages != 2 || tools != 1 {
		t.Fatalf("expected dedicated chat tables to be populated, got sessions=%d turns=%d messages=%d tools=%d", sessions, turns, messages, tools)
	}

	var finalOutput, verificationStatus string
	if err := store.db.QueryRowContext(context.Background(), `SELECT final_output, verification_status FROM chat_turns WHERE id = 1`).Scan(&finalOutput, &verificationStatus); err != nil {
		t.Fatalf("read chat verification metadata returned error: %v", err)
	}
	if finalOutput != "hi there" || verificationStatus != "satisfied" {
		t.Fatalf("expected chat verification metadata, got final=%q status=%q", finalOutput, verificationStatus)
	}

	detail, err := store.GetChatSessionDetail(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetChatSessionDetail returned error: %v", err)
	}
	if got := detail.ToolsByTurnID[detail.Turns[0].ID]; len(got) != 1 || got[0].Command != "go test ./..." {
		t.Fatalf("expected chat tool outcome detail, got %#v", detail.ToolsByTurnID)
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

func TestRecordRunPersistsToolInvocationDetails(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	err := store.RecordRun(context.Background(), RunRecord{
		StartedAt:  timeNowForTest(),
		FinishedAt: timeNowForTest(),
		Provider:   "openrouter",
		Task:       "check docker",
		TaskClass:  core.TaskClassGeneral,
		Success:    true,
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
			Arguments: `{"command":"docker info"}`,
			Command:   "docker info",
			Success:   true,
		}},
	})
	if err != nil {
		t.Fatalf("RecordRun returned error: %v", err)
	}

	runs, err := store.ListRecentRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecentRuns returned error: %v", err)
	}
	if len(runs) != 1 || len(runs[0].Tools) != 1 {
		t.Fatalf("expected one run with one tool, got %#v", runs)
	}
	tool := runs[0].Tools[0]
	if tool.Arguments != `{"command":"docker info"}` || tool.Command != "docker info" {
		t.Fatalf("expected persisted invocation details, got %#v", tool)
	}
}

func TestRecordRunPersistsVerificationSummary(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	err := store.RecordRun(context.Background(), RunRecord{
		StartedAt:                   timeNowForTest(),
		FinishedAt:                  timeNowForTest(),
		Provider:                    "openrouter",
		Task:                        "do something",
		TaskClass:                   core.TaskClassGeneral,
		Success:                     true,
		FinalOutput:                 "done",
		VerificationStatus:          "satisfied",
		VerificationReason:          "all requested work completed",
		VerificationMissingActions:  "",
		VerificationRepairTriggered: true,
		Phases: []PhaseRecord{{
			Phase:          core.PhaseVerification,
			Provider:       "openrouter",
			RequestedModel: "test-model",
			ActualModel:    "test-model",
			Success:        true,
		}},
	})
	if err != nil {
		t.Fatalf("RecordRun returned error: %v", err)
	}

	var finalOutput, status, reason string
	var repair int
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT final_output, verification_status, verification_reason, verification_repair_triggered
		FROM runs WHERE id = 1`).Scan(&finalOutput, &status, &reason, &repair); err != nil {
		t.Fatalf("read run verification metadata returned error: %v", err)
	}
	if finalOutput != "done" || status != "satisfied" || reason != "all requested work completed" || repair != 1 {
		t.Fatalf("unexpected run verification metadata: final=%q status=%q reason=%q repair=%d", finalOutput, status, reason, repair)
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
	}, nil, nil)
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
	}, nil, nil)
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
