package tools

import (
	"fmt"
	"os"

	"github.com/glebenator/cvkeharness/internal/promptdump"
	"github.com/glebenator/cvkeharness/memory"
	"github.com/glebenator/cvkeharness/provider"
	"github.com/glebenator/cvkeharness/recovery"
	"github.com/glebenator/cvkeharness/securitypolicy"
	"github.com/glebenator/cvkeharness/state"
)

// DefaultRegistryOptions collects the standard runtime tool dependencies.
type DefaultRegistryOptions struct {
	AllowedCommands      []string
	Store                *state.Store
	Memory               *memory.Manager
	Judge                provider.Provider
	Advisor              provider.Provider
	AdvisorModel         string
	SafetyMode           string
	SafetyModel          string
	PrimaryModel         string
	PromptDumper         *promptdump.Dumper
	WebSearch            WebSearchOptions
	BlockManualApprovals bool
	SecurityPolicy       *securitypolicy.EffectivePolicy
	Recovery             recovery.Options
}

// NewDefaultRegistry creates the standard tool registry used by the CLI.
func NewDefaultRegistry(allowedCommands []string, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	return NewDefaultRegistryWithStoreAndMemory(allowedCommands, nil, nil, judge, safetyMode, safetyModel, primaryModel)
}

// NewDefaultRegistryWithStore creates the standard registry. Durable legacy
// command approvals are quarantined; effect-policy runtimes consume only
// exact, expiring, one-time action grants.
func NewDefaultRegistryWithStore(allowedCommands []string, store *state.Store, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	return NewDefaultRegistryWithStoreAndMemory(allowedCommands, store, nil, judge, safetyMode, safetyModel, primaryModel)
}

// NewDefaultRegistryWithStoreAndMemory creates the standard registry with
// shell access plus optional ad hoc memory recording.
func NewDefaultRegistryWithStoreAndMemory(allowedCommands []string, store *state.Store, mem *memory.Manager, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	return NewDefaultRegistryWithStoreMemoryAndPromptDumper(allowedCommands, store, mem, judge, safetyMode, safetyModel, primaryModel, nil)
}

// NewDefaultRegistryWithStoreMemoryAndPromptDumper creates the standard
// registry and optionally captures LLM judge prompts for debugging.
func NewDefaultRegistryWithStoreMemoryAndPromptDumper(allowedCommands []string, store *state.Store, mem *memory.Manager, judge provider.Provider, safetyMode, safetyModel, primaryModel string, dumper *promptdump.Dumper) *Registry {
	registry, _ := NewDefaultRegistryFromOptions(DefaultRegistryOptions{
		AllowedCommands: allowedCommands,
		Store:           store,
		Memory:          mem,
		Judge:           judge,
		SafetyMode:      safetyMode,
		SafetyModel:     safetyModel,
		PrimaryModel:    primaryModel,
		PromptDumper:    dumper,
	})
	return registry
}

// NewDefaultRegistryFromOptions creates the standard registry and can return
// configuration errors for optional tools that require credentials.
func NewDefaultRegistryFromOptions(opts DefaultRegistryOptions) (*Registry, error) {
	registry := NewRegistry()
	registry.Register(&CalculateTool{})

	var approver ShellApprover
	var humanApprover ShellApprover
	if opts.BlockManualApprovals {
		humanApprover = NewBlockingApprover()
	} else {
		humanApprover = NewUserPromptApprover(os.Stdin, os.Stdout)
	}
	llmApprover := NewLLMJudgeApproverWithPromptDumper(opts.Judge, opts.SafetyModel, opts.PromptDumper)
	if opts.SafetyMode == SafetyModeLLMAdvisor || (opts.SecurityPolicy != nil && opts.SecurityPolicy.Profile == securitypolicy.ProfileLLMAdvisor) {
		humanApprover = NewLLMAdvisorApprover(opts.Advisor, opts.AdvisorModel, humanApprover, opts.PromptDumper)
		// The advisor supplies recommendations even for LLM-review controls;
		// the old binary judge must not preempt the human's decision.
		llmApprover = nil
	}
	if opts.SecurityPolicy != nil {
		registry.ConfigureSecurityWithStore(*opts.SecurityPolicy, humanApprover, llmApprover, opts.Store)
	}
	switch opts.SafetyMode {
	case "", SafetyModeLLMJudge:
		approver = llmApprover
	case SafetyModeUserConfirm, SafetyModeLLMAdvisor:
		approver = humanApprover
	case SafetyModeUserConfirmAll:
		approver = humanApprover
	}

	if opts.Memory != nil {
		registry.Register(NewMemoryRecordFindingTool(opts.Memory))
		registry.Register(NewMemoryRememberTargetTool(opts.Memory))
	}
	if opts.Store != nil && opts.Store.Available() {
		registry.Register(NewScheduleManageTool(opts.Store))
		registry.Register(NewSystemCronManageTool(opts.Store))
		if opts.SecurityPolicy != nil && recovery.Supported() {
			opts.Recovery.Policy = opts.SecurityPolicy.Hash
			engine, err := recovery.New(opts.Store, opts.Recovery)
			if err != nil {
				return nil, fmt.Errorf("initialize recovery: %w", err)
			}
			registry.Register(NewRecoveryManageTool(engine))
			registry.Register(NewRecoveryFleetTool(engine))
		}
	}
	if webTools, err := NewWebSearchTools(opts.WebSearch); err != nil {
		return nil, err
	} else {
		for _, tool := range webTools {
			registry.Register(tool)
		}
	}
	shell := NewShellToolWithApprovals(opts.AllowedCommands, nil, approver, opts.PrimaryModel, opts.Store)
	if opts.SecurityPolicy != nil {
		shell.applySecurityPolicy(*opts.SecurityPolicy, humanApprover, llmApprover)
	}
	switch opts.SafetyMode {
	case SafetyModeUserConfirmAll:
		shell.approvalRequired = true
	case SafetyModeUnrestricted:
		shell.unrestricted = true
	}
	registry.Register(shell)
	return registry, nil
}
