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
