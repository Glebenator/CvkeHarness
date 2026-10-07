package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// UserEndpoint records an operator's endpoint label, not verified machine
// identity, operational knowledge, or permission to execute on that endpoint.
// It is independent of operational-memory imports, expiry, and promotion.
type UserEndpoint struct {
	Name         string
	Endpoint     string
	Declaration  string
	DeclaredAt   time.Time
	EvidenceHash string
}

func (s *Store) SaveUserEndpoint(ctx context.Context, item UserEndpoint) error {
	if !s.Available() {
		return s.Err()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_endpoints
		(name, endpoint, declaration, declared_at, evidence_hash) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET endpoint=excluded.endpoint,
		declaration=excluded.declaration, declared_at=excluded.declared_at,
		evidence_hash=excluded.evidence_hash`,
		item.Name, item.Endpoint, item.Declaration, item.DeclaredAt.UTC(), item.EvidenceHash)
	return err
}

func (s *Store) ListUserEndpoints(ctx context.Context) ([]UserEndpoint, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, endpoint, declaration, declared_at, evidence_hash
		FROM user_endpoints ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []UserEndpoint
	for rows.Next() {
		var item UserEndpoint
		if err := rows.Scan(&item.Name, &item.Endpoint, &item.Declaration, &item.DeclaredAt, &item.EvidenceHash); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ForgetUserEndpoint(ctx context.Context, name string) error {
	if !s.Available() {
		return s.Err()
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_endpoints WHERE name = ?`, name)
	return err
}

// CaptureUserEndpoint inserts a declaration without silently replacing a name.
// The transaction serializes the existence check and write. Capture keys outlive
// forget so an old resumed turn cannot resurrect a deleted declaration.
func (s *Store) CaptureUserEndpoint(ctx context.Context, item UserEndpoint, turnID string) (string, error) {
	if !s.Available() {
		return "failed", s.Err()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "failed", err
	}
	defer tx.Rollback()
	var endpoint string
	err = tx.QueryRowContext(ctx, `SELECT endpoint FROM user_endpoints WHERE name=?`, item.Name).Scan(&endpoint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "failed", err
	}
	status := "saved"
	if err == nil {
		if endpoint != item.Endpoint {
			return "needs_clarification", nil
		}
		status = "unchanged"
	}
	if turnID != "" {
		var exists int
		e := tx.QueryRowContext(ctx, `SELECT 1 FROM endpoint_captures WHERE turn_id=? AND name=?`, turnID, item.Name).Scan(&exists)
		if e == nil && err != nil {
			return "needs_clarification", nil
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return "failed", e
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO endpoint_captures(turn_id,name) VALUES (?,?)`, turnID, item.Name); e != nil {
			return "failed", e
		}
	}
	if status == "saved" {
		_, err = tx.ExecContext(ctx, `INSERT INTO user_endpoints(name,endpoint,declaration,declared_at,evidence_hash) VALUES (?,?,?,?,?)`, item.Name, item.Endpoint, item.Declaration, item.DeclaredAt.UTC(), item.EvidenceHash)
		if err != nil {
			return "failed", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "failed", err
	}
	return status, nil
}
