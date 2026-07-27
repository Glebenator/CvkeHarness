package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

func TestChatTurnTelemetryCarriesSessionAndTurnCorrelation(t *testing.T) {
	t.Parallel()

	writer := telemetry.NewWriter(t.TempDir(), telemetry.StreamTest, nil)
	fakeProvider := &sequenceProvider{
		fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			return &provider.ChatResponse{
				Model:   "pinned-model",
				Message: provider.Message{Role: "assistant", Content: "done"},
			}, nil
		},
	}
	a := New(Options{
		Provider:                      fakeProvider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  tools.NewRegistry(),
		DefaultModel:                  "default-model",
		MaxIterations:                 1,
		MaxTokens:                     128,
		DisableCompletionVerification: true,
		Router:                        &chatRouterStub{selection: core.RoutingSelection{Phase: core.PhaseChat, Requested: core.NewModelRef("openrouter", "pinned-model")}},
		MemoryRetriever:               &memoryStub{},
		TelemetryWriter:               writer,
	})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned error: %v", err)
	}
	ctx := telemetry.WithFields(context.Background(), telemetry.Fields{SessionID: "session_1", TurnID: "turn_1"})
	if _, err := session.Turn(ctx, "finish"); err != nil {
		t.Fatalf("Turn returned error: %v", err)
	}
	events, err := telemetry.ReadEvents(filepath.Join(writer.Path()))
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected chat telemetry events")
	}
	var sawCompleted bool
	for _, event := range events {
		if event.SessionID != "session_1" || event.TurnID != "turn_1" || event.RunID == "" {
			t.Fatalf("expected reconstructed chat correlation on every event, got %#v", event)
		}
		if event.Type == telemetry.EventTaskCompleted {
			sawCompleted = true
		}
	}
	if !sawCompleted {
		t.Fatalf("expected task_completed event, got %#v", events)
	}
}
