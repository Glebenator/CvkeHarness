package recovery

import (
	"errors"
	"runtime"
)

// ErrUnsupportedPlatform indicates that the executor cannot provide its
// filesystem identity, metadata and locking guarantees on this platform.
var ErrUnsupportedPlatform = errors.New("recovery executor is not supported on " + runtime.GOOS + "; use Linux (including WSL2) or macOS")

// Supported reports whether this platform has the safe file executor. Service
// and snapshot adapters have additional platform and filesystem requirements.
func Supported() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}
