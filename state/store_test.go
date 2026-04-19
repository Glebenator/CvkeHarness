package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenGracefullyHandlesMissingPath(t *testing.T) {
	t.Parallel()

	store := Open("")
	if store.Available() {
		t.Fatal("expected unavailable store for empty path")
	}
	if store.Err() == nil {
		t.Fatal("expected initialization error for empty path")
	}
}

func TestOpenGracefullyHandlesCorruptSQLiteFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	store := Open(path)
	if store.Available() {
		t.Fatal("expected unavailable store for corrupt sqlite file")
	}
	if store.Err() == nil {
		t.Fatal("expected initialization error for corrupt sqlite file")
	}
}

func TestSaveAndListCommandApprovals(t *testing.T) {
	t.Parallel()

	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	if !store.Available() {
		t.Fatalf("expected store to be available, got %v", store.Err())
	}

	if err := store.SaveCommandApproval(context.Background(), CommandApproval{
		Command:   "echo hello",
		Status:    "approved",
		Source:    "llm_judge",
		Rationale: "safe diagnostic command",
	}); err != nil {
		t.Fatalf("SaveCommandApproval returned error: %v", err)
	}

	approvals, err := store.ListCommandApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListCommandApprovals returned error: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected 1 command approval, got %d", len(approvals))
	}
	if approvals[0].Command != "echo hello" {
		t.Fatalf("expected persisted command approval, got %#v", approvals[0])
	}
}
