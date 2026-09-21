package state

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRecoveryGuardHeartbeatCannotErasePendingDeadline(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "state.db")
	s := Open(path)
	lease := json.RawMessage(`{"heartbeat":1}`)
	if err := s.PutRecoveryGuard(ctx, "machine", "ssh", lease); err != nil {
		t.Fatal(err)
	}
	got, pending, err := s.RecoveryGuard(ctx, "machine", "ssh")
	if err != nil || string(got) != string(lease) || len(pending) != 0 {
		t.Fatalf("idle guard: %s %s %v", got, pending, err)
	}
	armed := json.RawMessage(`{"id":"operation","deadline":1234}`)
	if err = s.ArmRecoveryGuard(ctx, "machine", "ssh", armed); err != nil {
		t.Fatal(err)
	}
	if err = s.ArmRecoveryGuard(ctx, "machine", "ssh", json.RawMessage(`{"id":"other"}`)); err == nil {
		t.Fatal("pending deadline overwritten")
	}
	if err = s.PutRecoveryGuard(ctx, "machine", "ssh", json.RawMessage(`{"heartbeat":2}`)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = Open(path)
	defer s.Close()
	got, pending, err = s.RecoveryGuard(ctx, "machine", "ssh")
	if err != nil || string(got) != `{"heartbeat":2}` || string(pending) != string(armed) {
		t.Fatalf("persistent guard: %s %s %v", got, pending, err)
	}
	if yes, err := s.RecoveryGuardArmed(ctx, "machine"); err != nil || !yes {
		t.Fatalf("armed lookup: %t %v", yes, err)
	}
	if yes, err := s.RecoveryGuardArmed(ctx, "different-machine"); err != nil || yes {
		t.Fatalf("cross-target lookup: %t %v", yes, err)
	}
	if err = s.ClearRecoveryGuard(ctx, "machine", "ssh", json.RawMessage(`{"id":"other"}`)); err == nil {
		t.Fatal("different operation disarmed watchdog")
	}
	if err = s.ClearRecoveryGuard(ctx, "machine", "ssh", armed); err != nil {
		t.Fatal(err)
	}
	_, pending, err = s.RecoveryGuard(ctx, "machine", "ssh")
	if err != nil || len(pending) != 0 {
		t.Fatalf("cleared guard: %s %v", pending, err)
	}
}
