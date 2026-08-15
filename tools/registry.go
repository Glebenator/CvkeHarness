package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/secrets"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

// Tool represents an executable action that an LLM can request.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry manages the set of available tools.
type Registry struct {
	tools          map[string]Tool
	securityPolicy *securitypolicy.EffectivePolicy
	humanApprover  ShellApprover
	llmApprover    ShellApprover
	approvalStore  *state.Store
}

// ConfigureSecurityWithStore also enables exact, expiring one-time grants for
// deferred/background approval flows.
func (r *Registry) ConfigureSecurityWithStore(policy securitypolicy.EffectivePolicy, human, llm ShellApprover, store *state.Store) {
	r.ConfigureSecurity(policy, human, llm)
	r.approvalStore = store
}

// ConfigureSecurity installs the same immutable policy used by shell
// execution as the authorization boundary for all other tools.
func (r *Registry) ConfigureSecurity(policy securitypolicy.EffectivePolicy, human, llm ShellApprover) {
	copyPolicy := policy
	r.securityPolicy = &copyPolicy
	r.humanApprover = human
	r.llmApprover = llm
}

// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get finds a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns the provider format for sharing available tools with an LLM.
func (r *Registry) Definitions() []provider.ToolDef {
	return r.definitionsForNames(r.Names())
}

// DefinitionsForTask returns only the tools relevant to one task turn.
func (r *Registry) DefinitionsForTask(taskClass core.TaskClass, task string) []provider.ToolDef {
	var names []string
	lower := strings.ToLower(task)
	for _, name := range r.Names() {
		if toolRelevantForTask(name, taskClass, lower) {
			names = append(names, name)
		}
	}
	return r.definitionsForNames(names)
}

