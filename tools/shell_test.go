package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/coolcake/cvkeharness/state"
)

type recordingObserver struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingObserver) Observe(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordingObserver) snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

func TestValidateShellCommand_AllowsSafeDiagnostics(t *testing.T) {
	t.Parallel()

	safeCommands := []string{
		"ps aux",
		"df -h",
		"free -m",
		"uptime",
		"journalctl -n 50",
		"df -h && uptime",
		"ps aux; uptime",
		"uptime || df -h",
		"ps aux | grep docker",
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
		"ps > /tmp/output.txt",
		"ps < /etc/passwd",
		"ps `whoami`",
		"ps $(whoami)",
		"ps & whoami",
		"ps\nwhoami",
		"ps\rwhoami",
		"ps &&",
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
	if err := ValidateAllowedShellCommand("ps aux && journalctl -n 50", allowed); err != nil {
		t.Fatalf("expected chained allowed commands to pass validation: %v", err)
	}
	if err := ValidateAllowedShellCommand("ps aux | journalctl -n 50", allowed); err != nil {
		t.Fatalf("expected piped allowed commands to pass validation: %v", err)
	}

	if err := ValidateAllowedShellCommand("df -h", allowed); err == nil {
		t.Fatal("expected disallowed base command to be rejected")
	}
	if err := ValidateAllowedShellCommand("ps aux && whoami", allowed); err == nil {
		t.Fatal("expected chained disallowed command to be rejected")
	}
	if err := ValidateAllowedShellCommand("ps aux | whoami", allowed); err == nil {
		t.Fatal("expected piped disallowed command to be rejected")
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
			HistoryNote: "Command required manual approval and was approved by the user for this run only.",
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

func TestShellTool_PersistsApprovedSegments(t *testing.T) {
	t.Parallel()

	store := state.Open(t.TempDir() + "/state.db")
	defer store.Close()

	tool := NewShellToolWithApprovals([]string{"ps"}, nil, staticApprover{
		decision: ShellApprovalDecision{
			Approved:    true,
			Mode:        SafetyModeLLMJudge,
			HistoryNote: "Approved by the safety model.",
			Remember:    true,
		},
	}, "primary", store)

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello && echo goodbye"}`)); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	approvals, err := store.ListCommandApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListCommandApprovals returned unexpected error: %v", err)
	}
	if len(approvals) != 2 {
		t.Fatalf("expected 2 persisted approvals, got %d", len(approvals))
	}

	tool.approver = staticApprover{
		err: fmt.Errorf("approval path should not be reached once commands are remembered"),
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello && echo goodbye"}`)); err != nil {
		t.Fatalf("expected remembered approval to bypass secondary gate, got %v", err)
	}
}

func TestShellTool_ApproveOnceDoesNotPersistSegments(t *testing.T) {
	t.Parallel()

	store := state.Open(t.TempDir() + "/state.db")
	defer store.Close()

	tool := NewShellToolWithApprovals([]string{"ps"}, nil, staticApprover{
		decision: ShellApprovalDecision{
			Approved:    true,
			Mode:        SafetyModeUserConfirm,
			HistoryNote: "Command required manual approval and was approved by the user for this run only.",
			Remember:    false,
		},
	}, "primary", store)

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`)); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	approvals, err := store.ListCommandApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListCommandApprovals returned unexpected error: %v", err)
	}
	if len(approvals) != 0 {
		t.Fatalf("expected approve-once path to skip persistence, got %#v", approvals)
	}

	tool.approver = staticApprover{
		err: fmt.Errorf("approval path reached again as expected"),
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`)); err == nil {
		t.Fatal("expected second execution to request approval again")
	}
}

func TestShellTool_AllowsApprovedPipelines(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"echo", "grep"}, nil, "primary")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello | grep hell"}`))
	if err != nil {
		t.Fatalf("expected approved pipeline to execute, got %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected pipeline output to include match, got %q", result)
	}
}

func TestShellTool_StreamsEventsWhilePreservingOutput(t *testing.T) {
	t.Parallel()

	observer := &recordingObserver{}
	ctx := WithEventObserver(WithToolCallContext(context.Background(), "call-1", "shell_execute"), observer)
	tool := NewShellToolWithApprover([]string{"echo"}, nil, "primary")

	result, err := tool.Execute(ctx, json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected command output to be preserved, got %q", result)
	}

	events := observer.snapshot()
	if len(events) == 0 {
		t.Fatal("expected shell execution to emit events")
	}

	var sawStart, sawApproval, sawOutput, sawFinish bool
	for _, event := range events {
		switch event.Type {
		case EventShellCommandStarted:
			sawStart = event.Command == "echo hello" && event.ToolCallID == "call-1"
		case EventShellApproval:
			sawApproval = event.ApprovalMode == "allowlist"
		case EventShellOutput:
			sawOutput = sawOutput || strings.Contains(event.Output, "hello")
		case EventShellCommandFinished:
			sawFinish = event.Success && event.ExitCodeKnown && event.ExitCode == 0
		}
	}

	if !sawStart {
		t.Fatal("expected shell command start event")
	}
	if !sawApproval {
		t.Fatal("expected shell approval event for allowlist execution")
	}
	if !sawOutput {
		t.Fatal("expected shell output event to include command output")
	}
	if !sawFinish {
		t.Fatal("expected successful shell finish event")
	}
}

func TestUserPromptApproverApprovesYes(t *testing.T) {
	t.Parallel()

	approver := NewUserPromptApprover(strings.NewReader("2\n"), &strings.Builder{})
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
	if decision.Remember {
		t.Fatal("expected approve once to avoid remembering the command")
	}
}

func TestUserPromptApproverCanApproveAndRemember(t *testing.T) {
	t.Parallel()

	approver := NewUserPromptApprover(strings.NewReader("3\n"), &strings.Builder{})
	decision, err := approver.Approve(context.Background(), ShellApprovalRequest{
		Command:         "echo hello",
		ValidationError: `command "echo" is not in the allowlist`,
	})
	if err != nil {
		t.Fatalf("Approve returned unexpected error: %v", err)
	}
	if !decision.Remember {
		t.Fatal("expected third option to remember the approval")
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
