package state

import (
	"context"
	"time"
)

// SaveModelApproval upserts a model approval.
func (s *Store) SaveModelApproval(ctx context.Context, approval ModelApproval) error {
	if !s.Available() {
		return s.Err()
	}
	if approval.ApprovedAt.IsZero() {
		approval.ApprovedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO model_approvals (provider, model, status, source, rationale, approved_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, model) DO UPDATE SET
			status = excluded.status,
			source = excluded.source,
			rationale = excluded.rationale,
			approved_at = excluded.approved_at`,
		approval.Provider, approval.Model, approval.Status, approval.Source, approval.Rationale, approval.ApprovedAt.UTC(),
	)
	return err
}

// ListModelApprovals returns approvals ordered by newest first.
func (s *Store) ListModelApprovals(ctx context.Context) ([]ModelApproval, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, model, status, source, rationale, approved_at
		FROM model_approvals
		ORDER BY approved_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ModelApproval
	for rows.Next() {
		var item ModelApproval
		if err := rows.Scan(&item.Provider, &item.Model, &item.Status, &item.Source, &item.Rationale, &item.ApprovedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListApprovedModelApprovals returns durable reusable model approvals only.
// One-time approvals are consumed by the flow that created them and must not
// become authorization for a later run or chat session.
func (s *Store) ListApprovedModelApprovals(ctx context.Context) ([]ModelApproval, error) {
	approvals, err := s.ListModelApprovals(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]ModelApproval, 0, len(approvals))
	for _, approval := range approvals {
		if approval.Status != ApprovalStatusApproved {
			continue
		}
		out = append(out, approval)
	}
	return out, nil
}

// SaveCommandApproval upserts a shell command approval.
func (s *Store) SaveCommandApproval(ctx context.Context, approval CommandApproval) error {
	if !s.Available() {
		return s.Err()
	}
	if approval.ApprovedAt.IsZero() {
		approval.ApprovedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO command_approvals (command, status, source, rationale, approved_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(command) DO UPDATE SET
			status = excluded.status,
			source = excluded.source,
			rationale = excluded.rationale,
			approved_at = excluded.approved_at`,
		approval.Command, approval.Status, approval.Source, approval.Rationale, approval.ApprovedAt.UTC(),
	)
	return err
}

// ListCommandApprovals returns command approvals ordered by newest first.
func (s *Store) ListCommandApprovals(ctx context.Context) ([]CommandApproval, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT command, status, source, rationale, approved_at
		FROM command_approvals
		ORDER BY approved_at DESC, command ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CommandApproval
	for rows.Next() {
		var item CommandApproval
		if err := rows.Scan(&item.Command, &item.Status, &item.Source, &item.Rationale, &item.ApprovedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListApprovedCommandApprovals returns durable reusable command approvals
// only. One-time approvals must never cross a run or chat-session boundary.
func (s *Store) ListApprovedCommandApprovals(ctx context.Context) ([]CommandApproval, error) {
	approvals, err := s.ListCommandApprovals(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]CommandApproval, 0, len(approvals))
	for _, approval := range approvals {
		if approval.Status != ApprovalStatusApproved {
			continue
		}
		out = append(out, approval)
	}
	return out, nil
}