func (r *Registry) definitionsForNames(names []string) []provider.ToolDef {
	var defs []provider.ToolDef
	for _, name := range names {
		t, ok := r.tools[name]
		if !ok {
			continue
		}
		defs = append(defs, provider.ToolDef{
			Type: "function",
			Function: provider.ToolFuncDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return defs
}

// Names returns the registered tool names in stable order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ExecuteTool attempts to find and run a tool based on the model's requested call.
func (r *Registry) ExecuteTool(ctx context.Context, call provider.ToolCall) (string, error) {
	if call.Function.Name == "" {
		return "", fmt.Errorf("tool call missing function name")
	}

	t, ok := r.Get(call.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", call.Function.Name)
	}

	toolCtx := WithToolCallContext(ctx, call.ID, call.Function.Name)
	if err := r.authorizeToolCall(toolCtx, call); err != nil {
		return "", err
	}
	return t.Execute(toolCtx, json.RawMessage(call.Function.Arguments))
}

func (r *Registry) authorizeToolCall(ctx context.Context, call provider.ToolCall) error {
	if r.securityPolicy == nil || call.Function.Name == "shell_execute" {
		return nil
	}
	effects := policyToolEffects(call.Function.Name, call.Function.Arguments)
	decision := securitypolicy.DecisionAllow
	var reasons []string
	for _, effect := range effects {
		effectDecision := r.securityPolicy.Decision(effect.Setting)
		if effectDecision == "" {
			effectDecision = r.securityPolicy.Decision(securitypolicy.SettingUnknownCommands)
		}
		decision = strictestSecurityDecision(decision, effectDecision)
		reasons = append(reasons, effect.Detail+" is "+string(effectDecision))
	}
	reason := strings.Join(uniqueStrings(reasons), "; ")
	if decision == securitypolicy.DecisionDeny {
		return fmt.Errorf("security violation: %s", reason)
	}
	if decision == securitypolicy.DecisionAllow {
		return nil
	}
	grant, grantErr := toolSecurityGrantBinding(call.Function.Name, call.Function.Arguments, *r.securityPolicy, effects)
	if grantErr != nil {
		return fmt.Errorf("security grant binding: %w", grantErr)
	}
	if r.approvalStore != nil && r.approvalStore.Available() {
		consumed, consumeErr := r.approvalStore.ConsumeSecurityActionGrant(ctx, grant, time.Now().UTC())
		if consumeErr != nil {
			return fmt.Errorf("security grant lookup: %w", consumeErr)
		}
		if consumed {
			return nil
		}
	}
	approver := r.humanApprover
	request := ShellApprovalRequest{
		Command:         secrets.Mask(call.Function.Name + " " + call.Function.Arguments),
		ValidationError: reason,
		Effects:         effects,
		ActionKind:      call.Function.Name,
		ActionPayload:   call.Function.Arguments,
		GrantDigest:     grant.Digest,
		Grant:           grant,
	}
	if decision == securitypolicy.DecisionLLMReview && r.llmApprover != nil {
		if _, advisoryErr := r.llmApprover.Approve(ctx, request); advisoryErr != nil {
			return advisoryErr
		}
		request.ValidationError = "advisory model found no immediate hazard; human approval is still required; " + request.ValidationError
	}
	payload, _ := json.Marshal(map[string]any{
		"tool_name": call.Function.Name,
		"decision":  decision,
		"reason":    reason,
		"effects":   effects,
		"policy":    r.securityPolicy.Hash,
	})
	_ = telemetry.Record(ctx, telemetry.Event{Type: telemetry.EventApprovalRequested, Payload: payload})
	if approver == nil {
		return ApprovalRequiredError{Request: request}
	}
	result, err := approver.Approve(ctx, request)
	if err != nil {
		return err
	}
	if !result.Approved {
		return fmt.Errorf("security violation: approval gate did not approve %s", call.Function.Name)
	}
	return nil
}

func classifyToolEffects(name string, raw json.RawMessage) []ShellEffect {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	action, _ := args["action"].(string)
	switch name {
	case "schedule_manage":
		switch action {
		case "list", "runs":
			return []ShellEffect{{Setting: securitypolicy.SettingReadCommands, Detail: "scheduled-job inspection"}}
		default:
			return []ShellEffect{{Setting: securitypolicy.SettingScheduledChanges, Detail: "scheduled-job " + action}}
		}
	case "system_cron_manage":
		switch action {
		case "list", "show", "dry_run":
			return []ShellEffect{{Setting: securitypolicy.SettingReadCommands, Detail: "system-cron inspection"}}
		default:
			return []ShellEffect{{Setting: securitypolicy.SettingScheduledChanges, Detail: "system-cron " + action}}
		}
	case "memory_record_finding":
		return []ShellEffect{{Setting: securitypolicy.SettingFileAppend, Detail: "durable memory write"}}
	case "web_search", "web_fetch":
		return []ShellEffect{{Setting: securitypolicy.SettingNetworkAccess, Detail: "external web access"}}
	default:
		return []ShellEffect{{Setting: securitypolicy.SettingUnknownCommands, Detail: "unclassified tool " + name}}
	}
}

func policyToolEffects(name, arguments string) []ShellEffect {
	effects := classifyToolEffects(name, json.RawMessage(arguments))
	if secrets.Contains(arguments) {
		effects = append(effects, ShellEffect{Setting: securitypolicy.SettingCredentialAccess, Detail: "literal secret-like value in tool arguments"})
	}
	return uniqueEffects(effects)
}

func toolRelevantForTask(name string, taskClass core.TaskClass, lower string) bool {
	switch name {
	case "shell_execute":
		// Advertisement is not authorization. Shell parsing, target binding,
		// allowlists, approvals, and fail-closed judge checks remain enforced by
		// ShellTool at execution time.
		return true
	case "memory_record_finding":
		return containsAny(lower, "remember", "record finding", "note this", "save this", "memory")
	case "schedule_manage":
		return containsAny(lower, "schedule", "remind", "recurring", "every ", "daily", "weekly", "job", "health check")
	case "system_cron_manage":
		return containsAny(lower, "system cron", "user cron", "os cron", "crontab")
	case "web_search", "web_fetch":
		return containsAny(lower, "web", "url", "http", "https", "docs", "documentation", "release note", "latest", "current ", "search online", "website", "github issue")
	default:
		return true
	}
}

func containsAny(s string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}
