package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/scheduler"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
	"github.com/coolcake/cvkeharness/tools"
)

// RunJobFunc lets the cmd package provide the real agent runner without
// coupling this package back to Cobra internals.
type RunJobFunc func(context.Context, string) (state.ScheduledJobRun, error)

// Service centralizes command behavior reused by the full-screen TUI.
type Service struct {
	cfg       *config.Config
	store     *state.Store
	cron      *systemcron.Client
	runJobNow RunJobFunc
}

// NewService creates the TUI service facade.
func NewService(cfg *config.Config, store *state.Store, cron *systemcron.Client, runJobNow RunJobFunc) *Service {
	if cron == nil {
		cron = systemcron.New(nil)
	}
	return &Service{cfg: cfg, store: store, cron: cron, runJobNow: runJobNow}
}

// Snapshot is all data needed to render the TUI without doing work in View.
type Snapshot struct {
	Jobs             []state.ScheduledJob
	JobRuns          map[string][]state.ScheduledJobRun
	CronEntries      []systemcron.Entry
	RawCron          string
	CronAudits       []state.SystemCronAudit
	FavoriteModels   []string
	ApprovedModels   []string
	Routing          []state.RoutingCandidate
	RecentModels     []state.RecentModelUsage
	ModelAliases     []state.ModelAlias
	ModelStats       []state.ModelStats
	CommandAllow     []string
	CommandApprovals []state.CommandApproval
	Memory           string
	MemoryEntries    []state.MemoryEntry
	Snapshots        []state.Snapshot
	Runs             []state.RunSummary
	Chats            []state.ChatSessionSummary
	ChatDetails      map[int64]state.ChatSessionDetail
	LoadedAt         time.Time
}

// LoadSnapshot reads all dashboard data. It keeps partial sections useful when
// optional host features, such as crontab, are unavailable.
func (s *Service) LoadSnapshot(ctx context.Context) (Snapshot, error) {
	if s == nil || s.cfg == nil || s.store == nil {
		return Snapshot{}, fmt.Errorf("TUI service is not configured")
	}
	if !s.store.Available() {
		return Snapshot{}, fmt.Errorf("state database unavailable: %w", s.store.Err())
	}

	var firstErr error
	remember := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	snap := Snapshot{
		JobRuns:     make(map[string][]state.ScheduledJobRun),
		ChatDetails: make(map[int64]state.ChatSessionDetail),
		LoadedAt:    time.Now(),
	}

	var err error
	snap.Jobs, err = s.store.ListScheduledJobs(ctx, true)
	remember(err)
	for _, job := range snap.Jobs {
		runs, runErr := s.store.ListScheduledJobRuns(ctx, job.ID, 10)
		remember(runErr)
		if runErr == nil {
			snap.JobRuns[job.ID] = runs
		}
	}

	snap.CronEntries, snap.RawCron, err = s.cron.List(ctx)
	remember(err)
	snap.CronAudits, err = s.store.ListSystemCronAudits(ctx, 20)
	remember(err)

	snap.FavoriteModels = append([]string(nil), s.cfg.FavoriteModels...)
	sort.Strings(snap.FavoriteModels)
	snap.ApprovedModels = append([]string(nil), s.cfg.ApprovedModels...)
	sort.Strings(snap.ApprovedModels)
	snap.Routing, err = s.store.ListRoutingCandidates(ctx)
	remember(err)
	snap.RecentModels, err = s.store.ListRecentModelUsage(ctx, 20)
	remember(err)
	snap.ModelAliases, err = s.store.ListModelAliases(ctx)
	remember(err)
	snap.ModelStats, err = s.store.ListAllModelStats(ctx)
	remember(err)

	snap.CommandAllow = append([]string(nil), s.cfg.AllowedCommands...)
	sort.Strings(snap.CommandAllow)
	snap.CommandApprovals, err = s.store.ListCommandApprovals(ctx)
	remember(err)

	mem := memory.NewManager(s.cfg.MemoryDir, s.store, s.cfg.MemoryMaxSnippets)
	snap.Memory, err = mem.Show(ctx)
	remember(err)
	snap.MemoryEntries, err = s.store.ListMemoryEntries(ctx, state.MemoryFilter{OnlyActive: true})
	remember(err)
	snap.Snapshots, err = s.store.ListSnapshots(ctx)
	remember(err)

	snap.Runs, err = s.store.ListRecentRuns(ctx, 20)
	remember(err)
	snap.Chats, err = s.store.ListRecentChatSessions(ctx, 20)
	remember(err)
	for _, chat := range snap.Chats {
		detail, detailErr := s.store.GetChatSessionDetail(ctx, chat.ID)
		remember(detailErr)
		if detailErr == nil {
			snap.ChatDetails[chat.ID] = detail
		}
	}

	return snap, firstErr
}

func (s *Service) CreateJob(ctx context.Context, name, kind, spec, prompt string) (state.ScheduledJob, error) {
	return scheduler.New(s.store).Create(ctx, name, kind, spec, prompt)
}

func (s *Service) UpdateJob(ctx context.Context, id, name, kind, spec, prompt string) (state.ScheduledJob, error) {
	return scheduler.New(s.store).Update(ctx, id, name, kind, spec, prompt)
}

