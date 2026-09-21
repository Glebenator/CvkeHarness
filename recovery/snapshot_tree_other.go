//go:build !linux && !darwin

package recovery

import (
	"context"
	"os"
)

func snapshotInventory(context.Context, *os.File, SnapshotTarget) (SnapshotInventory, error) {
	return SnapshotInventory{}, ErrUnsupportedPlatform
}
func openSnapshotArtifact(*os.File, string) (*os.File, SubvolumeIdentity, error) {
	return nil, SubvolumeIdentity{}, ErrUnsupportedPlatform
}
func (e *Engine) checkSnapshotCapacity(context.Context, *Operation, *os.File) error {
	return ErrUnsupportedPlatform
}
