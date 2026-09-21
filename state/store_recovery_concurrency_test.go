package state

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryWriterReservationBlocksHeartbeatSnapshotUpgrade(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "state with ?#%", "state.db")
	s, heartbeat := Open(path), Open(path)
	defer s.Close()
	defer heartbeat.Close()
	if !s.Available() || !heartbeat.Available() {
		t.Fatalf("open %v %v", s.Err(), heartbeat.Err())
	}
	if err := heartbeat.PutRecoveryGuard(ctx, "target", "ssh", json.RawMessage(`{"sequence":1}`)); err != nil {
		t.Fatal(err)
	}
	// The evidence writer reads before inserting. A deferred read transaction
	// would let this heartbeat commit, then fail its own upgrade to a write.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_checks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- heartbeat.PutRecoveryGuard(ctx, "target", "ssh", json.RawMessage(`{"sequence":2}`)) }()
	select {
	case err := <-done:
		t.Fatalf("heartbeat jumped ahead of pending journal writer: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = tx.ExecContext(ctx, `UPDATE recovery_guards SET lease=lease WHERE target='target'`); err != nil {
		t.Fatalf("read-to-write upgrade failed: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lease, _, err := s.RecoveryGuard(ctx, "target", "ssh")
	if err != nil || string(lease) != `{"sequence":2}` {
		t.Fatalf("heartbeat lost %s %v", lease, err)
	}
}

func TestRecoveryBusyTimeoutOnEveryPooledConnection(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "state", "state.db"))
	defer s.Close()
	ctx := context.Background()
	first, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var timeout int
	if err = second.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("new connection timeout %d %v", timeout, err)
	}
}

func TestRecoveryEvidenceAndHeartbeatConcurrentStores(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "state", "state.db")
	s, h := Open(path), Open(path)
	defer s.Close()
	defer h.Close()
	op := RecoveryOperation{ID: "operation", Target: "target", Policy: "policy", Status: "ready", Digest: "digest", Manifest: json.RawMessage(`{}`)}
	if err := s.SaveRecoveryOperation(ctx, &op); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			if err := h.PutRecoveryGuard(ctx, "target", "ssh", json.RawMessage(`{"live":true}`)); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 64; i++ {
		if _, err := s.RecordRecoveryCheck(ctx, op, "probe", json.RawMessage(`{"passed":true}`)); err != nil {
			t.Fatalf("evidence %d: %v", i, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	checks, err := s.RecoveryChecks(ctx, op.ID)
	if err != nil || len(checks) != 64 {
		t.Fatalf("evidence lost %d %v", len(checks), err)
	}
	for i, c := range checks {
		if c.Sequence != int64(i+1) {
			t.Fatal("sequence gap")
		}
	}
}
