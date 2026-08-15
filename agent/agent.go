package agent

import (
	"context"
	"encoding/json"
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

// ProviderResolver resolves a concrete provider client by name.
type ProviderResolver interface {
	Resolve(provider string) (provider.Provider, error)
}

// Router picks a model for a routed phase.
type Router interface {
	Select(ctx context.Context, phase core.Phase, task string, taskClass core.TaskClass, toolNames []string) (core.RoutingSelection, error)
}

// MemoryRetriever loads the current memory context for a phase.
type MemoryRetriever interface {
	Retrieve(ctx context.Context, input core.RetrievalContext) (memory.RetrievalResult, error)
}

// MemoryCurator persists newly learned lessons.
type MemoryCurator interface {
	PersistLessons(ctx context.Context, lessons []memory.Lesson) error
}

type memoryTargetResolver interface {
	ResolveTarget(ctx context.Context, input memory.TargetResolutionInput) (memory.TargetResolution, error)
}

type structuredMemoryCurator interface {
	CurateRunOutcome(ctx context.Context, outcome memory.RunOutcome) error
}

// RunRecorder stores a structured run record.
type RunRecorder interface {
	RecordRun(ctx context.Context, record state.RunRecord) error
}

// BlockedWorkStore persists resumable user-action waits.
type BlockedWorkStore interface {
	SaveBlockedWork(ctx context.Context, work state.BlockedWork) (string, error)
	ResolveBlockedWork(ctx context.Context, id string, nextState state.TaskState) error
}

// Options configures a new agent.
type Options struct {
	Provider           provider.Provider
	ProviderName       string
	ProviderResolver   ProviderResolver
	ToolRegistry       *tools.Registry
	EventObserver      tools.EventObserver
	DefaultModel       string
	MaxIterations      int
	MaxTokens          int
	MaxRepairAttempts  int
	RoutingConfig      core.RoutingConfig
	Router             Router
	MemoryRetriever    MemoryRetriever
	MemoryCurator      MemoryCurator
	RunRecorder        RunRecorder
	BlockedWorkStore   BlockedWorkStore
	PromptDumper       *promptdump.Dumper
	TelemetryWriter    *telemetry.Writer
	SafetyMode         string
	SafetyModel        string
	ClassifierProvider provider.Provider
	// DisableCompletionVerification is intended for focused tests and special
	// harnesses that already evaluate completion externally.
	DisableCompletionVerification bool
}

func (a *Agent) maxRepairAttempts() int {
	if a != nil && a.opts.MaxRepairAttempts > 0 {
		return a.opts.MaxRepairAttempts
	}
	return 2
}

// Agent orchestrates routed model execution and tool use.
type Agent struct {
	opts Options
}

// RunResult contains the user-facing output plus structured execution details.
type RunResult struct {
	Output       string
	Run          state.RunRecord
	Routing      []core.RoutingSelection
	Verification CompletionVerification
}

// New creates a new Agent.
func New(opts Options) *Agent {
	return &Agent{opts: opts}
}

// Run executes the harness loop for a given prompt.
func (a *Agent) Run(ctx context.Context, prompt string) (result RunResult, err error) {
	logger := log.FromContext(ctx)
	ctx = a.withRunTelemetry(ctx)
	ctx = tools.WithEventObserver(ctx, a.opts.EventObserver)
	classification := a.classifyTask(ctx, prompt, classificationContext{})
	taskClass := classification.Class
	toolDefs := a.toolDefinitionsForTask(taskClass, prompt)
	toolNames := toolNamesFromDefs(toolDefs)

	startedAt := time.Now().UTC()
	runRecord := state.RunRecord{
		StartedAt:      startedAt,
		Provider:       a.opts.ProviderName,
		Task:           prompt,
		TaskClass:      taskClass,
		TaskState:      state.TaskStateRunning,
		RoutingEnabled: a.opts.RoutingConfig.Enabled,
	}

	defer func() {
		runRecord.FinishedAt = time.Now().UTC()
		runRecord.TaskState = taskStateForError(err)
		runRecord.Success = runRecord.TaskState == state.TaskStateCompleted
		if err != nil {
			runRecord.ErrorMessage = err.Error()
		}
		result.Run = runRecord
		if a.opts.RunRecorder != nil {
			if recErr := a.opts.RunRecorder.RecordRun(ctx, runRecord); recErr != nil {
				logger.Warn("failed to record run", "error", recErr)
			}
		}
		_ = telemetry.Record(telemetry.WithFields(ctx, telemetry.Fields{TaskState: string(runRecord.TaskState)}), telemetry.Event{
			Type:      telemetry.EventTaskCompleted,
			TaskState: string(runRecord.TaskState),
		})
	}()

	logger.Info("CvkeHarness starting task", "task", prompt, "task_class", taskClass)

	var routingSelections []core.RoutingSelection
	var planningNotes string
	if a.opts.RoutingConfig.Enabled && a.opts.Router != nil {
		planningSelection, planRecord, planText, planErr := a.runPlanningPhase(ctx, prompt, taskClass, toolNames)
		routingSelections = append(routingSelections, planningSelection)
		if planRecord.Provider != "" {
			runRecord.Phases = append(runRecord.Phases, planRecord)
		}
		if planErr != nil {
			logger.Warn("planning phase failed, continuing with execution", "error", planErr)
		}
		planningNotes = planText
	}

	execSelection := core.RoutingSelection{
		Phase:       core.PhaseExecution,
		Requested:   core.NewModelRef(a.opts.ProviderName, a.opts.DefaultModel),
		UsedDefault: true,
		Reason:      "routing disabled; using the configured default model",
	}
	if a.opts.Router != nil {
		execSelection, err = a.opts.Router.Select(ctx, core.PhaseExecution, prompt, taskClass, toolNames)
		if err != nil {
			return result, err
		}
	}
	routingSelections = append(routingSelections, execSelection)

	output, phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, execErr := a.runExecutionPhase(ctx, prompt, taskClass, toolDefs, toolNames, planningNotes, execSelection)
	runRecord.Phases = append(runRecord.Phases, phaseRecord)
	if verificationRecord.Provider != "" {
		runRecord.Phases = append(runRecord.Phases, verificationRecord)
		routingSelections = append(routingSelections, core.RoutingSelection{
			Phase:     core.PhaseVerification,
			Requested: execSelection.Requested,
			Reason:    "completion verification uses the same model as execution",
		})
	}
	runRecord.Tools = append(runRecord.Tools, toolOutcomes...)
	runRecord.FinalOutput = output
	runRecord.VerificationStatus = verification.Status
	runRecord.VerificationReason = verification.Reason
	runRecord.VerificationMissingActions = verification.missingActionsText()
	runRecord.VerificationRepairTriggered = verification.RepairTriggered
	result.Output = output
	result.Routing = routingSelections
	result.Verification = verification
	if execErr != nil {
		err = execErr
	}

	if a.opts.MemoryCurator != nil && !isBlockedTaskError(execErr) {
		if curator, ok := a.opts.MemoryCurator.(structuredMemoryCurator); ok {
			if curErr := curator.CurateRunOutcome(ctx, memory.RunOutcome{
				Task:           prompt,
				TaskClass:      taskClass,
				Target:         targetResolution,
				Output:         output,
				ExecutionError: errString(execErr),
				ToolCalls:      observedCalls,
			}); curErr != nil {
				logger.Warn("failed to curate run outcome", "error", curErr)
			}
		} else {
			var curationLessons []memory.Lesson
			if a.opts.RoutingConfig.Enabled && a.opts.Router != nil {
				curationSelection, curationRecord, modelLessons, curErr := a.runCurationPhase(ctx, prompt, taskClass, output, toolOutcomes, execErr)
				routingSelections = append(routingSelections, curationSelection)
				result.Routing = routingSelections
				if curationRecord.Provider != "" {
					runRecord.Phases = append(runRecord.Phases, curationRecord)
				}
				if curErr == nil && len(modelLessons) > 0 {
					curationLessons = modelLessons
				}
			}
			if len(curationLessons) > 0 {
				if persistErr := a.opts.MemoryCurator.PersistLessons(ctx, curationLessons); persistErr != nil {
					logger.Warn("failed to persist lessons", "error", persistErr)
				}
			}
		}
	}

	return result, err
}

// ResumeBlockedWork retries the stored task after the required user action has occurred.
func (a *Agent) ResumeBlockedWork(ctx context.Context, work state.BlockedWork) (RunResult, error) {
	if a.opts.BlockedWorkStore != nil {
		if err := a.opts.BlockedWorkStore.ResolveBlockedWork(ctx, work.ID, state.TaskStateRunning); err != nil {
			return RunResult{}, err
		}
	}
	resumeCtx := ctx
	if a.opts.TelemetryWriter != nil {
		resumeCtx = telemetry.WithWriter(resumeCtx, a.opts.TelemetryWriter)
	}
	payload, _ := json.Marshal(map[string]any{"blocked_work_id": work.ID})
	_ = telemetry.Record(resumeCtx, telemetry.Event{
		Type:      telemetry.EventTaskResumed,
		TaskState: string(state.TaskStateRunning),
		Payload:   payload,
	})
	return a.Run(ctx, work.Task)
}

func (a *Agent) runPlanningPhase(ctx context.Context, prompt string, taskClass core.TaskClass, toolNames []string) (core.RoutingSelection, state.PhaseRecord, string, error) {
	selection, err := a.opts.Router.Select(ctx, core.PhasePlanning, prompt, taskClass, nil)
	if err != nil {
		return core.RoutingSelection{}, state.PhaseRecord{}, "", err
	}

	planPrompt := "Write a concise 3-step plan for the task below. Do not call tools. Do not answer the user directly.\n\nTask:\n" + prompt
	content, record, _, err := a.singleModelCall(ctx, core.PhasePlanning, selection, taskClass, toolNames, planPrompt)
	if err != nil {
		return selection, record, "", err
	}
	return selection, record, strings.TrimSpace(content), nil
}

func (a *Agent) runExecutionPhase(ctx context.Context, prompt string, taskClass core.TaskClass, toolDefs []provider.ToolDef, toolNames []string, planningNotes string, selection core.RoutingSelection) (string, state.PhaseRecord, state.PhaseRecord, CompletionVerification, []state.ToolOutcome, []memory.ObservedToolCall, memory.TargetResolution, error) {
	logger := log.FromContext(ctx)
	targetResolution := a.resolveTarget(ctx, memory.TargetResolutionInput{Task: prompt})
	execCtx := core.RetrievalContext{
		Task:          prompt,
		TaskClass:     taskClass,
		Phase:         core.PhaseExecution,
		ActiveModel:   selection.Requested,
		RuntimeHostID: targetResolution.RuntimeHostID,
		TargetID:      targetResolution.TargetID,
		TargetKind:    targetResolution.TargetKind,
		ToolNames:     toolNames,
	}
	retrieved, err := a.retrieveMemory(ctx, execCtx)
	if err != nil {
		return "", state.PhaseRecord{}, state.PhaseRecord{}, CompletionVerification{}, nil, nil, targetResolution, err
	}
	emitMemoryInjection(ctx, core.PhaseExecution, retrieved)

	volatileMessages := []provider.Message{{Role: "user", Content: prompt}}
	plan := buildPromptPlan(retrieved, planningNotes, volatileMessages, toolDefs)
	chat := NewChatState(append(append([]provider.Message(nil), plan.SystemMessages...), volatileMessages...)...)

	phaseRecord := state.PhaseRecord{
		Phase:          core.PhaseExecution,
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		Confidence:     selection.Confidence,
		Explanation:    selection.Reason,
	}

	execProvider, err := a.resolveProvider(selection.Requested.Provider)
	if err != nil {
		return "", phaseRecord, state.PhaseRecord{}, CompletionVerification{}, nil, nil, targetResolution, err
	}

	var toolOutcomes []state.ToolOutcome
	var observedCalls []memory.ObservedToolCall
	var output string
	var actualModel = selection.Requested.Model
	var refreshed bool
	var repairAttempts int
	var lastRepairFingerprint string
	var latestVerification CompletionVerification
	var latestVerificationRecord state.PhaseRecord
	failuresByTool := make(map[string]int)

	for iter := 1; iter <= a.opts.MaxIterations; iter++ {
		iterCtx := log.WithIteration(ctx, iter)
		req := &provider.ChatRequest{
			Model:       selection.Requested.Model,
			Messages:    chat.Messages(),
			Tools:       toolDefs,
			Temperature: 0.2,
			MaxTokens:   a.opts.MaxTokens,
		}
		iterPlan := plan
		iterPlan.PromptHash = hashJSON([]any{req.Messages, req.Tools})
		emitPromptPlanned(iterCtx, core.PhaseExecution, iter, selection.Requested.Provider, selection.Requested.Model, iterPlan, len(req.Messages))
		dump := a.dumpPrompt(iterCtx, promptdump.Metadata{
			Phase:     core.PhaseExecution,
			Provider:  selection.Requested.Provider,
			Model:     selection.Requested.Model,
			TaskClass: taskClass,
			Iteration: iter,
			Label:     "execution-loop",
		}, req)

		start := time.Now()
		resp, err := execProvider.ChatCompletion(iterCtx, req)
		a.finishPromptDump(dump, resp, err)
		duration := time.Since(start)
		emitModelCallCompleted(iterCtx, core.PhaseExecution, iter, selection.Requested.Provider, selection.Requested.Model, resp, duration.Milliseconds(), err)
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
			return "", phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, fmt.Errorf("LLM API error on iteration %d: %w", iter, err)
		}

		if resp.Model != "" {
			actualModel = resp.Model
		}
		phaseRecord.ActualModel = actualModel

		chat.Add(resp.Message)
		if len(resp.Message.ToolCalls) == 0 {
			output = resp.Message.Content
			if a.opts.DisableCompletionVerification {
				logger.Info("agent finished task")
				phaseRecord.Success = true
				return output, phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, nil
			}
			verification, verificationRecord, err := a.verifyCompletion(iterCtx, selection, taskClass, prompt, output, observedCalls, nil)
			latestVerification = verification
			latestVerificationRecord = verificationRecord
			if err != nil {
				return output, phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, fmt.Errorf("completion verification failed: %w", err)
			}
			verification.RepairTriggered = repairAttempts > 0
			latestVerification = verification
			if verification.satisfied() {
				logger.Info("agent finished task after verification")
				phaseRecord.Success = true
				return output, phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, nil
			}
			fingerprint := repairFingerprint(output, toolNames, len(observedCalls), verification)
			noProgress := lastRepairFingerprint != "" && fingerprint == lastRepairFingerprint
			if repairAttempts < a.maxRepairAttempts() && iter < a.opts.MaxIterations && !noProgress {
				reclassified := a.classifyTask(iterCtx, prompt, classificationContext{
					PreviousActionablePrompt: prompt,
					PreviousActionableClass:  taskClass,
					PreviousToolNames:        uniqueObservedToolNames(observedCalls),
					RepairInstruction:        verification.repairPrompt(),
				})
				newToolDefs := a.toolDefinitionsForTask(reclassified.Class, prompt+"\n"+verification.repairPrompt())
				newToolNames := toolNamesFromDefs(newToolDefs)
				capabilitiesChanged := core.ToolsetKey(newToolNames) != core.ToolsetKey(toolNames)
				capabilityUnavailable := requiredCapabilityUnavailable(verification, newToolNames)
				repairAttempts++
				emitRepairAttempt(iterCtx, repairAttempts, verification.Reason, capabilitiesChanged, false, capabilityUnavailable, newToolNames)
				if capabilityUnavailable {
					phaseRecord.Success = false
					return output, phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, incompleteTaskError{verification: verification}
				}
				if capabilitiesChanged {
					messages := chat.Messages()
					if len(messages) >= len(plan.SystemMessages) {
						volatile := append([]provider.Message(nil), messages[len(plan.SystemMessages):]...)
						plan = buildPromptPlan(retrieved, planningNotes, volatile, newToolDefs)
						chat = NewChatState(append(append([]provider.Message(nil), plan.SystemMessages...), volatile...)...)
					}
					toolDefs, toolNames, taskClass = newToolDefs, newToolNames, reclassified.Class
				}
				lastRepairFingerprint = fingerprint
				verification.RepairTriggered = true
				latestVerification = verification
				chat.AddSystem(verification.repairPrompt())
				continue
			}
			emitRepairAttempt(iterCtx, repairAttempts, verification.Reason, false, noProgress, false, toolNames)
			phaseRecord.Success = false
			return output, phaseRecord, verificationRecord, verification, toolOutcomes, observedCalls, targetResolution, incompleteTaskError{verification: verification}
		}

		for _, call := range resp.Message.ToolCalls {
			command := commandForToolCall(call)
			if command != "" {
				targetResolution = a.resolveTarget(ctx, memory.TargetResolutionInput{
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
			resultStr, toolErr := a.opts.ToolRegistry.ExecuteTool(toolCtx, call)
			durationMs := time.Since(toolStart).Milliseconds()

			outcome := state.ToolOutcome{
				Phase:      core.PhaseExecution,
				Provider:   selection.Requested.Provider,
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
					workID, persistErr := a.persistBlockedWork(iterCtx, prompt, taskClass, targetResolution, chat.Messages(), call, approvalErr)
					if persistErr != nil {
						return output, phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, persistErr
					}
					_ = telemetry.Record(telemetry.WithFields(iterCtx, telemetry.Fields{TaskState: string(state.TaskStateBlockedWaitingUser)}), telemetry.Event{
						Type:      telemetry.EventTaskBlocked,
						TaskState: string(state.TaskStateBlockedWaitingUser),
					})
					return output, phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, blockedTaskError{
						workID:  workID,
						reason:  toolErr.Error(),
						request: approvalErr.Request,
					}
				}

				if !refreshed && (outcome.PolicyDenied || failuresByTool[call.Function.Name] >= 2) {
					refreshed = true
					refresh, refreshErr := a.retrieveMemory(ctx, core.RetrievalContext{
						Task:          prompt,
						TaskClass:     taskClass,
						Phase:         core.PhaseExecution,
						ActiveModel:   selection.Requested,
						ActualModel:   core.NewModelRef(selection.Requested.Provider, actualModel),
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
						emitMemoryInjection(ctx, core.PhaseExecution, refresh)
						if refreshedText := retrievedBrief(refresh); strings.TrimSpace(refreshedText) != "" {
							chat.AddSystem("Refreshed learned context after tool trouble:\n" + refreshedText)
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
			chat.AddToolResult(call.ID, resultStr)
		}
	}

	return output, phaseRecord, latestVerificationRecord, latestVerification, toolOutcomes, observedCalls, targetResolution, fmt.Errorf("agent exceeded max iterations (%d) without completing the task", a.opts.MaxIterations)
}

func (a *Agent) withRunTelemetry(ctx context.Context) context.Context {
	if a == nil || a.opts.TelemetryWriter == nil {
		return ctx
	}
	runID, err := newRunCorrelationID()
	if err != nil {
		return telemetry.WithWriter(ctx, a.opts.TelemetryWriter)
	}
	ctx = telemetry.WithWriter(ctx, a.opts.TelemetryWriter)
	return telemetry.WithFields(ctx, telemetry.Fields{RunID: runID, JobID: scheduledJobIDFromContext(ctx)})
}

func (a *Agent) persistBlockedWork(ctx context.Context, prompt string, taskClass core.TaskClass, target memory.TargetResolution, messages []provider.Message, call provider.ToolCall, approvalErr tools.ApprovalRequiredError) (string, error) {
	if a.opts.BlockedWorkStore == nil {
		return "", nil
	}
	snapshot, _ := json.Marshal(messages)
	continuation, _ := json.Marshal(map[string]any{
		"target":    target,
		"tool_call": call,
	})
	pendingType := "security_action"
	pendingPayload := ""
	if approvalErr.Request.Grant.Digest != "" {
		encodedGrant, _ := json.Marshal(approvalErr.Request.Grant)
		pendingPayload = string(encodedGrant)
	}
	if pendingPayload == "" {
		pendingType = "shell_command"
		pendingPayload = secrets.Mask(strings.TrimSpace(approvalErr.Request.Command))
	}
	work := state.BlockedWork{
		Task:                   prompt,
		TaskClass:              taskClass,
		TaskState:              state.TaskStateBlockedWaitingUser,
		BlockedReason:          approvalErr.Error(),
		PendingApprovalType:    pendingType,
		PendingApprovalPayload: pendingPayload,
		ConversationSnapshot:   secrets.Mask(string(snapshot)),
		ContinuationData:       secrets.Mask(string(continuation)),
		ScheduledJobID:         scheduledJobIDFromContext(ctx),
	}
	return a.opts.BlockedWorkStore.SaveBlockedWork(ctx, work)
}

func (a *Agent) runCurationPhase(ctx context.Context, prompt string, taskClass core.TaskClass, output string, tools []state.ToolOutcome, execErr error) (core.RoutingSelection, state.PhaseRecord, []memory.Lesson, error) {
	selection, err := a.opts.Router.Select(ctx, core.PhaseCuration, prompt, taskClass, nil)
	if err != nil {
		return core.RoutingSelection{}, state.PhaseRecord{}, nil, err
	}

	summary := map[string]any{
		"task":        prompt,
		"task_class":  taskClass,
		"output":      output,
		"run_error":   errString(execErr),
		"tool_events": tools,
	}
	payload, _ := json.Marshal(summary)
	curPrompt := "Review the run summary and return only concrete reusable lessons that are clearly worth remembering for future runs. Prefer verified environment facts, stable user preferences explicitly stated by the user, or narrow tool/model heuristics tied to an observed failure. Do not return generic advice, summaries of the task, or lessons that are only useful for this single run. It is valid to return zero lessons.\n\nReturn JSON with shape {\"lessons\":[{\"body\":\"...\",\"scope\":\"global|model|tool|model_tool|task_class\",\"tool_name\":\"\",\"confidence\":0.0}]}\n\nRun summary:\n" + string(payload)

	content, record, actualModel, err := a.singleModelCall(ctx, core.PhaseCuration, selection, taskClass, nil, curPrompt)
	if err != nil {
		return selection, record, nil, err
	}

	var parsed struct {
		Lessons []struct {
			Body       string  `json:"body"`
			Scope      string  `json:"scope"`
			ToolName   string  `json:"tool_name"`
			Confidence float64 `json:"confidence"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &parsed); err != nil {
		return selection, record, nil, err
	}

	var lessons []memory.Lesson
	for _, item := range parsed.Lessons {
		if strings.TrimSpace(item.Body) == "" {
			continue
		}
		lessons = append(lessons, memory.Lesson{
			Body:       item.Body,
			Scope:      item.Scope,
			Provider:   selection.Requested.Provider,
			Model:      actualModel,
			ToolName:   item.ToolName,
			TaskClass:  taskClass,
			Phase:      core.PhaseCuration,
			Confidence: item.Confidence,
		})
	}
	return selection, record, lessons, nil
}

func (a *Agent) singleModelCall(ctx context.Context, phase core.Phase, selection core.RoutingSelection, taskClass core.TaskClass, toolNames []string, userPrompt string) (string, state.PhaseRecord, string, error) {
	targetResolution := a.resolveTarget(ctx, memory.TargetResolutionInput{Task: userPrompt})
	retrieved, err := a.retrieveMemory(ctx, core.RetrievalContext{
		Task:          userPrompt,
		TaskClass:     taskClass,
		Phase:         phase,
		ActiveModel:   selection.Requested,
		RuntimeHostID: targetResolution.RuntimeHostID,
		TargetID:      targetResolution.TargetID,
		TargetKind:    targetResolution.TargetKind,
		ToolNames:     toolNames,
	})
	if err != nil {
		return "", state.PhaseRecord{}, "", err
	}
	emitMemoryInjection(ctx, phase, retrieved)

	p, err := a.resolveProvider(selection.Requested.Provider)
	if err != nil {
		return "", state.PhaseRecord{}, "", err
	}

	req := &provider.ChatRequest{
		Model: selection.Requested.Model,
		Messages: func() []provider.Message {
			volatile := []provider.Message{{Role: "user", Content: userPrompt}}
			plan := buildPromptPlan(retrieved, "", volatile, nil)
			return append(append([]provider.Message(nil), plan.SystemMessages...), volatile...)
		}(),
		Temperature: 0.1,
		MaxTokens:   minInt(a.opts.MaxTokens, 1024),
	}
	singlePlan := buildPromptPlan(retrieved, "", []provider.Message{{Role: "user", Content: userPrompt}}, nil)
	singlePlan.PromptHash = hashJSON([]any{req.Messages, req.Tools})
	emitPromptPlanned(ctx, phase, 0, selection.Requested.Provider, selection.Requested.Model, singlePlan, len(req.Messages))
	dump := a.dumpPrompt(ctx, promptdump.Metadata{
		Phase:     phase,
		Provider:  selection.Requested.Provider,
		Model:     selection.Requested.Model,
		TaskClass: taskClass,
		Label:     "single-model-call",
	}, req)

	record := state.PhaseRecord{
		Phase:          phase,
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		Confidence:     selection.Confidence,
		Explanation:    selection.Reason,
	}

	start := time.Now()
	resp, err := p.ChatCompletion(ctx, req)
	a.finishPromptDump(dump, resp, err)
	record.LatencyMs = time.Since(start).Milliseconds()
	emitModelCallCompleted(ctx, phase, 0, selection.Requested.Provider, selection.Requested.Model, resp, record.LatencyMs, err)
	if err != nil {
		return "", record, "", err
	}
	record.Success = true
	record.ActualModel = resp.Model
	record.PromptTokens = resp.Usage.PromptTokens
	record.CompletionTokens = resp.Usage.CompletionTokens
	record.TotalTokens = resp.Usage.TotalTokens
	if cachedTokens, ok := resp.Usage.CachedTokens(); ok {
		record.CachedTokens = cachedTokens
		record.CachedTokensKnown = true
	}
	return resp.Message.Content, record, resp.Model, nil
}

func (a *Agent) dumpPrompt(ctx context.Context, meta promptdump.Metadata, req *provider.ChatRequest) *promptdump.Handle {
	if a == nil || a.opts.PromptDumper == nil || !a.opts.PromptDumper.Enabled() {
		return nil
	}
	if meta.Model == "" && req != nil {
		meta.Model = req.Model
	}
	handle, err := a.opts.PromptDumper.Begin(ctx, meta, req)
	if err != nil {
		log.FromContext(ctx).Warn("failed to dump prompt", "error", err, "phase", meta.Phase)
	}
	return handle
}

func (a *Agent) finishPromptDump(handle *promptdump.Handle, resp *provider.ChatResponse, callErr error) {
	if a == nil || a.opts.PromptDumper == nil || handle == nil {
		return
	}
	result := promptdump.Result{Err: callErr}
	if resp != nil {
		result.ActualModel = resp.Model
		result.Usage = resp.Usage
	}
	_ = a.opts.PromptDumper.Finish(handle, result)
}

func (a *Agent) retrieveMemory(ctx context.Context, input core.RetrievalContext) (memory.RetrievalResult, error) {
	if a.opts.MemoryRetriever == nil {
		rules := `You are CvkeHarness.
Before deciding to use a tool, think through the problem step-by-step.
If you encounter an error using a tool, read the error message carefully and try a different approach or adjust your arguments.`
		hostSummary := fmt.Sprintf("Runtime host summary:\n- active model: %s", input.ActiveModel.String())
		return memory.RetrievalResult{
			BuiltInRules:       rules,
			RuntimeHostSummary: hostSummary,
			Sources: []memory.InjectionSource{
				{Name: "built-in rules", Origin: "harness fallback", Chars: len([]rune(rules)), Preview: compactMemoryPreview(rules)},
				{Name: memory.TargetsFile, Origin: "runtime host summary", Chars: len([]rune(hostSummary)), Preview: compactMemoryPreview(hostSummary)},
			},
		}, nil
	}
	return a.opts.MemoryRetriever.Retrieve(ctx, input)
}

func (a *Agent) toolDefinitionsForTask(taskClass core.TaskClass, task string) []provider.ToolDef {
	if a == nil || a.opts.ToolRegistry == nil {
		return nil
	}
	return a.opts.ToolRegistry.DefinitionsForTask(taskClass, task)
}

func emitMemoryInjection(ctx context.Context, phase core.Phase, retrieved memory.RetrievalResult) {
	sources := retrieved.Sources
	if len(sources) == 0 {
		sources = memorySourcesFromResult(retrieved)
	}
	if len(sources) == 0 {
		return
	}
	var parts []string
	var total int
	eventSources := make([]tools.MemorySource, 0, len(sources))
	for _, source := range sources {
		total += source.Chars
		label := source.Name
		if source.Origin != "" {
			label += " (" + source.Origin + ")"
		}
		parts = append(parts, fmt.Sprintf("%s: %d chars", label, source.Chars))
		eventSources = append(eventSources, tools.MemorySource{
			Name:    source.Name,
			Origin:  source.Origin,
			Chars:   source.Chars,
			Preview: source.Preview,
		})
	}
	summary := fmt.Sprintf("%s memory injected: %s", phase, strings.Join(parts, "; "))
	log.FromContext(ctx).Info("memory injected", "phase", phase, "sections", len(sources), "chars", total, "summary", summary)
	tools.EmitEvent(ctx, tools.Event{
		Type:          tools.EventMemoryInjected,
		Output:        summary,
		MemorySources: eventSources,
	})
	payload, _ := json.Marshal(map[string]any{
		"sections": len(sources),
		"chars":    total,
		"summary":  summary,
	})
	_ = telemetry.Record(telemetry.WithFields(ctx, telemetry.Fields{Phase: string(phase)}), telemetry.Event{
		Type:    telemetry.EventMemoryRetrieved,
		Phase:   string(phase),
		Payload: payload,
	})
}

func memorySourcesFromResult(result memory.RetrievalResult) []memory.InjectionSource {
	sections := []struct {
		name   string
		origin string
		text   string
	}{
		{name: "built-in rules", origin: "harness", text: result.BuiltInRules},
		{name: memory.GuidanceFile, origin: "memory file", text: result.Guidance},
		{name: memory.TargetsFile, origin: "runtime host summary", text: result.RuntimeHostSummary},
		{name: memory.TargetsFile, origin: "target summary", text: result.TargetSummary},
		{name: memory.PlaybooksFile, origin: "playbook match", text: result.PlaybookBrief},
		{name: memory.CautionsFile, origin: "caution match", text: result.CautionBrief},
		{name: memory.FindingsFile, origin: "fallback finding", text: result.FallbackBrief},
	}
	sources := make([]memory.InjectionSource, 0, len(sections))
	for _, section := range sections {
		text := strings.TrimSpace(section.text)
		if text == "" {
			continue
		}
		sources = append(sources, memory.InjectionSource{
			Name:    section.name,
			Origin:  section.origin,
			Chars:   len([]rune(text)),
			Preview: compactMemoryPreview(text),
		})
	}
	return sources
}

func compactMemoryPreview(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) <= 160 {
		return text
	}
	return strings.TrimSpace(string(runes[:159])) + "…"
}

func (a *Agent) resolveProvider(providerName string) (provider.Provider, error) {
	if providerName == "" || providerName == a.opts.ProviderName {
		if a.opts.Provider != nil {
			return a.opts.Provider, nil
		}
	}
	if a.opts.ProviderResolver == nil {
		return nil, fmt.Errorf("no provider resolver configured for %q", providerName)
	}
	return a.opts.ProviderResolver.Resolve(providerName)
}

func classifyPolicyDenial(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "user denied command execution"):
		return true, "user_denial"
	case strings.Contains(lower, "deemed this command dangerous"):
		return true, "judge_denial"
	case strings.Contains(lower, "safety constraint violated"):
		return true, "safety_denial"
	case strings.Contains(lower, "security violation"):
		return true, "allowlist_denial"
	case strings.Contains(lower, "blocked shell syntax"):
		return true, "syntax_denial"
	default:
		return false, ""
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *Agent) resolveTarget(ctx context.Context, input memory.TargetResolutionInput) memory.TargetResolution {
	resolver, ok := a.opts.MemoryRetriever.(memoryTargetResolver)
	if !ok {
		return memory.TargetResolution{}
	}
	resolution, err := resolver.ResolveTarget(ctx, input)
	if err != nil {
		return memory.TargetResolution{}
	}
	return resolution
}

func retrievedBrief(retrieved memory.RetrievalResult) string {
	var parts []string
	for _, section := range []string{
		retrieved.RuntimeHostSummary,
		retrieved.TargetSummary,
		retrieved.PlaybookBrief,
		retrieved.CautionBrief,
		retrieved.FallbackBrief,
	} {
		section = strings.TrimSpace(section)
		if section != "" {
			parts = append(parts, section)
		}
	}
	return strings.Join(parts, "\n\n")
}

func commandForToolCall(call provider.ToolCall) string {
	if call.Function.Name != "shell_execute" {
		return ""
	}
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Command)
}
