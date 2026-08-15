package tools

import (
	"context"
	"testing"

	"github.com/coolcake/cvkeharness/internal/telemetry"
)

type correlationObserver struct{ events []Event }

func (o *correlationObserver) Observe(event Event) { o.events = append(o.events, event) }

func TestEmitEventIncludesSessionAndTurnCorrelation(t *testing.T) {
	observer := &correlationObserver{}
	ctx := WithEventObserver(context.Background(), observer)
	ctx = telemetry.WithFields(ctx, telemetry.Fields{SessionID: "session_9", TurnID: "turn_3"})
	EmitEvent(ctx, Event{Type: EventToolCallStarted})
	if len(observer.events) != 1 || observer.events[0].SessionID != "session_9" || observer.events[0].TurnID != "turn_3" {
		t.Fatalf("expected TUI event correlation, got %#v", observer.events)
	}
}
