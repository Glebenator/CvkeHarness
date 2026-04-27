package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SaveScheduledJob inserts or updates a scheduled job.
func (s *Store) SaveScheduledJob(ctx context.Context, job ScheduledJob) error {
	if !s.Available() {
		return s.Err()
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	var next any
	if !job.NextRunAt.IsZero() {
		next = job.NextRunAt.UTC()
	}
	var last any
	if !job.LastRunAt.IsZero() {
		last = job.LastRunAt.UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scheduled_jobs (
			id, name, schedule_kind, schedule_spec, prompt, enabled, next_run_at,
			last_run_at, last_run_status, consecutive_failures, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			schedule_kind = excluded.schedule_kind,
			schedule_spec = excluded.schedule_spec,
			prompt = excluded.prompt,
			enabled = excluded.enabled,
			next_run_at = excluded.next_run_at,
			last_run_at = excluded.last_run_at,
			last_run_status = excluded.last_run_status,
			consecutive_failures = excluded.consecutive_failures,
			updated_at = excluded.updated_at`,
		job.ID, job.Name, job.ScheduleKind, job.ScheduleSpec, job.Prompt,
		boolToInt(job.Enabled), next, last, job.LastRunStatus, job.ConsecutiveFail,
		job.CreatedAt.UTC(), job.UpdatedAt.UTC(),
	)
	return err
}

// GetScheduledJob returns one scheduled job.
func (s *Store) GetScheduledJob(ctx context.Context, id string) (ScheduledJob, error) {
	if !s.Available() {
		return ScheduledJob{}, s.Err()
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, schedule_kind, schedule_spec, prompt, enabled, next_run_at,
			last_run_at, last_run_status, consecutive_failures, created_at, updated_at
		FROM scheduled_jobs WHERE id = ?`, id)
	return scanScheduledJob(row)
}

// ListScheduledJobs returns jobs ordered by next run time.
func (s *Store) ListScheduledJobs(ctx context.Context, includeDisabled bool) ([]ScheduledJob, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	query := `
		SELECT id, name, schedule_kind, schedule_spec, prompt, enabled, next_run_at,
			last_run_at, last_run_status, consecutive_failures, created_at, updated_at
		FROM scheduled_jobs`
	if !includeDisabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY next_run_at IS NULL, next_run_at ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledJob
	for rows.Next() {
		job, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// ListDueScheduledJobs returns enabled jobs due at or before now.
func (s *Store) ListDueScheduledJobs(ctx context.Context, now time.Time) ([]ScheduledJob, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, schedule_kind, schedule_spec, prompt, enabled, next_run_at,
			last_run_at, last_run_status, consecutive_failures, created_at, updated_at
		FROM scheduled_jobs
		WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC`, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledJob
	for rows.Next() {
		job, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// DeleteScheduledJob removes a scheduled job and its run records.
func (s *Store) DeleteScheduledJob(ctx context.Context, id string) error {
	if !s.Available() {
		return s.Err()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_job_runs WHERE job_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_jobs WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordScheduledJobRun inserts one scheduler run record.
func (s *Store) RecordScheduledJobRun(ctx context.Context, run ScheduledJobRun) (int64, error) {
	if !s.Available() {
		return 0, s.Err()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO scheduled_job_runs (job_id, started_at, finished_at, status, output, error, run_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.JobID, run.StartedAt.UTC(), run.FinishedAt.UTC(), run.Status, run.Output, run.Error, run.RunID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListScheduledJobRuns returns recent runs for a job.
func (s *Store) ListScheduledJobRuns(ctx context.Context, jobID string, limit int) ([]ScheduledJobRun, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, started_at, finished_at, status, output, error, run_id
		FROM scheduled_job_runs
		WHERE job_id = ?
		ORDER BY started_at DESC
		LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledJobRun
	for rows.Next() {
		var item ScheduledJobRun
		if err := rows.Scan(&item.ID, &item.JobID, &item.StartedAt, &item.FinishedAt, &item.Status, &item.Output, &item.Error, &item.RunID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// RecordSystemCronAudit inserts a system crontab audit event.
func (s *Store) RecordSystemCronAudit(ctx context.Context, audit SystemCronAudit) error {
	if !s.Available() {
		return s.Err()
	}
	if audit.CreatedAt.IsZero() {
		audit.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_cron_audit (
			action, target, old_snippet, new_snippet, success, error_message, initiating_tool, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		audit.Action, audit.Target, audit.OldSnippet, audit.NewSnippet, boolToInt(audit.Success),
		audit.ErrorMessage, audit.InitiatingTool, audit.CreatedAt.UTC(),
	)
	return err
}

type scheduledJobScanner interface {
	Scan(dest ...any) error
}

func scanScheduledJob(scanner scheduledJobScanner) (ScheduledJob, error) {
	var job ScheduledJob
	var enabled int
	var next, last sql.NullTime
	err := scanner.Scan(
		&job.ID, &job.Name, &job.ScheduleKind, &job.ScheduleSpec, &job.Prompt,
		&enabled, &next, &last, &job.LastRunStatus, &job.ConsecutiveFail,
		&job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScheduledJob{}, err
		}
		return ScheduledJob{}, err
	}
	job.Enabled = enabled == 1
	if next.Valid {
		job.NextRunAt = next.Time
	}
	if last.Valid {
		job.LastRunAt = last.Time
	}
	return job, nil
}
