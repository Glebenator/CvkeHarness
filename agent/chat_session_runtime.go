package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/internal/secrets"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

// ChatConversation owns one in-process interactive chat session.
type ChatConversation struct {
	agent              *Agent
	selection          core.RoutingSelection
	history            *ChatState
	previousActionable classificationContext
}

// ChatTurnResult contains one assistant turn plus transcript and stats.
type ChatTurnResult struct {
	Output            string
	TaskState         state.TaskState
	TaskClass         core.TaskClass
	Phase             state.PhaseRecord
	VerificationPhase state.PhaseRecord
	Tools             []state.ToolOutcome
	Observed          []memory.ObservedToolCall
	Target            memory.TargetResolution
	Transcript        []provider.Message
	Routing           core.RoutingSelection
	Verification      CompletionVerification
	Curation          []memory.Lesson
	ExecutionErr      error
	CurationError     error
	BlockedWorkID     string
	ApprovalSummary   string
	ApprovalReason    string
	ApprovalEffects   []tools.ShellEffect
}

// ChatTool describes one capability registered for the current chat runtime.
// Registration only means the capability can be advertised; execution still
// passes through task filtering, target binding, policy, and approval checks.
type ChatTool struct {
	Name        string
	Description string
}

// StartChat creates a new interactive chat session with a pinned model.
func (a *Agent) StartChat(ctx context.Context) (*ChatConversation, core.RoutingSelection, error) {
	toolNames := []string{}
	if a.opts.ToolRegistry != nil {
		toolNames = a.opts.ToolRegistry.Names()
	}

	selection := core.RoutingSelection{
		Phase:       core.PhaseChat,
		Requested:   core.NewModelRef(a.opts.ProviderName, a.opts.DefaultModel),
		UsedDefault: true,
		Reason:      "routing disabled; using the configured default model",
	}
	if a.opts.Router != nil {
		task := "interactive chat session"
		taskClass := core.TaskClassGeneral
		var err error
		selection, err = a.opts.Router.Select(ctx, core.PhaseChat, task, taskClass, toolNames)
		if err != nil {
			return nil, core.RoutingSelection{}, err
		}
	}

	return &ChatConversation{
		agent:     a,
		selection: selection,
		history:   NewChatState(),
	}, selection, nil
}

// Tools returns the capabilities registered for this conversation in stable
// name order. It does not grant or imply authorization to execute them.
func (c *ChatConversation) Tools() []ChatTool {
	if c == nil || c.agent == nil || c.agent.opts.ToolRegistry == nil {
		return nil
	}
	names := c.agent.opts.ToolRegistry.Names()
	items := make([]ChatTool, 0, len(names))
	for _, name := range names {
		tool, ok := c.agent.opts.ToolRegistry.Get(name)
		if !ok {
			continue
		}
		items = append(items, ChatTool{Name: tool.Name(), Description: tool.Description()})
	}
	return items
}

