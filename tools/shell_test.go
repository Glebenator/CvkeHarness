package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/shellpolicy"
	"github.com/coolcake/cvkeharness/internal/telemetry"
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

func TestValidateAllowedShellCommand_RejectsEscapedJournalMutation(t *testing.T) {
	t.Parallel()

	if err := ValidateAllowedShellCommand(`journalctl --vacu\um-time=1s`, []string{"journalctl"}); err == nil {
		t.Fatal("escaped journalctl mutation flag must not bypass the approval gate")
	}
}

func TestReusableShellSegmentRejectsContextDependentCommands(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"rm *.tmp", "rm relative.txt", "./repair.sh", "python repair.py", "systemctl restart $UNIT"} {
		parsed, err := ParseShellCommand(command)
		if err != nil {
			continue
		}
		if ReusableShellSegment(parsed.Segments[0]) {
			t.Fatalf("context-dependent command %q must not be reusable", command)
		}
	}
	parsed, err := ParseShellCommand("systemctl restart api")
	if err != nil || !ReusableShellSegment(parsed.Segments[0]) {
		t.Fatalf("expected exact target-level service action to be reusable, err=%v", err)
	}
}

func TestShellPolicyCorpus(t *testing.T) {
	t.Parallel()

	cases, err := shellpolicy.LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus returned unexpected error: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("expected shared shell policy corpus to include cases")
	}

	defaultAllowed := config.DefaultConfig().AllowedCommands
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.ID, func(t *testing.T) {
			t.Parallel()

			actual := shellpolicy.DecisionAllow
			allowed := defaultAllowed
			if len(testCase.AllowedCommands) > 0 {
				allowed = testCase.AllowedCommands
			}
			if err := ValidateShellCommand(testCase.Command); err != nil {
				actual = shellpolicy.DecisionDeny
			} else if err := ValidateAllowedShellCommand(testCase.Command, allowed); err != nil {
				actual = shellpolicy.DecisionRequireApproval
			}

			if actual != testCase.ExpectedDecision {
				t.Fatalf("expected %s for %q, got %s", testCase.ExpectedDecision, testCase.Command, actual)
			}
		})
	}
}

func TestParseShellCommand_Regressions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		command   string
		wantError bool
		segments  []string
		operators []string
	}{
		{
			name:     "quoted strings",
			command:  `printf "hello world" && printf 'done'`,
			segments: []string{`printf "hello world"`, `printf 'done'`},
			operators: []string{
				"&&",
			},
		},
		{
			name:     "escaped spaces",
			command:  `printf hello\ world`,
			segments: []string{`printf hello\ world`},
		},
		{
			name:      "empty input",
			command:   "  ",
			wantError: true,
		},
		{
			name:      "unterminated single quote",
			command:   `printf 'hello`,
			wantError: true,
		},
		{
			name:      "unterminated double quote",
			command:   `printf "hello`,
			wantError: true,
		},
		{
			name:     "pipeline",
			command:  "ps aux | grep docker",
			segments: []string{"ps aux", "grep docker"},
			operators: []string{
				"|",
			},
		},
		{
			name:     "or chain",
			command:  "uptime || df -h",
			segments: []string{"uptime", "df -h"},
			operators: []string{
				"||",
			},
		},
		{
			name:      "trailing semicolon",
			command:   "ps aux;",
			wantError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := ParseShellCommand(tt.command)
			if tt.wantError {
				if err == nil {
					t.Fatalf("ParseShellCommand(%q) unexpectedly succeeded", tt.command)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseShellCommand(%q) returned unexpected error: %v", tt.command, err)
			}
			if len(parsed.Segments) != len(tt.segments) {
				t.Fatalf("expected %d segments, got %#v", len(tt.segments), parsed.Segments)
			}
			for idx, want := range tt.segments {
				if parsed.Segments[idx].Normalized != want {
					t.Fatalf("segment %d: expected %q, got %q", idx, want, parsed.Segments[idx].Normalized)
				}
			}
			if strings.Join(parsed.Operators, ",") != strings.Join(tt.operators, ",") {
				t.Fatalf("expected operators %#v, got %#v", tt.operators, parsed.Operators)
			}
		})
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

func TestShellTool_UserConfirmAllPromptsForAllowlistedCommand(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"echo"}, staticApprover{
		decision: ShellApprovalDecision{
			Approved:    true,
			Mode:        SafetyModeUserConfirmAll,
			HistoryNote: "Command required approval by setup policy.",
			Remember:    true,
		},
	}, "primary")
	tool.approvalRequired = true

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if !strings.Contains(result, "setup policy") {
		t.Fatalf("expected approval note to be preserved, got %q", result)
	}

	tool.approver = staticApprover{err: fmt.Errorf("approval requested again")}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`)); err == nil {
		t.Fatal("expected approvalRequired mode to ask again instead of remembering")
	}
}

func TestShellTool_UnrestrictedBypassesAllowlist(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"ps"}, nil, "primary")
	tool.unrestricted = true

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo unrestricted"}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if !strings.Contains(result, "unrestricted") {
		t.Fatalf("expected command output, got %q", result)
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

func TestBlockingApproverReturnsApprovalRequiredError(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"ps"}, NewBlockingApprover(), "primary")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err == nil {
		t.Fatal("expected blocked approval error")
	}
	approvalErr, ok := IsApprovalRequired(err)
	if !ok {
		t.Fatalf("expected ApprovalRequiredError, got %T: %v", err, err)
	}
	if approvalErr.Request.Command != "echo hello" {
		t.Fatalf("expected command to persist in approval request, got %#v", approvalErr)
	}
}

func TestShellToolFailsClosedWhenApproverDoesNotApprove(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"ps"}, staticApprover{
		decision: ShellApprovalDecision{Approved: false, Mode: SafetyModeUserConfirm},
	}, "primary")
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo should-not-run"}`)); err == nil {
		t.Fatal("expected non-approved decision to fail closed")
	}
}

