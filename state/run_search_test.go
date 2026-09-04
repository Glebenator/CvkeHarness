package state

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestRunSearchFindsOlderResultsAndPaginatesWithoutDuplicates(t *testing.T) {
	db := Open(filepath.Join(t.TempDir(), "state.db"))
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	for i := 0; i < 60; i++ {
		task := fmt.Sprintf("inspect worker %d", i)
		if err := db.RecordRun(ctx, RunRecord{StartedAt: now, FinishedAt: now, Task: task, Success: i%2 == 0, FinalOutput: "literal 100% completion", Tools: []ToolOutcome{{ToolName: "shell", Command: "ssh staging.example status"}}}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := db.SearchRuns(ctx, 25, 0, "", "all")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.SearchRuns(ctx, 25, 25, "", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 25 || len(b) != 25 || a[24].ID <= b[0].ID {
		t.Fatal("unstable pagination")
	}
	results, err := db.SearchRuns(ctx, 25, 0, "worker 0", "success")
	if err != nil || len(results) != 1 {
		t.Fatalf("older search lost result: %d %v", len(results), err)
	}
	results, err = db.SearchRuns(ctx, 100, 0, "staging.example", "failed")
	if err != nil || len(results) != 30 {
		t.Fatalf("command/status search: %d %v", len(results), err)
	}
	results, err = db.SearchRuns(ctx, 100, 0, "100%", "all")
	if err != nil || len(results) != 60 {
		t.Fatalf("literal query failed: %d %v", len(results), err)
	}
}

func TestActivityTotalsIncludeHistoryBeyondVisiblePage(t *testing.T) {
	db := Open(filepath.Join(t.TempDir(), "state.db"))
	defer db.Close()
	ctx := context.Background()
	for i := 0; i < 105; i++ {
		if err := db.RecordRun(ctx, RunRecord{StartedAt: time.Now(), Success: i < 100}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ {
		if _, err := db.StartChatSession(ctx, ChatSession{StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := db.ActivityTotals(ctx)
	if err != nil || totals.Runs != 105 || totals.SuccessfulRuns != 100 || totals.ChatSessions != 7 {
		t.Fatalf("incorrect totals: %+v %v", totals, err)
	}
}

func TestRunTargetFilterUsesRecordedIdentityAndMigratesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := Open(path)
	ctx := context.Background()
	if err := db.RecordRun(ctx, RunRecord{StartedAt: time.Now(), Task: "check target-prod", Success: true}); err != nil {
		t.Fatal(err)
	}
	// Recreate the prior schema, then reopen through the real migration path.
	for _, column := range []string{"target_id", "target_environment", "target_ambiguous"} {
		if _, err := db.db.ExecContext(ctx, "ALTER TABLE runs DROP COLUMN "+column); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db = Open(path)
	defer db.Close()
	for _, id := range []string{"target-prod", "target-prod-2"} {
		if err := db.RecordRun(ctx, RunRecord{StartedAt: time.Now(), Task: "inspect", Success: true, TargetID: id, TargetEnvironment: "production", TargetAmbiguous: true}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := db.SearchRunsForTarget(ctx, 25, 0, "", "success", RunTargetFilter{ID: "target-prod"})
	if err != nil || len(runs) != 1 {
		t.Fatalf("exact target filter: %+v %v", runs, err)
	}
	if runs[0].TargetEnvironment != "production" || !runs[0].TargetAmbiguous {
		t.Fatalf("lost target context: %+v", runs[0])
	}
	unknown, err := db.SearchRunsForTarget(ctx, 25, 0, "target-prod", "all", RunTargetFilter{Unknown: true})
	if err != nil || len(unknown) != 1 || unknown[0].TargetID != "" {
		t.Fatalf("legacy target was inferred: %+v %v", unknown, err)
	}
	targets, err := db.RunTargets(ctx)
	if err != nil || len(targets) != 2 || targets[0] != "target-prod" || targets[1] != "target-prod-2" {
		t.Fatalf("target choices: %v %v", targets, err)
	}
}
