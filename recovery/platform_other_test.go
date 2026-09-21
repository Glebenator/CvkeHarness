//go:build !linux && !darwin

package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryPlatformRefusesFilesystemOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "must-not-be-created")
	check := func(err error) {
		t.Helper()
		if !errors.Is(err, ErrUnsupportedPlatform) {
			t.Fatalf("expected unsupported platform, got %v", err)
		}
	}
	_, err := privateDirectory(path)
	check(err)
	_, err = openDirectory(path)
	check(err)
	_, err = parentFor(path)
	check(err)
	_, _, err = dirIdentity(nil)
	check(err)
	_, _, err = readRegular(nil, "target", 1024)
	check(err)
	_, err = lockDirectory(nil)
	check(err)
	check(writeAt(nil, "target", []byte("new"), Fingerprint{}))
	check(renameAt(nil, "old", "new"))
	_, err = childPrivateDirectory(nil, "private", true)
	check(err)
	check(moveAt(nil, "old", nil, "new"))
	check(removeAt(nil, "target"))
	_, err = platformMachineID()
	check(err)
	_, err = snapshotInventory(context.Background(), nil, SnapshotTarget{})
	check(err)
	_, _, err = openSnapshotArtifact(nil, "checkpoint")
	check(err)
	engine := &Engine{}
	check(engine.checkCapacity(context.Background(), nil))
	check(engine.checkSnapshotCapacity(context.Background(), nil, nil))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unsupported operation created an artifact: %v", err)
	}
}
