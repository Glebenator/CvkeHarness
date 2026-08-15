package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/state"
)

func TestServiceExportChatSessionWritesPersistedTranscript(t *testing.T) {
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
		UserInput:      "inspect service",
		FinalOutput:    "service is healthy",
		RequestedModel: "test-model",
		TaskState:      state.TaskStateCompleted,
		Success:        true,
	}, nil, nil); err != nil {
		t.Fatalf("AppendChatTurn returned error: %v", err)
	}

	svc := NewService(&config.Config{StateDBPath: statePath}, store, nil, nil, nil)
	path, err := svc.ExportChatSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ExportChatSession returned error: %v", err)
	}
	if filepath.Dir(path) != filepath.Join(dir, "exports") {
		t.Fatalf("export path = %q, want exports next to state DB", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	for _, want := range []string{"inspect service", "service is healthy"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("export missing %q:\n%s", want, body)
		}
	}
}