// Turn executes one user prompt inside the active chat session.
func (c *ChatConversation) Turn(ctx context.Context, prompt string) (ChatTurnResult, error) {
	logger := log.FromContext(ctx)
	ctx = c.agent.withRunTelemetry(ctx)
	ctx = tools.WithEventObserver(ctx, c.agent.opts.EventObserver)

	classification := c.agent.classifyTask(ctx, prompt, c.previousActionable)
	taskClass := classification.Class
	phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, transcript, output, execErr := c.runChatTurn(ctx, prompt, taskClass)
	result := ChatTurnResult{
		Output:            output,
		TaskState:         taskStateForError(execErr),
		TaskClass:         taskClass,
		Phase:             phaseRecord,
		VerificationPhase: verificationRecord,
		Tools:             toolOutcomes,
		Observed:          observedCalls,
		Target:            targetResolution,
		Transcript:        transcript,
		Routing:           c.selection,
		Verification:      verification,
		ExecutionErr:      execErr,
	}
	if classification.Actionable || len(toolOutcomes) > 0 {
		c.previousActionable = classificationContext{
			PreviousActionablePrompt: prompt,
			PreviousActionableClass:  taskClass,
			PreviousToolNames:        uniqueObservedToolNames(observedCalls),
		}
	}
	if isBlockedTaskError(execErr) {
		var blocked blockedTaskError
		if errors.As(execErr, &blocked) {
			result.BlockedWorkID = blocked.WorkID()
			result.ApprovalSummary = blocked.request.Command
			result.ApprovalReason = blocked.request.ValidationError
			result.ApprovalEffects = append([]tools.ShellEffect(nil), blocked.request.Effects...)
		}
		_ = telemetry.Record(telemetry.WithFields(ctx, telemetry.Fields{TaskState: string(state.TaskStateBlockedWaitingUser)}), telemetry.Event{
			Type:      telemetry.EventTaskBlocked,
			TaskState: string(state.TaskStateBlockedWaitingUser),
		})
	}
	_ = telemetry.Record(telemetry.WithFields(ctx, telemetry.Fields{TaskState: string(result.TaskState)}), telemetry.Event{
		Type:      telemetry.EventTaskCompleted,
		TaskState: string(result.TaskState),
	})

	if c.agent.opts.MemoryCurator != nil && !isBlockedTaskError(execErr) {
		if curator, ok := c.agent.opts.MemoryCurator.(structuredMemoryCurator); ok {
			curErr := curator.CurateRunOutcome(ctx, memory.RunOutcome{
				Task:           prompt,
				TaskClass:      taskClass,
				Target:         targetResolution,
				Output:         output,
				ExecutionError: errString(execErr),
				ToolCalls:      observedCalls,
			})
			result.CurationError = curErr
			if curErr != nil {
				logger.Warn("failed to curate chat outcome", "error", curErr)
			}
		} else if c.agent.opts.Router != nil {
			_, _, lessons, curErr := c.agent.runCurationPhase(ctx, prompt, taskClass, output, toolOutcomes, execErr)
			result.Curation = lessons
			result.CurationError = curErr
			if curErr == nil && len(lessons) > 0 {
				if persistErr := c.agent.opts.MemoryCurator.PersistLessons(ctx, lessons); persistErr != nil {
					logger.Warn("failed to persist chat lessons", "error", persistErr)
				}
			}
		}
	}

	return result, execErr
}

