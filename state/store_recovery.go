package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// RecoveryOperation contains no file contents. Private original/candidate files
// live beside the database. Revision provides compare-and-swap journal writes.
type RecoveryOperation struct {
	ID        string          `json:"id"`
	Target    string          `json:"target"`
	Policy    string          `json:"policy"`
	Status    string          `json:"status"`
	Digest    string          `json:"digest"`
	Manifest  json.RawMessage `json:"manifest"`
	Revision  int64           `json:"revision"`
	UpdatedAt time.Time       `json:"updated_at"`
	Problem   string          `json:"problem,omitempty"`
}

func migrateRecovery(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS recovery_operations (
		 id TEXT PRIMARY KEY, target TEXT NOT NULL, policy TEXT NOT NULL,
		 status TEXT NOT NULL, digest TEXT NOT NULL, manifest BLOB NOT NULL,
		 revision INTEGER NOT NULL, updated_at TEXT NOT NULL, problem TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS recovery_events (
		 operation_id TEXT NOT NULL REFERENCES recovery_operations(id),
		 revision INTEGER NOT NULL, status TEXT NOT NULL, at TEXT NOT NULL,
		 PRIMARY KEY(operation_id, revision)
		);
		CREATE TABLE IF NOT EXISTS recovery_checks (
		 operation_id TEXT NOT NULL REFERENCES recovery_operations(id),
		 sequence INTEGER NOT NULL, revision INTEGER NOT NULL,
		 kind TEXT NOT NULL, observed_at TEXT NOT NULL, evidence BLOB NOT NULL,
		 PRIMARY KEY(operation_id, sequence)
		);
		CREATE TABLE IF NOT EXISTS recovery_guards (
		 target TEXT NOT NULL, name TEXT NOT NULL, lease BLOB NOT NULL,
		 pending BLOB, PRIMARY KEY(target,name)
		);
		CREATE TABLE IF NOT EXISTS recovery_batches (
		 id TEXT PRIMARY KEY, target TEXT NOT NULL, policy TEXT NOT NULL,
		 digest TEXT NOT NULL, manifest BLOB NOT NULL, progress BLOB NOT NULL,
		 status TEXT NOT NULL, problem TEXT NOT NULL, revision INTEGER NOT NULL,
		 updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS recovery_batch_claims (
		 target TEXT NOT NULL, operation_id TEXT NOT NULL, batch_id TEXT NOT NULL,
		 PRIMARY KEY(target,operation_id)
		);
		CREATE TABLE IF NOT EXISTS recovery_batch_events (
		 batch_id TEXT NOT NULL, revision INTEGER NOT NULL, status TEXT NOT NULL,
		 progress BLOB NOT NULL, at TEXT NOT NULL, PRIMARY KEY(batch_id,revision)
		);`)
	return err
}

// RecoveryCheck stores bounded executor measurements, separate from the
// immutable authorization manifest. No file contents or command output.
type RecoveryCheck struct {
	Sequence   int64           `json:"sequence"`
	Revision   int64           `json:"revision"`
	Kind       string          `json:"kind"`
	ObservedAt time.Time       `json:"observed_at"`
	Evidence   json.RawMessage `json:"evidence"`
}

func (s *Store) RecordRecoveryCheck(ctx context.Context, op RecoveryOperation, kind string, evidence json.RawMessage) (RecoveryCheck, error) {
	if !s.Available() || op.ID == "" || len(kind) > 64 || len(evidence) > 64<<10 || !json.Valid(evidence) {
		return RecoveryCheck{}, fmt.Errorf("invalid recovery check")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryCheck{}, err
	}
	defer tx.Rollback()
	var seq int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM recovery_checks WHERE operation_id=?`, op.ID).Scan(&seq); err != nil {
		return RecoveryCheck{}, err
	}
	if seq > 128 {
		return RecoveryCheck{}, fmt.Errorf("operation measurement limit exceeded; prepare a new reviewed operation")
	}
	out := RecoveryCheck{Sequence: seq, Revision: op.Revision, Kind: kind, ObservedAt: time.Now().UTC(), Evidence: evidence}
	result, err := tx.ExecContext(ctx, `INSERT INTO recovery_checks(operation_id,sequence,revision,kind,observed_at,evidence) SELECT id,?,?,?,?,? FROM recovery_operations WHERE id=? AND revision=? AND digest=?`, seq, op.Revision, kind, out.ObservedAt.Format(time.RFC3339Nano), []byte(evidence), op.ID, op.Revision, op.Digest)
	if err != nil {
		return out, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return out, fmt.Errorf("recovery operation changed before measurement could be recorded")
	}
	return out, tx.Commit()
}

func (s *Store) RecoveryChecks(ctx context.Context, id string) ([]RecoveryCheck, error) {
	if !s.Available() {
		return nil, fmt.Errorf("recovery requires an available state database")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,revision,kind,observed_at,evidence FROM recovery_checks WHERE operation_id=? ORDER BY sequence LIMIT 128`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var checks []RecoveryCheck
	for rows.Next() {
		var c RecoveryCheck
		var observed string
		if err = rows.Scan(&c.Sequence, &c.Revision, &c.Kind, &observed, &c.Evidence); err != nil {
			return nil, err
		}
		if c.ObservedAt, err = time.Parse(time.RFC3339Nano, observed); err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

// Path returns the configured database location, used for private recovery assets.
func (s *Store) Path() string { return s.path }

func (s *Store) SaveRecoveryOperation(ctx context.Context, op *RecoveryOperation) error {
	if !s.Available() {
		return fmt.Errorf("recovery requires an available state database")
	}
	if op.ID == "" || op.Target == "" || op.Status == "" || !json.Valid(op.Manifest) {
		return fmt.Errorf("invalid recovery record")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	next := op.Revision + 1
	if op.Revision == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO recovery_operations(id,target,policy,status,digest,manifest,revision,updated_at,problem) VALUES(?,?,?,?,?,?,?,?,?)`, op.ID, op.Target, op.Policy, op.Status, op.Digest, []byte(op.Manifest), next, now.Format(time.RFC3339Nano), op.Problem)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE recovery_operations SET status=?, manifest=?, revision=?, updated_at=?, problem=? WHERE id=? AND revision=? AND target=? AND policy=? AND digest=?`, op.Status, []byte(op.Manifest), next, now.Format(time.RFC3339Nano), op.Problem, op.ID, op.Revision, op.Target, op.Policy, op.Digest)
		if err == nil {
			n, e := result.RowsAffected()
			err = e
			if err == nil && n != 1 {
				err = fmt.Errorf("recovery journal changed concurrently")
			}
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO recovery_events(operation_id,revision,status,at) VALUES(?,?,?,?)`, op.ID, next, op.Status, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	op.Revision, op.UpdatedAt = next, now
	return nil
}

func (s *Store) GetRecoveryOperation(ctx context.Context, id string) (RecoveryOperation, error) {
	if !s.Available() {
		return RecoveryOperation{}, fmt.Errorf("recovery requires an available state database")
	}
	return scanRecovery(s.db.QueryRowContext(ctx, `SELECT id,target,policy,status,digest,manifest,revision,updated_at,problem FROM recovery_operations WHERE id=?`, id))
}

func (s *Store) ListRecoveryOperations(ctx context.Context) ([]RecoveryOperation, error) {
	if !s.Available() {
		return nil, fmt.Errorf("recovery requires an available state database")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,target,policy,status,digest,manifest,revision,updated_at,problem FROM recovery_operations ORDER BY updated_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ops := []RecoveryOperation{}
	for rows.Next() {
		op, e := scanRecovery(rows)
		if e != nil {
			return nil, e
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

func scanRecovery(row interface{ Scan(...any) error }) (RecoveryOperation, error) {
	var op RecoveryOperation
	var at string
	err := row.Scan(&op.ID, &op.Target, &op.Policy, &op.Status, &op.Digest, &op.Manifest, &op.Revision, &at, &op.Problem)
	if err != nil {
		return op, err
	}
	op.UpdatedAt, err = time.Parse(time.RFC3339Nano, at)
	return op, err
}
