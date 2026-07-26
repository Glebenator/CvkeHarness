package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// GetTarget returns one live-inventory target by stable identifier.
func (s *Store) GetTarget(ctx context.Context, targetID string) (Target, error) {
	if !s.Available() {
		return Target{}, s.Err()
	}
	var item Target
	var verifiedAt, expiresAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, kind, environment, primary_name, transport, remote_identity, confidence, status,
		       first_seen_at, last_seen_at, verified_at, expires_at
		FROM targets
		WHERE id = ?`, targetID).Scan(
		&item.ID,
		&item.Kind,
		&item.Environment,
		&item.PrimaryName,
		&item.Transport,
		&item.RemoteIdentity,
		&item.Confidence,
		&item.Status,
		&item.FirstSeenAt,
		&item.LastSeenAt,
		&verifiedAt,
		&expiresAt,
	)
	if err != nil {
		return Target{}, err
	}
	item.VerifiedAt = nullTimeValue(verifiedAt)
	item.ExpiresAt = nullTimeValue(expiresAt)
	return item, nil
}

// LoadOperationalMemory returns the structured target-aware memory index.
func (s *Store) LoadOperationalMemory(ctx context.Context) (OperationalMemory, error) {
	if !s.Available() {
		return OperationalMemory{}, s.Err()
	}

	mem := OperationalMemory{}

	targetRows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, environment, primary_name, transport, remote_identity, confidence, status,
		       first_seen_at, last_seen_at, verified_at, expires_at
		FROM targets
		ORDER BY kind, primary_name, id`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer targetRows.Close()
	for targetRows.Next() {
		var item Target
		var verifiedAt, expiresAt sql.NullTime
		if err := targetRows.Scan(
			&item.ID,
			&item.Kind,
			&item.Environment,
			&item.PrimaryName,
			&item.Transport,
			&item.RemoteIdentity,
			&item.Confidence,
			&item.Status,
			&item.FirstSeenAt,
			&item.LastSeenAt,
			&verifiedAt,
			&expiresAt,
		); err != nil {
			return OperationalMemory{}, err
		}
		item.VerifiedAt = nullTimeValue(verifiedAt)
		item.ExpiresAt = nullTimeValue(expiresAt)
		mem.Targets = append(mem.Targets, item)
	}
	if err := targetRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	aliasRows, err := s.db.QueryContext(ctx, `
		SELECT target_id, alias, alias_type, confidence, last_seen_at
		FROM target_aliases
		ORDER BY target_id, alias_type, alias`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer aliasRows.Close()
	for aliasRows.Next() {
		var item TargetAlias
		if err := aliasRows.Scan(
			&item.TargetID,
			&item.Alias,
			&item.AliasType,
			&item.Confidence,
			&item.LastSeenAt,
		); err != nil {
			return OperationalMemory{}, err
		}
		mem.TargetAliases = append(mem.TargetAliases, item)
	}
	if err := aliasRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	factRows, err := s.db.QueryContext(ctx, `
		SELECT host_id, environment, key, value, status, source, evidence_ref, evidence_hash, trust,
		       confidence, observed_at, verified_at, expires_at, updated_at
		FROM host_facts
		ORDER BY host_id, key`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer factRows.Close()
	for factRows.Next() {
		var item HostFact
		var observedAt, expiresAt sql.NullTime
		if err := factRows.Scan(
			&item.HostID,
			&item.Environment,
			&item.Key,
			&item.Value,
			&item.Status,
			&item.Source,
			&item.EvidenceRef,
			&item.EvidenceHash,
			&item.Trust,
			&item.Confidence,
			&observedAt,
			&item.VerifiedAt,
			&expiresAt,
			&item.UpdatedAt,
		); err != nil {
			return OperationalMemory{}, err
		}
		item.ObservedAt = nullTimeValue(observedAt)
		item.ExpiresAt = nullTimeValue(expiresAt)
		mem.HostFacts = append(mem.HostFacts, item)
	}
	if err := factRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	playbookRows, err := s.db.QueryContext(ctx, `
		SELECT id, target_id, environment, intent, tool_name, status, source, evidence_ref, evidence_hash,
		       trust, title, confidence, success_count, failure_count, observed_at, last_verified_at,
		       expires_at, last_used_at, created_at, updated_at, match_terms_json, preconditions_json,
		       verify_steps_json, action_steps_json, success_checks_json, notes
		FROM playbooks
		ORDER BY target_id, intent, tool_name, updated_at DESC`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer playbookRows.Close()
	for playbookRows.Next() {
		var (
			item              Playbook
			matchTermsJSON    string
			preconditionsJSON string
			verifyJSON        string
			actionJSON        string
			successJSON       string
			observedAt        sql.NullTime
			lastVerifiedAt    sql.NullTime
			expiresAt         sql.NullTime
		)
		if err := playbookRows.Scan(
			&item.ID,
			&item.TargetID,
			&item.Environment,
			&item.Intent,
			&item.ToolName,
			&item.Status,
			&item.Source,
			&item.EvidenceRef,
			&item.EvidenceHash,
			&item.Trust,
			&item.Title,
			&item.Confidence,
			&item.SuccessCount,
			&item.FailureCount,
			&observedAt,
			&lastVerifiedAt,
			&expiresAt,
			&item.LastUsedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
			&matchTermsJSON,
			&preconditionsJSON,
			&verifyJSON,
			&actionJSON,
			&successJSON,
			&item.Notes,
		); err != nil {
			return OperationalMemory{}, err
		}
		item.MatchTerms = parseStringList(matchTermsJSON)
		item.Preconditions = parseStringList(preconditionsJSON)
		item.VerifySteps = parseStringList(verifyJSON)
		item.ActionSteps = parseStringList(actionJSON)
		item.SuccessChecks = parseStringList(successJSON)
		item.ObservedAt = nullTimeValue(observedAt)
		item.LastVerifiedAt = nullTimeValue(lastVerifiedAt)
		item.ExpiresAt = nullTimeValue(expiresAt)
		mem.Playbooks = append(mem.Playbooks, item)
	}
	if err := playbookRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	findingRows, err := s.db.QueryContext(ctx, `
		SELECT id, target_id, environment, intent, tool_name, status, origin, source, evidence_ref,
		       evidence_hash, trust, body, confidence, seen_count, observed_at, verified_at, expires_at,
		       created_at, updated_at
		FROM findings
		ORDER BY target_id, intent, updated_at DESC`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer findingRows.Close()
	for findingRows.Next() {
		var item Finding
		var observedAt, verifiedAt, expiresAt sql.NullTime
		if err := findingRows.Scan(
			&item.ID,
			&item.TargetID,
			&item.Environment,
			&item.Intent,
			&item.ToolName,
			&item.Status,
			&item.Origin,
			&item.Source,
			&item.EvidenceRef,
			&item.EvidenceHash,
			&item.Trust,
			&item.Body,
			&item.Confidence,
			&item.SeenCount,
			&observedAt,
			&verifiedAt,
			&expiresAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return OperationalMemory{}, err
		}
		item.ObservedAt = nullTimeValue(observedAt)
		item.VerifiedAt = nullTimeValue(verifiedAt)
		item.ExpiresAt = nullTimeValue(expiresAt)
		mem.Findings = append(mem.Findings, item)
	}
	if err := findingRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	cautionRows, err := s.db.QueryContext(ctx, `
		SELECT id, target_id, environment, intent, tool_name, status, source, evidence_ref, evidence_hash,
		       trust, body, confidence, failure_count, observed_at, verified_at, expires_at, last_seen_at,
		       created_at, updated_at
		FROM cautions
		ORDER BY target_id, intent, updated_at DESC`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer cautionRows.Close()
	for cautionRows.Next() {
		var item Caution
		var observedAt, verifiedAt, expiresAt sql.NullTime
		if err := cautionRows.Scan(
			&item.ID,
			&item.TargetID,
			&item.Environment,
			&item.Intent,
			&item.ToolName,
			&item.Status,
			&item.Source,
			&item.EvidenceRef,
			&item.EvidenceHash,
			&item.Trust,
			&item.Body,
			&item.Confidence,
			&item.FailureCount,
			&observedAt,
			&verifiedAt,
			&expiresAt,
			&item.LastSeenAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return OperationalMemory{}, err
		}
		item.ObservedAt = nullTimeValue(observedAt)
		item.VerifiedAt = nullTimeValue(verifiedAt)
		item.ExpiresAt = nullTimeValue(expiresAt)
		mem.Cautions = append(mem.Cautions, item)
	}
	if err := cautionRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	return mem, nil
}

// ReplaceOperationalMemory replaces the structured memory index.
func (s *Store) ReplaceOperationalMemory(ctx context.Context, mem OperationalMemory) error {
	if !s.Available() {
		return s.Err()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DELETE FROM target_aliases`,
		`DELETE FROM host_facts`,
		`DELETE FROM playbooks`,
		`DELETE FROM findings`,
		`DELETE FROM cautions`,
		`DELETE FROM targets`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	for _, item := range mem.Targets {
		if item.FirstSeenAt.IsZero() {
			item.FirstSeenAt = now
		}
		if item.LastSeenAt.IsZero() {
			item.LastSeenAt = item.FirstSeenAt
		}
		if item.Status == "" {
			item.Status = MemoryStatusCandidate
		}
		if item.Environment == "" {
			item.Environment = EnvironmentUnknown
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO targets (
				id, kind, environment, primary_name, transport, remote_identity, confidence, status,
				first_seen_at, last_seen_at, verified_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.Kind,
			item.Environment,
			item.PrimaryName,
			item.Transport,
			item.RemoteIdentity,
			item.Confidence,
			item.Status,
			item.FirstSeenAt.UTC(),
			item.LastSeenAt.UTC(),
			nullableTime(item.VerifiedAt),
			nullableTime(item.ExpiresAt),
		); err != nil {
			return err
		}
	}

	for _, item := range mem.TargetAliases {
		if item.LastSeenAt.IsZero() {
			item.LastSeenAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO target_aliases (target_id, alias, alias_type, confidence, last_seen_at)
			VALUES (?, ?, ?, ?, ?)`,
			item.TargetID,
			item.Alias,
			item.AliasType,
			item.Confidence,
			item.LastSeenAt.UTC(),
		); err != nil {
			return err
		}
	}

	for _, item := range mem.HostFacts {
		if item.VerifiedAt.IsZero() {
			return fmt.Errorf("host fact %q is missing verified_at", item.Key)
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.VerifiedAt
		}
		if item.ObservedAt.IsZero() {
			item.ObservedAt = item.VerifiedAt
		}
		if item.Environment == "" {
			item.Environment = EnvironmentUnknown
		}
		if item.Status == "" {
			item.Status = MemoryStatusCandidate
		}
		if item.Trust == "" {
			item.Trust = MemoryTrustUntrusted
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO host_facts (
				host_id, environment, key, value, status, source, evidence_ref, evidence_hash, trust,
				confidence, observed_at, verified_at, expires_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.HostID,
			item.Environment,
			item.Key,
			item.Value,
			item.Status,
			item.Source,
			item.EvidenceRef,
			item.EvidenceHash,
			item.Trust,
			item.Confidence,
			item.ObservedAt.UTC(),
			item.VerifiedAt.UTC(),
			nullableTime(item.ExpiresAt),
			item.UpdatedAt.UTC(),
		); err != nil {
			return err
		}
	}

	for _, item := range mem.Playbooks {
		if item.ObservedAt.IsZero() {
			item.ObservedAt = firstNonZeroTime(item.LastUsedAt, item.CreatedAt, now)
		}
		if item.LastUsedAt.IsZero() {
			item.LastUsedAt = item.ObservedAt
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = item.ObservedAt
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.LastUsedAt
		}
		if item.Status == "" {
			item.Status = MemoryStatusCandidate
		}
		if item.Environment == "" {
			item.Environment = EnvironmentUnknown
		}
		if item.Trust == "" {
			item.Trust = MemoryTrustUntrusted
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO playbooks (
				id, target_id, environment, intent, tool_name, status, source, evidence_ref, evidence_hash,
				trust, title, confidence, success_count, failure_count, observed_at, last_verified_at,
				expires_at, last_used_at, created_at, updated_at, match_terms_json, preconditions_json,
				verify_steps_json, action_steps_json, success_checks_json, notes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.TargetID,
			item.Environment,
			item.Intent,
			item.ToolName,
			item.Status,
			item.Source,
			item.EvidenceRef,
			item.EvidenceHash,
			item.Trust,
			item.Title,
			item.Confidence,
			item.SuccessCount,
			item.FailureCount,
			item.ObservedAt.UTC(),
			nullableTime(item.LastVerifiedAt),
			nullableTime(item.ExpiresAt),
			item.LastUsedAt.UTC(),
			item.CreatedAt.UTC(),
			item.UpdatedAt.UTC(),
			mustJSONString(item.MatchTerms),
			mustJSONString(item.Preconditions),
			mustJSONString(item.VerifySteps),
			mustJSONString(item.ActionSteps),
			mustJSONString(item.SuccessChecks),
			item.Notes,
		); err != nil {
			return err
		}
	}

	for _, item := range mem.Findings {
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		if item.Status == "" {
			item.Status = MemoryStatusCandidate
		}
		if item.SeenCount <= 0 {
			item.SeenCount = 1
		}
		if item.ObservedAt.IsZero() {
			item.ObservedAt = item.CreatedAt
		}
		if item.Environment == "" {
			item.Environment = EnvironmentUnknown
		}
		if item.Trust == "" {
			item.Trust = MemoryTrustUntrusted
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO findings (
				id, target_id, environment, intent, tool_name, status, origin, source, evidence_ref,
				evidence_hash, trust, body, confidence, seen_count, observed_at, verified_at, expires_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.TargetID,
			item.Environment,
			item.Intent,
			item.ToolName,
			item.Status,
			item.Origin,
			item.Source,
			item.EvidenceRef,
			item.EvidenceHash,
			item.Trust,
			item.Body,
			item.Confidence,
			item.SeenCount,
			item.ObservedAt.UTC(),
			nullableTime(item.VerifiedAt),
			nullableTime(item.ExpiresAt),
			item.CreatedAt.UTC(),
			item.UpdatedAt.UTC(),
		); err != nil {
			return err
		}
	}

	for _, item := range mem.Cautions {
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		if item.LastSeenAt.IsZero() {
			item.LastSeenAt = item.UpdatedAt
		}
		if item.Status == "" {
			item.Status = MemoryStatusCandidate
		}
		if item.FailureCount <= 0 {
			item.FailureCount = 1
		}
		if item.ObservedAt.IsZero() {
			item.ObservedAt = item.CreatedAt
		}
		if item.Environment == "" {
			item.Environment = EnvironmentUnknown
		}
		if item.Trust == "" {
			item.Trust = MemoryTrustUntrusted
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cautions (
				id, target_id, environment, intent, tool_name, status, source, evidence_ref, evidence_hash,
				trust, body, confidence, failure_count, observed_at, verified_at, expires_at, last_seen_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.TargetID,
			item.Environment,
			item.Intent,
			item.ToolName,
			item.Status,
			item.Source,
			item.EvidenceRef,
			item.EvidenceHash,
			item.Trust,
			item.Body,
			item.Confidence,
			item.FailureCount,
			item.ObservedAt.UTC(),
			nullableTime(item.VerifiedAt),
			nullableTime(item.ExpiresAt),
			item.LastSeenAt.UTC(),
			item.CreatedAt.UTC(),
			item.UpdatedAt.UTC(),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func parseStringList(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func mustJSONString(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func nullableTime(ts time.Time) any {
	if ts.IsZero() {
		return nil
	}
	return ts.UTC()
}

func nullTimeValue(ts sql.NullTime) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}
