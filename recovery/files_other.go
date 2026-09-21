//go:build !linux && !darwin

package recovery

import "os"

// Unsupported platforms must never fall back to path-based writes with weaker
// identity, metadata or locking checks. These stubs also keep shared planning
// and configuration types available to the rest of the application.
func openDirectory(string) (*os.File, error) { return nil, ErrUnsupportedPlatform }
func parentFor(string) (*os.File, error)     { return nil, ErrUnsupportedPlatform }
func dirIdentity(*os.File) (uint64, uint64, error) {
	return 0, 0, ErrUnsupportedPlatform
}
func readRegular(*os.File, string, int64) (Fingerprint, []byte, error) {
	return Fingerprint{}, nil, ErrUnsupportedPlatform
}
func lockDirectory(*os.File) (func(), error) { return nil, ErrUnsupportedPlatform }
func writeAt(*os.File, string, []byte, Fingerprint) error {
	return ErrUnsupportedPlatform
}
func renameAt(*os.File, string, string) error { return ErrUnsupportedPlatform }
func childPrivateDirectory(*os.File, string, bool) (*os.File, error) {
	return nil, ErrUnsupportedPlatform
}
func moveAt(*os.File, string, *os.File, string) error { return ErrUnsupportedPlatform }
func removeAt(*os.File, string) error                 { return ErrUnsupportedPlatform }
func privateDirectory(string) (*os.File, error)       { return nil, ErrUnsupportedPlatform }
func platformMachineID() ([]byte, error)              { return nil, ErrUnsupportedPlatform }
