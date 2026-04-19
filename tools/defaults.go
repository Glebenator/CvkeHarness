package tools

import (
	"context"
	"os"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
)

// NewDefaultRegistry creates the standard tool registry used by the CLI.
func NewDefaultRegistry(allowedCommands []string, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	return NewDefaultRegistryWithStore(allowedCommands, nil, judge, safetyMode, safetyModel, primaryModel)
}

// NewDefaultRegistryWithStore creates the standard registry and reuses
// persisted command approvals when a state store is available.
func NewDefaultRegistryWithStore(allowedCommands []string, store *state.Store, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	registry := NewRegistry()

	var approver ShellApprover
	switch safetyMode {
	case "", SafetyModeLLMJudge:
		approver = NewLLMJudgeApprover(judge, safetyModel)
	case SafetyModeUserConfirm:
		approver = NewUserPromptApprover(os.Stdin, os.Stdout)
	}

	var approvedCommands []string
	if store != nil && store.Available() {
		if approvals, err := store.ListCommandApprovals(context.Background()); err == nil {
			for _, approval := range approvals {
				if approval.Status != "approved" && approval.Status != "approved_once" {
					continue
				}
				approvedCommands = append(approvedCommands, approval.Command)
			}
		}
	}

	registry.Register(NewShellToolWithApprovals(allowedCommands, approvedCommands, approver, primaryModel, store))
	return registry
}
