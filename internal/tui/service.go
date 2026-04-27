package tui

import (
	"context"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/scheduler"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
)

// RunJobFunc triggers a scheduled job by ID.
type RunJobFunc func(ctx context.Context, id string) (state.ScheduledJobRun, error)

// Service provides all data the TUI needs.
type Service struct {
	cfg       *config.Config
	store     *state.Store
	cron      *systemcron.Client
	scheduler *scheduler.Service
	runJobNow RunJobFunc
}

// NewService creates a dashboard data service.
func NewService(
	cfg *config.Config,
	store *state.Store,
	cron *systemcron.Client,
	sched *scheduler.Service,
	runJobNow RunJobFunc,
) *Service {
	return &Service{
		cfg:       cfg,
		store:     store,
		cron:      cron,
		scheduler: sched,
		runJobNow: runJobNow,
	}
}

// Config returns the loaded configuration.
func (s *Service) Config() *config.Config { return s.cfg }

// RecentRuns returns the most recent agent runs.
func (s *Service) RecentRuns(ctx context.Context, limit int) ([]state.RunSummary, error) {
	if s.store == nil || !s.store.Available() {
		return nil, nil
	}
	return s.store.ListRecentRuns(ctx, limit)
}

// RecentChatSessions returns the most recent chat sessions.
func (s *Service) RecentChatSessions(ctx context.Context, limit int) ([]state.ChatSessionSummary, error) {
	if s.store == nil || !s.store.Available() {
		return nil, nil
	}
	return s.store.ListRecentChatSessions(ctx, limit)
}

// ChatSessionDetail returns full detail for a chat session.
func (s *Service) ChatSessionDetail(ctx context.Context, id int64) (state.ChatSessionDetail, error) {
	if s.store == nil || !s.store.Available() {
		return state.ChatSessionDetail{}, nil
	}
	return s.store.GetChatSessionDetail(ctx, id)
}

// ScheduledJobs returns all scheduled jobs.
func (s *Service) ScheduledJobs(ctx context.Context) ([]state.ScheduledJob, error) {
	if s.store == nil || !s.store.Available() {
		return nil, nil
	}
	return s.store.ListScheduledJobs(ctx, true)
}

// ScheduledJobRuns returns run history for a job.
func (s *Service) ScheduledJobRuns(ctx context.Context, jobID string, limit int) ([]state.ScheduledJobRun, error) {
	if s.store == nil || !s.store.Available() {
		return nil, nil
	}
	return s.store.ListScheduledJobRuns(ctx, jobID, limit)
}

// CronEntries returns system crontab entries.
func (s *Service) CronEntries(ctx context.Context) ([]systemcron.Entry, error) {
	if s.cron == nil {
		return nil, nil
	}
	entries, _, err := s.cron.List(ctx)
	return entries, err
}

// CronAudits returns system crontab audit records.
func (s *Service) CronAudits(ctx context.Context, limit int) ([]state.SystemCronAudit, error) {
	if s.store == nil || !s.store.Available() {
		return nil, nil
	}
	return s.store.ListSystemCronAudits(ctx, limit)
}

// RunJobNow triggers a one-off execution of a scheduled job.
func (s *Service) RunJobNow(ctx context.Context, id string) (state.ScheduledJobRun, error) {
	if s.runJobNow == nil {
		return state.ScheduledJobRun{}, nil
	}
	return s.runJobNow(ctx, id)
}

// ModelStats returns normalized model performance stats.
func (s *Service) ModelStats(ctx context.Context) ([]state.ModelStats, error) {
	if s.store == nil || !s.store.Available() {
		return nil, nil
	}
	return s.store.ListAllModelStats(ctx)
}

// CreateJob creates a new scheduled job.
func (s *Service) CreateJob(ctx context.Context, name, kind, spec, prompt string) (state.ScheduledJob, error) {
	if s.scheduler == nil {
		return state.ScheduledJob{}, nil
	}
	return s.scheduler.Create(ctx, name, kind, spec, prompt)
}

// DeleteJob removes a scheduled job.
func (s *Service) DeleteJob(ctx context.Context, id string) error {
	if s.store == nil || !s.store.Available() {
		return nil
	}
	return s.store.DeleteScheduledJob(ctx, id)
}

// SetJobEnabled pauses or resumes a job.
func (s *Service) SetJobEnabled(ctx context.Context, id string, enabled bool) (state.ScheduledJob, error) {
	if s.scheduler == nil {
		return state.ScheduledJob{}, nil
	}
	return s.scheduler.SetEnabled(ctx, id, enabled)
}
