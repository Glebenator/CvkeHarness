package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/coolcake/cvkeharness/core"
)

// Store persists run/routing/memory metadata. It gracefully degrades to no-op
// behavior when SQLite is unavailable or corrupted.
type Store struct {
	db  *sql.DB
	err error
}

// Open creates or opens the SQLite state database.
func Open(path string) *Store {
	if strings.TrimSpace(path) == "" {
		return &Store{err: fmt.Errorf("state db path is empty")}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return &Store{err: err}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return &Store{err: err}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return &Store{err: err}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return &Store{err: err}
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return &Store{err: err}
	}

	return &Store{db: db}
}

// Available reports whether the backing database is usable.
func (s *Store) Available() bool {
	return s != nil && s.db != nil && s.err == nil
}

// Err returns the initialization error, if any.
func (s *Store) Err() error {
	if s == nil {
		return fmt.Errorf("state store is nil")
	}
	return s.err
}

// Close closes the underlying DB when available.
func (s *Store) Close() error {
	if !s.Available() {
		return nil
	}
	return s.db.Close()
}

// RecordRun inserts run/phase/tool data and refreshes aggregates.
func (s *Store) RecordRun(ctx context.Context, record RunRecord) error {
	if !s.Available() {
		return s.Err()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO runs (
			started_at, finished_at, provider, task, task_class, success, error_message, routing_enabled
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.StartedAt.UTC(),
		record.FinishedAt.UTC(),
		record.Provider,
		record.Task,
		string(record.TaskClass),
		boolToInt(record.Success),
		record.ErrorMessage,
		boolToInt(record.RoutingEnabled),
	)
	if err != nil {
		return err
	}

	runID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	for _, phase := range record.Phases {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO phase_records (
				run_id, phase, provider, requested_model, actual_model, success, latency_ms,
				prompt_tokens, completion_tokens, total_tokens, confidence, explanation, task_class
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID,
			string(phase.Phase),
			phase.Provider,
			phase.RequestedModel,
			phase.ActualModel,
			boolToInt(phase.Success),
			phase.LatencyMs,
			phase.PromptTokens,
			phase.CompletionTokens,
			phase.TotalTokens,
			phase.Confidence,
			phase.Explanation,
			string(record.TaskClass),
		); err != nil {
			return err
		}

		if err := s.bumpModelStatsTx(ctx, tx, record.TaskClass, phase, record.Tools); err != nil {
			return err
		}
	}

	for _, tool := range record.Tools {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tool_outcomes (
				run_id, phase, provider, model, tool_name, toolset, success, policy_denied, denial_class, error_message, duration_ms, task_class
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID,
			string(tool.Phase),
			tool.Provider,
			tool.Model,
			tool.ToolName,
			tool.Toolset,
			boolToInt(tool.Success),
			boolToInt(tool.PolicyDenied),
			tool.DenialClass,
			tool.ErrorMessage,
			tool.DurationMs,
			string(record.TaskClass),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) bumpModelStatsTx(ctx context.Context, tx *sql.Tx, taskClass core.TaskClass, phase PhaseRecord, tools []ToolOutcome) error {
	toolset := ""
	for _, tool := range tools {
		if tool.Phase == phase.Phase {
			toolset = tool.Toolset
			break
		}
	}

	var prevRuns, prevSuccesses, prevDenials int
	var prevLatency float64
	err := tx.QueryRowContext(ctx, `
		SELECT runs, successes, policy_denials, avg_latency_ms
		FROM model_stats
		WHERE provider = ? AND model = ? AND phase = ? AND task_class = ? AND toolset = ?`,
		phase.Provider, phase.ActualModel, string(phase.Phase), string(taskClass), toolset,
	).Scan(&prevRuns, &prevSuccesses, &prevDenials, &prevLatency)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	denials := 0
	for _, tool := range tools {
		if tool.Phase == phase.Phase && tool.PolicyDenied {
			denials++
		}
	}

	runs := prevRuns + 1
	successes := prevSuccesses
	if phase.Success {
		successes++
	}
	policyDenials := prevDenials + denials
	avgLatency := phase.LatencyMs
	if prevRuns > 0 {
		avgLatency = int64(((prevLatency * float64(prevRuns)) + float64(phase.LatencyMs)) / float64(runs))
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO model_stats (
			provider, model, phase, task_class, toolset, runs, successes, policy_denials, avg_latency_ms, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, model, phase, task_class, toolset) DO UPDATE SET
			runs = excluded.runs,
			successes = excluded.successes,
			policy_denials = excluded.policy_denials,
			avg_latency_ms = excluded.avg_latency_ms,
			last_seen_at = excluded.last_seen_at`,
		phase.Provider,
		phase.ActualModel,
		string(phase.Phase),
		string(taskClass),
		toolset,
		runs,
		successes,
		policyDenials,
		avgLatency,
		time.Now().UTC(),
	)
	return err
}

// ListModelStats returns routing aggregates for a phase/profile.
func (s *Store) ListModelStats(ctx context.Context, phase core.Phase, taskClass core.TaskClass, toolset string) ([]ModelStats, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, model, phase, task_class, toolset, runs, successes, policy_denials, avg_latency_ms, last_seen_at
		FROM model_stats
		WHERE phase = ? AND task_class = ? AND toolset = ?
		ORDER BY last_seen_at DESC`,
		string(phase), string(taskClass), toolset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ModelStats
	for rows.Next() {
		var item ModelStats
		if err := rows.Scan(
			&item.Provider,
			&item.Model,
			&item.Phase,
			&item.TaskClass,
			&item.Toolset,
			&item.Runs,
			&item.Successes,
			&item.PolicyDenials,
			&item.AvgLatencyMs,
			&item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListAllModelStats returns all aggregate model stats.
func (s *Store) ListAllModelStats(ctx context.Context) ([]ModelStats, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, model, phase, task_class, toolset, runs, successes, policy_denials, avg_latency_ms, last_seen_at
		FROM model_stats
		ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ModelStats
	for rows.Next() {
		var item ModelStats
		if err := rows.Scan(
			&item.Provider,
			&item.Model,
			&item.Phase,
			&item.TaskClass,
			&item.Toolset,
			&item.Runs,
			&item.Successes,
			&item.PolicyDenials,
			&item.AvgLatencyMs,
			&item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SaveRoutingCandidates replaces the learned routing candidates for a profile.
func (s *Store) SaveRoutingCandidates(ctx context.Context, phase core.Phase, taskClass core.TaskClass, toolset string, candidates []RoutingCandidate) error {
	if !s.Available() {
		return s.Err()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM routing_candidates WHERE phase = ? AND task_class = ? AND toolset = ?`,
		string(phase), string(taskClass), toolset,
	); err != nil {
		return err
	}

	for _, candidate := range candidates {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO routing_candidates (
				provider, model, phase, task_class, toolset, approved, score, confidence, reason, status, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			candidate.Provider,
			candidate.Model,
			string(candidate.Phase),
			string(candidate.TaskClass),
			candidate.Toolset,
			boolToInt(candidate.Approved),
			candidate.Score,
			candidate.Confidence,
			candidate.Reason,
			candidate.Status,
			candidate.UpdatedAt.UTC(),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ListRoutingCandidates returns the latest learned routing candidates.
func (s *Store) ListRoutingCandidates(ctx context.Context) ([]RoutingCandidate, error) {
	if !s.Available() {
		return nil, s.Err()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, model, phase, task_class, toolset, approved, score, confidence, reason, status, updated_at
		FROM routing_candidates
		ORDER BY updated_at DESC, score DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RoutingCandidate
	for rows.Next() {
		var item RoutingCandidate
		var approved int
		if err := rows.Scan(
			&item.Provider, &item.Model, &item.Phase, &item.TaskClass, &item.Toolset,
			&approved, &item.Score, &item.Confidence, &item.Reason, &item.Status, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.Approved = approved == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func migrate(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at DATETIME NOT NULL,
			finished_at DATETIME NOT NULL,
			provider TEXT NOT NULL,
			task TEXT NOT NULL,
			task_class TEXT NOT NULL,
			success INTEGER NOT NULL,
			error_message TEXT NOT NULL DEFAULT '',
			routing_enabled INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS phase_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			phase TEXT NOT NULL,
			provider TEXT NOT NULL,
			requested_model TEXT NOT NULL,
			actual_model TEXT NOT NULL,
			success INTEGER NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			confidence REAL NOT NULL DEFAULT 0,
			explanation TEXT NOT NULL DEFAULT '',
			task_class TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id)
		);`,
		`CREATE TABLE IF NOT EXISTS tool_outcomes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			phase TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			toolset TEXT NOT NULL DEFAULT '',
			success INTEGER NOT NULL,
			policy_denied INTEGER NOT NULL DEFAULT 0,
			denial_class TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			task_class TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(run_id) REFERENCES runs(id)
		);`,
		`CREATE TABLE IF NOT EXISTS model_stats (
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			phase TEXT NOT NULL,
			task_class TEXT NOT NULL,
			toolset TEXT NOT NULL DEFAULT '',
			runs INTEGER NOT NULL DEFAULT 0,
			successes INTEGER NOT NULL DEFAULT 0,
			policy_denials INTEGER NOT NULL DEFAULT 0,
			avg_latency_ms REAL NOT NULL DEFAULT 0,
			last_seen_at DATETIME NOT NULL,
			PRIMARY KEY(provider, model, phase, task_class, toolset)
		);`,
		`CREATE TABLE IF NOT EXISTS memory_entries (
			id TEXT PRIMARY KEY,
			source_file TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'global',
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			task_class TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			confidence REAL NOT NULL DEFAULT 0,
			seen_count INTEGER NOT NULL DEFAULT 1,
			body TEXT NOT NULL,
			normalized TEXT NOT NULL DEFAULT '',
			snapshot_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_seen_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS targets (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			primary_name TEXT NOT NULL,
			transport TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			first_seen_at DATETIME NOT NULL,
			last_seen_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS target_aliases (
			target_id TEXT NOT NULL,
			alias TEXT NOT NULL,
			alias_type TEXT NOT NULL DEFAULT 'alias',
			confidence REAL NOT NULL DEFAULT 0,
			last_seen_at DATETIME NOT NULL,
			PRIMARY KEY(target_id, alias, alias_type),
			FOREIGN KEY(target_id) REFERENCES targets(id)
		);`,
		`CREATE TABLE IF NOT EXISTS host_facts (
			host_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0,
			verified_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY(host_id, key),
			FOREIGN KEY(host_id) REFERENCES targets(id)
		);`,
		`CREATE TABLE IF NOT EXISTS playbooks (
			id TEXT PRIMARY KEY,
			target_id TEXT NOT NULL,
			intent TEXT NOT NULL DEFAULT 'general',
			tool_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			title TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			success_count INTEGER NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 0,
			last_verified_at DATETIME NOT NULL,
			last_used_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			match_terms_json TEXT NOT NULL DEFAULT '[]',
			preconditions_json TEXT NOT NULL DEFAULT '[]',
			verify_steps_json TEXT NOT NULL DEFAULT '[]',
			action_steps_json TEXT NOT NULL DEFAULT '[]',
			success_checks_json TEXT NOT NULL DEFAULT '[]',
			notes TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(target_id) REFERENCES targets(id)
		);`,
		`CREATE TABLE IF NOT EXISTS findings (
			id TEXT PRIMARY KEY,
			target_id TEXT NOT NULL,
			intent TEXT NOT NULL DEFAULT 'general',
			tool_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			origin TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0,
			seen_count INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(target_id) REFERENCES targets(id)
		);`,
		`CREATE TABLE IF NOT EXISTS cautions (
			id TEXT PRIMARY KEY,
			target_id TEXT NOT NULL,
			intent TEXT NOT NULL DEFAULT 'general',
			tool_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			body TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 1,
			last_seen_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(target_id) REFERENCES targets(id)
		);`,
		`CREATE TABLE IF NOT EXISTS routing_candidates (
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			phase TEXT NOT NULL,
			task_class TEXT NOT NULL,
			toolset TEXT NOT NULL DEFAULT '',
			approved INTEGER NOT NULL DEFAULT 0,
			score REAL NOT NULL DEFAULT 0,
			confidence REAL NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			PRIMARY KEY(provider, model, phase, task_class, toolset)
		);`,
		`CREATE TABLE IF NOT EXISTS model_approvals (
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			status TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			rationale TEXT NOT NULL DEFAULT '',
			approved_at DATETIME NOT NULL,
			PRIMARY KEY(provider, model)
		);`,
		`CREATE TABLE IF NOT EXISTS command_approvals (
			command TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			rationale TEXT NOT NULL DEFAULT '',
			approved_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS chat_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at DATETIME NOT NULL,
			finished_at DATETIME,
			provider TEXT NOT NULL,
			pinned_model TEXT NOT NULL,
			routing_enabled INTEGER NOT NULL DEFAULT 0,
			exit_reason TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS chat_turns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id INTEGER NOT NULL,
			turn_index INTEGER NOT NULL,
			user_input TEXT NOT NULL,
			task_class TEXT NOT NULL,
			requested_model TEXT NOT NULL,
			actual_model TEXT NOT NULL,
			success INTEGER NOT NULL DEFAULT 0,
			error_message TEXT NOT NULL DEFAULT '',
			latency_ms INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(session_id) REFERENCES chat_sessions(id)
		);`,
		`CREATE TABLE IF NOT EXISTS chat_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id INTEGER NOT NULL,
			turn_id INTEGER NOT NULL,
			message_index INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			tool_call_id TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			tool_arguments TEXT NOT NULL DEFAULT '',
			tool_calls_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			FOREIGN KEY(session_id) REFERENCES chat_sessions(id),
			FOREIGN KEY(turn_id) REFERENCES chat_turns(id)
		);`,
		`CREATE TABLE IF NOT EXISTS snapshots (
			id TEXT PRIMARY KEY,
			source_file TEXT NOT NULL,
			path TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		);`,
	}

	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE memory_entries ADD COLUMN seen_count INTEGER NOT NULL DEFAULT 1`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
