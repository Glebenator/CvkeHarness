package state

import (
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
