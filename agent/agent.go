package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

// Agent orchestrates the LLM and tools.
type Agent struct {
	provider      provider.Provider
	toolsRegistry *tools.Registry
	model         string
	maxIterations int
	maxTokens     int
}

// New creates a new Agent.
func New(p provider.Provider, r *tools.Registry, model string, maxIterations int, maxTokens int) *Agent {
	return &Agent{
		provider:      p,
		toolsRegistry: r,
		model:         model,
		maxIterations: maxIterations,
		maxTokens:     maxTokens,
	}
}

// Run executes the agentic loop for a given prompt until a final answer is produced or the iteration limit is hit.
func (a *Agent) Run(ctx context.Context, prompt string) (string, error) {
	logger := log.FromContext(ctx)
	logger.Info("CvkeHarness starting task", "task", prompt)

	systemPrompt := `You are CvkeHarness, a highly capable DevOps AI assistant.
Before deciding to use a tool, think through the problem step-by-step.
If you encounter an error using a tool, read the error message carefully and try a different approach or adjust your arguments.`

	messages := []provider.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	toolDefs := a.toolsRegistry.Definitions()

	for iter := 1; iter <= a.maxIterations; iter++ {
		iterCtx := log.WithIteration(ctx, iter)
		iterLog := log.FromContext(iterCtx)
		iterLog.Info("starting execution loop")

		req := &provider.ChatRequest{
			Model:       a.model,
			Messages:    messages,
			Tools:       toolDefs,
			Temperature: 0.2, // low temp for more deterministic DevOps actions
			MaxTokens:   a.maxTokens,
		}

		start := time.Now()
		resp, err := a.provider.ChatCompletion(iterCtx, req)
		duration := time.Since(start)

		if err != nil {
			_ = telemetry.RecordEvent(telemetry.TelemetryEvent{
				Timestamp:    start.UTC(),
				Model:        a.model,
				Success:      false,
				DurationMs:   duration.Milliseconds(),
				ErrorMessage: err.Error(),
			})
			return "", fmt.Errorf("LLM API error on iteration %d: %w", iter, err)
		}

		// Append the assistant's response to history
		messages = append(messages, resp.Message)

		// 1. Did the model just return text? Then it's done.
		if len(resp.Message.ToolCalls) == 0 {
			iterLog.Info("agent finished task")
			return resp.Message.Content, nil
		}

		// 2. The model requested one or more tool calls.
		iterLog.Info("agent requested tools", "count", len(resp.Message.ToolCalls))

		// Execute them (serially for now to keep history ordered, could go parallel later)
		for _, call := range resp.Message.ToolCalls {
			toolLog := iterLog.With("tool", call.Function.Name)
			toolLog.Info("executing tool")

			resultStr, err := a.toolsRegistry.ExecuteTool(telemetry.WithModel(iterCtx, resp.Model), call)
			if err != nil {
				toolLog.Warn("tool execution failed", "error", err)
				resultStr = fmt.Sprintf("Error executing tool: %v", err)
			} else {
				toolLog.Info("tool execution succeeded")
			}

			// Feed result back to the model
			messages = append(messages, provider.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    resultStr,
			})
		}
	}

	return "", fmt.Errorf("agent exceeded max iterations (%d) without completing the task", a.maxIterations)
}
