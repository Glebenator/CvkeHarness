package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/shellpolicy"
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
		"ps aux\nuptime",
		"ps aux\r\nuptime",
		"printf 'hello\nworld'",
		"printf \"hello\nworld\"",
		"python3 - <<'PY'\nprint('hello')\nPY",
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
		"ps `whoami`",
		"ps $(whoami)",
		"ps \\\naux",
		"printf $\\\n(whoami)",
		"ps & whoami",
		"ps &&",
		"cat <<EOF\n$(whoami)\nEOF",
		"cat <<< 'data'",
		"cat <<'EOF' trailing\ndata\nEOF",
		"cat <<'EOF'\ndata",
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

func TestValidateShellCommand_AllowsClassifiableRedirection(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"ps > /tmp/output.txt", "ps < /etc/hosts", "ps 2>/dev/null", "ps 2>&1"} {
		if err := ValidateShellCommand(command); err != nil {
			t.Fatalf("ValidateShellCommand(%q): %v", command, err)
		}
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
	if err := ValidateAllowedShellCommand("ps <<'EOF'\naux\nEOF", allowed); err == nil {
		t.Fatal("expected an allowlisted command with a heredoc to require secondary approval")
	}
	if err := ValidateAllowedShellCommand("systemctl restart sshd", []string{"systemctl"}); err == nil {
		t.Fatal("bare systemctl allowlist must not auto-approve mutations")
	}
	if err := ValidateAllowedShellCommand("journalctl --vacuum-time=1d", []string{"journalctl"}); err == nil {
		t.Fatal("bare journalctl allowlist must not auto-approve retention deletion")
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
			name:     "multiline commands",
			command:  "ps aux\nuptime\r\ndf -h",
			segments: []string{"ps aux", "uptime", "df -h"},
			operators: []string{
				"\n",
				"\n",
			},
		},
		{
			name:     "blank lines and operator continuation",
			command:  "ps aux &&\n\nuptime",
			segments: []string{"ps aux", "uptime"},
			operators: []string{
				"&&",
			},
		},
		{
			name:     "quoted multiline strings",
			command:  "printf 'hello\nworld' && printf \"goodbye\nworld\"",
			segments: []string{"printf 'hello\nworld'", "printf \"goodbye\nworld\""},
			operators: []string{
				"&&",
			},
		},
		{
			name:      "quoted heredoc body is opaque",
			command:   "python3 - <<'PY'\nimport os\nprint('a | b && c > d < e $(f)')\nPY",
			segments:  []string{"python3 - <<'PY'\nimport os\nprint('a | b && c > d < e $(f)')\nPY"},
			operators: nil,
		},
		{
			name:     "quoted heredoc followed by command",
			command:  "python3 - <<\"PY\"\nprint('hello')\nPY\nuptime",
			segments: []string{"python3 - <<\"PY\"\nprint('hello')\nPY", "uptime"},
			operators: []string{
				"\n",
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

func TestQuotedHeredocApprovalIdentityPreservesBodyWhitespace(t *testing.T) {
	t.Parallel()

	withOneSpace, err := ParseShellCommand("python3 - <<'PY'\nif True:\n print('x')\nPY")
	if err != nil {
		t.Fatalf("ParseShellCommand returned unexpected error: %v", err)
	}
	withFourSpaces, err := ParseShellCommand("python3 - <<'PY'\nif True:\n    print('x')\nPY")
	if err != nil {
		t.Fatalf("ParseShellCommand returned unexpected error: %v", err)
	}
	if withOneSpace.Segments[0].Normalized == withFourSpaces.Segments[0].Normalized {
		t.Fatal("expected indentation changes in a heredoc body to produce distinct approval identities")
	}
	if got := normalizeApprovedShellCommand(withFourSpaces.Segments[0].Normalized); got != withFourSpaces.Segments[0].Normalized {
		t.Fatalf("expected persisted heredoc approval identity to round-trip exactly, got %q", got)
	}
}

type staticApprover struct {
	decision ShellApprovalDecision
	err      error
}

type requestRecordingApprover struct {
	request ShellApprovalRequest
}

func (a *requestRecordingApprover) Approve(_ context.Context, req ShellApprovalRequest) (ShellApprovalDecision, error) {
	a.request = req
	return ShellApprovalDecision{Approved: true, Mode: SafetyModeLLMJudge}, nil
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

func TestShellTool_RoutesMultilineCommandThroughApprovalGate(t *testing.T) {
	t.Parallel()

	approver := &requestRecordingApprover{}
	tool := NewShellToolWithApprover([]string{"printf"}, approver, "primary")
	command := "printf 'first\\n'\nuname -s"
	args, err := json.Marshal(ShellArgs{Command: command})
	if err != nil {
		t.Fatalf("Marshal returned unexpected error: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if approver.request.Command != command {
		t.Fatalf("expected approval gate to receive multiline command %q, got %q", command, approver.request.Command)
	}
	if !strings.Contains(result, "first") {
		t.Fatalf("expected multiline command output, got %q", result)
	}
}

func TestShellTool_RoutesQuotedHeredocThroughApprovalGate(t *testing.T) {
	t.Parallel()

	approver := &requestRecordingApprover{}
	tool := NewShellToolWithApprover([]string{"python3"}, approver, "primary")
	command := "python3 - <<'PY'\nprint('heredoc reached judge')\nPY"
	args, err := json.Marshal(ShellArgs{Command: command})
	if err != nil {
		t.Fatalf("Marshal returned unexpected error: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if approver.request.Command != command {
		t.Fatalf("expected approval gate to receive complete heredoc %q, got %q", command, approver.request.Command)
	}
	if !strings.Contains(approver.request.ValidationError, "requires secondary approval") {
		t.Fatalf("expected heredoc to require secondary approval, got %q", approver.request.ValidationError)
	}
	if !strings.Contains(result, "heredoc reached judge") {
		t.Fatalf("expected heredoc output, got %q", result)
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
	if !strings.Contains(err.Error(), "Policy reason:") || !strings.Contains(err.Error(), approvalErr.Request.ValidationError) {
		t.Fatalf("blocked error should retain the exact policy reason, got %q", err.Error())
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
