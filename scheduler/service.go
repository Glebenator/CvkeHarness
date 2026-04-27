package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

const (
	RunStatusOK    = "ok"
	RunStatusError = "error"
)

// Runner executes a scheduled prompt.
type Runner interface {
	RunScheduledJob(ctx context.Context, job state.ScheduledJob) (output string, runID int64, err error)
}

// Service manages durable scheduled jobs.
type Service struct {
	store *state.Store
	now   func() time.Time
}

// New creates a scheduler service.
func New(store *state.Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Create validates and persists a new scheduled job.
func (s *Service) Create(ctx context.Context, name, kind, spec, prompt string) (state.ScheduledJob, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Scheduled job"
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return state.ScheduledJob{}, fmt.Errorf("prompt cannot be empty")
	}
	now := s.now().UTC()
	next, err := NextRun(kind, spec, now)
	if err != nil {
		return state.ScheduledJob{}, err
	}
	id, err := newID("job")
	if err != nil {
		return state.ScheduledJob{}, err
	}
	job := state.ScheduledJob{
		ID:           id,
		Name:         name,
		ScheduleKind: strings.TrimSpace(kind),
		ScheduleSpec: strings.TrimSpace(spec),
		Prompt:       prompt,
		Enabled:      true,
		NextRunAt:    next,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.SaveScheduledJob(ctx, job); err != nil {
		return state.ScheduledJob{}, err
	}
	return job, nil
}

// Update replaces editable fields and recomputes the next run.
func (s *Service) Update(ctx context.Context, id, name, kind, spec, prompt string) (state.ScheduledJob, error) {
	job, err := s.store.GetScheduledJob(ctx, id)
	if err != nil {
		return state.ScheduledJob{}, err
	}
	if strings.TrimSpace(name) != "" {
		job.Name = strings.TrimSpace(name)
	}
	if strings.TrimSpace(kind) != "" {
		job.ScheduleKind = strings.TrimSpace(kind)
	}
	if strings.TrimSpace(spec) != "" {
		job.ScheduleSpec = strings.TrimSpace(spec)
	}
	if strings.TrimSpace(prompt) != "" {
		job.Prompt = strings.TrimSpace(prompt)
	}
	next, err := NextRun(job.ScheduleKind, job.ScheduleSpec, s.now())
	if err != nil {
		return state.ScheduledJob{}, err
	}
	job.NextRunAt = next
	job.UpdatedAt = s.now()
	if err := s.store.SaveScheduledJob(ctx, job); err != nil {
		return state.ScheduledJob{}, err
	}
	return job, nil
}

// SetEnabled pauses or resumes a job.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) (state.ScheduledJob, error) {
	job, err := s.store.GetScheduledJob(ctx, id)
	if err != nil {
		return state.ScheduledJob{}, err
	}
	job.Enabled = enabled
	if enabled {
		next, err := NextRun(job.ScheduleKind, job.ScheduleSpec, s.now())
		if err != nil {
			return state.ScheduledJob{}, err
		}
		job.NextRunAt = next
	} else {
		job.NextRunAt = time.Time{}
	}
	job.UpdatedAt = s.now()
	if err := s.store.SaveScheduledJob(ctx, job); err != nil {
		return state.ScheduledJob{}, err
	}
	return job, nil
}

// RunDue executes all due jobs once.
func (s *Service) RunDue(ctx context.Context, runner Runner) ([]state.ScheduledJobRun, error) {
	jobs, err := s.store.ListDueScheduledJobs(ctx, s.now())
	if err != nil {
		return nil, err
	}
	var runs []state.ScheduledJobRun
	var firstErr error
	for _, job := range jobs {
		run, err := s.RunNow(ctx, runner, job.ID, false)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if run.JobID == "" {
				continue
			}
		}
		runs = append(runs, run)
	}
	return runs, firstErr
}

// RunNow executes one job, optionally preserving schedule for manual runs.
func (s *Service) RunNow(ctx context.Context, runner Runner, id string, manual bool) (state.ScheduledJobRun, error) {
	job, err := s.store.GetScheduledJob(ctx, id)
	if err != nil {
		return state.ScheduledJobRun{}, err
	}
	started := s.now()
	output, runID, execErr := runner.RunScheduledJob(ctx, job)
	finished := s.now()
	status := RunStatusOK
	errText := ""
	if execErr != nil {
		status = RunStatusError
		errText = execErr.Error()
	}
	run := state.ScheduledJobRun{
		JobID:      job.ID,
		StartedAt:  started,
		FinishedAt: finished,
		Status:     status,
		Output:     output,
		Error:      errText,
		RunID:      runID,
	}
	insertedID, recErr := s.store.RecordScheduledJobRun(ctx, run)
	if recErr != nil {
		return state.ScheduledJobRun{}, recErr
	}
	run.ID = insertedID

	job.LastRunAt = started
	job.LastRunStatus = status
	if execErr != nil {
		job.ConsecutiveFail++
	} else {
		job.ConsecutiveFail = 0
	}
	if !manual {
		if job.ScheduleKind == KindAt {
			job.Enabled = false
			job.NextRunAt = time.Time{}
		} else if next, err := NextRun(job.ScheduleKind, job.ScheduleSpec, finished); err == nil {
			job.NextRunAt = next
		} else {
			job.Enabled = false
			job.NextRunAt = time.Time{}
		}
	}
	job.UpdatedAt = finished
	if err := s.store.SaveScheduledJob(ctx, job); err != nil {
		return state.ScheduledJobRun{}, err
	}
	return run, execErr
}

func newID(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
