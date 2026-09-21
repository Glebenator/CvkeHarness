package recovery

import (
	"errors"
	"testing"
)

func TestRecoveryPlatformContract(t *testing.T) {
	engine, err := New(nil, Options{})
	if engine != nil || err == nil {
		t.Fatalf("engine created without a persistent store: %v", err)
	}
	if errors.Is(err, ErrUnsupportedPlatform) == Supported() {
		t.Fatalf("platform support and constructor disagree: %v", err)
	}
}
