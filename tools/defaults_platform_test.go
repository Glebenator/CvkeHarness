package tools

import (
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

func TestDefaultRegistryRecoveryPlatformSupport(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state", "state.db"))
	t.Cleanup(func() { store.Close() })
	if !store.Available() {
		t.Fatal(store.Err())
	}
	policy, err := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewDefaultRegistryFromOptions(DefaultRegistryOptions{
		Store: store, SecurityPolicy: &policy, BlockManualApprovals: true,
	})
	if err != nil {
		t.Fatalf("runtime initialization failed: %v", err)
	}
	for _, name := range []string{"shell_execute", "safety_calculate", "schedule_manage"} {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("ordinary tool %q disappeared", name)
		}
	}
	for _, name := range []string{"recovery_manage", "recovery_fleet"} {
		if _, ok := registry.Get(name); ok != recovery.Supported() {
			t.Errorf("tool %q advertised without platform support, or absent on a supported platform", name)
		}
	}
}
