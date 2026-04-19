package tools

import (
	"os"

	"github.com/coolcake/cvkeharness/provider"
)

// NewDefaultRegistry creates the standard tool registry used by the CLI.
func NewDefaultRegistry(allowedCommands []string, judge provider.Provider, safetyMode, safetyModel, primaryModel string) *Registry {
	registry := NewRegistry()

	var approver ShellApprover
	switch safetyMode {
	case "", SafetyModeLLMJudge:
		approver = NewLLMJudgeApprover(judge, safetyModel)
	case SafetyModeUserConfirm:
		approver = NewUserPromptApprover(os.Stdin, os.Stdout)
	}

	registry.Register(NewShellToolWithApprover(allowedCommands, approver, primaryModel))
	return registry
}
