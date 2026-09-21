package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RecoveryBatch separates the immutable reviewed plan from mutable, revisioned
// dispatch/repair evidence. Claims prevent resetting budgets by wrapping the
// same target operation in another batch in this controller database.
type RecoveryBatch struct {
	ID        string          `json:"id"`
	Target    string          `json:"target"`
	Policy    string          `json:"policy"`
	Digest    string          `json:"digest"`
	Manifest  json.RawMessage `json:"manifest"`
	Progress  json.RawMessage `json:"progress"`
	Status    string          `json:"status"`
	Problem   string          `json:"problem,omitempty"`
	Revision  int64           `json:"revision"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type RecoveryBatchClaim struct{ Target, OperationID string }

func (s *Store) SaveRecoveryBatch(ctx context.Context, b *RecoveryBatch, claims []RecoveryBatchClaim) error {
	if !s.Available() || b.ID == "" || !json.Valid(b.Manifest) || !json.Valid(b.Progress) || len(b.Manifest) > 64<<20 || len(b.Progress) > 64<<10 {
		return fmt.Errorf("invalid recovery batch")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	next, now := b.Revision+1, time.Now().UTC()
	if b.Revision == 0 {
		if len(claims) < 1 || len(claims) > 32 {
			return fmt.Errorf("batch requires bounded target claims")
		}
		for _, c := range claims {
			if _, err = tx.ExecContext(ctx, `INSERT INTO recovery_batch_claims(target,operation_id,batch_id) VALUES(?,?,?)`, c.Target, c.OperationID, b.ID); err != nil {
				return fmt.Errorf("target operation already belongs to a batch: %w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO recovery_batches(id,target,policy,digest,manifest,progress,status,problem,revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, b.ID, b.Target, b.Policy, b.Digest, []byte(b.Manifest), []byte(b.Progress), b.Status, b.Problem, next, now.Format(time.RFC3339Nano))
	} else {
		if len(claims) != 0 {
			return fmt.Errorf("cannot alter batch claims")
		}
		result, e := tx.ExecContext(ctx, `UPDATE recovery_batches SET progress=?,status=?,problem=?,revision=?,updated_at=? WHERE id=? AND revision=? AND target=? AND policy=? AND digest=? AND manifest=?`, []byte(b.Progress), b.Status, b.Problem, next, now.Format(time.RFC3339Nano), b.ID, b.Revision, b.Target, b.Policy, b.Digest, []byte(b.Manifest))
		err = e
		if err == nil {
			n, e := result.RowsAffected()
			err = e
			if err == nil && n != 1 {
				err = fmt.Errorf("batch changed concurrently or immutable plan changed")
			}
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO recovery_batch_events(batch_id,revision,status,progress,at) VALUES(?,?,?,?,?)`, b.ID, next, b.Status, []byte(b.Progress), now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	b.Revision, b.UpdatedAt = next, now
	return nil
}

func (s *Store) GetRecoveryBatch(ctx context.Context, id string) (RecoveryBatch, error) {
	if !s.Available() {
		return RecoveryBatch{}, fmt.Errorf("recovery state unavailable")
	}
	return scanBatch(s.db.QueryRowContext(ctx, `SELECT id,target,policy,digest,manifest,progress,status,problem,revision,updated_at FROM recovery_batches WHERE id=?`, id))
}
func (s *Store) ListRecoveryBatches(ctx context.Context) ([]RecoveryBatch, error) {
	if !s.Available() {
		return nil, fmt.Errorf("recovery state unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,target,policy,digest,manifest,progress,status,problem,revision,updated_at FROM recovery_batches ORDER BY updated_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecoveryBatch{}
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func scanBatch(row interface{ Scan(...any) error }) (RecoveryBatch, error) {
	var b RecoveryBatch
	var at string
	err := row.Scan(&b.ID, &b.Target, &b.Policy, &b.Digest, &b.Manifest, &b.Progress, &b.Status, &b.Problem, &b.Revision, &at)
	if err != nil {
		return b, err
	}
	b.UpdatedAt, err = time.Parse(time.RFC3339Nano, at)
	return b, err
}
