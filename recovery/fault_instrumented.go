//go:build recoveryfault

package recovery

import (
	"os"
	"strconv"
)

// This hook only exists in explicitly instrumented lab binaries. No normal
// build reads these environment variables or exposes a fault-injection switch.
func buildFaultHook() func(string, int) error {
	return func(stage string, index int) error {
		if os.Getenv("CVKE_RECOVERY_FAULT") == stage && (os.Getenv("CVKE_RECOVERY_FAULT_INDEX") == "" || os.Getenv("CVKE_RECOVERY_FAULT_INDEX") == strconv.Itoa(index)) {
			os.Exit(86)
		}
		return nil
	}
}
