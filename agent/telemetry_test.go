package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
)

func TestModelCallTelemetryIncludesCacheHitRatio(t *testing.T) {
	t.Parallel()

	writer := telemetry.NewWriter(t.TempDir(), telemetry.StreamTest, nil)
	ctx := telemetry.WithWriter(context.Background(), writer)
	emitModelCallCompleted(ctx, core.PhaseExecution, 1, "openrouter", "requested", &provider.ChatResponse{
		Model: "actual",
		Usage: provider.Usage{
			PromptTokens: 20,
			PromptTokenDetails: &provider.PromptTokenDetails{
				CachedTokens: 5,
			},
		},
	}, 12, nil)

	events, err := telemetry.ReadEvents(filepath.Join(writer.Path()))
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %#v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload["cached_tokens"] != float64(5) || payload["cache_hit_ratio"] != 0.25 {
		t.Fatalf("expected cached token telemetry, got %#v", payload)
	}
}
