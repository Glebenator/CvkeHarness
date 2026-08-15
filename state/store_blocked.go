package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coolcake/cvkeharness/core"
)

// SaveBlockedWork persists or updates one resumable blocked task.
func (s *Store) SaveBlockedWork(ctx context.Context, work BlockedWork) (string, error) {
	if !s.Available() {
		return "", s.Err()
	}
	now := time.Now().UTC()
	if work.ID == "" {
		id, err := newBlockedWorkID()
		if err != nil {
			return "", err
		}
		work.ID = id
	}
	if work.CreatedAt.IsZero() {
		work.CreatedAt = now
	}
	if work.UpdatedAt.IsZero() {
		work.UpdatedAt = now
	}
	if work.TaskState == "" {
		work.TaskState = TaskStateBlockedWaitingUser
	}
	var resolved any
	if !work.ResolvedAt.IsZero() {
		resolved = work.ResolvedAt.UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO blocked_work (
			id, created_at, updated_at, resolved_at, task, task_class, task_state,
			blocked_reason, pending_approval_type, pending_approval_payload,
			conversation_snapshot, continuation_data, scheduled_job_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			updated_at = excluded.updated_at,
			resolved_at = excluded.resolved_at,
			task = excluded.task,
			task_class = excluded.task_class,
			task_state = excluded.task_state,
			blocked_reason = excluded.blocked_reason,
			pending_approval_type = excluded.pending_approval_type,
			pending_approval_payload = excluded.pending_approval_payload,
			conversation_snapshot = excluded.conversation_snapshot,
			continuation_data = excluded.continuation_data,
			scheduled_job_id = excluded.scheduled_job_id`,
		work.ID, work.CreatedAt.UTC(), work.UpdatedAt.UTC(), resolved, work.Task,
		string(work.TaskClass), string(work.TaskState), work.BlockedReason,
		work.PendingApprovalType, work.PendingApprovalPayload, work.ConversationSnapshot,
		work.ContinuationData, work.ScheduledJobID,
	)
	if err != nil {
		return "", err
	}
	return work.ID, nil
}

// GetBlockedWork returns one blocked-work record.
func (s *Store) GetBlockedWork(ctx context.Context, id string) (BlockedWork, error) {
	if !s.Available() {
		return BlockedWork{}, s.Err()
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, created_at, updated_at, resolved_at, task, task_class, task_state,
			blocked_reason, pending_approval_type, pending_approval_payload,
			conversation_snapshot, continuation_data, scheduled_job_id
		FROM blocked_work
		WHERE id = ?`, id)
	return scanBlockedWork(row)
}

// ListBlockedWork returns unresolved work waiting on explicit user action.
func (s *Store) ListBlockedWork(ctx context.Context) ([]BlockedWork, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, updated_at, resolved_at, task, task_class, task_state,
			blocked_reason, pending_approval_type, pending_approval_payload,
			conversation_snapshot, continuation_data, scheduled_job_id
		FROM blocked_work
		WHERE task_state = ?
		ORDER BY created_at ASC`, TaskStateBlockedWaitingUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockedWork
	for rows.Next() {
		item, err := scanBlockedWork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ResolveBlockedWork marks a blocked unit as no longer waiting for user action.
func (s *Store) ResolveBlockedWork(ctx context.Context, id string, nextState TaskState) error {
	if !s.Available() {
		return s.Err()
	}
	if nextState == "" {
		nextState = TaskStateRunning
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE blocked_work
		SET task_state = ?, resolved_at = ?, updated_at = ?
		WHERE id = ?`,
		string(nextState), time.Now().UTC(), time.Now().UTC(), id)
	return err
}

// ResolveBlockedShellCommand clears unresolved shell blockers for an approved command segment.
func (s *Store) ResolveBlockedShellCommand(ctx context.Context, command string) ([]BlockedWork, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	pending, err := s.ListBlockedWork(ctx)
	if err != nil {
		return nil, err
	}
	var resolved []BlockedWork
	for _, work := range pending {
		if work.PendingApprovalType != "shell_command" || work.PendingApprovalPayload != command {
			continue
		}
		if err := s.ResolveBlockedWork(ctx, work.ID, TaskStateRunning); err != nil {
			return nil, err
		}
		if work.ScheduledJobID != "" {
			if err := s.UnblockScheduledJob(ctx, work.ScheduledJobID); err != nil {
				return nil, err
			}
		}
		work.TaskState = TaskStateRunning
		resolved = append(resolved, work)
	}
	return resolved, nil
}

// ResolveBlockedSecurityGrant clears work waiting on one exact grant digest.
func (s *Store) ResolveBlockedSecurityGrant(ctx context.Context, digest string) ([]BlockedWork, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	pending, err := s.ListBlockedWork(ctx)
	if err != nil {
		return nil, err
	}
	var resolved []BlockedWork
	for _, work := range pending {
		if work.PendingApprovalType != "security_action" || blockedGrantDigest(work.PendingApprovalPayload) != digest {
			continue
		}
		if err := s.ResolveBlockedWork(ctx, work.ID, TaskStateRunning); err != nil {
			return nil, err
		}
		if work.ScheduledJobID != "" {
			if err := s.UnblockScheduledJob(ctx, work.ScheduledJobID); err != nil {
				return nil, err
			}
		}
		work.TaskState = TaskStateRunning
		resolved = append(resolved, work)
	}
	return resolved, nil
}

func blockedGrantDigest(payload string) string {
	var grant SecurityActionGrant
	if json.Unmarshal([]byte(payload), &grant) == nil && grant.Digest != "" {
		return grant.Digest
	}
	return payload
}

type blockedWorkScanner interface {
	Scan(dest ...any) error
}

func scanBlockedWork(scanner blockedWorkScanner) (BlockedWork, error) {
	var item BlockedWork
	var taskClass, taskState string
	var resolvedTime sql.NullTime
	if err := scanner.Scan(
		&item.ID, &item.CreatedAt, &item.UpdatedAt, &resolvedTime, &item.Task,
		&taskClass, &taskState, &item.BlockedReason, &item.PendingApprovalType,
		&item.PendingApprovalPayload, &item.ConversationSnapshot, &item.ContinuationData,
		&item.ScheduledJobID,
	); err != nil {
		return BlockedWork{}, err
	}
	if resolvedTime.Valid {
		item.ResolvedAt = resolvedTime.Time
	}
	item.TaskClass = core.TaskClass(taskClass)
	item.TaskState = TaskState(taskState)
	return item, nil
}

func newBlockedWorkID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate blocked work id: %w", err)
	}
	return "blocked_" + hex.EncodeToString(raw[:]), nil
}
