package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SaveSecurityActionGrant persists an exact, expiring authorization. Reissuing
// the same digest resets it to the requested bounded use count.
func (s *Store) SaveSecurityActionGrant(ctx context.Context, grant SecurityActionGrant) error {
	if !s.Available() {
		return s.Err()
	}
	if strings.TrimSpace(grant.Digest) == "" || strings.TrimSpace(grant.PolicyHash) == "" || strings.TrimSpace(grant.EffectDigest) == "" {
		return fmt.Errorf("security grant is missing a binding")
	}
	if grant.RemainingUses <= 0 {
		grant.RemainingUses = 1
	}
	if grant.CreatedAt.IsZero() {
		grant.CreatedAt = time.Now().UTC()
	}
	if grant.ExpiresAt.IsZero() || !grant.ExpiresAt.After(grant.CreatedAt) {
		return fmt.Errorf("security grant expiry must be after creation")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO security_action_grants (
			digest, action_kind, masked_summary, effect_digest, policy_hash,
			host, principal, working_directory, source, expires_at,
			remaining_uses, created_at, used_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(digest) DO UPDATE SET
			action_kind = excluded.action_kind,
			masked_summary = excluded.masked_summary,
			effect_digest = excluded.effect_digest,
			policy_hash = excluded.policy_hash,
			host = excluded.host,
			principal = excluded.principal,
			working_directory = excluded.working_directory,
			source = excluded.source,
			expires_at = excluded.expires_at,
			remaining_uses = excluded.remaining_uses,
			created_at = excluded.created_at,
			used_at = NULL`,
		grant.Digest, grant.ActionKind, grant.MaskedSummary, grant.EffectDigest, grant.PolicyHash,
		grant.Host, grant.Principal, grant.WorkingDirectory, grant.Source, grant.ExpiresAt.UTC(),
		grant.RemainingUses, grant.CreatedAt.UTC(),
	)
	return err
}

// ConsumeSecurityActionGrant atomically spends one use only when every scope
// binding still matches and the grant has not expired.
func (s *Store) ConsumeSecurityActionGrant(ctx context.Context, grant SecurityActionGrant, now time.Time) (bool, error) {
	if !s.Available() {
		return false, s.Err()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var result sql.Result
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		result, err = s.db.ExecContext(ctx, `
			UPDATE security_action_grants
			SET remaining_uses = remaining_uses - 1, used_at = ?
			WHERE digest = ? AND action_kind = ? AND effect_digest = ? AND policy_hash = ?
				AND host = ? AND principal = ? AND working_directory = ?
				AND remaining_uses > 0 AND expires_at > ?`,
			now.UTC(), grant.Digest, grant.ActionKind, grant.EffectDigest, grant.PolicyHash,
			grant.Host, grant.Principal, grant.WorkingDirectory, now.UTC(),
		)
		if err == nil {
			break
		}
		if !isSQLiteBusy(err) {
			return false, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Millisecond):
		}
	}
	if err != nil {
		return false, fmt.Errorf("consume security grant after bounded lock retries: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked")
}

// ListSecurityActionGrants returns newest grants for local inspection. Raw
// commands are deliberately unavailable; only masked summaries are retained.
func (s *Store) ListSecurityActionGrants(ctx context.Context) ([]SecurityActionGrant, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT digest, action_kind, masked_summary, effect_digest, policy_hash,
			host, principal, working_directory, source, expires_at,
			remaining_uses, created_at, used_at
		FROM security_action_grants
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var grants []SecurityActionGrant
	for rows.Next() {
		var grant SecurityActionGrant
		var used sql.NullTime
		if err := rows.Scan(
			&grant.Digest, &grant.ActionKind, &grant.MaskedSummary, &grant.EffectDigest, &grant.PolicyHash,
			&grant.Host, &grant.Principal, &grant.WorkingDirectory, &grant.Source, &grant.ExpiresAt,
			&grant.RemainingUses, &grant.CreatedAt, &used,
		); err != nil {
			return nil, err
		}
		if used.Valid {
			grant.UsedAt = used.Time
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}
