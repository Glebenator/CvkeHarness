package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/coolcake/cvkeharness/internal/telemetry"
)

// SchedulerHealth is the query-ready scheduler projection rebuilt from canonical events.
type SchedulerHealth struct {
	JobID           string
	LastStatus      string
	Blocked         bool
	Overdue         bool
	Claimed         bool
	ClaimLeaseMs    int64
	StaleClaim      bool
	HeartbeatFresh  bool
	LastClaimAt     time.Time
	LastHeartbeatAt time.Time
	LastStartedAt   time.Time
	LastFinishedAt  time.Time
	LastEventAt     time.Time
}

// ProjectTelemetryEvent materializes query projections from the canonical event stream.
func (s *Store) ProjectTelemetryEvent(ctx context.Context, event telemetry.Event) error {
	if !s.Available() {
		return s.Err()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_events (
			event_id, timestamp, stream, event_type, session_id, run_id, turn_id, job_id,
			phase, iteration, provider, requested_model, actual_model, task_state,
			target_id, tool_call_id, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.Timestamp.UTC(), string(event.Stream), string(event.Type), event.SessionID,
		event.RunID, event.TurnID, event.JobID, event.Phase, event.Iteration, event.Provider,
		event.RequestedModel, event.ActualModel, event.TaskState, event.TargetID, event.ToolCallID,
		string(event.Payload),
	); err != nil {
		return err
	}

	if event.RunID != "" && event.TaskState != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO telemetry_task_projection (
				run_id, session_id, turn_id, job_id, task_state, target_id, last_event_type, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(run_id) DO UPDATE SET
				session_id = excluded.session_id,
				turn_id = excluded.turn_id,
				job_id = excluded.job_id,
				task_state = excluded.task_state,
				target_id = excluded.target_id,
				last_event_type = excluded.last_event_type,
				updated_at = excluded.updated_at`,
			event.RunID, event.SessionID, event.TurnID, event.JobID, event.TaskState,
			event.TargetID, string(event.Type), event.Timestamp.UTC(),
		); err != nil {
			return err
		}
	}

	if event.JobID != "" && isSchedulerTelemetryEvent(event.Type) {
		if err := projectSchedulerHealthTx(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RebuildTelemetryProjections clears derived telemetry tables and rebuilds them from events.
func (s *Store) RebuildTelemetryProjections(ctx context.Context, events []telemetry.Event) error {
	if !s.Available() {
		return s.Err()
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM telemetry_task_projection`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM scheduler_health_projection`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM telemetry_events`); err != nil {
		return err
	}
	for _, event := range events {
		if err := s.ProjectTelemetryEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// ListSchedulerHealth returns the derived scheduler-health view.
func (s *Store) ListSchedulerHealth(ctx context.Context) ([]SchedulerHealth, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT job_id, last_status, blocked, overdue, claimed, claim_lease_ms, last_claim_at, last_heartbeat_at,
			last_started_at, last_finished_at, last_event_at
		FROM scheduler_health_projection
		ORDER BY blocked DESC, overdue DESC, last_event_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SchedulerHealth
	for rows.Next() {
		var item SchedulerHealth
		var blocked, overdue, claimed int
		var claimAt, heartbeatAt, startedAt, finishedAt sql.NullTime
		if err := rows.Scan(
			&item.JobID, &item.LastStatus, &blocked, &overdue, &claimed, &item.ClaimLeaseMs,
			&claimAt, &heartbeatAt, &startedAt, &finishedAt, &item.LastEventAt,
		); err != nil {
			return nil, err
		}
		item.Blocked = blocked == 1
		item.Overdue = overdue == 1
		item.Claimed = claimed == 1
		if claimAt.Valid {
			item.LastClaimAt = claimAt.Time
		}
		if heartbeatAt.Valid {
			item.LastHeartbeatAt = heartbeatAt.Time
		}
		if startedAt.Valid {
			item.LastStartedAt = startedAt.Time
		}
		if finishedAt.Valid {
			item.LastFinishedAt = finishedAt.Time
		}
		item.StaleClaim, item.HeartbeatFresh = schedulerClaimFreshness(item, time.Now().UTC())
		out = append(out, item)
	}
	return out, rows.Err()
}

func isSchedulerTelemetryEvent(kind telemetry.EventType) bool {
	switch kind {
	case telemetry.EventSchedulerClaimed,
		telemetry.EventSchedulerHeartbeat,
		telemetry.EventSchedulerStarted,
		telemetry.EventSchedulerFinished,
		telemetry.EventSchedulerOverdue:
		return true
	default:
		return false
	}
}

func projectSchedulerHealthTx(ctx context.Context, tx *sql.Tx, event telemetry.Event) error {
	var existing SchedulerHealth
	var blocked, overdue, claimed int
	var claimAt, heartbeatAt, startedAt, finishedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT job_id, last_status, blocked, overdue, claimed, claim_lease_ms, last_claim_at, last_heartbeat_at,
			last_started_at, last_finished_at, last_event_at
		FROM scheduler_health_projection
		WHERE job_id = ?`, event.JobID).Scan(
		&existing.JobID, &existing.LastStatus, &blocked, &overdue, &claimed, &existing.ClaimLeaseMs,
		&claimAt, &heartbeatAt, &startedAt, &finishedAt, &existing.LastEventAt,
	)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		existing.JobID = event.JobID
	}
	existing.Blocked = blocked == 1
	existing.Overdue = overdue == 1
	existing.Claimed = claimed == 1
	if claimAt.Valid {
		existing.LastClaimAt = claimAt.Time
	}
	if heartbeatAt.Valid {
		existing.LastHeartbeatAt = heartbeatAt.Time
	}
	if startedAt.Valid {
		existing.LastStartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		existing.LastFinishedAt = finishedAt.Time
	}
	existing.LastEventAt = event.Timestamp.UTC()
	if leaseMs := schedulerClaimLeaseMs(event.Payload); leaseMs > 0 {
		existing.ClaimLeaseMs = leaseMs
	}

	switch event.Type {
	case telemetry.EventSchedulerClaimed:
		existing.Claimed = true
		existing.LastClaimAt = event.Timestamp.UTC()
	case telemetry.EventSchedulerHeartbeat:
		existing.Claimed = true
		existing.LastHeartbeatAt = event.Timestamp.UTC()
	case telemetry.EventSchedulerStarted:
		existing.Claimed = true
		existing.LastStartedAt = event.Timestamp.UTC()
		existing.Overdue = false
	case telemetry.EventSchedulerFinished:
		existing.Claimed = false
		existing.LastFinishedAt = event.Timestamp.UTC()
		existing.LastStatus = event.TaskState
		existing.Blocked = event.TaskState == string(TaskStateBlockedWaitingUser)
		existing.Overdue = false
	case telemetry.EventSchedulerOverdue:
		existing.Overdue = true
	}
	var claimValue, heartbeatValue, startedValue, finishedValue any
	if !existing.LastClaimAt.IsZero() {
		claimValue = existing.LastClaimAt.UTC()
	}
	if !existing.LastHeartbeatAt.IsZero() {
		heartbeatValue = existing.LastHeartbeatAt.UTC()
	}
	if !existing.LastStartedAt.IsZero() {
		startedValue = existing.LastStartedAt.UTC()
	}
	if !existing.LastFinishedAt.IsZero() {
		finishedValue = existing.LastFinishedAt.UTC()
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO scheduler_health_projection (
			job_id, last_status, blocked, overdue, claimed, claim_lease_ms, last_claim_at, last_heartbeat_at,
			last_started_at, last_finished_at, last_event_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			last_status = excluded.last_status,
			blocked = excluded.blocked,
			overdue = excluded.overdue,
			claimed = excluded.claimed,
			claim_lease_ms = excluded.claim_lease_ms,
			last_claim_at = excluded.last_claim_at,
			last_heartbeat_at = excluded.last_heartbeat_at,
			last_started_at = excluded.last_started_at,
			last_finished_at = excluded.last_finished_at,
			last_event_at = excluded.last_event_at`,
		existing.JobID, existing.LastStatus, boolToInt(existing.Blocked), boolToInt(existing.Overdue),
		boolToInt(existing.Claimed), existing.ClaimLeaseMs, claimValue, heartbeatValue, startedValue, finishedValue, existing.LastEventAt.UTC(),
	)
	return err
}

func schedulerClaimLeaseMs(payload []byte) int64 {
	if len(payload) == 0 {
		return 0
	}
	var raw struct {
		ClaimLeaseMs int64 `json:"claim_lease_ms"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return 0
	}
	return raw.ClaimLeaseMs
}

func schedulerClaimFreshness(item SchedulerHealth, now time.Time) (stale bool, fresh bool) {
	if !item.Claimed || item.ClaimLeaseMs <= 0 {
		return false, false
	}
	lastSignal := item.LastHeartbeatAt
	if lastSignal.IsZero() {
		lastSignal = item.LastClaimAt
	}
	if lastSignal.IsZero() {
		return false, false
	}
	fresh = now.Before(lastSignal.Add(time.Duration(item.ClaimLeaseMs) * time.Millisecond))
	return !fresh, fresh
}
