package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/state"
)

func fixture(t *testing.T) (*Engine, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "target")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	s := state.Open(filepath.Join(base, "state", "state.db"))
	t.Cleanup(func() { s.Close() })
	e, err := New(s, Options{Roots: []string{root}, Policy: "test-policy"})
	if err != nil {
		t.Fatal(err)
	}
	return e, root
}

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
}
func content(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != want {
		t.Fatalf("%s: got %q/%v; want %q", path, b, err, want)
	}
}
func prepare(t *testing.T, e *Engine, changes ...Change) Operation {
	t.Helper()
	op, err := e.Prepare(context.Background(), Request{Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestReplaceCreateDeleteRecover(t *testing.T) {
	e, root := fixture(t)
	old := filepath.Join(root, "config")
	deleted := filepath.Join(root, "obsolete")
	created := filepath.Join(root, "new")
	put(t, old, "old config")
	put(t, deleted, "keep recoverable")
	op := prepare(t, e, Change{Action: "replace", Path: old, Content: "new config"}, Change{Action: "create", Path: created, Content: "created"}, Change{Action: "delete", Path: deleted})
	if op.Status != Ready {
		t.Fatal(op.Status)
	}
	ctx := context.Background()
	op, err := e.Apply(ctx, op.ID, op.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != Committed {
		t.Fatal(op.Status)
	}
	content(t, old, "new config")
	content(t, created, "created")
	if _, err = os.Stat(deleted); !os.IsNotExist(err) {
		t.Fatalf("deleted path still exists: %v", err)
	}
	op, err = e.Recover(ctx, op.ID, op.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != Recovered {
		t.Fatal(op.Status)
	}
	content(t, old, "old config")
	content(t, deleted, "keep recoverable")
	if _, err = os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file still exists: %v", err)
	}
	if info, _ := os.Stat(old); info.Mode().Perm() != 0640 {
		t.Fatal("mode not restored")
	}
	if _, err = e.Recover(ctx, op.ID, op.Digest); err != nil {
		t.Fatalf("recovery is not idempotent: %v", err)
	}
}

func TestStalePlanNeverOverwrites(t *testing.T) {
	e, root := fixture(t)
	p := filepath.Join(root, "config")
	put(t, p, "old")
	op := prepare(t, e, Change{Action: "replace", Path: p, Content: "proposal"})
	put(t, p, "concurrent edit")
	if _, err := e.Apply(context.Background(), op.ID, op.Digest); err == nil {
		t.Fatal("stale plan applied")
	}
	content(t, p, "concurrent edit")
}

func TestRestoreConflictAndPartialRecovery(t *testing.T) {
	e, root := fixture(t)
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	put(t, a, "a0")
	put(t, b, "b0")
	op := prepare(t, e, Change{Action: "replace", Path: a, Content: "a1"}, Change{Action: "replace", Path: b, Content: "b1"})
	ctx := context.Background()
	if _, err := e.Apply(ctx, op.ID, op.Digest); err != nil {
		t.Fatal(err)
	}
	put(t, a, "someone else's edit")
	op, err := e.Recover(ctx, op.ID, op.Digest)
	if err == nil || op.Status != RecoveryFailed {
		t.Fatalf("missing conflict: %s %v", op.Status, err)
	}
	content(t, a, "someone else's edit")
	content(t, b, "b0")
}

func TestCrashStagesReconcileWithoutRetryingMutation(t *testing.T) {
	for _, stage := range []string{"preparing", "backup", "ready", "applying", "applied", "verifying", "committed", "rolling_back", "restored"} {
		t.Run(stage, func(t *testing.T) {
			e, root := fixture(t)
			a := filepath.Join(root, "a")
			b := filepath.Join(root, "b")
			put(t, a, "a0")
			put(t, b, "b0")
			crash := errors.New("injected crash")
			e.fault = func(s string, i int) error {
				if s == stage {
					return crash
				}
				return nil
			}
			ctx := context.Background()
			op, err := e.Prepare(ctx, Request{Changes: []Change{{Action: "replace", Path: a, Content: "a1"}, {Action: "replace", Path: b, Content: "b1"}}})
			if err == nil {
				op, err = e.Apply(ctx, op.ID, op.Digest)
			}
			if err == nil && (stage == "rolling_back" || stage == "restored") {
				op, err = e.Recover(ctx, op.ID, op.Digest)
			}
			if !errors.Is(err, crash) {
				t.Fatalf("fault did not fire: %v", err)
			}
			// A fresh executor has no in-memory progress or provider dependency.
			fresh, err := New(e.store, Options{Roots: []string{root}, Policy: "test-policy"})
			if err != nil {
				t.Fatal(err)
			}
			op, err = fresh.Reconcile(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "preparing", "backup":
				if op.Status != PreparationFailed {
					t.Fatal(op.Status)
				}
			case "ready":
				if op.Status != Ready {
					t.Fatal(op.Status)
				}
			default:
				if stage != "committed" && op.Status != Unknown {
					t.Fatalf("interruption hidden: %s", op.Status)
				}
				if _, err = fresh.Recover(ctx, op.ID, op.Digest); err != nil {
					t.Fatal(err)
				}
			}
			content(t, a, "a0")
			content(t, b, "b0")
		})
	}
}

func TestBackupTamperPreventsApply(t *testing.T) {
	e, root := fixture(t)
	p := filepath.Join(root, "config")
	put(t, p, "original")
	op := prepare(t, e, Change{Action: "replace", Path: p, Content: "new"})
	put(t, filepath.Join(e.assets, op.ID, op.Plan.Entries[0].Backup), "tampered")
	if _, err := e.Apply(context.Background(), op.ID, op.Digest); err == nil {
		t.Fatal("applied with damaged backup")
	}
	content(t, p, "original")
}

func TestPolicyTargetAndDigestBinding(t *testing.T) {
	e, root := fixture(t)
	p := filepath.Join(root, "config")
	put(t, p, "original")
	op := prepare(t, e, Change{Action: "replace", Path: p, Content: "new"})
	ctx := context.Background()
	if _, err := e.Apply(ctx, op.ID, "different"); err == nil {
		t.Fatal("wrong digest accepted")
	}
	e.policy = "changed"
	if _, err := e.Apply(ctx, op.ID, op.Digest); err == nil {
		t.Fatal("changed policy accepted")
	}
	e.policy = op.Policy
	e.target = "different-machine"
	if _, err := e.Apply(ctx, op.ID, op.Digest); err == nil {
		t.Fatal("wrong target accepted")
	}
	content(t, p, "original")
}

func TestRefusesSymlinksHardlinksAndOutOfScope(t *testing.T) {
	e, root := fixture(t)
	p := filepath.Join(root, "original")
	put(t, p, "original")
	link := filepath.Join(root, "link")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Prepare(context.Background(), Request{Changes: []Change{{Action: "replace", Path: link, Content: "new"}}}); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err := e.Prepare(context.Background(), Request{Changes: []Change{{Action: "create", Path: filepath.Join(filepath.Dir(root), "outside"), Content: "new"}}}); err == nil {
		t.Fatal("escaped scope")
	}
	if err := os.Link(p, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Prepare(context.Background(), Request{Changes: []Change{{Action: "delete", Path: p}}}); err == nil {
		t.Fatal("accepted hardlink")
	}
	content(t, p, "original")
}

func TestDirectorySymlinkSwapDoesNotRedirectApply(t *testing.T) {
	e, root := fixture(t)
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config")
	put(t, p, "original")
	op := prepare(t, e, Change{Action: "replace", Path: p, Content: "new"})
	moved := filepath.Join(root, "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), op.ID, op.Digest); err == nil {
		t.Fatal("accepted swapped directory")
	}
	content(t, filepath.Join(moved, "config"), "original")
}

func TestByteBudgetAndPrivateArtifacts(t *testing.T) {
	e, root := fixture(t)
	e.limits.MaxBytes = 8
	e.limits.MaxFileBytes = 8
	p := filepath.Join(root, "config")
	put(t, p, "12345")
	if _, err := e.Prepare(context.Background(), Request{Changes: []Change{{Action: "replace", Path: p, Content: "6789"}}}); err == nil {
		t.Fatal("combined budget ignored")
	}
	op := prepare(t, e, Change{Action: "replace", Path: p, Content: "12"})
	for _, name := range []string{op.Plan.Entries[0].Backup, op.Plan.Entries[0].Candidate} {
		i, err := os.Stat(filepath.Join(e.assets, op.ID, name))
		if err != nil || i.Mode().Perm() != 0600 {
			t.Fatalf("artifact permissions: %v", err)
		}
	}
	if strings.Contains(string(op.Manifest), "12345") {
		t.Fatal("journal contains file contents")
	}
}