func (c *ChatConversation) runChatTurn(ctx context.Context, prompt string, taskClass core.TaskClass) (state.PhaseRecord, state.PhaseRecord, CompletionVerification, []state.ToolOutcome, []memory.ObservedToolCall, memory.TargetResolution, []provider.Message, string, error) {
	logger := log.FromContext(ctx)
	targetResolution := c.agent.resolveTarget(ctx, memory.TargetResolutionInput{Task: prompt})
	toolDefs := c.agent.toolDefinitionsForTask(taskClass, prompt)
	toolNames := toolNamesFromDefs(toolDefs)
	retrieved, err := c.agent.retrieveMemory(ctx, core.RetrievalContext{
		Task:          prompt,
		TaskClass:     taskClass,
		Phase:         core.PhaseChat,
		ActiveModel:   c.selection.Requested,
		RuntimeHostID: targetResolution.RuntimeHostID,
		TargetID:      targetResolution.TargetID,
		TargetKind:    targetResolution.TargetKind,
		ToolNames:     toolNames,
	})
	if err != nil {
		return state.PhaseRecord{}, state.PhaseRecord{}, CompletionVerification{}, nil, nil, targetResolution, nil, "", err
	}
	emitMemoryInjection(ctx, core.PhaseChat, retrieved)

	execProvider, err := c.agent.resolveProvider(c.selection.Requested.Provider)
	if err != nil {
		return state.PhaseRecord{}, state.PhaseRecord{}, CompletionVerification{}, nil, nil, targetResolution, nil, "", err
	}

	userMessage := provider.Message{Role: "user", Content: prompt}
	c.history.Add(userMessage)
	transcript := []provider.Message{userMessage}

	volatileMessages := c.history.Messages()
	plan := buildPromptPlan(retrieved, "", volatileMessages, toolDefs)
	turnChat := NewChatState(append(append([]provider.Message(nil), plan.SystemMessages...), volatileMessages...)...)

	phaseRecord := state.PhaseRecord{
		Phase:          core.PhaseChat,
		Provider:       c.selection.Requested.Provider,
		RequestedModel: c.selection.Requested.Model,
		Confidence:     c.selection.Confidence,
		Explanation:    c.selection.Reason,
	}

	var actualModel = c.selection.Requested.Model
	var refreshed bool
	var repairAttempts int
	var lastRepairFingerprint string
	var latestVerification CompletionVerification
	var latestVerificationRecord state.PhaseRecord
	failuresByTool := make(map[string]int)
	var toolOutcomes []state.ToolOutcome
	var observedCalls []memory.ObservedToolCall

	for iter := 1; iter <= c.agent.opts.MaxIterations; iter++ {
		iterCtx := log.WithIteration(ctx, iter)
		req := &provider.ChatRequest{
			Model:       c.selection.Requested.Model,
			Messages:    turnChat.Messages(),
			Tools:       toolDefs,
			Temperature: 0.2,
			MaxTokens:   c.agent.opts.MaxTokens,
		}
		iterPlan := plan
		iterPlan.PromptHash = hashJSON([]any{req.Messages, req.Tools})
		emitPromptPlanned(iterCtx, core.PhaseChat, iter, c.selection.Requested.Provider, c.selection.Requested.Model, iterPlan, len(req.Messages))
		dump := c.agent.dumpPrompt(iterCtx, promptdump.Metadata{
			Phase:     core.PhaseChat,
			Provider:  c.selection.Requested.Provider,
			Model:     c.selection.Requested.Model,
			TaskClass: taskClass,
			Iteration: iter,
			Label:     "chat-turn",
		}, req)

		start := time.Now()
		resp, err := execProvider.ChatCompletion(iterCtx, req)
		c.agent.finishPromptDump(dump, resp, err)
		duration := time.Since(start)
		emitModelCallCompleted(iterCtx, core.PhaseChat, iter, c.selection.Requested.Provider, c.selection.Requested.Model, resp, duration.Milliseconds(), err)
		phaseRecord.LatencyMs += duration.Milliseconds()
		if resp != nil {
			phaseRecord.PromptTokens += resp.Usage.PromptTokens
			phaseRecord.CompletionTokens += resp.Usage.CompletionTokens
			phaseRecord.TotalTokens += resp.Usage.TotalTokens
			if cachedTokens, ok := resp.Usage.CachedTokens(); ok {
				phaseRecord.CachedTokens += cachedTokens
				phaseRecord.CachedTokensKnown = true
			}
		}

		if err != nil {
			return phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, transcript, "", fmt.Errorf("LLM API error on iteration %d: %w", iter, err)
		}

		if resp.Model != "" {
			actualModel = resp.Model
		}
		phaseRecord.ActualModel = actualModel

		turnChat.Add(resp.Message)
		if len(resp.Message.ToolCalls) > 0 {
			c.history.Add(resp.Message)
			transcript = append(transcript, resp.Message)
		}

		if len(resp.Message.ToolCalls) == 0 {
			output := resp.Message.Content
			if c.agent.opts.DisableCompletionVerification {
				logger.Info("chat turn finished")
				c.history.Add(resp.Message)
				transcript = append(transcript, resp.Message)
				phaseRecord.Success = true
				return phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, transcript, output, nil
			}
			verification, verificationRecord, err := c.agent.verifyCompletion(iterCtx, c.selection, taskClass, prompt, output, observedCalls, nil)
			verification.RepairTriggered = repairAttempts > 0
			latestVerification = verification
			latestVerificationRecord = verificationRecord
			if err != nil {
				return phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, transcript, output, fmt.Errorf("completion verification failed: %w", err)
			}
			if verification.satisfied() {
				logger.Info("chat turn finished after verification")
				c.history.Add(resp.Message)
				transcript = append(transcript, resp.Message)
				phaseRecord.Success = true
				return phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, transcript, output, nil
			}
			fingerprint := repairFingerprint(output, toolNames, len(observedCalls), verification)
			noProgress := lastRepairFingerprint != "" && fingerprint == lastRepairFingerprint
			if repairAttempts < c.agent.maxRepairAttempts() && iter < c.agent.opts.MaxIterations && !noProgress {
				repairContext := c.previousActionable
				repairContext.RepairInstruction = verification.repairPrompt()
				reclassified := c.agent.classifyTask(iterCtx, prompt, repairContext)
				newToolDefs := c.agent.toolDefinitionsForTask(reclassified.Class, prompt+"\n"+verification.repairPrompt())
				newToolNames := toolNamesFromDefs(newToolDefs)
				capabilitiesChanged := core.ToolsetKey(newToolNames) != core.ToolsetKey(toolNames)
				capabilityUnavailable := requiredCapabilityUnavailable(verification, newToolNames)
				repairAttempts++
				emitRepairAttempt(iterCtx, repairAttempts, verification.Reason, capabilitiesChanged, false, capabilityUnavailable, newToolNames)
				if capabilityUnavailable {
					phaseRecord.Success = false
					c.history.Add(resp.Message)
					transcript = append(transcript, resp.Message)
					return phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, transcript, output, incompleteTaskError{verification: verification}
				}
				if capabilitiesChanged {
					messages := turnChat.Messages()
					if len(messages) >= len(plan.SystemMessages) {
						volatile := append([]provider.Message(nil), messages[len(plan.SystemMessages):]...)
						plan = buildPromptPlan(retrieved, "", volatile, newToolDefs)
						turnChat = NewChatState(append(append([]provider.Message(nil), plan.SystemMessages...), volatile...)...)
					}
					toolDefs, toolNames, taskClass = newToolDefs, newToolNames, reclassified.Class
				}
				lastRepairFingerprint = fingerprint
				verification.RepairTriggered = true
				latestVerification = verification
				turnChat.AddSystem(verification.repairPrompt())
				continue
			}
			emitRepairAttempt(iterCtx, repairAttempts, verification.Reason, false, noProgress, false, toolNames)
			phaseRecord.Success = false
			c.history.Add(resp.Message)
			transcript = append(transcript, resp.Message)
			return phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, transcript, output, incompleteTaskError{verification: verification}
		}

		for _, call := range resp.Message.ToolCalls {
			command := commandForToolCall(call)
			if command != "" {
				targetResolution = c.agent.resolveTarget(ctx, memory.TargetResolutionInput{
					Task:    prompt,
					Command: command,
				})
			}
			toolStart := time.Now()
			toolCtx := tools.WithToolCallContext(telemetry.WithFields(telemetry.WithModel(iterCtx, actualModel), telemetry.Fields{
				ToolCallID: call.ID,
				TargetID:   targetResolution.TargetID,
			}), call.ID, call.Function.Name)
			tools.EmitEvent(toolCtx, tools.Event{
				Type:    tools.EventToolCallStarted,
				Success: true,
			})
			payload, _ := json.Marshal(map[string]any{"tool_name": call.Function.Name})
			_ = telemetry.Record(toolCtx, telemetry.Event{
				Type:       telemetry.EventToolStarted,
				ToolCallID: call.ID,
				TargetID:   targetResolution.TargetID,
				Payload:    payload,
			})
			resultStr, toolErr := c.agent.opts.ToolRegistry.ExecuteTool(toolCtx, call)
			durationMs := time.Since(toolStart).Milliseconds()

			outcome := state.ToolOutcome{
				Phase:      core.PhaseChat,
				Provider:   c.selection.Requested.Provider,
				Model:      actualModel,
				ToolName:   call.Function.Name,
				Toolset:    core.ToolsetKey(toolNames),
				Arguments:  call.Function.Arguments,
				Command:    command,
				Success:    toolErr == nil,
				DurationMs: durationMs,
			}

			if toolErr != nil {
				failuresByTool[call.Function.Name]++
				outcome.ErrorMessage = toolErr.Error()
				outcome.PolicyDenied, outcome.DenialClass = classifyPolicyDenial(toolErr)
				resultStr = fmt.Sprintf("Error executing tool: %v", toolErr)

				if approvalErr, ok := tools.IsApprovalRequired(toolErr); ok {
					phaseRecord.Success = false
					workID, persistErr := c.agent.persistBlockedWork(iterCtx, prompt, taskClass, targetResolution, turnChat.Messages(), call, approvalErr)
					if persistErr != nil {
						return phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, transcript, "", persistErr
					}
					return phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, transcript, "", blockedTaskError{
						workID:  workID,
						reason:  toolErr.Error(),
						request: approvalErr.Request,
					}
				}

				if !refreshed && (outcome.PolicyDenied || failuresByTool[call.Function.Name] >= 2) {
					refreshed = true
					refresh, refreshErr := c.agent.retrieveMemory(ctx, core.RetrievalContext{
						Task:          prompt,
						TaskClass:     taskClass,
						Phase:         core.PhaseChat,
						ActiveModel:   c.selection.Requested,
						ActualModel:   core.NewModelRef(c.selection.Requested.Provider, actualModel),
						RuntimeHostID: targetResolution.RuntimeHostID,
						TargetID:      targetResolution.TargetID,
						TargetKind:    targetResolution.TargetKind,
						ToolNames:     toolNames,
						Trouble: &core.ToolTrouble{
							Tool:        call.Function.Name,
							DenialClass: outcome.DenialClass,
							Repeated:    failuresByTool[call.Function.Name] >= 2,
						},
					})
					if refreshErr == nil {
						emitMemoryInjection(ctx, core.PhaseChat, refresh)
						if refreshedText := retrievedBrief(refresh); strings.TrimSpace(refreshedText) != "" {
							turnChat.AddSystem("Refreshed learned context after tool trouble:\n" + refreshedText)
						}
					}
				}
			}

			tools.EmitEvent(toolCtx, tools.Event{
				Type:         tools.EventToolCallFinished,
				Success:      toolErr == nil,
				Duration:     time.Duration(durationMs) * time.Millisecond,
				ErrorMessage: outcome.ErrorMessage,
				Output:       resultStr,
			})
			outcome.OutputInline, outcome.OutputOriginalBytes, outcome.OutputStoredBytes, outcome.OutputTruncated, outcome.OutputDigest = state.SummarizeToolOutput(resultStr)
			payload, _ = json.Marshal(map[string]any{
				"tool_name":             call.Function.Name,
				"command":               command,
				"success":               toolErr == nil,
				"duration_ms":           durationMs,
				"error":                 outcome.ErrorMessage,
				"output_inline":         outcome.OutputInline,
				"output_original_bytes": outcome.OutputOriginalBytes,
				"output_stored_bytes":   outcome.OutputStoredBytes,
				"output_truncated":      outcome.OutputTruncated,
				"output_digest":         outcome.OutputDigest,
			})
			_ = telemetry.Record(toolCtx, telemetry.Event{
				Type:       telemetry.EventToolFinished,
				ToolCallID: call.ID,
				TargetID:   targetResolution.TargetID,
				Payload:    payload,
			})

			toolOutcomes = append(toolOutcomes, outcome)
			observedCalls = append(observedCalls, memory.ObservedToolCall{
				ToolName:     call.Function.Name,
				Command:      command,
				Result:       resultStr,
				Success:      toolErr == nil,
				PolicyDenied: outcome.PolicyDenied,
				DenialClass:  outcome.DenialClass,
				DurationMs:   durationMs,
			})
			toolMessage := provider.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    resultStr,
			}
			turnChat.AddToolResult(call.ID, resultStr)
			c.history.Add(toolMessage)
			transcript = append(transcript, toolMessage)
		}
	}

	return phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, transcript, "", fmt.Errorf("agent exceeded max iterations (%d) without completing the task", c.agent.opts.MaxIterations)
}

