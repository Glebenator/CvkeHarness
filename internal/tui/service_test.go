package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/scheduler"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
)

type fakeCronRunner struct {
	content string
}

func (r *fakeCronRunner) List(context.Context) (string, error) {
	return r.content, nil
}

func (r *fakeCronRunner) Install(_ context.Context, content string) error {
	r.content = content
	return nil
}

type fakeScheduledRunner struct{}

func (fakeScheduledRunner) RunScheduledJob(context.Context, state.ScheduledJob) (string, int64, error) {
	return "job output", 42, nil
}

func testService(t *testing.T) (*Service, *state.Store, *fakeCronRunner) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if !store.Available() {
		t.Fatalf("expected store to open, got %v", store.Err())
	}

	cfg := config.DefaultConfig()
	cfg.StateDBPath = filepath.Join(t.TempDir(), "state.db")
	cfg.MemoryDir = t.TempDir()
	cfg.LogLevel = "off"
	cfg.Provider = "openrouter"
	cfg.DefaultModel = "model-a"
	cfg.AllowedCommands = []string{"echo"}
	cfg.FavoriteModels = nil
	cfg.ApprovedModels = []string{"openrouter/model-a"}

	cron := &fakeCronRunner{content: "# existing\n* * * * * echo old\n"}
	svc := NewService(cfg, store, systemcron.New(cron), func(ctx context.Context, id string) (state.ScheduledJobRun, error) {
		return scheduler.New(store).RunNow(ctx, fakeScheduledRunner{}, id, true)
	})
	return svc, store, cron
}

func TestServiceManagesScheduledJobs(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := testService(t)
	defer store.Close()

	job, err := svc.CreateJob(ctx, "Health", "every", "5m", "check health")
	if err != nil {
		t.Fatalf("CreateJob returned error: %v", err)
	}
	if job.ID == "" || !job.Enabled {
		t.Fatalf("unexpected created job: %#v", job)
	}

	paused, err := svc.SetJobEnabled(ctx, job.ID, false)
	if err != nil {
		t.Fatalf("SetJobEnabled(false) returned error: %v", err)
	}
	if paused.Enabled || !paused.NextRunAt.IsZero() {
		t.Fatalf("expected paused job with no next run, got %#v", paused)
	}

	run, err := svc.RunJobNow(ctx, job.ID)
	if err != nil {
		t.Fatalf("RunJobNow returned error: %v", err)
	}
	if run.Status != "ok" || run.Output != "job output" || run.RunID != 42 {
		t.Fatalf("unexpected run: %#v", run)
	}

	runs, err := store.ListScheduledJobRuns(ctx, job.ID, 5)
	if err != nil {
		t.Fatalf("ListScheduledJobRuns returned error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected recorded run, got %#v", runs)
	}
}

func TestServiceCronMutationAndApprovalHelpers(t *testing.T) {
	ctx := context.Background()
	svc, store, cron := testService(t)
	defer store.Close()

	mutation, diff, err := svc.CronMutation(ctx, "add", "", "*/5 * * * *", "echo hi", "hello")
	if err != nil {
		t.Fatalf("CronMutation returned error: %v", err)
	}
	if !strings.Contains(diff, "echo hi") {
		t.Fatalf("expected diff to include new command, got %q", diff)
	}
	if err := svc.ApplyCronMutation(ctx, mutation); err != nil {
		t.Fatalf("ApplyCronMutation returned error: %v", err)
	}
	if !strings.Contains(cron.content, "echo hi") {
		t.Fatalf("expected fake crontab to be updated, got %q", cron.content)
	}

	approved, err := svc.ApproveCommand(ctx, "echo hello")
	if err != nil {
		t.Fatalf("ApproveCommand returned error: %v", err)
	}
	if len(approved) != 1 || approved[0] != "echo hello" {
		t.Fatalf("unexpected approved command segments: %#v", approved)
	}

	model, err := svc.ApproveModel(ctx, "openrouter/model-b")
	if err != nil {
		t.Fatalf("ApproveModel returned error: %v", err)
	}
	if model != "openrouter/model-b" {
		t.Fatalf("unexpected approved model: %s", model)
	}

	audits, err := store.ListSystemCronAudits(ctx, 5)
	if err != nil {
		t.Fatalf("ListSystemCronAudits returned error: %v", err)
	}
	if len(audits) != 1 || audits[0].InitiatingTool != "tui" {
		t.Fatalf("unexpected audits: %#v", audits)
	}
}

func TestModelNavigationFormAndErrorDisplay(t *testing.T) {
	svc, store, _ := testService(t)
	defer store.Close()

	m := NewModel(svc, "")
	m.snap.LoadedAt = time.Now()
	updated, _ := m.updateNormal(key("n"))
	m = updated.(Model)
	if m.mode != modeForm || m.form == nil || m.form.Action != actionJobCreate {
		t.Fatalf("expected job create form, got mode=%v form=%#v", m.mode, m.form)
	}

	updated, cmd := m.submitForm()
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected submit to return command")
	}
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if !strings.Contains(m.status, "prompt cannot be empty") {
		t.Fatalf("expected validation error in status, got %q", m.status)
	}
}

func TestModelConfirmCancel(t *testing.T) {
	svc, store, _ := testService(t)
	defer store.Close()

	m := NewModel(svc, "")
	m.mode = modeConfirm
	m.confirm = &confirmState{Title: "Delete", Body: "Delete?", Action: confirmJobDelete, JobID: "job_missing"}
	updated, _ := m.updateConfirm(key("n"))
	m = updated.(Model)
	if m.mode != modeNormal || m.confirm != nil || m.status != "Cancelled" {
		t.Fatalf("expected cancelled confirmation, got mode=%v status=%q", m.mode, m.status)
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
