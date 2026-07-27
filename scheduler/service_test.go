package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

type fakeRunner struct {
	err error
}

func (r fakeRunner) RunScheduledJob(context.Context, state.ScheduledJob) (string, int64, error) {
	if r.err != nil {
		return "", 0, r.err
	}
	return "ok", 42, nil
}

type blockedRunnerError struct{}

func (blockedRunnerError) Error() string              { return "waiting for approval" }
func (blockedRunnerError) TaskState() state.TaskState { return state.TaskStateBlockedWaitingUser }
func (blockedRunnerError) WorkID() string             { return "blocked_work_1" }

func TestServiceLifecycle(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}
	defer store.Close()

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	svc := New(store)
	svc.now = func() time.Time { return now }

	job, err := svc.Create(context.Background(), "health", KindEvery, "5m", "check health")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !job.NextRunAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("unexpected next run: %s", job.NextRunAt)
	}

	paused, err := svc.SetEnabled(context.Background(), job.ID, false)
	if err != nil {
		t.Fatalf("SetEnabled false returned error: %v", err)
	}
	if paused.Enabled || !paused.NextRunAt.IsZero() {
		t.Fatalf("expected paused job with no next run: %#v", paused)
	}

	resumed, err := svc.SetEnabled(context.Background(), job.ID, true)
	if err != nil {
		t.Fatalf("SetEnabled true returned error: %v", err)
	}
	if !resumed.Enabled || resumed.NextRunAt.IsZero() {
		t.Fatalf("expected resumed job with next run: %#v", resumed)
	}
}

func TestRunDueRecordsSuccessAndFailure(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}
	defer store.Close()

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	svc := New(store)
	svc.now = func() time.Time { return now }
	job, err := svc.Create(context.Background(), "health", KindEvery, "5m", "check health")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	job.NextRunAt = now.Add(-time.Minute)
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}

	runs, err := svc.RunDue(context.Background(), fakeRunner{})
	if err != nil {
		t.Fatalf("RunDue returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != RunStatusOK {
		t.Fatalf("expected one ok run, got %#v", runs)
	}

	job, err = store.GetScheduledJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetScheduledJob returned error: %v", err)
	}
	job.NextRunAt = now.Add(-time.Minute)
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}
	_, err = svc.RunDue(context.Background(), fakeRunner{err: fmt.Errorf("boom")})
	if err == nil {
		t.Fatal("expected runner error")
	}
	history, err := store.ListScheduledJobRuns(context.Background(), job.ID, 10)
	if err != nil {
		t.Fatalf("ListScheduledJobRuns returned error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected two run records, got %d", len(history))
	}
}

func TestRunDueClearsClaimAndAdvancesSchedule(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}
	defer store.Close()

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	svc := New(store)
	svc.SetClaimOwner("owner-a")
	svc.now = func() time.Time { return now }
	job, err := svc.Create(context.Background(), "health", KindEvery, "5m", "check health")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	job.NextRunAt = now.Add(-time.Minute)
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}

	runs, err := svc.RunDue(context.Background(), fakeRunner{})
	if err != nil {
		t.Fatalf("RunDue returned error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %#v", runs)
	}
	got, err := store.GetScheduledJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetScheduledJob returned error: %v", err)
	}
	if got.ClaimedBy != "" || !got.ClaimExpiresAt.IsZero() || !got.ClaimHeartbeatAt.IsZero() {
		t.Fatalf("expected completed job claim to be cleared, got %#v", got)
	}
	if !got.NextRunAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("expected next run to advance, got %s", got.NextRunAt)
	}
}

func TestManualRunRefusesActivelyClaimedJob(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}
	defer store.Close()

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	svc := New(store)
	svc.SetClaimOwner("manual-owner")
	svc.now = func() time.Time { return now }
	job, err := svc.Create(context.Background(), "health", KindEvery, "5m", "check health")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.ClaimScheduledJob(context.Background(), job.ID, "daemon-owner", now, 5*time.Minute); err != nil {
		t.Fatalf("ClaimScheduledJob returned error: %v", err)
	}

	_, err = svc.RunNow(context.Background(), fakeRunner{}, job.ID, true)
	if err == nil {
		t.Fatal("expected manual run to refuse an actively claimed job")
	}
}

func TestRunDueRecordsBlockedJobWithoutRetryingUntilUserAction(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}
	defer store.Close()

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	svc := New(store)
	svc.now = func() time.Time { return now }
	job, err := svc.Create(context.Background(), "health", KindEvery, "5m", "check health")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	job.NextRunAt = now.Add(-time.Minute)
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}

	runs, err := svc.RunDue(context.Background(), fakeRunner{err: blockedRunnerError{}})
	if err != nil {
		t.Fatalf("RunDue returned unexpected error for blocked work: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != RunStatusBlocked {
		t.Fatalf("expected one blocked run, got %#v", runs)
	}
	blocked, err := store.GetScheduledJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetScheduledJob returned error: %v", err)
	}
	if !blocked.Blocked || blocked.BlockedWorkID != "blocked_work_1" {
		t.Fatalf("expected blocked scheduler metadata, got %#v", blocked)
	}

	retries, err := svc.RunDue(context.Background(), fakeRunner{})
	if err != nil {
		t.Fatalf("second RunDue returned error: %v", err)
	}
	if len(retries) != 0 {
		t.Fatalf("expected blocked job to avoid noisy retries, got %#v", retries)
	}
}