func (s *Service) SetJobEnabled(ctx context.Context, id string, enabled bool) (state.ScheduledJob, error) {
	return scheduler.New(s.store).SetEnabled(ctx, id, enabled)
}

func (s *Service) DeleteJob(ctx context.Context, id string) error {
	return s.store.DeleteScheduledJob(ctx, id)
}

func (s *Service) RunJobNow(ctx context.Context, id string) (state.ScheduledJobRun, error) {
	if s.runJobNow == nil {
		return state.ScheduledJobRun{}, fmt.Errorf("run-now is unavailable")
	}
	return s.runJobNow(ctx, id)
}

func (s *Service) CronMutation(ctx context.Context, action, target, schedule, command, name string) (systemcron.Mutation, string, error) {
	var (
		mutation systemcron.Mutation
		err      error
	)
	switch action {
	case "add":
		mutation, err = s.cron.Add(ctx, schedule, command, name)
	case "update":
		mutation, err = s.cron.Update(ctx, target, schedule, command)
	case "remove":
		mutation, err = s.cron.Remove(ctx, target)
	case "enable":
		mutation, err = s.cron.SetEnabled(ctx, target, true)
	case "disable":
		mutation, err = s.cron.SetEnabled(ctx, target, false)
	default:
		return systemcron.Mutation{}, "", fmt.Errorf("unsupported cron action %q", action)
	}
	if err != nil {
		return systemcron.Mutation{}, "", err
	}
	return mutation, systemcron.Diff(mutation.OldContent, mutation.NewContent), nil
}

func (s *Service) ApplyCronMutation(ctx context.Context, mutation systemcron.Mutation) error {
	err := s.cron.Apply(ctx, mutation)
	success := err == nil
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = s.store.RecordSystemCronAudit(ctx, state.SystemCronAudit{
		Action:         mutation.Action,
		Target:         mutation.Target,
		OldSnippet:     mutation.OldContent,
		NewSnippet:     mutation.NewContent,
		Success:        success,
		ErrorMessage:   msg,
		InitiatingTool: "tui",
	})
	return err
}

func (s *Service) FavoriteModel(raw string, favorite bool) (string, error) {
	normalized, err := normalizeModelArg(s.cfg, raw)
	if err != nil {
		return "", err
	}
	set := make(map[string]bool, len(s.cfg.FavoriteModels)+1)
	for _, item := range s.cfg.FavoriteModels {
		set[item] = true
	}
	if favorite {
		set[normalized] = true
	} else {
		delete(set, normalized)
	}
	s.cfg.FavoriteModels = s.cfg.FavoriteModels[:0]
	for item := range set {
		s.cfg.FavoriteModels = append(s.cfg.FavoriteModels, item)
	}
	sort.Strings(s.cfg.FavoriteModels)
	return normalized, s.cfg.Save()
}

func (s *Service) ApproveModel(ctx context.Context, raw string) (string, error) {
	normalized, err := normalizeModelArg(s.cfg, raw)
	if err != nil {
		return "", err
	}
	if !contains(s.cfg.ApprovedModels, normalized) {
		s.cfg.ApprovedModels = append(s.cfg.ApprovedModels, normalized)
		sort.Strings(s.cfg.ApprovedModels)
	}
	if err := s.cfg.Save(); err != nil {
		return "", err
	}
	ref := core.ParseModelRef(normalized, s.cfg.Provider)
	return normalized, s.store.SaveModelApproval(ctx, state.ModelApproval{
		Provider:  ref.Provider,
		Model:     ref.Model,
		Status:    state.ApprovalStatusApproved,
		Source:    "tui",
		Rationale: "user approved via tui",
	})
}

func (s *Service) ApproveCommand(ctx context.Context, raw string) ([]string, error) {
	parsed, err := tools.ParseShellCommand(raw)
	if err != nil {
		return nil, err
	}
	if len(parsed.Segments) == 0 {
		return nil, fmt.Errorf("command cannot be empty")
	}
	now := time.Now().UTC()
	approved := make([]string, 0, len(parsed.Segments))
	for _, segment := range parsed.Segments {
		if err := s.store.SaveCommandApproval(ctx, state.CommandApproval{
			Command:    segment.Normalized,
			Status:     state.ApprovalStatusApproved,
			Source:     "tui",
			Rationale:  "user approved via tui",
			ApprovedAt: now,
		}); err != nil {
			return nil, err
		}
		approved = append(approved, segment.Normalized)
	}
	return approved, nil
}

func (s *Service) ReindexMemory(ctx context.Context) error {
	mem := memory.NewManager(s.cfg.MemoryDir, s.store, s.cfg.MemoryMaxSnippets)
	return mem.Reindex(ctx)
}

func (s *Service) RollbackMemory(ctx context.Context, snapshotID string) error {
	mem := memory.NewManager(s.cfg.MemoryDir, s.store, s.cfg.MemoryMaxSnippets)
	return mem.Rollback(ctx, snapshotID)
}

func normalizeModelArg(cfg *config.Config, raw string) (string, error) {
	ref := core.ParseModelRef(raw, cfg.Provider)
	if ref.IsZero() {
		return "", fmt.Errorf("invalid model reference %q", raw)
	}
	return ref.String(), nil
}

func contains(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
