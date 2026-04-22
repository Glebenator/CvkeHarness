package state

import (
	"context"
	"encoding/json"
	"time"
)

// LoadOperationalMemory returns the structured target-aware memory index.
func (s *Store) LoadOperationalMemory(ctx context.Context) (OperationalMemory, error) {
	if !s.Available() {
		return OperationalMemory{}, s.Err()
	}

	mem := OperationalMemory{}

	targetRows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, primary_name, transport, confidence, status, first_seen_at, last_seen_at
		FROM targets
		ORDER BY kind, primary_name, id`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer targetRows.Close()
	for targetRows.Next() {
		var item Target
		if err := targetRows.Scan(
			&item.ID,
			&item.Kind,
			&item.PrimaryName,
			&item.Transport,
			&item.Confidence,
			&item.Status,
			&item.FirstSeenAt,
			&item.LastSeenAt,
		); err != nil {
			return OperationalMemory{}, err
		}
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
		SELECT host_id, key, value, confidence, verified_at, updated_at
		FROM host_facts
		ORDER BY host_id, key`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer factRows.Close()
	for factRows.Next() {
		var item HostFact
		if err := factRows.Scan(
			&item.HostID,
			&item.Key,
			&item.Value,
			&item.Confidence,
			&item.VerifiedAt,
			&item.UpdatedAt,
		); err != nil {
			return OperationalMemory{}, err
		}
		mem.HostFacts = append(mem.HostFacts, item)
	}
	if err := factRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	playbookRows, err := s.db.QueryContext(ctx, `
		SELECT id, target_id, intent, tool_name, status, title, confidence, success_count, failure_count,
		       last_verified_at, last_used_at, created_at, updated_at, match_terms_json, preconditions_json,
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
		)
		if err := playbookRows.Scan(
			&item.ID,
			&item.TargetID,
			&item.Intent,
			&item.ToolName,
			&item.Status,
			&item.Title,
			&item.Confidence,
			&item.SuccessCount,
			&item.FailureCount,
			&item.LastVerifiedAt,
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
		mem.Playbooks = append(mem.Playbooks, item)
	}
	if err := playbookRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	findingRows, err := s.db.QueryContext(ctx, `
		SELECT id, target_id, intent, tool_name, status, origin, body, confidence, seen_count, created_at, updated_at
		FROM findings
		ORDER BY target_id, intent, updated_at DESC`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer findingRows.Close()
	for findingRows.Next() {
		var item Finding
		if err := findingRows.Scan(
			&item.ID,
			&item.TargetID,
			&item.Intent,
			&item.ToolName,
			&item.Status,
			&item.Origin,
			&item.Body,
			&item.Confidence,
			&item.SeenCount,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return OperationalMemory{}, err
		}
		mem.Findings = append(mem.Findings, item)
	}
	if err := findingRows.Err(); err != nil {
		return OperationalMemory{}, err
	}

	cautionRows, err := s.db.QueryContext(ctx, `
		SELECT id, target_id, intent, tool_name, status, body, confidence, failure_count, last_seen_at, created_at, updated_at
		FROM cautions
		ORDER BY target_id, intent, updated_at DESC`)
	if err != nil {
		return OperationalMemory{}, err
	}
	defer cautionRows.Close()
	for cautionRows.Next() {
		var item Caution
		if err := cautionRows.Scan(
			&item.ID,
			&item.TargetID,
			&item.Intent,
			&item.ToolName,
			&item.Status,
			&item.Body,
			&item.Confidence,
			&item.FailureCount,
			&item.LastSeenAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return OperationalMemory{}, err
		}
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
			item.Status = "active"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO targets (id, kind, primary_name, transport, confidence, status, first_seen_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.Kind,
			item.PrimaryName,
			item.Transport,
			item.Confidence,
			item.Status,
			item.FirstSeenAt.UTC(),
			item.LastSeenAt.UTC(),
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
			item.VerifiedAt = now
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.VerifiedAt
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO host_facts (host_id, key, value, confidence, verified_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			item.HostID,
			item.Key,
			item.Value,
			item.Confidence,
			item.VerifiedAt.UTC(),
			item.UpdatedAt.UTC(),
		); err != nil {
			return err
		}
	}

	for _, item := range mem.Playbooks {
		if item.LastVerifiedAt.IsZero() {
			item.LastVerifiedAt = now
		}
		if item.LastUsedAt.IsZero() {
			item.LastUsedAt = item.LastVerifiedAt
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = item.LastVerifiedAt
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.LastUsedAt
		}
		if item.Status == "" {
			item.Status = "active"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO playbooks (
				id, target_id, intent, tool_name, status, title, confidence, success_count, failure_count,
				last_verified_at, last_used_at, created_at, updated_at, match_terms_json, preconditions_json,
				verify_steps_json, action_steps_json, success_checks_json, notes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.TargetID,
			item.Intent,
			item.ToolName,
			item.Status,
			item.Title,
			item.Confidence,
			item.SuccessCount,
			item.FailureCount,
			item.LastVerifiedAt.UTC(),
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
			item.Status = "active"
		}
		if item.SeenCount <= 0 {
			item.SeenCount = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO findings (id, target_id, intent, tool_name, status, origin, body, confidence, seen_count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.TargetID,
			item.Intent,
			item.ToolName,
			item.Status,
			item.Origin,
			item.Body,
			item.Confidence,
			item.SeenCount,
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
			item.Status = "active"
		}
		if item.FailureCount <= 0 {
			item.FailureCount = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cautions (id, target_id, intent, tool_name, status, body, confidence, failure_count, last_seen_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.TargetID,
			item.Intent,
			item.ToolName,
			item.Status,
			item.Body,
			item.Confidence,
			item.FailureCount,
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
