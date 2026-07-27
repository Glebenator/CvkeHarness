package state

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/internal/telemetry"
)

func TestRebuildTelemetryProjectionsFromCanonicalEvents(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}

	base := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	leasePayload, _ := json.Marshal(map[string]any{"claim_lease_ms": int64((5 * time.Minute).Milliseconds())})
	events := []telemetry.Event{
		{
			EventID:   "evt_1",
			Timestamp: base,
			Stream:    telemetry.StreamLive,
			Type:      telemetry.EventTaskBlocked,
			RunID:     "run_1",
			SessionID: "session_1",
			TurnID:    "turn_1",
			JobID:     "job_1",
			TaskState: string(TaskStateBlockedWaitingUser),
			TargetID:  "target_1",
		},
		{
			EventID:   "evt_2",
			Timestamp: base.Add(time.Second),
			Stream:    telemetry.StreamLive,
			Type:      telemetry.EventSchedulerOverdue,
			JobID:     "job_1",
		},
		{
			EventID:   "evt_3",
			Timestamp: base.Add(2 * time.Second),
			Stream:    telemetry.StreamLive,
			Type:      telemetry.EventSchedulerClaimed,
			JobID:     "job_1",
			Payload:   leasePayload,
		},
		{
			EventID:   "evt_4",
			Timestamp: base.Add(3 * time.Second),
			Stream:    telemetry.StreamLive,
			Type:      telemetry.EventSchedulerStarted,
			JobID:     "job_1",
			Payload:   leasePayload,
		},
		{
			EventID:   "evt_5",
			Timestamp: base.Add(4 * time.Second),
			Stream:    telemetry.StreamLive,
			Type:      telemetry.EventSchedulerFinished,
			JobID:     "job_1",
			TaskState: string(TaskStateBlockedWaitingUser),
		},
	}
	if err := store.RebuildTelemetryProjections(context.Background(), events); err != nil {
		t.Fatalf("RebuildTelemetryProjections returned error: %v", err)
	}

	var taskState, targetID, lastEvent string
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT task_state, target_id, last_event_type
		FROM telemetry_task_projection
		WHERE run_id = ?`, "run_1").Scan(&taskState, &targetID, &lastEvent); err != nil {
		t.Fatalf("query telemetry_task_projection returned error: %v", err)
	}
	if taskState != string(TaskStateBlockedWaitingUser) || targetID != "target_1" || lastEvent != string(telemetry.EventTaskBlocked) {
		t.Fatalf("unexpected task projection state=%q target=%q event=%q", taskState, targetID, lastEvent)
	}

	health, err := store.ListSchedulerHealth(context.Background())
	if err != nil {
		t.Fatalf("ListSchedulerHealth returned error: %v", err)
	}
	if len(health) != 1 {
		t.Fatalf("expected one scheduler-health row, got %#v", health)
	}
	got := health[0]
	if got.JobID != "job_1" || got.LastStatus != string(TaskStateBlockedWaitingUser) || !got.Blocked || got.Overdue || got.Claimed {
		t.Fatalf("unexpected scheduler health projection: %#v", got)
	}
	if got.ClaimLeaseMs != int64((5 * time.Minute).Milliseconds()) {
		t.Fatalf("expected claim lease to rebuild from event payload, got %#v", got)
	}
}

func TestSchedulerClaimFreshness(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	stale, fresh := schedulerClaimFreshness(SchedulerHealth{
		Claimed:         true,
		ClaimLeaseMs:    int64(time.Minute.Milliseconds()),
		LastHeartbeatAt: now.Add(-2 * time.Minute),
	}, now)
	if !stale || fresh {
		t.Fatalf("expected stale claim, got stale=%v fresh=%v", stale, fresh)
	}

	stale, fresh = schedulerClaimFreshness(SchedulerHealth{
		Claimed:      true,
		ClaimLeaseMs: int64(time.Minute.Milliseconds()),
		LastClaimAt:  now.Add(-30 * time.Second),
	}, now)
	if stale || !fresh {
		t.Fatalf("expected fresh claim, got stale=%v fresh=%v", stale, fresh)
	}
}
