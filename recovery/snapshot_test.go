package recovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotRefusesUnavailableNativeFilesystem(t *testing.T) {
	e, root := fixture(t)
	e.snapshots["work"] = SnapshotTarget{Name: "work", Path: root, Store: filepath.Join(filepath.Dir(root), ".cvkeharness-snapshots"), MaxEntries: 100, MaxLogicalBytes: 1 << 20}
	put(t, filepath.Join(root, "settings"), "original")
	_, err := e.PrepareSnapshot(context.Background(), "work")
	if err == nil {
		t.Skip("test temp directory happens to be an eligible Btrfs subvolume; real adapter success is tested by the VM lab")
	}
	if !strings.Contains(err.Error(), "Btrfs") {
		t.Fatalf("unexpected capability refusal: %v", err)
	}
	content(t, filepath.Join(root, "settings"), "original")
}

func TestSnapshotTargetsAreExplicitAndProtectState(t *testing.T) {
	e, root := fixture(t)
	if _, err := e.PrepareSnapshot(context.Background(), "unconfigured"); err == nil {
		t.Fatal("unconfigured native target accepted")
	}
	for name, path := range map[string]string{"state": filepath.Dir(e.assets), "whole": "/", "credential": filepath.Join(root, ".ssh")} {
		if name == "credential" {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		e.snapshots[name] = SnapshotTarget{Name: name, Path: path, Store: filepath.Join(filepath.Dir(path), ".cvkeharness-snapshots"), MaxEntries: 100, MaxLogicalBytes: 1 << 20}
		if _, err := e.PrepareSnapshot(context.Background(), name); err == nil {
			t.Fatalf("protected native target accepted: %s", name)
		}
	}
}
