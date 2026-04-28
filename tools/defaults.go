package tools

import (
	"context"
	"os"

	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
)

// NewDefaultRegistry creates the standard tool registry used by the CLI.
func NewDefaultRegistry(allowedCommands []string, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	return NewDefaultRegistryWithStoreAndMemory(allowedCommands, nil, nil, judge, safetyMode, safetyModel, primaryModel)
}

// NewDefaultRegistryWithStore creates the standard registry and reuses
// persisted command approvals when a state store is available.
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
	registry := NewRegistry()

	var approver ShellApprover
	switch safetyMode {
	case "", SafetyModeLLMJudge:
		approver = NewLLMJudgeApproverWithPromptDumper(judge, safetyModel, dumper)
	case SafetyModeUserConfirm:
		approver = NewUserPromptApprover(os.Stdin, os.Stdout)
	case SafetyModeUserConfirmAll:
		approver = NewUserPromptApprover(os.Stdin, os.Stdout)
	}

	var approvedCommands []string
	if store != nil && store.Available() {
		if approvals, err := store.ListApprovedCommandApprovals(context.Background()); err == nil {
			for _, approval := range approvals {
				approvedCommands = append(approvedCommands, approval.Command)
			}
		}
	}

	if mem != nil {
		registry.Register(NewMemoryRecordFindingTool(mem))
	}
	if store != nil && store.Available() {
		registry.Register(NewScheduleManageTool(store))
		registry.Register(NewSystemCronManageTool(store))
	}
	shell := NewShellToolWithApprovals(allowedCommands, approvedCommands, approver, primaryModel, store)
	switch safetyMode {
	case SafetyModeUserConfirmAll:
		shell.approvalRequired = true
	case SafetyModeUnrestricted:
		shell.unrestricted = true
	}
	registry.Register(shell)
	return registry
}