// Selection returns the pinned routing decision for the active chat session.
func (c *ChatConversation) Selection() core.RoutingSelection {
	return c.selection
}

// History returns the persisted in-memory conversation history for the session.
func (c *ChatConversation) History() []provider.Message {
	return c.history.Messages()
}

func TranscriptToStateMessages(sessionID, turnID int64, startIndex int, at time.Time, transcript []provider.Message) []state.ChatMessage {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	out := make([]state.ChatMessage, 0, len(transcript))
	nextIndex := startIndex
	for _, message := range transcript {
		item := state.ChatMessage{
			SessionID:    sessionID,
			TurnID:       turnID,
			MessageIndex: nextIndex,
			Role:         message.Role,
			Content:      message.Content,
			ToolCallID:   message.ToolCallID,
			CreatedAt:    at,
		}
		if len(message.ToolCalls) == 1 {
			item.ToolName = message.ToolCalls[0].Function.Name
			item.ToolArguments = message.ToolCalls[0].Function.Arguments
		}
		if len(message.ToolCalls) > 0 {
			if raw, err := json.Marshal(message.ToolCalls); err == nil {
				item.ToolCallsJSON = string(raw)
			}
		}
		item.Content = secrets.Mask(item.Content)
		item.ToolArguments = secrets.Mask(item.ToolArguments)
		item.ToolCallsJSON = secrets.Mask(item.ToolCallsJSON)
		out = append(out, item)
		nextIndex++
	}
	return out
}
