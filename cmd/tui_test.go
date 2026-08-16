package cmd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestCanceledChatTurnStillPersistsLocalAuditRecord(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatalf("state database unavailable: %v", store.Err())
	}
	sessionID, err := store.StartChatSession(context.Background(), state.ChatSession{
		Provider:    "test",
		PinnedModel: "test-model",
	})
	if err != nil {
		t.Fatalf("start chat session: %v", err)
	}

	executionCtx, cancel := context.WithCancel(context.Background())
	cancel()
	recordChatTurn(chatPersistenceContext(executionCtx), store, sessionID, "interrupted prompt", agent.ChatTurnResult{
		TaskState: state.TaskStateIncomplete,
		TaskClass: core.TaskClassPolicySensitive,
		Phase: state.PhaseRecord{
			Phase:          core.PhaseChat,
			Provider:       "test",
			RequestedModel: "test-model",
			ActualModel:    "test-model",
		},
	})

	detail, err := store.GetChatSessionDetail(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("get chat session detail: %v", err)
	}
	if len(detail.Turns) != 1 || detail.Turns[0].UserInput != "interrupted prompt" || detail.Turns[0].TaskState != state.TaskStateIncomplete {
		t.Fatalf("canceled turn audit record was lost: %#v", detail.Turns)
	}
}
