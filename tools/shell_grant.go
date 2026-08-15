package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/secrets"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

const actionAnalyzerVersion = "effect-policy-v1"

// ApproveBlockedWork creates one exact, time-limited grant for a persisted
// action after re-evaluating it against the current policy and its original
// executor scope. The grant cannot authorize a different action, host,
// principal, working directory, effect set, or policy version.
func ApproveBlockedWork(ctx context.Context, store *state.Store, policy securitypolicy.EffectivePolicy, workID string, ttl time.Duration, source string) (state.SecurityActionGrant, error) {
	if store == nil || !store.Available() {
		return state.SecurityActionGrant{}, fmt.Errorf("state database unavailable")
	}
	work, err := store.GetBlockedWork(ctx, strings.TrimSpace(workID))
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	if work.TaskState != state.TaskStateBlockedWaitingUser {
		return state.SecurityActionGrant{}, fmt.Errorf("blocked work %s is not awaiting approval", work.ID)
	}
	if work.PendingApprovalType != "security_action" || work.PendingApprovalPayload == "" {
		return state.SecurityActionGrant{}, fmt.Errorf("blocked work %s is not waiting on a scoped security action", work.ID)
	}

	var expected state.SecurityActionGrant
	if err := json.Unmarshal([]byte(work.PendingApprovalPayload), &expected); err != nil || expected.Digest == "" {
		return state.SecurityActionGrant{}, fmt.Errorf("blocked work %s does not contain a scoped approval envelope", work.ID)
	}
	var continuation struct {
		ToolCall provider.ToolCall `json:"tool_call"`
	}
	if err := json.Unmarshal([]byte(work.ContinuationData), &continuation); err != nil {
		return state.SecurityActionGrant{}, fmt.Errorf("read blocked action: %w", err)
	}
	call := continuation.ToolCall
	if strings.TrimSpace(call.Function.Name) == "" {
		return state.SecurityActionGrant{}, fmt.Errorf("blocked work %s does not identify an action", work.ID)
	}

	actionPayload := call.Function.Arguments
	if call.Function.Name == "shell_execute" {
		var shellArgs ShellArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &shellArgs); err != nil {
			return state.SecurityActionGrant{}, fmt.Errorf("read blocked shell action: %w", err)
		}
		actionPayload = shellArgs.Command
	}
	grant, err := NewBlockedWorkSecurityGrant(call.Function.Name, actionPayload, policy, expected, ttl, source)
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	if err := store.SaveSecurityActionGrant(ctx, grant); err != nil {
		return state.SecurityActionGrant{}, err
	}
	if _, err := store.ResolveBlockedSecurityGrant(ctx, grant.Digest); err != nil {
		return state.SecurityActionGrant{}, err
	}
	return grant, nil
}

// NewShellSecurityGrant creates an exact, scoped, one-time authorization. The
// raw command is used only for the digest and is never persisted.
func NewShellSecurityGrant(command string, policy securitypolicy.EffectivePolicy, ttl time.Duration, source string) (state.SecurityActionGrant, error) {
	grant, assessment, err := shellSecurityGrantBinding(command, policy)
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	if assessment.Decision == securitypolicy.DecisionDeny {
		return state.SecurityActionGrant{}, fmt.Errorf("profile %s denies this action: %s", policy.Profile, assessment.Reason)
	}
	if ttl <= 0 || ttl > time.Hour {
		return state.SecurityActionGrant{}, fmt.Errorf("security grant lifetime must be between 1ns and 1h")
	}
	now := time.Now().UTC()
	grant.Source = strings.TrimSpace(source)
	grant.CreatedAt = now
	grant.ExpiresAt = now.Add(ttl)
	grant.RemainingUses = 1
	return grant, nil
}

func shellSecurityGrantBinding(command string, policy securitypolicy.EffectivePolicy) (state.SecurityActionGrant, ShellAssessment, error) {
	command = strings.TrimSpace(command)
	assessment, err := AssessShellCommand(command, policy)
	if err != nil {
		return state.SecurityActionGrant{}, ShellAssessment{}, err
	}
	grant, err := securityGrantBinding("shell_execute", command, assessment.Effects, policy)
	return grant, assessment, err
}

// NewToolSecurityGrant creates an exact one-time grant for a non-shell tool
// call. It is used by the blocked-work approval command and the registry uses
// the same binding when consuming it.
func NewToolSecurityGrant(name, arguments string, policy securitypolicy.EffectivePolicy, ttl time.Duration, source string) (state.SecurityActionGrant, error) {
	effects := policyToolEffects(name, arguments)
	decision := securitypolicy.DecisionAllow
	for _, effect := range effects {
		effectDecision := policy.Decision(effect.Setting)
		if effectDecision == "" {
			effectDecision = policy.Decision(securitypolicy.SettingUnknownCommands)
		}
		decision = strictestSecurityDecision(decision, effectDecision)
	}
	if decision == securitypolicy.DecisionDeny {
		return state.SecurityActionGrant{}, fmt.Errorf("profile %s denies this tool action", policy.Profile)
	}
	if ttl <= 0 || ttl > time.Hour {
		return state.SecurityActionGrant{}, fmt.Errorf("security grant lifetime must be between 1ns and 1h")
	}
	grant, err := securityGrantBinding(name, arguments, effects, policy)
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	now := time.Now().UTC()
	grant.Source = strings.TrimSpace(source)
	grant.CreatedAt = now
	grant.ExpiresAt = now.Add(ttl)
	grant.RemainingUses = 1
	return grant, nil
}

