package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
)

func TestResolveBlockedShellCommandUnblocksScheduledJob(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatalf("store unavailable: %v", store.Err())
	}

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	job := ScheduledJob{
		ID:            "job_blocked",
		Name:          "health",
		ScheduleKind:  "every",
		ScheduleSpec:  "5m",
		Prompt:        "check health",
		Enabled:       true,
		NextRunAt:     now,
		Blocked:       true,
		BlockedReason: "waiting for approval",
		BlockedWorkID: "blocked_work",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.SaveScheduledJob(context.Background(), job); err != nil {
		t.Fatalf("SaveScheduledJob returned error: %v", err)
	}
	if _, err := store.SaveBlockedWork(context.Background(), BlockedWork{
		ID:                     "blocked_work",
		CreatedAt:              now,
		UpdatedAt:              now,
		Task:                   "check health",
		TaskClass:              core.TaskClassPolicySensitive,
		TaskState:              TaskStateBlockedWaitingUser,
		BlockedReason:          "waiting for approval",
		PendingApprovalType:    "shell_command",
		PendingApprovalPayload: "echo hello",
		ScheduledJobID:         job.ID,
	}); err != nil {
		t.Fatalf("SaveBlockedWork returned error: %v", err)
	}

	resolved, err := store.ResolveBlockedShellCommand(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("ResolveBlockedShellCommand returned error: %v", err)
	}
	if len(resolved) != 1 || resolved[0].TaskState != TaskStateRunning {
		t.Fatalf("expected one resumed blocked work item, got %#v", resolved)
	}
	got, err := store.GetScheduledJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetScheduledJob returned error: %v", err)
	}
	if got.Blocked || got.BlockedReason != "" || got.BlockedWorkID != "" {
		t.Fatalf("expected scheduled job to be unblocked, got %#v", got)
	}
}
