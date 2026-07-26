package state

import (
	"context"
	"fmt"
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

// ListApprovedModelApprovals returns reusable model approvals only.
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
	if approval.TargetID == "" || approval.Environment == "" || approval.Environment == EnvironmentUnknown ||
		approval.RemoteIdentity == "" {
		return fmt.Errorf("scoped command approval requires an unambiguous target, environment, and remote identity")
	}
	if approval.Command == "" || approval.Action == "" {
		return fmt.Errorf("scoped command approval requires a command and action")
	}
	if approval.ApprovedAt.IsZero() {
		approval.ApprovedAt = time.Now().UTC()
	}
	if approval.ExpiresAt.IsZero() || !approval.ExpiresAt.After(approval.ApprovedAt) {
		return fmt.Errorf("scoped command approval requires a future expiry")
	}
	if approval.PolicyVersion == "" {
		approval.PolicyVersion = CommandApprovalPolicyVersion
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scoped_command_approvals (
			target_id, environment, remote_identity, command, action, status, source, rationale,
			policy_version, approved_at, expires_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(target_id, environment, command, action, policy_version) DO UPDATE SET
			remote_identity = excluded.remote_identity,
			status = excluded.status,
			source = excluded.source,
			rationale = excluded.rationale,
			approved_at = excluded.approved_at,
			expires_at = excluded.expires_at`,
		approval.TargetID,
		approval.Environment,
		approval.RemoteIdentity,
		approval.Command,
		approval.Action,
		approval.Status,
		approval.Source,
		approval.Rationale,
		approval.PolicyVersion,
		approval.ApprovedAt.UTC(),
		approval.ExpiresAt.UTC(),
	)
	return err
}

// ListCommandApprovals returns command approvals ordered by newest first.
func (s *Store) ListCommandApprovals(ctx context.Context) ([]CommandApproval, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT target_id, environment, remote_identity, command, action, status, source, rationale,
		       policy_version, approved_at, expires_at
		FROM scoped_command_approvals
		ORDER BY approved_at DESC, target_id, command`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CommandApproval
	for rows.Next() {
		var item CommandApproval
		if err := rows.Scan(
			&item.TargetID,
			&item.Environment,
			&item.RemoteIdentity,
			&item.Command,
			&item.Action,
			&item.Status,
			&item.Source,
			&item.Rationale,
			&item.PolicyVersion,
			&item.ApprovedAt,
			&item.ExpiresAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListApprovedCommandApprovals returns live, deliberately user-created command
// approvals for one exact target and environment. One-off approvals are never
// reusable.
func (s *Store) ListApprovedCommandApprovals(ctx context.Context, targetID, environment string, now time.Time) ([]CommandApproval, error) {
	if targetID == "" || environment == "" || environment == EnvironmentUnknown {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	approvals, err := s.ListCommandApprovals(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]CommandApproval, 0, len(approvals))
	for _, approval := range approvals {
		if approval.Status != ApprovalStatusApproved {
			continue
		}
		if approval.TargetID != targetID || approval.Environment != environment {
			continue
		}
		if approval.PolicyVersion != CommandApprovalPolicyVersion || !approval.ExpiresAt.After(now) {
			continue
		}
		if approval.Source != "cli" && approval.Source != "user_confirm" {
			continue
		}
		out = append(out, approval)
	}
	return out, nil
}