func TestShellTool_LLMJudgeNeverPersistsApprovedSegments(t *testing.T) {
	t.Parallel()

	store := state.Open(t.TempDir() + "/state.db")
	defer store.Close()
	now := time.Now().UTC()
	if err := store.ReplaceOperationalMemory(context.Background(), state.OperationalMemory{Targets: []state.Target{{
		ID: "runtime-1", Kind: "runtime", Environment: state.EnvironmentRuntime,
		PrimaryName: "runtime.local", Transport: "local", RemoteIdentity: "local:runtime-1",
		Status: state.MemoryStatusActive, FirstSeenAt: now, LastSeenAt: now, VerifiedAt: now, ExpiresAt: now.Add(time.Hour),
	}}}); err != nil {
		t.Fatalf("ReplaceOperationalMemory returned error: %v", err)
	}

	tool := NewShellToolWithApprovalStore([]string{"ps"}, staticApprover{
		decision: ShellApprovalDecision{
			Approved:    true,
			Mode:        SafetyModeLLMJudge,
			HistoryNote: "Approved by the safety model.",
			Remember:    true,
		},
	}, "primary", store)

	ctx := telemetry.WithFields(context.Background(), telemetry.Fields{TargetID: "runtime-1"})
	if _, err := tool.Execute(ctx, json.RawMessage(`{"command":"echo hello && echo goodbye"}`)); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	approvals, err := store.ListCommandApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListCommandApprovals returned unexpected error: %v", err)
	}
	if len(approvals) != 0 {
		t.Fatalf("expected no persisted approvals from LLM judge, got %d", len(approvals))
	}
}

