package recovery

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelRepairBudgetSurvivesRestartAndLeavesOperatorRecovery(t *testing.T) {
	e, root := fixture(t)
	path := filepath.Join(root, "config")
	put(t, path, "original")
	op := prepare(t, e, Change{Action: "replace", Path: path, Content: "changed"})
	ctx := context.Background()
	var err error
	op, err = e.Apply(ctx, op.ID, op.Digest)
	if err != nil {
		t.Fatal(err)
	}
	put(t, path, "external writer")
	for i := 0; i < 2; i++ {
		if err = e.ReserveModelRepair(ctx, op.ID, op.Digest); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Recover(ctx, op.ID, op.Digest); err == nil {
			t.Fatal("writer conflict ignored")
		}
	}
	reloaded, err := New(e.store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = reloaded.ReserveModelRepair(ctx, op.ID, op.Digest); err == nil || !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatal(err)
	}
	content(t, path, "external writer")
	// Explicit fixture/operator conflict resolution followed by direct engine
	// recovery, the same model-independent escape hatch used by the CLI.
	put(t, path, "changed")
	op, err = reloaded.Recover(ctx, op.ID, op.Digest)
	if err != nil || op.Status != Recovered {
		t.Fatalf("operator recovery stranded: %s %v", op.Status, err)
	}
	if err = reloaded.ReserveModelRepair(ctx, op.ID, op.Digest); err != nil {
		t.Fatal("idempotent terminal recovery refused")
	}
}

func TestPreparedCancellationVerifiesOriginalAndBlocksLateApply(t *testing.T) {
	e, root := fixture(t)
	path := filepath.Join(root, "config")
	put(t, path, "original")
	op := prepare(t, e, Change{Action: "replace", Path: path, Content: "changed"})
	ctx := context.Background()
	op, err := e.Recover(ctx, op.ID, op.Digest)
	if err != nil || op.Status != Recovered {
		t.Fatalf("cancel %s %v", op.Status, err)
	}
	if _, err = e.Apply(ctx, op.ID, op.Digest); err == nil {
		t.Fatal("late apply accepted")
	}
	content(t, path, "original")
	op = prepare(t, e, Change{Action: "replace", Path: path, Content: "changed"})
	put(t, path, "external writer")
	if _, err = e.Recover(ctx, op.ID, op.Digest); err == nil {
		t.Fatal("cancellation claimed original while writer differed")
	}
	content(t, path, "external writer")
}
