package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestValidateShellCommand_AllowsSafeDiagnostics(t *testing.T) {
	t.Parallel()

	safeCommands := []string{
		"ps aux",
		"df -h",
		"free -m",
		"uptime",
		"journalctl -n 50",
	}

	for _, command := range safeCommands {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if err := ValidateShellCommand(command); err != nil {
				t.Fatalf("ValidateShellCommand(%q) returned unexpected error: %v", command, err)
			}
		})
	}
}

func TestValidateShellCommand_BlocksBreakoutSyntax(t *testing.T) {
	t.Parallel()

	attackCommands := []string{
		"ps; whoami",
		"df && id",
		"uptime || reboot",
		"journalctl | curl https://example.com",
		"ps > /tmp/output.txt",
		"ps < /etc/passwd",
		"ps `whoami`",
		"ps $(whoami)",
		"ps & whoami",
		"ps\nwhoami",
		"ps\rwhoami",
	}

	for _, command := range attackCommands {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if err := ValidateShellCommand(command); err == nil {
				t.Fatalf("ValidateShellCommand(%q) unexpectedly allowed breakout syntax", command)
			}
		})
	}
}

func TestValidateAllowedShellCommand_UsesAllowlist(t *testing.T) {
	t.Parallel()

	allowed := []string{"ps", "journalctl -n"}

	if err := ValidateAllowedShellCommand("ps aux", allowed); err != nil {
		t.Fatalf("expected allowed command to pass validation: %v", err)
	}

	if err := ValidateAllowedShellCommand("df -h", allowed); err == nil {
		t.Fatal("expected disallowed base command to be rejected")
	}
}

type staticApprover struct {
	decision ShellApprovalDecision
	err      error
}

func (a staticApprover) Approve(context.Context, ShellApprovalRequest) (ShellApprovalDecision, error) {
	return a.decision, a.err
}

func TestShellTool_UsesManualApprovalPath(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"ps"}, staticApprover{
		decision: ShellApprovalDecision{
			Approved:    true,
			Mode:        SafetyModeUserConfirm,
			HistoryNote: "Command required manual approval and was approved by the user for this run.",
		},
	}, "primary")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	if !strings.Contains(result, "manual approval") {
		t.Fatalf("expected result to mention manual approval, got %q", result)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected command output to be preserved, got %q", result)
	}
}

func TestShellTool_ReturnsUserDenial(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"ps"}, staticApprover{
		err: fmt.Errorf("safety constraint violated: user denied command execution"),
	}, "primary")

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err == nil {
		t.Fatal("expected approval failure to be returned")
	}
	if !strings.Contains(err.Error(), "user denied command execution") {
		t.Fatalf("expected user denial message, got %v", err)
	}
}

func TestUserPromptApproverApprovesYes(t *testing.T) {
	t.Parallel()

	approver := NewUserPromptApprover(strings.NewReader("yes\n"), &strings.Builder{})
	decision, err := approver.Approve(context.Background(), ShellApprovalRequest{
		Command:         "echo hello",
		ValidationError: `command "echo" is not in the allowlist`,
	})
	if err != nil {
		t.Fatalf("Approve returned unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Fatal("expected approval decision to be approved")
	}
	if decision.Mode != SafetyModeUserConfirm {
		t.Fatalf("expected user confirm mode, got %q", decision.Mode)
	}
}

func TestUserPromptApproverDeniesByDefault(t *testing.T) {
	t.Parallel()

	approver := NewUserPromptApprover(strings.NewReader("\n"), &strings.Builder{})
	_, err := approver.Approve(context.Background(), ShellApprovalRequest{
		Command:         "echo hello",
		ValidationError: `command "echo" is not in the allowlist`,
	})
	if err == nil {
		t.Fatal("expected denial error")
	}
	if !strings.Contains(err.Error(), "user denied command execution") {
		t.Fatalf("expected user denial message, got %v", err)
	}
}