func TestScopedApprovalRejectsExpiredOrChangedTargetIdentity(t *testing.T) {
	t.Parallel()

	store := state.Open(t.TempDir() + "/state.db")
	defer store.Close()
	now := time.Now().UTC()
	target := state.Target{
		ID: "target-1", Kind: "ssh", Environment: "production",
		PrimaryName: "api", Transport: "ssh", RemoteIdentity: "ops@api",
		Status: state.MemoryStatusActive, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.ReplaceOperationalMemory(context.Background(), state.OperationalMemory{Targets: []state.Target{target}}); err != nil {
		t.Fatalf("ReplaceOperationalMemory returned error: %v", err)
	}
	if err := store.SaveCommandApproval(context.Background(), state.CommandApproval{
		TargetID: target.ID, Environment: target.Environment, RemoteIdentity: target.RemoteIdentity,
		Command: "systemctl restart api", Action: "systemctl restart", Status: state.ApprovalStatusApproved,
		Source: "cli_policy", PolicyVersion: state.CommandApprovalPolicyVersion,
		ApprovedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveCommandApproval returned error: %v", err)
	}
	tool := NewShellToolWithApprovalStore(nil, NewBlockingApprover(), "primary", store)
	parsed, err := ParseShellCommand("systemctl restart api")
	if err != nil {
		t.Fatalf("ParseShellCommand returned error: %v", err)
	}
	ctx := telemetry.WithFields(context.Background(), telemetry.Fields{TargetID: target.ID})
	if got := tool.scopedApprovedCommands(ctx, parsed); !got[parsed.Segments[0].Normalized] {
		t.Fatalf("expected approval for unchanged live identity, got %#v", got)
	}

	target.RemoteIdentity = "admin@api"
	if err := store.ReplaceOperationalMemory(context.Background(), state.OperationalMemory{Targets: []state.Target{target}}); err != nil {
		t.Fatalf("ReplaceOperationalMemory identity change returned error: %v", err)
	}
	if got := tool.scopedApprovedCommands(ctx, parsed); len(got) != 0 {
		t.Fatalf("changed remote identity must invalidate approval, got %#v", got)
	}

	target.RemoteIdentity = "ops@api"
	target.ExpiresAt = now.Add(-time.Minute)
	if err := store.ReplaceOperationalMemory(context.Background(), state.OperationalMemory{Targets: []state.Target{target}}); err != nil {
		t.Fatalf("ReplaceOperationalMemory expiry returned error: %v", err)
	}
	if got := tool.scopedApprovedCommands(ctx, parsed); len(got) != 0 {
		t.Fatalf("expired target must invalidate approval, got %#v", got)
	}
}

func TestInteractiveRememberedApprovalRequiresAndMatchesSession(t *testing.T) {
	t.Parallel()

	store := state.Open(t.TempDir() + "/state.db")
	defer store.Close()
	now := time.Now().UTC()
	target := state.Target{
		ID: "target-1", Kind: "ssh", Environment: "production",
		PrimaryName: "api", Transport: "ssh", RemoteIdentity: "ops@api",
		Status: state.MemoryStatusActive, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.ReplaceOperationalMemory(context.Background(), state.OperationalMemory{Targets: []state.Target{target}}); err != nil {
		t.Fatalf("ReplaceOperationalMemory returned error: %v", err)
	}
	tool := NewShellToolWithApprovalStore(nil, staticApprover{decision: ShellApprovalDecision{
		Approved: true, Mode: SafetyModeUserConfirm, Remember: true,
	}}, "primary", store)
	command := json.RawMessage(`{"command":"echo session-bound"}`)
	missingSession := telemetry.WithFields(context.Background(), telemetry.Fields{TargetID: target.ID})
	if _, err := tool.Execute(missingSession, command); err != nil {
		t.Fatalf("missing-session execution may proceed once after confirmation, got %v", err)
	}
	if approvals, err := store.ListCommandApprovals(context.Background()); err != nil || len(approvals) != 0 {
		t.Fatalf("missing session must not persist approval, got %#v err=%v", approvals, err)
	}

	sessionA := telemetry.WithFields(context.Background(), telemetry.Fields{TargetID: target.ID, SessionID: "session-a"})
	if _, err := tool.Execute(sessionA, command); err != nil {
		t.Fatalf("session-a confirmation returned error: %v", err)
	}
	tool.approver = staticApprover{err: fmt.Errorf("approval requested")}
	if _, err := tool.Execute(sessionA, command); err != nil {
		t.Fatalf("same session should reuse approval, got %v", err)
	}
	sessionB := telemetry.WithFields(context.Background(), telemetry.Fields{TargetID: target.ID, SessionID: "session-b"})
	if _, err := tool.Execute(sessionB, command); err == nil || !strings.Contains(err.Error(), "approval requested") {
		t.Fatalf("different session must request approval again, got %v", err)
	}
}

func TestShellTool_LLMJudgeCannotApproveUnresolvedTarget(t *testing.T) {
	t.Parallel()

	tool := NewShellToolWithApprover([]string{"ps"}, staticApprover{
		decision: ShellApprovalDecision{Approved: true, Mode: SafetyModeLLMJudge},
	}, "primary")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"systemctl restart api"}`))
	if err == nil || !strings.Contains(err.Error(), "unresolved or ambiguous target") {
		t.Fatalf("expected unresolved LLM approval to fail closed, got %v", err)
	}
}

func TestShellTool_ApproveOnceDoesNotPersistSegments(t *testing.T) {
	t.Parallel()

	store := state.Open(t.TempDir() + "/state.db")
	defer store.Close()

	tool := NewShellToolWithApprovalStore([]string{"ps"}, staticApprover{
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
