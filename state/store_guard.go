package state

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Store) RecoveryGuardArmed(ctx context.Context, target string) (bool, error) {
	if !s.Available() {
		return false, fmt.Errorf("recovery state unavailable")
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_guards WHERE target=? AND pending IS NOT NULL`, target).Scan(&n)
	return n != 0, err
}

// The watchdog lease and pending deadline are separate durable fields. A
// heartbeat must never erase the armed operation, including after restart.
func (s *Store) PutRecoveryGuard(ctx context.Context, target, name string, lease json.RawMessage) error {
	if !s.Available() || target == "" || name == "" || len(name) > 64 || len(lease) > 16<<10 || !json.Valid(lease) {
		return fmt.Errorf("invalid recovery guard lease")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO recovery_guards(target,name,lease) VALUES(?,?,?) ON CONFLICT(target,name) DO UPDATE SET lease=excluded.lease`, target, name, []byte(lease))
	return err
}

func (s *Store) RecoveryGuard(ctx context.Context, target, name string) (lease, pending json.RawMessage, err error) {
	if !s.Available() {
		return nil, nil, fmt.Errorf("recovery state unavailable")
	}
	var leaseBytes, pendingBytes []byte
	err = s.db.QueryRowContext(ctx, `SELECT lease,pending FROM recovery_guards WHERE target=? AND name=?`, target, name).Scan(&leaseBytes, &pendingBytes)
	lease, pending = leaseBytes, pendingBytes
	return
}

func (s *Store) ArmRecoveryGuard(ctx context.Context, target, name string, pending json.RawMessage) error {
	if !s.Available() || len(pending) > 16<<10 || !json.Valid(pending) {
		return fmt.Errorf("invalid pending guard operation")
	}
	r, err := s.db.ExecContext(ctx, `UPDATE recovery_guards SET pending=? WHERE target=? AND name=? AND pending IS NULL`, []byte(pending), target, name)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("guard unavailable or already armed")
	}
	return nil
}

func (s *Store) ClearRecoveryGuard(ctx context.Context, target, name string, expected json.RawMessage) error {
	if !s.Available() || !json.Valid(expected) {
		return fmt.Errorf("invalid pending guard receipt")
	}
	r, err := s.db.ExecContext(ctx, `UPDATE recovery_guards SET pending=NULL WHERE target=? AND name=? AND pending=?`, target, name, []byte(expected))
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("pending guard operation changed")
	}
	return nil
}
