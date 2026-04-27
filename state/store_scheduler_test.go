package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestClaimDueScheduledJobsOnlyOneOwner(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	job := ScheduledJob{
		ID:           "job_one",
		Name:         "health",
		ScheduleKind: "every",
		ScheduleSpec: "5m",
		Prompt:       "check",
		Enabled:      true,
		NextRunAt:    now.Add(-time.Minute),
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
	}
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}

	first, err := store.ClaimDueScheduledJobs(context.Background(), "owner-a", now, 5*time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimDueScheduledJobs owner-a returned error: %v", err)
	}
	second, err := store.ClaimDueScheduledJobs(context.Background(), "owner-b", now, 5*time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimDueScheduledJobs owner-b returned error: %v", err)
	}
	if len(first) != 1 || first[0].ClaimedBy != "owner-a" {
		t.Fatalf("expected owner-a to claim job, got %#v", first)
	}
	if len(second) != 0 {
		t.Fatalf("expected owner-b to claim no jobs, got %#v", second)
	}
}

func TestExpiredClaimCanBeReclaimed(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	job := ScheduledJob{
		ID:               "job_expired",
		Name:             "health",
		ScheduleKind:     "every",
		ScheduleSpec:     "5m",
		Prompt:           "check",
		Enabled:          true,
		NextRunAt:        now.Add(-time.Minute),
		ClaimedBy:        "old-owner",
		ClaimExpiresAt:   now.Add(-time.Second),
		ClaimHeartbeatAt: now.Add(-10 * time.Minute),
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now.Add(-time.Hour),
	}
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}

	claimed, err := store.ClaimDueScheduledJobs(context.Background(), "new-owner", now, 5*time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimDueScheduledJobs returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ClaimedBy != "new-owner" {
		t.Fatalf("expected new owner to reclaim expired job, got %#v", claimed)
	}
}

func TestRefreshScheduledJobClaimExtendsLease(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}

	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	job := ScheduledJob{
		ID:           "job_refresh",
		Name:         "health",
		ScheduleKind: "every",
		ScheduleSpec: "5m",
		Prompt:       "check",
		Enabled:      true,
		NextRunAt:    now.Add(-time.Minute),
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
	}
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}
	if _, err := store.ClaimScheduledJob(context.Background(), job.ID, "owner", now, time.Minute); err != nil {
		t.Fatalf("ClaimScheduledJob returned error: %v", err)
	}
	later := now.Add(30 * time.Second)
	if err := store.RefreshScheduledJobClaim(context.Background(), job.ID, "owner", later, 5*time.Minute); err != nil {
		t.Fatalf("RefreshScheduledJobClaim returned error: %v", err)
	}
	got, err := store.GetScheduledJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetScheduledJob returned error: %v", err)
	}
	if !got.ClaimHeartbeatAt.Equal(later) || !got.ClaimExpiresAt.Equal(later.Add(5*time.Minute)) {
		t.Fatalf("expected refreshed lease, got expires=%s heartbeat=%s", got.ClaimExpiresAt, got.ClaimHeartbeatAt)
	}
}
