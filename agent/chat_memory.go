package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

type pendingEndpointRequest struct{ Request, Name, Clarification string }
type chatRequestContext struct {
	CurrentUserMessage  string                  `json:"current_user_message"`
	PreviousUserMessage string                  `json:"previous_user_message,omitempty"`
	PendingRequest      string                  `json:"pending_request,omitempty"`
	TargetName          string                  `json:"unresolved_target_name,omitempty"`
	Clarification       string                  `json:"assistant_clarification,omitempty"`
	Memory              *memory.EndpointCapture `json:"memory_result,omitempty"`
	AwaitingTarget      bool                    `json:"-"`
}
type chatRequestKey struct{}

func captureAllowed(mode, prompt string) bool {
	return mode != "off" && (mode != "explicit_only" || memory.ExplicitEndpointRequest(prompt))
}

// Capture goes through the same registry authorization and approval path as a
// model-requested write. Its context is never supplied to background Agent.Run.
func (c *ChatConversation) captureEndpoint(ctx context.Context, prompt string, class core.TaskClass, d memory.EndpointDeclaration) (*memory.EndpointCapture, error) {
	result := &memory.EndpointCapture{Status: "failed", Name: d.Name, Endpoint: d.Endpoint}
	if c.agent.opts.ToolRegistry == nil {
		result.Reason = "endpoint memory tool unavailable"
		return result, nil
	}
	args, _ := json.Marshal(map[string]string{"name": d.Name, "endpoint": d.Endpoint})
	call := provider.ToolCall{ID: "capture-endpoint", Function: provider.ToolFunction{Name: "memory_remember_target", Arguments: string(args)}}
	for {
		raw, err := c.agent.opts.ToolRegistry.ExecuteTool(ctx, call)
		if approval, ok := tools.IsApprovalRequired(err); ok {
			id, persistErr := c.agent.persistBlockedWork(ctx, prompt, class, memory.TargetResolution{}, c.history.Messages(), call, approval)
			if persistErr != nil {
				result.Reason = persistErr.Error()
				return result, persistErr
			}
			if !c.agent.opts.AwaitManualApprovals {
				result.Reason = "memory write requires approval"
				return result, blockedTaskError{workID: id, reason: err.Error(), request: approval.Request}
			}
			if err := c.awaitApproval(ctx, id, approval.Request); err != nil {
				result.Reason = err.Error()
				return result, err
			}
			continue
		}
		if err != nil {
			result.Reason = err.Error()
			return result, nil
		}
		var receipt struct {
			Capture memory.EndpointCapture `json:"capture"`
		}
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil || receipt.Capture.Status == "" {
			result.Reason = "memory tool returned no verified capture result"
			return result, nil
		}
		return &receipt.Capture, nil
	}
}

func newCaptureContext(ctx context.Context, mode string) context.Context {
	id := telemetry.FieldsFromContext(ctx).TurnID
	if id == "" {
		id = rand.Text()
	}
	return tools.WithEndpointCapture(ctx, id, mode, nil)
}

func finishChatMessage(c *ChatConversation, prompt, output string) []provider.Message {
	messages := []provider.Message{{Role: "user", Content: prompt}, {Role: "assistant", Content: output}}
	for _, m := range messages {
		c.history.Add(m)
	}
	return messages
}

func captureFailure(r *memory.EndpointCapture) error {
	return fmt.Errorf("endpoint memory: %s", r.Summary())
}

// Model verification can judge execution, but cannot request another write for
// memory already resolved by the runtime. Mixed execution obligations remain.
func enforceMemoryVerification(v CompletionVerification, r *memory.EndpointCapture) CompletionVerification {
	if r == nil {
		return v
	}
	var remaining []string
	removed := false
	for _, action := range v.MissingActions {
		lower := strings.ToLower(action)
		if strings.Contains(lower, "memory_remember_target") || strings.Contains(lower, "memory_record_finding") || strings.Contains(lower, "remember the address") ||
			((strings.Contains(lower, "save") || strings.Contains(lower, "persist")) && (strings.Contains(lower, "address") || strings.Contains(lower, "endpoint"))) {
			removed = true
			continue
		}
		remaining = append(remaining, action)
	}
	if removed {
		v.MissingActions = remaining
		// Never infer task success from memory success. If the model evaluated only
		// memory, stop uncertain rather than looping or claiming execution completed.
		if len(remaining) == 0 {
			v.Status = verificationUncertain
			v.Reason = "memory outcome is final; execution completion was not established"
			v.RepairInstruction = ""
			v.StopReason = tools.VerificationStopNoProgress
		}
		if len(remaining) > 0 {
			v.RepairInstruction = "Complete only these remaining execution actions: " + strings.Join(remaining, "; ")
		}
	}
	return v
}