func toolSecurityGrantBinding(name, arguments string, policy securitypolicy.EffectivePolicy, effects []ShellEffect) (state.SecurityActionGrant, error) {
	return securityGrantBinding(name, arguments, effects, policy)
}

// NewBlockedWorkSecurityGrant reconstructs a reviewed action using the
// executor scope captured when it blocked. This lets an operator approve from
// a different directory without weakening the grant consumed by the executor.
func NewBlockedWorkSecurityGrant(actionKind, actionPayload string, policy securitypolicy.EffectivePolicy, expected state.SecurityActionGrant, ttl time.Duration, source string) (state.SecurityActionGrant, error) {
	var effects []ShellEffect
	decision := securitypolicy.DecisionAllow
	if actionKind == "shell_execute" {
		assessment, err := AssessShellCommand(actionPayload, policy)
		if err != nil {
			return state.SecurityActionGrant{}, err
		}
		effects = assessment.Effects
		decision = assessment.Decision
	} else {
		effects = policyToolEffects(actionKind, actionPayload)
		for _, effect := range effects {
			effectDecision := policy.Decision(effect.Setting)
			if effectDecision == "" {
				effectDecision = policy.Decision(securitypolicy.SettingUnknownCommands)
			}
			decision = strictestSecurityDecision(decision, effectDecision)
		}
	}
	if decision == securitypolicy.DecisionDeny {
		return state.SecurityActionGrant{}, fmt.Errorf("profile %s denies this action", policy.Profile)
	}
	if ttl <= 0 || ttl > time.Hour {
		return state.SecurityActionGrant{}, fmt.Errorf("security grant lifetime must be between 1ns and 1h")
	}
	grant, err := securityGrantBindingAtScope(actionKind, actionPayload, effects, policy, expected.Host, expected.Principal, expected.WorkingDirectory)
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	if grant.Digest != expected.Digest || grant.EffectDigest != expected.EffectDigest || grant.PolicyHash != expected.PolicyHash || grant.ActionKind != expected.ActionKind {
		return state.SecurityActionGrant{}, fmt.Errorf("blocked action no longer matches its reviewed effect or policy")
	}
	now := time.Now().UTC()
	grant.Source = strings.TrimSpace(source)
	grant.CreatedAt = now
	grant.ExpiresAt = now.Add(ttl)
	grant.RemainingUses = 1
	return grant, nil
}

func securityGrantBinding(actionKind, actionPayload string, effects []ShellEffect, policy securitypolicy.EffectivePolicy) (state.SecurityActionGrant, error) {
	host, err := os.Hostname()
	if err != nil {
		return state.SecurityActionGrant{}, fmt.Errorf("resolve grant host: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return state.SecurityActionGrant{}, fmt.Errorf("resolve grant working directory: %w", err)
	}
	principal := "unknown"
	if current, currentErr := user.Current(); currentErr == nil {
		principal = current.Uid + ":" + current.Username
	}
	return securityGrantBindingAtScope(actionKind, actionPayload, effects, policy, host, principal, cwd)
}

func securityGrantBindingAtScope(actionKind, actionPayload string, effects []ShellEffect, policy securitypolicy.EffectivePolicy, host, principal, cwd string) (state.SecurityActionGrant, error) {
	effectJSON, err := json.Marshal(effects)
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	effectSum := sha256.Sum256(effectJSON)
	effectDigest := hex.EncodeToString(effectSum[:])
	actionMaterial := strings.Join([]string{
		actionAnalyzerVersion,
		actionKind,
		policy.Hash,
		host,
		principal,
		cwd,
		effectDigest,
		actionPayload,
	}, "\x00")
	actionSum := sha256.Sum256([]byte(actionMaterial))
	summaryText := actionPayload
	if actionKind != "shell_execute" {
		summaryText = actionKind + " " + actionPayload
	}
	summary := secrets.Mask(summaryText)
	if len(summary) > 240 {
		summary = summary[:240] + "..."
	}
	return state.SecurityActionGrant{
		Digest:           hex.EncodeToString(actionSum[:]),
		ActionKind:       actionKind,
		MaskedSummary:    summary,
		EffectDigest:     effectDigest,
		PolicyHash:       policy.Hash,
		Host:             host,
		Principal:        principal,
		WorkingDirectory: cwd,
	}, nil
}
