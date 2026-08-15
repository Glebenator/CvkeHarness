package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriterPersistsCanonicalEnvelopeWithRedaction(t *testing.T) {
	t.Parallel()

	writer := NewWriter(t.TempDir(), StreamTest, nil)
	writer.now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	writer.newID = func() (string, error) { return "evt_fixed", nil }

	payload, _ := json.Marshal(map[string]any{
		"authorization": "Authorization: Bearer abcdefghijklmnop",
	})
	ctx := WithFields(WithWriter(context.Background(), writer), Fields{
		SessionID:      "session-1",
		RunID:          "run-1",
		TurnID:         "turn-1",
		JobID:          "job-1",
		Phase:          "execution",
		Iteration:      2,
		Provider:       "openrouter",
		RequestedModel: "requested-model",
		ActualModel:    "actual-model",
		TaskState:      "running",
		TargetID:       "target-1",
		ToolCallID:     "tool-1",
	})
	if err := Record(ctx, Event{
		Type:    EventToolStarted,
		Payload: payload,
	}); err != nil {
		t.Fatalf("Record returned error: %v", err)
	}

	events, err := ReadEvents(filepath.Join(writer.path))
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	got := events[0]
	if got.EventID != "evt_fixed" || got.Stream != StreamTest || got.Type != EventToolStarted {
		t.Fatalf("unexpected event identity: %#v", got)
	}
	if got.SessionID != "session-1" || got.RunID != "run-1" || got.TurnID != "turn-1" || got.JobID != "job-1" {
		t.Fatalf("expected correlation ids to survive projection: %#v", got)
	}
	if got.Phase != "execution" || got.Iteration != 2 || got.Provider != "openrouter" || got.RequestedModel != "requested-model" || got.ActualModel != "actual-model" || got.TaskState != "running" || got.TargetID != "target-1" || got.ToolCallID != "tool-1" {
		t.Fatalf("expected correlation fields, got %#v", got)
	}
	var decoded map[string]string
	if err := json.Unmarshal(got.Payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded["authorization"] != "[REDACTED]" {
		t.Fatalf("expected redacted payload, got %#v", decoded)
	}
	if info, err := os.Stat(writer.path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("expected private telemetry file, info=%v err=%v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(writer.path)); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("expected private telemetry directory, info=%v err=%v", info, err)
	}
}
