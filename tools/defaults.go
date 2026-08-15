package tools

import (
	"context"
	"os"

	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

// DefaultRegistryOptions collects the standard runtime tool dependencies.
type DefaultRegistryOptions struct {
	AllowedCommands      []string
	Store                *state.Store
	Memory               *memory.Manager
	Judge                provider.Provider
	SafetyMode           string
	SafetyModel          string
	PrimaryModel         string
	PromptDumper         *promptdump.Dumper
	WebSearch            WebSearchOptions
	BlockManualApprovals bool
	SecurityPolicy       *securitypolicy.EffectivePolicy
}

// NewDefaultRegistry creates the standard tool registry used by the CLI.
func NewDefaultRegistry(allowedCommands []string, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	return NewDefaultRegistryWithStoreAndMemory(allowedCommands, nil, nil, judge, safetyMode, safetyModel, primaryModel)
}

// NewDefaultRegistryWithStore creates the standard registry. Legacy runtimes
// may reuse old command approvals; effect-policy runtimes consume only scoped
// one-time action grants.
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

	var approver ShellApprover
	var humanApprover ShellApprover
	if opts.BlockManualApprovals {
		humanApprover = NewBlockingApprover()
	} else {
		humanApprover = NewUserPromptApprover(os.Stdin, os.Stdout)
	}
	llmApprover := NewLLMJudgeApproverWithPromptDumper(opts.Judge, opts.SafetyModel, opts.PromptDumper)
	if opts.SecurityPolicy != nil {
		registry.ConfigureSecurityWithStore(*opts.SecurityPolicy, humanApprover, llmApprover, opts.Store)
	}
	switch opts.SafetyMode {
	case "", SafetyModeLLMJudge:
		approver = llmApprover
	case SafetyModeUserConfirm:
		approver = humanApprover
	case SafetyModeUserConfirmAll:
		approver = humanApprover
	}

	var approvedCommands []string
	if opts.SecurityPolicy == nil && opts.Store != nil && opts.Store.Available() {
		if approvals, err := opts.Store.ListApprovedCommandApprovals(context.Background()); err == nil {
			for _, approval := range approvals {
				approvedCommands = append(approvedCommands, approval.Command)
			}
		}
	}

	if opts.Memory != nil {
		registry.Register(NewMemoryRecordFindingTool(opts.Memory))
	}
	if opts.Store != nil && opts.Store.Available() {
		registry.Register(NewScheduleManageTool(opts.Store))
		registry.Register(NewSystemCronManageTool(opts.Store))
	}
	if webTools, err := NewWebSearchTools(opts.WebSearch); err != nil {
		return nil, err
	} else {
		for _, tool := range webTools {
			registry.Register(tool)
		}
	}
	shell := NewShellToolWithApprovals(opts.AllowedCommands, approvedCommands, approver, opts.PrimaryModel, opts.Store)
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
