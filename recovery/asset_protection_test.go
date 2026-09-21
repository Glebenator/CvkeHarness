package recovery

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryExecutableCannotBeReplacedByTypedFileOrSnapshot(t *testing.T) {
	e, root := fixture(t)
	path := filepath.Join(root, "cvkeharness")
	put(t, path, "fixture executable")
	e.executable = path
	if _, err := e.Prepare(context.Background(), Request{Changes: []Change{{Action: "delete", Path: path}}}); err == nil || !strings.Contains(err.Error(), "executable is protected") {
		t.Fatalf("file protection: %v", err)
	}
	target := SnapshotTarget{Name: "work", Path: root, Store: filepath.Join(filepath.Dir(root), ".cvkeharness-snapshots"), MaxEntries: 100, MaxLogicalBytes: 1024}
	if _, err := e.snapshotHandles(target); err == nil || !strings.Contains(err.Error(), "running recovery executable") {
		t.Fatalf("snapshot protection: %v", err)
	}
	content(t, path, "fixture executable")
}

func TestSnapshotCannotReplaceConfiguredServiceExecutable(t *testing.T) {
	e, root := fixture(t)
	s := testNGINXService()
	s.Binary = filepath.Join(root, "nginx")
	e.services[s.Name] = s
	target := SnapshotTarget{Name: "work", Path: root, Store: filepath.Join(filepath.Dir(root), ".cvkeharness-snapshots"), MaxEntries: 100, MaxLogicalBytes: 1024}
	if _, err := e.snapshotHandles(target); err == nil || !strings.Contains(err.Error(), "service transaction") {
		t.Fatalf("snapshot service executable protection: %v", err)
	}
}
