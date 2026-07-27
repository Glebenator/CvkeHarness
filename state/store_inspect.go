package state

import (
	"context"
	"database/sql"

	"github.com/coolcake/cvkeharness/core"
)

// ListRecentRuns returns recent agent runs with phase and tool summaries.
func (s *Store) ListRecentRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, started_at, finished_at, provider, task, task_class, task_state, success,
			error_message, final_output, verification_status, verification_reason,
			verification_missing_actions, verification_repair_triggered, routing_enabled
		FROM runs
		ORDER BY started_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RunSummary
	for rows.Next() {
		var item RunSummary
		var taskClass, taskState string
		var success, repair, routing int
		if err := rows.Scan(
			&item.ID, &item.StartedAt, &item.FinishedAt, &item.Provider, &item.Task,
			&taskClass, &taskState, &success, &item.ErrorMessage, &item.FinalOutput,
			&item.VerificationStatus, &item.VerificationReason,
			&item.VerificationMissingActions, &repair, &routing,
		); err != nil {
			return nil, err
		}
		item.TaskClass = core.TaskClass(taskClass)
		item.TaskState = TaskState(taskState)
		item.Success = success == 1
		item.VerificationRepairTriggered = repair == 1
		item.RoutingEnabled = routing == 1
		item.Phases, err = s.listRunPhases(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Tools, err = s.listRunTools(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) listRunPhases(ctx context.Context, runID int64) ([]PhaseRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT phase, provider, requested_model, actual_model, success, latency_ms,
			prompt_tokens, completion_tokens, total_tokens, confidence, explanation
		FROM phase_records
		WHERE run_id = ?
		ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PhaseRecord
	for rows.Next() {
		var item PhaseRecord
		var phase string
		var success int
		if err := rows.Scan(
			&phase, &item.Provider, &item.RequestedModel, &item.ActualModel,
			&success, &item.LatencyMs, &item.PromptTokens, &item.CompletionTokens,
			&item.TotalTokens, &item.Confidence, &item.Explanation,
		); err != nil {
			return nil, err
		}
		item.Phase = core.Phase(phase)
		item.Success = success == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) listRunTools(ctx context.Context, runID int64) ([]ToolOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT phase, provider, model, tool_name, toolset, arguments, command, success, policy_denied,
			denial_class, error_message, duration_ms
		FROM tool_outcomes
		WHERE run_id = ?
		ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ToolOutcome
	for rows.Next() {
		var item ToolOutcome
		var phase string
		var success, denied int
		if err := rows.Scan(
			&phase, &item.Provider, &item.Model, &item.ToolName, &item.Toolset,
			&item.Arguments, &item.Command, &success, &denied, &item.DenialClass, &item.ErrorMessage, &item.DurationMs,
		); err != nil {
			return nil, err
		}
		item.Phase = core.Phase(phase)
		item.Success = success == 1
		item.PolicyDenied = denied == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListRecentChatSessions returns recent persisted chat sessions.
func (s *Store) ListRecentChatSessions(ctx context.Context, limit int) ([]ChatSessionSummary, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT cs.id, cs.started_at, cs.finished_at, cs.provider, cs.pinned_model,
			cs.routing_enabled, cs.exit_reason, COUNT(ct.id)
		FROM chat_sessions cs
		LEFT JOIN chat_turns ct ON ct.session_id = cs.id
		GROUP BY cs.id
		ORDER BY cs.started_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatSessionSummary
	for rows.Next() {
		item, err := scanChatSessionSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetChatSessionDetail returns one chat session and its transcript rows.
func (s *Store) GetChatSessionDetail(ctx context.Context, id int64) (ChatSessionDetail, error) {
	if !s.Available() {
		return ChatSessionDetail{}, s.Err()
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT cs.id, cs.started_at, cs.finished_at, cs.provider, cs.pinned_model,
			cs.routing_enabled, cs.exit_reason, COUNT(ct.id)
		FROM chat_sessions cs
		LEFT JOIN chat_turns ct ON ct.session_id = cs.id
		WHERE cs.id = ?
		GROUP BY cs.id`, id)
	session, err := scanChatSessionSummary(row)
	if err != nil {
		return ChatSessionDetail{}, err
	}

	turns, err := s.listChatTurns(ctx, id)
	if err != nil {
		return ChatSessionDetail{}, err
	}
	messages, err := s.listChatMessages(ctx, id)
	if err != nil {
		return ChatSessionDetail{}, err
	}
	tools, err := s.listChatTools(ctx, id)
	if err != nil {
		return ChatSessionDetail{}, err
	}
	return ChatSessionDetail{Session: session, Turns: turns, Messages: messages, ToolsByTurnID: tools}, nil
}

type chatSessionScanner interface {
	Scan(dest ...any) error
}

func scanChatSessionSummary(scanner chatSessionScanner) (ChatSessionSummary, error) {
	var item ChatSessionSummary
	var finished sql.NullTime
	var routing int
	if err := scanner.Scan(
		&item.ID, &item.StartedAt, &finished, &item.Provider, &item.PinnedModel,
		&routing, &item.ExitReason, &item.TurnCount,
	); err != nil {
		return ChatSessionSummary{}, err
	}
	if finished.Valid {
		item.FinishedAt = finished.Time
	}
	item.RoutingEnabled = routing == 1
	return item, nil
}

func (s *Store) listChatTurns(ctx context.Context, sessionID int64) ([]ChatTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, turn_index, user_input, task_class, requested_model,
			actual_model, task_state, success, error_message, latency_ms, prompt_tokens,
			completion_tokens, total_tokens, final_output, verification_status,
			verification_reason, verification_missing_actions, verification_repair_triggered,
			created_at
		FROM chat_turns
		WHERE session_id = ?
		ORDER BY turn_index ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatTurn
	for rows.Next() {
		var item ChatTurn
		var taskClass, taskState string
		var success, repair int
		if err := rows.Scan(
			&item.ID, &item.SessionID, &item.TurnIndex, &item.UserInput, &taskClass,
			&item.RequestedModel, &item.ActualModel, &taskState, &success, &item.ErrorMessage,
			&item.LatencyMs, &item.PromptTokens, &item.CompletionTokens, &item.TotalTokens,
			&item.FinalOutput, &item.VerificationStatus, &item.VerificationReason,
			&item.VerificationMissingActions, &repair, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.TaskClass = core.TaskClass(taskClass)
		item.TaskState = TaskState(taskState)
		item.Success = success == 1
		item.VerificationRepairTriggered = repair == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) listChatMessages(ctx context.Context, sessionID int64) ([]ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, turn_id, message_index, role, content, tool_call_id,
			tool_name, tool_arguments, tool_calls_json, created_at
		FROM chat_messages
		WHERE session_id = ?
		ORDER BY message_index ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatMessage
	for rows.Next() {
		var item ChatMessage
		if err := rows.Scan(
			&item.ID, &item.SessionID, &item.TurnID, &item.MessageIndex, &item.Role,
			&item.Content, &item.ToolCallID, &item.ToolName, &item.ToolArguments,
			&item.ToolCallsJSON, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) listChatTools(ctx context.Context, sessionID int64) (map[int64][]ToolOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT turn_id, phase, provider, model, tool_name, toolset, arguments, command,
			success, policy_denied, denial_class, error_message, duration_ms
		FROM chat_tool_outcomes
		WHERE session_id = ?
		ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]ToolOutcome)
	for rows.Next() {
		var turnID int64
		var item ToolOutcome
		var phase string
		var success, denied int
		if err := rows.Scan(
			&turnID, &phase, &item.Provider, &item.Model, &item.ToolName, &item.Toolset,
			&item.Arguments, &item.Command, &success, &denied, &item.DenialClass,
			&item.ErrorMessage, &item.DurationMs,
		); err != nil {
			return nil, err
		}
		item.Phase = core.Phase(phase)
		item.Success = success == 1
		item.PolicyDenied = denied == 1
		out[turnID] = append(out[turnID], item)
	}
	return out, rows.Err()
}

// ListSystemCronAudits returns recent crontab mutation audit records.
func (s *Store) ListSystemCronAudits(ctx context.Context, limit int) ([]SystemCronAudit, error) {
	if !s.Available() {
		return nil, s.Err()
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, action, target, old_snippet, new_snippet, success,
			error_message, initiating_tool, created_at
		FROM system_cron_audit
		ORDER BY created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SystemCronAudit
	for rows.Next() {
		var item SystemCronAudit
		var success int
		if err := rows.Scan(
			&item.ID, &item.Action, &item.Target, &item.OldSnippet, &item.NewSnippet,
			&success, &item.ErrorMessage, &item.InitiatingTool, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.Success = success == 1
		out = append(out, item)
	}
	return out, rows.Err()
}
