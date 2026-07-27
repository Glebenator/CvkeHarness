package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/state"
)

const (
	RunStatusOK      = "ok"
	RunStatusError   = "error"
	RunStatusBlocked = "blocked"

	DefaultClaimLease        = 5 * time.Minute
	DefaultHeartbeatInterval = time.Minute
)

// Runner executes a scheduled prompt.
type Runner interface {
	RunScheduledJob(ctx context.Context, job state.ScheduledJob) (output string, runID int64, err error)
}

// Service manages durable scheduled jobs.
type Service struct {
	store             *state.Store
	now               func() time.Time
	owner             string
	claimLease        time.Duration
	heartbeatInterval time.Duration
	telemetryWriter   *telemetry.Writer
}

// New creates a scheduler service.
func New(store *state.Store) *Service {
	return &Service{
		store:             store,
		now:               func() time.Time { return time.Now().UTC() },
		claimLease:        DefaultClaimLease,
		heartbeatInterval: DefaultHeartbeatInterval,
	}
}

// SetClaimOwner sets the stable owner ID used for scheduler job claims.
func (s *Service) SetClaimOwner(owner string) {
	s.owner = strings.TrimSpace(owner)
}

// SetClaimLease sets the claim lease duration.
func (s *Service) SetClaimLease(lease time.Duration) {
	if lease > 0 {
		s.claimLease = lease
	}
}

// SetHeartbeatInterval sets how often active claims are renewed.
func (s *Service) SetHeartbeatInterval(interval time.Duration) {
	if interval > 0 {
		s.heartbeatInterval = interval
	}
}

// SetTelemetryWriter configures canonical scheduler event emission.
func (s *Service) SetTelemetryWriter(writer *telemetry.Writer) {
	s.telemetryWriter = writer
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
	jobs, err := s.store.ClaimDueScheduledJobs(ctx, s.claimOwner(), s.now(), s.claimLease, 100)
	if err != nil {
		return nil, err
	}
	var runs []state.ScheduledJobRun
	var firstErr error
	for _, job := range jobs {
		eventCtx := s.telemetryContext(ctx, job.ID)
		if job.NextRunAt.Before(s.now()) {
			_ = telemetry.Record(eventCtx, telemetry.Event{
				Type:  telemetry.EventSchedulerOverdue,
				JobID: job.ID,
			})
		}
		_ = telemetry.Record(eventCtx, telemetry.Event{
			Type:    telemetry.EventSchedulerClaimed,
			JobID:   job.ID,
			Payload: s.schedulerTelemetryPayload(),
		})
		run, err := s.runClaimed(ctx, runner, job, false)
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
	job, err := s.store.ClaimScheduledJob(ctx, id, s.claimOwner(), s.now(), s.claimLease)
	if err != nil {
		return state.ScheduledJobRun{}, err
	}
	return s.runClaimed(ctx, runner, job, manual)
}

func (s *Service) runClaimed(ctx context.Context, runner Runner, job state.ScheduledJob, manual bool) (state.ScheduledJobRun, error) {
	owner := s.claimOwner()
	stopHeartbeat := s.startHeartbeat(ctx, job.ID, owner)
	defer stopHeartbeat()
	defer func() { _ = s.store.ReleaseScheduledJobClaim(context.Background(), job.ID, owner) }()

	job, err := s.store.GetScheduledJob(ctx, job.ID)
	if err != nil {
		return state.ScheduledJobRun{}, err
	}
	started := s.now()
	_ = telemetry.Record(s.telemetryContext(ctx, job.ID), telemetry.Event{
		Type:    telemetry.EventSchedulerStarted,
		JobID:   job.ID,
		Payload: s.schedulerTelemetryPayload(),
	})
	output, runID, execErr := runner.RunScheduledJob(ctx, job)
	finished := s.now()
	status := RunStatusOK
	errText := ""
	blockedState := blockedTaskState(execErr)
	if blockedState == state.TaskStateBlockedWaitingUser {
		status = RunStatusBlocked
		errText = execErr.Error()
	} else if execErr != nil {
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
	if status == RunStatusBlocked {
		job.Blocked = true
		job.BlockedReason = errText
		if carrier, ok := execErr.(interface{ WorkID() string }); ok {
			job.BlockedWorkID = carrier.WorkID()
		}
	} else if execErr != nil {
		job.ConsecutiveFail++
	} else {
		job.ConsecutiveFail = 0
		job.Blocked = false
		job.BlockedReason = ""
		job.BlockedWorkID = ""
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
	job.ClaimedBy = ""
	job.ClaimExpiresAt = time.Time{}
	job.ClaimHeartbeatAt = time.Time{}
	job.UpdatedAt = finished
	if err := s.store.SaveScheduledJob(ctx, job); err != nil {
		return state.ScheduledJobRun{}, err
	}
	_ = telemetry.Record(s.telemetryContext(ctx, job.ID), telemetry.Event{
		Type:      telemetry.EventSchedulerFinished,
		JobID:     job.ID,
		TaskState: schedulerTaskState(status),
	})
	if status == RunStatusBlocked {
		return run, nil
	}
	return run, execErr
}

func blockedTaskState(err error) state.TaskState {
	if err == nil {
		return ""
	}
	carrier, ok := err.(interface{ TaskState() state.TaskState })
	if !ok {
		return ""
	}
	return carrier.TaskState()
}

func (s *Service) startHeartbeat(ctx context.Context, jobID, owner string) func() {
	interval := s.heartbeatInterval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	if interval >= s.claimLease && s.claimLease > 0 {
		interval = s.claimLease / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_ = s.store.RefreshScheduledJobClaim(context.Background(), jobID, owner, s.now(), s.claimLease)
				_ = telemetry.Record(s.telemetryContext(context.Background(), jobID), telemetry.Event{
					Type:    telemetry.EventSchedulerHeartbeat,
					JobID:   jobID,
					Payload: s.schedulerTelemetryPayload(),
				})
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *Service) claimOwner() string {
	if s.owner == "" {
		owner, err := newClaimOwner()
		if err != nil {
			return "unknown"
		}
		s.owner = owner
	}
	return s.owner
}

func newClaimOwner() (string, error) {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	id, err := newID("owner")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%s", host, os.Getpid(), id), nil
}

func newID(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}

func (s *Service) telemetryContext(ctx context.Context, jobID string) context.Context {
	if s.telemetryWriter == nil {
		return ctx
	}
	ctx = telemetry.WithWriter(ctx, s.telemetryWriter)
	return telemetry.WithFields(ctx, telemetry.Fields{JobID: jobID})
}

func schedulerTaskState(status string) string {
	switch status {
	case RunStatusOK:
		return string(state.TaskStateCompleted)
	case RunStatusBlocked:
		return string(state.TaskStateBlockedWaitingUser)
	default:
		return string(state.TaskStateFailed)
	}
}

func (s *Service) schedulerTelemetryPayload() []byte {
	payload, _ := json.Marshal(map[string]any{
		"claim_lease_ms": s.claimLease.Milliseconds(),
	})
	return payload
}
