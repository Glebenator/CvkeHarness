package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/log"
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

// Options configures a new agent.
type Options struct {
	Provider         provider.Provider
	ProviderName     string
	ProviderResolver ProviderResolver
	ToolRegistry     *tools.Registry
	EventObserver    tools.EventObserver
	DefaultModel     string
	MaxIterations    int
	MaxTokens        int
	RoutingConfig    core.RoutingConfig
	Router           Router
	MemoryRetriever  MemoryRetriever
	MemoryCurator    MemoryCurator
	RunRecorder      RunRecorder
}

// Agent orchestrates routed model execution and tool use.
type Agent struct {
	opts Options
}

// RunResult contains the user-facing output plus structured execution details.
type RunResult struct {
	Output  string
	Run     state.RunRecord
	Routing []core.RoutingSelection
}

// New creates a new Agent.
func New(opts Options) *Agent {
	return &Agent{opts: opts}
}

// Run executes the harness loop for a given prompt.
func (a *Agent) Run(ctx context.Context, prompt string) (result RunResult, err error) {
	logger := log.FromContext(ctx)
	ctx = tools.WithEventObserver(ctx, a.opts.EventObserver)
	taskClass := core.ClassifyTask(prompt)
	toolNames := []string{}
	if a.opts.ToolRegistry != nil {
		toolNames = a.opts.ToolRegistry.Names()
	}

	startedAt := time.Now().UTC()
	runRecord := state.RunRecord{
		StartedAt:      startedAt,
		Provider:       a.opts.ProviderName,
		Task:           prompt,
		TaskClass:      taskClass,
		RoutingEnabled: a.opts.RoutingConfig.Enabled,
	}

	defer func() {
		runRecord.FinishedAt = time.Now().UTC()
		runRecord.Success = err == nil
		if err != nil {
			runRecord.ErrorMessage = err.Error()
		}
		result.Run = runRecord
		if a.opts.RunRecorder != nil {
			if recErr := a.opts.RunRecorder.RecordRun(ctx, runRecord); recErr != nil {
				logger.Warn("failed to record run", "error", recErr)
			}
		}
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

	output, phaseRecord, toolOutcomes, observedCalls, targetResolution, execErr := a.runExecutionPhase(ctx, prompt, taskClass, toolNames, planningNotes, execSelection)
	runRecord.Phases = append(runRecord.Phases, phaseRecord)
	runRecord.Tools = append(runRecord.Tools, toolOutcomes...)
	result.Output = output
	result.Routing = routingSelections
	if execErr != nil {
		err = execErr
	}

	if a.opts.MemoryCurator != nil {
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

func (a *Agent) runExecutionPhase(ctx context.Context, prompt string, taskClass core.TaskClass, toolNames []string, planningNotes string, selection core.RoutingSelection) (string, state.PhaseRecord, []state.ToolOutcome, []memory.ObservedToolCall, memory.TargetResolution, error) {
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
		return "", state.PhaseRecord{}, nil, nil, targetResolution, err
	}

	systemMessages := initialSystemMessages(retrieved, planningNotes)
	chat := NewChatState(append(systemMessages, provider.Message{Role: "user", Content: prompt})...)
	toolDefs := a.opts.ToolRegistry.Definitions()

	phaseRecord := state.PhaseRecord{
		Phase:          core.PhaseExecution,
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		Confidence:     selection.Confidence,
		Explanation:    selection.Reason,
	}

	execProvider, err := a.resolveProvider(selection.Requested.Provider)
	if err != nil {
		return "", phaseRecord, nil, nil, targetResolution, err
	}

	var toolOutcomes []state.ToolOutcome
	var observedCalls []memory.ObservedToolCall
	var output string
	var actualModel = selection.Requested.Model
	var refreshed bool
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

		start := time.Now()
		resp, err := execProvider.ChatCompletion(iterCtx, req)
		duration := time.Since(start)
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
			_ = telemetry.RecordEvent(telemetry.TelemetryEvent{
				Timestamp:    start.UTC(),
				Model:        selection.Requested.String(),
				Success:      false,
				DurationMs:   duration.Milliseconds(),
				ErrorMessage: err.Error(),
			})
			return "", phaseRecord, toolOutcomes, observedCalls, targetResolution, fmt.Errorf("LLM API error on iteration %d: %w", iter, err)
		}

		if resp.Model != "" {
			actualModel = resp.Model
		}
		phaseRecord.ActualModel = actualModel

		chat.Add(resp.Message)
		if len(resp.Message.ToolCalls) == 0 {
			logger.Info("agent finished task")
			phaseRecord.Success = true
			return resp.Message.Content, phaseRecord, toolOutcomes, observedCalls, targetResolution, nil
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
			toolCtx := tools.WithToolCallContext(telemetry.WithModel(iterCtx, actualModel), call.ID, call.Function.Name)
			tools.EmitEvent(toolCtx, tools.Event{
				Type:    tools.EventToolCallStarted,
				Success: true,
			})
			resultStr, toolErr := a.opts.ToolRegistry.ExecuteTool(toolCtx, call)
			durationMs := time.Since(toolStart).Milliseconds()

			outcome := state.ToolOutcome{
				Phase:      core.PhaseExecution,
				Provider:   selection.Requested.Provider,
				Model:      actualModel,
				ToolName:   call.Function.Name,
				Toolset:    core.ToolsetKey(toolNames),
				Success:    toolErr == nil,
				DurationMs: durationMs,
			}

			if toolErr != nil {
				failuresByTool[call.Function.Name]++
				outcome.ErrorMessage = toolErr.Error()
				outcome.PolicyDenied, outcome.DenialClass = classifyPolicyDenial(toolErr)
				resultStr = fmt.Sprintf("Error executing tool: %v", toolErr)

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

	return output, phaseRecord, toolOutcomes, observedCalls, targetResolution, fmt.Errorf("agent exceeded max iterations (%d) without completing the task", a.opts.MaxIterations)
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

	p, err := a.resolveProvider(selection.Requested.Provider)
	if err != nil {
		return "", state.PhaseRecord{}, "", err
	}

	req := &provider.ChatRequest{
		Model: selection.Requested.Model,
		Messages: append(initialSystemMessages(retrieved, ""), provider.Message{
			Role:    "user",
			Content: userPrompt,
		}),
		Temperature: 0.1,
		MaxTokens:   minInt(a.opts.MaxTokens, 1024),
	}

	record := state.PhaseRecord{
		Phase:          phase,
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		Confidence:     selection.Confidence,
		Explanation:    selection.Reason,
	}

	start := time.Now()
	resp, err := p.ChatCompletion(ctx, req)
	record.LatencyMs = time.Since(start).Milliseconds()
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

func (a *Agent) retrieveMemory(ctx context.Context, input core.RetrievalContext) (memory.RetrievalResult, error) {
	if a.opts.MemoryRetriever == nil {
		return memory.RetrievalResult{
			BuiltInRules: `You are CvkeHarness.
Before deciding to use a tool, think through the problem step-by-step.
If you encounter an error using a tool, read the error message carefully and try a different approach or adjust your arguments.`,
			RuntimeHostSummary: fmt.Sprintf("Runtime host summary:\n- active model: %s", input.ActiveModel.String()),
		}, nil
	}
	return a.opts.MemoryRetriever.Retrieve(ctx, input)
}

func initialSystemMessages(retrieved memory.RetrievalResult, planningNotes string) []provider.Message {
	var messages []provider.Message
	if strings.TrimSpace(retrieved.BuiltInRules) != "" {
		messages = append(messages, provider.Message{Role: "system", Content: strings.TrimSpace(retrieved.BuiltInRules)})
	}
	if strings.TrimSpace(retrieved.Operator) != "" {
		messages = append(messages, provider.Message{Role: "system", Content: strings.TrimSpace(retrieved.Operator)})
	}
	if strings.TrimSpace(retrieved.Soul) != "" {
		messages = append(messages, provider.Message{Role: "system", Content: strings.TrimSpace(retrieved.Soul)})
	}
	if text := retrievedBrief(retrieved); strings.TrimSpace(text) != "" {
		messages = append(messages, provider.Message{Role: "system", Content: text})
	}
	if strings.TrimSpace(planningNotes) != "" {
		messages = append(messages, provider.Message{Role: "system", Content: "Planning notes:\n" + strings.TrimSpace(planningNotes)})
	}
	return messages
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
