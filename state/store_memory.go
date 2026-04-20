package state

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// SaveMemoryEntries upserts retrieved or curated memory metadata.
func (s *Store) SaveMemoryEntries(ctx context.Context, entries []MemoryEntry) error {
	if !s.Available() {
		return s.Err()
	}
	return s.saveMemoryEntriesTx(ctx, nil, entries)
}

// SyncMemoryEntries upserts the current entries for the given source files and
// marks any previously indexed-but-now-missing entries inactive.
func (s *Store) SyncMemoryEntries(ctx context.Context, sourceFiles []string, entries []MemoryEntry) error {
	if !s.Available() {
		return s.Err()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	if len(sourceFiles) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(sourceFiles)), ",")
		args := make([]any, 0, len(sourceFiles)+1)
		args = append(args, now)
		for _, sourceFile := range sourceFiles {
			args = append(args, sourceFile)
		}
		query := `UPDATE memory_entries SET status = 'inactive', updated_at = ? WHERE source_file IN (` + placeholders + `)`
		if len(entries) > 0 {
			query += ` AND id NOT IN (` + strings.TrimRight(strings.Repeat("?,", len(entries)), ",") + `)`
			for _, entry := range entries {
				args = append(args, entry.ID)
			}
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}

	if err := s.saveMemoryEntriesTx(ctx, tx, entries); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) saveMemoryEntriesTx(ctx context.Context, tx *sql.Tx, entries []MemoryEntry) error {
	var err error
	ownTx := false
	if tx == nil {
		tx, err = s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		ownTx = true
		defer tx.Rollback()
	}

	for _, entry := range entries {
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = time.Now().UTC()
		}
		if entry.UpdatedAt.IsZero() {
			entry.UpdatedAt = entry.CreatedAt
		}
		if entry.LastSeenAt.IsZero() {
			entry.LastSeenAt = entry.UpdatedAt
		}
		if entry.Status == "" {
			entry.Status = "active"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_entries (
				id, source_file, scope, provider, model, tool_name, task_class, phase, status, confidence,
				body, normalized, snapshot_id, created_at, updated_at, last_seen_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				scope = excluded.scope,
				provider = excluded.provider,
				model = excluded.model,
				tool_name = excluded.tool_name,
				task_class = excluded.task_class,
				phase = excluded.phase,
				status = excluded.status,
				confidence = excluded.confidence,
				body = excluded.body,
				normalized = excluded.normalized,
				snapshot_id = excluded.snapshot_id,
				updated_at = excluded.updated_at,
				last_seen_at = excluded.last_seen_at`,
			entry.ID, entry.SourceFile, entry.Scope, entry.Provider, entry.Model, entry.ToolName,
			string(entry.TaskClass), string(entry.Phase), entry.Status, entry.Confidence, entry.Body,
			entry.Normalized, entry.SnapshotID, entry.CreatedAt.UTC(), entry.UpdatedAt.UTC(), entry.LastSeenAt.UTC(),
		); err != nil {
			return err
		}
	}

	if ownTx {
		return tx.Commit()
	}
	return nil
}

// ListMemoryEntries returns active memory metadata.
func (s *Store) ListMemoryEntries(ctx context.Context, filter MemoryFilter) ([]MemoryEntry, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	query := `
		SELECT id, source_file, scope, provider, model, tool_name, task_class, phase, status, confidence,
		       body, normalized, snapshot_id, created_at, updated_at, last_seen_at
		FROM memory_entries
		WHERE 1=1`
	var args []any

	if len(filter.SourceFiles) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(filter.SourceFiles)), ",")
		query += " AND source_file IN (" + placeholders + ")"
		for _, source := range filter.SourceFiles {
			args = append(args, source)
		}
	}
	if filter.Phase != "" {
		query += " AND phase = ?"
		args = append(args, string(filter.Phase))
	}
	if filter.TaskClass != "" {
		query += " AND task_class = ?"
		args = append(args, string(filter.TaskClass))
	}
	if filter.ToolName != "" {
		query += " AND (tool_name = ? OR tool_name = '')"
		args = append(args, filter.ToolName)
	}
	if filter.Provider != "" {
		query += " AND (provider = ? OR provider = '')"
		args = append(args, filter.Provider)
	}
	if filter.Model != "" {
		query += " AND (model = ? OR model = '')"
		args = append(args, filter.Model)
	}
	if filter.OnlyActive {
		query += " AND status = 'active'"
	}
	query += " ORDER BY confidence DESC, updated_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemoryEntry
	for rows.Next() {
		var item MemoryEntry
		if err := rows.Scan(
			&item.ID, &item.SourceFile, &item.Scope, &item.Provider, &item.Model, &item.ToolName,
			&item.TaskClass, &item.Phase, &item.Status, &item.Confidence, &item.Body, &item.Normalized,
			&item.SnapshotID, &item.CreatedAt, &item.UpdatedAt, &item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SaveSnapshot upserts a snapshot metadata row.
func (s *Store) SaveSnapshot(ctx context.Context, snapshot Snapshot) error {
	if !s.Available() {
		return s.Err()
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO snapshots (id, source_file, path, reason, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source_file = excluded.source_file,
			path = excluded.path,
			reason = excluded.reason,
			created_at = excluded.created_at`,
		snapshot.ID, snapshot.SourceFile, snapshot.Path, snapshot.Reason, snapshot.CreatedAt.UTC(),
	)
	return err
}

// ListSnapshots returns all known snapshots.
func (s *Store) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_file, path, reason, created_at
		FROM snapshots
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var item Snapshot
		if err := rows.Scan(&item.ID, &item.SourceFile, &item.Path, &item.Reason, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetSnapshot returns one snapshot by ID.
func (s *Store) GetSnapshot(ctx context.Context, id string) (Snapshot, error) {
	if !s.Available() {
		return Snapshot{}, s.Err()
	}

	var item Snapshot
	err := s.db.QueryRowContext(ctx, `
		SELECT id, source_file, path, reason, created_at
		FROM snapshots
		WHERE id = ?`,
		id,
	).Scan(&item.ID, &item.SourceFile, &item.Path, &item.Reason, &item.CreatedAt)
	return item, err
}
