package tools

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/provider"
)

const (
	SafetyModeLLMJudge       = "llm_judge"
	SafetyModeUserConfirm    = "user_confirm"
	SafetyModeUserConfirmAll = "user_confirm_all"
	SafetyModeUnrestricted   = "unrestricted"
)

// ShellApprovalRequest describes a command that needs secondary approval.
type ShellApprovalRequest struct {
	Command         string
	ValidationError string
}

// ShellApprovalDecision captures how a gated command was handled.
type ShellApprovalDecision struct {
	Approved    bool
	Mode        string
	HistoryNote string
	Remember    bool
}

// ShellApprover decides whether a non-allowlisted command may run.
type ShellApprover interface {
	Approve(ctx context.Context, req ShellApprovalRequest) (ShellApprovalDecision, error)
}

type llmJudgeApprover struct {
	judge       provider.Provider
	safetyModel string
	dumper      *promptdump.Dumper
}

// NewLLMJudgeApprover creates a judge-backed shell approval path.
func NewLLMJudgeApprover(judge provider.Provider, safetyModel string) ShellApprover {
	return NewLLMJudgeApproverWithPromptDumper(judge, safetyModel, nil)
}

// NewLLMJudgeApproverWithPromptDumper creates a judge-backed shell approval
// path and optionally captures the full judge prompt for debugging.
func NewLLMJudgeApproverWithPromptDumper(judge provider.Provider, safetyModel string, dumper *promptdump.Dumper) ShellApprover {
	if judge == nil || strings.TrimSpace(safetyModel) == "" {
		return nil
	}
	return &llmJudgeApprover{
		judge:       judge,
		safetyModel: safetyModel,
		dumper:      dumper,
	}
}

func (a *llmJudgeApprover) Approve(ctx context.Context, req ShellApprovalRequest) (ShellApprovalDecision, error) {
	prompt := fmt.Sprintf("An automated DevOps agent wants to run the following bash command:\n`%s`\nIs this command safe (will not permanently delete vital data, alter kernel, or install clearly malicious software)? Reply strictly with 'SAFE' or 'DANGEROUS'. Provide no other output.", req.Command)

	chatReq := &provider.ChatRequest{
		Model:       a.safetyModel,
		Messages:    []provider.Message{{Role: "user", Content: prompt}},
		Temperature: 0.0,
		MaxTokens:   10,
	}
	var dump *promptdump.Handle
	if a.dumper != nil && a.dumper.Enabled() {
		dump, _ = a.dumper.Begin(ctx, promptdump.Metadata{
			Phase:    core.PhaseVerification,
			Model:    a.safetyModel,
			Label:    "shell-llm-judge",
			Provider: "",
		}, chatReq)
	}

	resp, err := a.judge.ChatCompletion(ctx, chatReq)
	if a.dumper != nil && a.dumper.Enabled() {
		result := promptdump.Result{Err: err}
		if resp != nil {
			result.ActualModel = resp.Model
			result.Usage = resp.Usage
		}
		_ = a.dumper.Finish(dump, result)
	}
	if err != nil {
		return ShellApprovalDecision{}, fmt.Errorf("LLM judge failed to evaluate command: %w\nOriginal safety error: %v", err, req.ValidationError)
	}

	decision := strings.TrimSpace(strings.ToUpper(resp.Message.Content))
	if !strings.Contains(decision, "SAFE") || strings.Contains(decision, "DANGEROUS") {
		return ShellApprovalDecision{}, fmt.Errorf("safety constraint violated: supervisor model deemed this command dangerous")
	}

	return ShellApprovalDecision{
		Approved: true,
		Mode:     SafetyModeLLMJudge,
		Remember: true,
	}, nil
}

// UserPromptApprover asks the terminal user to approve a gated command.
type UserPromptApprover struct {
	in  io.Reader
	out io.Writer
}

// NewUserPromptApprover creates a shell approver that waits for user input.
func NewUserPromptApprover(in io.Reader, out io.Writer) ShellApprover {
	if in == nil || out == nil {
		return nil
	}
	return &UserPromptApprover{
		in:  in,
		out: out,
	}
}

func (a *UserPromptApprover) Approve(_ context.Context, req ShellApprovalRequest) (ShellApprovalDecision, error) {
	idx, err := termui.Select(termui.SelectOptions{
		Title: "Command requires approval",
		Details: []string{
			"Command: " + req.Command,
			"Reason: " + req.ValidationError,
		},
		Choices: []termui.Choice{
			{Label: "Reject command", Description: "Keep this run within the current approval policy"},
			{Label: "Approve once", Description: "Run this command now, then ask again next time"},
			{Label: "Approve and remember", Description: "Run it now and reuse this approval for matching command segments"},
		},
		InitialIndex: 0,
		In:           a.in,
		Out:          a.out,
	})
	if err != nil {
		return ShellApprovalDecision{}, err
	}
	if idx == 0 {
		return ShellApprovalDecision{}, fmt.Errorf("safety constraint violated: user denied command execution")
	}

	historyNote := "Command required manual approval and was approved by the user for this run only."
	remember := false
	if idx == 2 {
		historyNote = "Command required manual approval and was approved by the user for reuse."
		remember = true
	}

	return ShellApprovalDecision{
		Approved:    true,
		Mode:        SafetyModeUserConfirm,
		HistoryNote: historyNote,
		Remember:    remember,
	}, nil
}
