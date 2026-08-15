package cmd

import (
	"context"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/state"
)

// startChatSession creates the shared in-process conversation and its
// persisted session record for the interactive operations console.
func startChatSession(ctx context.Context, a *agent.Agent, store *state.Store, cfg *config.Config) (*agent.ChatConversation, int64, error) {
	session, selection, err := a.StartChat(ctx)
	if err != nil {
		return nil, 0, err
	}

	var sessionID int64
	if store != nil && store.Available() {
		sessionID, err = store.StartChatSession(ctx, state.ChatSession{
			StartedAt:      time.Now().UTC(),
			Provider:       selection.Requested.Provider,
			PinnedModel:    selection.Requested.Model,
			RoutingEnabled: cfg.RoutingEnabled,
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return session, sessionID, nil
}

func finishChatSession(ctx context.Context, store *state.Store, sessionID int64, exitReason string) {
	if store == nil || !store.Available() || sessionID == 0 {
		return
	}
	if err := store.FinishChatSession(ctx, sessionID, time.Now().UTC(), exitReason); err != nil {
		log.FromContext(ctx).Warn("failed to finish chat session", "error", err)
	}
}

func recordChatTurn(ctx context.Context, store *state.Store, sessionID int64, userInput string, result agent.ChatTurnResult) {
	if !hasChatTurnActivity(result) {
		return
	}

	logger := log.FromContext(ctx)
	persistChatTurn(ctx, store, sessionID, userInput, result)
	if store != nil && store.Available() && result.Phase.Provider != "" {
		if err := store.RecordChatPhaseStats(ctx, result.TaskClass, result.Phase, result.Tools); err != nil {
			logger.Warn("failed to record chat stats", "error", err)
		}
	}
	if store != nil && store.Available() && result.VerificationPhase.Provider != "" {
		if err := store.RecordChatPhaseStats(ctx, result.TaskClass, result.VerificationPhase, nil); err != nil {
			logger.Warn("failed to record chat verification stats", "error", err)
		}
	}
}

func persistChatTurn(ctx context.Context, store *state.Store, sessionID int64, userInput string, result agent.ChatTurnResult) {
	if store == nil || !store.Available() || sessionID == 0 {
		return
	}

	turn := state.ChatTurn{
		SessionID:                   sessionID,
		UserInput:                   userInput,
		TaskClass:                   result.TaskClass,
		RequestedModel:              result.Phase.RequestedModel,
		ActualModel:                 result.Phase.ActualModel,
		TaskState:                   result.TaskState,
		Success:                     result.Phase.Success,
		ErrorMessage:                errorString(result.ExecutionErr),
		LatencyMs:                   result.Phase.LatencyMs,
		PromptTokens:                result.Phase.PromptTokens,
		CompletionTokens:            result.Phase.CompletionTokens,
		TotalTokens:                 result.Phase.TotalTokens,
		FinalOutput:                 result.Output,
		VerificationStatus:          result.Verification.Status,
		VerificationReason:          result.Verification.Reason,
		VerificationMissingActions:  strings.Join(result.Verification.MissingActions, "\n"),
		VerificationRepairTriggered: result.Verification.RepairTriggered,
		CreatedAt:                   time.Now().UTC(),
	}
	messages := agent.TranscriptToStateMessages(sessionID, 0, 0, turn.CreatedAt, result.Transcript)
	if _, err := store.AppendChatTurn(ctx, sessionID, turn, messages, result.Tools); err != nil {
		log.FromContext(ctx).Warn("failed to persist chat turn", "error", err)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func hasChatTurnActivity(result agent.ChatTurnResult) bool {
	if strings.TrimSpace(result.Output) != "" {
		return true
	}
	if len(result.Transcript) > 0 || len(result.Tools) > 0 {
		return true
	}
	if result.Phase.Provider != "" || result.Phase.RequestedModel != "" || result.Phase.ActualModel != "" {
		return true
	}
	return result.Phase.LatencyMs > 0 || result.Phase.TotalTokens > 0 || result.Phase.CachedTokensKnown
}
