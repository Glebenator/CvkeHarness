package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func saveModelAliasTx(ctx context.Context, tx *sql.Tx, provider, requestedModel, actualModel string, seenAt time.Time) error {
	provider = strings.TrimSpace(provider)
	requestedModel = strings.TrimSpace(requestedModel)
	actualModel = strings.TrimSpace(actualModel)
	if provider == "" || requestedModel == "" || actualModel == "" || requestedModel == actualModel {
		return nil
	}
	if seenAt.IsZero() {
		seenAt = time.Now().UTC()
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO model_aliases (
			provider, requested_model, actual_model, first_seen_at, last_seen_at, seen_count
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, requested_model, actual_model) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			seen_count = model_aliases.seen_count + 1`,
		provider,
		requestedModel,
		actualModel,
		seenAt.UTC(),
		seenAt.UTC(),
		1,
	)
	return err
}

// ListRecentModelUsage returns recently used requested/actual model pairs from
// both task runs and interactive chat sessions.
func (s *Store) ListRecentModelUsage(ctx context.Context, limit int) ([]RecentModelUsage, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, requested_model, actual_model, MAX(used_at) AS last_used_at, COUNT(*) AS uses, SUM(success) AS successes
		FROM (
			SELECT pr.provider AS provider, pr.requested_model AS requested_model, pr.actual_model AS actual_model,
				COALESCE(r.finished_at, r.started_at) AS used_at, pr.success AS success
			FROM phase_records pr
			INNER JOIN runs r ON r.id = pr.run_id
			UNION ALL
			SELECT cs.provider AS provider, ct.requested_model AS requested_model, ct.actual_model AS actual_model,
				ct.created_at AS used_at, ct.success AS success
			FROM chat_turns ct
			INNER JOIN chat_sessions cs ON cs.id = ct.session_id
		) AS usage
		GROUP BY provider, requested_model, actual_model
		ORDER BY last_used_at DESC, uses DESC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RecentModelUsage
	for rows.Next() {
		var item RecentModelUsage
		var lastUsedRaw string
		if err := rows.Scan(
			&item.Provider,
			&item.RequestedModel,
			&item.ActualModel,
			&lastUsedRaw,
			&item.Uses,
			&item.Successes,
		); err != nil {
			return nil, err
		}
		item.LastUsedAt, err = parseSQLiteTime(lastUsedRaw)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListModelAliases returns recorded requested-to-actual alias mappings.
func (s *Store) ListModelAliases(ctx context.Context) ([]ModelAlias, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, requested_model, actual_model, first_seen_at, last_seen_at, seen_count
		FROM model_aliases
		ORDER BY last_seen_at DESC, seen_count DESC, provider ASC, requested_model ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ModelAlias
	for rows.Next() {
		var item ModelAlias
		if err := rows.Scan(
			&item.Provider,
			&item.RequestedModel,
			&item.ActualModel,
			&item.FirstSeenAt,
			&item.LastSeenAt,
			&item.SeenCount,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func parseSQLiteTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse sqlite timestamp %q", raw)
}
