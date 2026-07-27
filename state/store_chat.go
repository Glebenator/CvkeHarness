package state

import (
	"context"
	"database/sql"
	"time"

	"github.com/coolcake/cvkeharness/core"
)

// StartChatSession inserts a new interactive chat session.
func (s *Store) StartChatSession(ctx context.Context, session ChatSession) (int64, error) {
	if !s.Available() {
		return 0, s.Err()
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_sessions (
			started_at, provider, pinned_model, routing_enabled, exit_reason
		) VALUES (?, ?, ?, ?, ?)`,
		session.StartedAt.UTC(),
		session.Provider,
		session.PinnedModel,
		boolToInt(session.RoutingEnabled),
		session.ExitReason,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishChatSession marks a chat session as finished.
func (s *Store) FinishChatSession(ctx context.Context, sessionID int64, finishedAt time.Time, exitReason string) error {
	if !s.Available() {
		return s.Err()
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE chat_sessions
		SET finished_at = ?, exit_reason = ?
		WHERE id = ?`,
		finishedAt.UTC(),
		exitReason,
		sessionID,
	)
	return err
}

// AppendChatTurn persists a chat turn, transcript messages, and tool outcomes.
func (s *Store) AppendChatTurn(ctx context.Context, sessionID int64, turn ChatTurn, messages []ChatMessage, tools []ToolOutcome) (int64, error) {
	if !s.Available() {
		return 0, s.Err()
	}
	if turn.TaskState == "" {
		if turn.Success {
			turn.TaskState = TaskStateCompleted
		} else {
			turn.TaskState = TaskStateFailed
		}
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	turnIndex, err := nextChatTurnIndexTx(ctx, tx, sessionID)
	if err != nil {
		return 0, err
	}
	messageIndex, err := nextChatMessageIndexTx(ctx, tx, sessionID)
	if err != nil {
		return 0, err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO chat_turns (
			session_id, turn_index, user_input, task_class, requested_model, actual_model,
			task_state, success, error_message, latency_ms, prompt_tokens, completion_tokens, total_tokens,
			final_output, verification_status, verification_reason, verification_missing_actions,
			verification_repair_triggered, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		turnIndex,
		turn.UserInput,
		string(turn.TaskClass),
		turn.RequestedModel,
		turn.ActualModel,
		string(turn.TaskState),
		boolToInt(turn.Success),
		turn.ErrorMessage,
		turn.LatencyMs,
		turn.PromptTokens,
		turn.CompletionTokens,
		turn.TotalTokens,
		turn.FinalOutput,
		turn.VerificationStatus,
		turn.VerificationReason,
		turn.VerificationMissingActions,
		boolToInt(turn.VerificationRepairTriggered),
		turn.CreatedAt.UTC(),
	)
	if err != nil {
		return 0, err
	}
	turnID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	var provider string
	if err := tx.QueryRowContext(ctx, `
		SELECT provider
		FROM chat_sessions
		WHERE id = ?`,
		sessionID,
	).Scan(&provider); err != nil {
		return 0, err
	}
	if err := saveModelAliasTx(ctx, tx, provider, turn.RequestedModel, turn.ActualModel, turn.CreatedAt); err != nil {
		return 0, err
	}

	for _, message := range messages {
		createdAt := message.CreatedAt
		if createdAt.IsZero() {
			createdAt = turn.CreatedAt
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chat_messages (
				session_id, turn_id, message_index, role, content, tool_call_id,
				tool_name, tool_arguments, tool_calls_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionID,
			turnID,
			messageIndex,
			message.Role,
			message.Content,
			message.ToolCallID,
			message.ToolName,
			message.ToolArguments,
			message.ToolCallsJSON,
			createdAt.UTC(),
		); err != nil {
			return 0, err
		}
		messageIndex++
	}

	for _, tool := range tools {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chat_tool_outcomes (
				session_id, turn_id, phase, provider, model, tool_name, toolset, arguments,
				command, success, policy_denied, denial_class, error_message, duration_ms,
				task_class, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionID,
			turnID,
			string(tool.Phase),
			tool.Provider,
			tool.Model,
			tool.ToolName,
			tool.Toolset,
			tool.Arguments,
			tool.Command,
			boolToInt(tool.Success),
			boolToInt(tool.PolicyDenied),
			tool.DenialClass,
			tool.ErrorMessage,
			tool.DurationMs,
			string(turn.TaskClass),
			turn.CreatedAt.UTC(),
		); err != nil {
			return 0, err
		}
	}

	return turnID, tx.Commit()
}

// RecordChatPhaseStats updates model stats for chat turns without creating a run record.
func (s *Store) RecordChatPhaseStats(ctx context.Context, taskClass core.TaskClass, phase PhaseRecord, tools []ToolOutcome) error {
	if !s.Available() {
		return s.Err()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.bumpModelStatsTx(ctx, tx, taskClass, phase, tools); err != nil {
		return err
	}
	return tx.Commit()
}

func nextChatTurnIndexTx(ctx context.Context, tx *sql.Tx, sessionID int64) (int, error) {
	var next int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(turn_index), 0) + 1
		FROM chat_turns
		WHERE session_id = ?`,
		sessionID,
	).Scan(&next); err != nil {
		return 0, err
	}
	return next, nil
}

func nextChatMessageIndexTx(ctx context.Context, tx *sql.Tx, sessionID int64) (int, error) {
	var next int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(message_index), 0) + 1
		FROM chat_messages
		WHERE session_id = ?`,
		sessionID,
	).Scan(&next); err != nil {
		return 0, err
	}
	return next, nil
}
