package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/coolcake/cvkeharness/provider"
)

const (
	SafetyModeLLMJudge    = "llm_judge"
	SafetyModeUserConfirm = "user_confirm"
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
}

// ShellApprover decides whether a non-allowlisted command may run.
type ShellApprover interface {
	Approve(ctx context.Context, req ShellApprovalRequest) (ShellApprovalDecision, error)
}

type llmJudgeApprover struct {
	judge       provider.Provider
	safetyModel string
}

// NewLLMJudgeApprover creates a judge-backed shell approval path.
func NewLLMJudgeApprover(judge provider.Provider, safetyModel string) ShellApprover {
	if judge == nil || strings.TrimSpace(safetyModel) == "" {
		return nil
	}
	return &llmJudgeApprover{
		judge:       judge,
		safetyModel: safetyModel,
	}
}

func (a *llmJudgeApprover) Approve(ctx context.Context, req ShellApprovalRequest) (ShellApprovalDecision, error) {
	prompt := fmt.Sprintf("An automated DevOps agent wants to run the following bash command:\n`%s`\nIs this command safe (will not permanently delete vital data, alter kernel, or install clearly malicious software)? Reply strictly with 'SAFE' or 'DANGEROUS'. Provide no other output.", req.Command)

	resp, err := a.judge.ChatCompletion(ctx, &provider.ChatRequest{
		Model:       a.safetyModel,
		Messages:    []provider.Message{{Role: "user", Content: prompt}},
		Temperature: 0.0,
		MaxTokens:   10,
	})
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
	}, nil
}

// UserPromptApprover asks the terminal user to approve a gated command.
type UserPromptApprover struct {
	in  *bufio.Reader
	out io.Writer
}

// NewUserPromptApprover creates a shell approver that waits for user input.
func NewUserPromptApprover(in io.Reader, out io.Writer) ShellApprover {
	if in == nil || out == nil {
		return nil
	}
	return &UserPromptApprover{
		in:  bufio.NewReader(in),
		out: out,
	}
}

func (a *UserPromptApprover) Approve(_ context.Context, req ShellApprovalRequest) (ShellApprovalDecision, error) {
	if _, err := fmt.Fprintf(a.out, "\nCommand requires manual approval.\nCommand: %s\nReason: %s\nApprove and run it? [y/N]: ", req.Command, req.ValidationError); err != nil {
		return ShellApprovalDecision{}, err
	}

	line, err := a.in.ReadString('\n')
	if err != nil {
		return ShellApprovalDecision{}, err
	}

	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return ShellApprovalDecision{}, fmt.Errorf("safety constraint violated: user denied command execution")
	}

	return ShellApprovalDecision{
		Approved:    true,
		Mode:        SafetyModeUserConfirm,
		HistoryNote: "Command required manual approval and was approved by the user for this run.",
	}, nil
}
