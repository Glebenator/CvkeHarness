//go:build !linux || (!amd64 && !arm64)

package recovery

import (
	"fmt"
	"os"
)

func nativeSubvolume(*os.File) (SubvolumeIdentity, error) {
	return SubvolumeIdentity{}, fmt.Errorf("native Btrfs adapter requires Linux amd64/arm64")
}
func nativeFilesystem(*os.File) (string, error) {
	return "", fmt.Errorf("native Btrfs adapter requires Linux amd64/arm64")
}
func nativeNamespace(*os.File) (SnapshotNamespace, error) {
	return SnapshotNamespace{}, fmt.Errorf("native Btrfs adapter requires Linux amd64/arm64")
}
func nativeSnapshot(*os.File, *os.File, string, bool) error {
	return fmt.Errorf("native Btrfs adapter requires Linux amd64/arm64")
}
func nativeExchange(*os.File, string, *os.File, string) error {
	return fmt.Errorf("native Btrfs adapter requires Linux amd64/arm64")
}
func nativeSync(*os.File) error { return fmt.Errorf("native Btrfs adapter requires Linux amd64/arm64") }
func nativeSameMount(*os.File, *os.File) error {
	return fmt.Errorf("native Btrfs adapter requires Linux amd64/arm64")
}
