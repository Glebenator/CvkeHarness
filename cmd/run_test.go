package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/state"
)

func TestPromptModelApprovalRejectsByDefault(t *testing.T) {
	t.Parallel()

	approved, err := promptModelApprovalWithIO(strings.NewReader("\n"), &strings.Builder{}, core.NewModelRef("openrouter", "gpt-best"), "higher confidence for execution")
	if err != nil {
		t.Fatalf("promptModelApprovalWithIO returned unexpected error: %v", err)
	}
	if approved {
		t.Fatal("expected blank fallback input to keep the current approved model")
	}
}

func TestPromptModelApprovalAcceptsSecondOption(t *testing.T) {
	t.Parallel()

	approved, err := promptModelApprovalWithIO(strings.NewReader("2\n"), &strings.Builder{}, core.NewModelRef("openrouter", "gpt-best"), "higher confidence for execution")
	if err != nil {
		t.Fatalf("promptModelApprovalWithIO returned unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected choosing the second option to approve the recommended model")
	}
}

func TestModelApprovalPromptRequiresInteractiveInputAndOutput(t *testing.T) {
	t.Parallel()

	if !shouldPromptModelApproval(true, true) {
		t.Fatal("expected an interactive terminal to permit the approval prompt")
	}
	for _, test := range []struct {
		stdinTTY  bool
		stdoutTTY bool
	}{
		{stdinTTY: false, stdoutTTY: true},
		{stdinTTY: true, stdoutTTY: false},
		{stdinTTY: false, stdoutTTY: false},
	} {
		if shouldPromptModelApproval(test.stdinTTY, test.stdoutTTY) {
			t.Fatalf("expected non-interactive combination stdin=%t stdout=%t to reject prompting", test.stdinTTY, test.stdoutTTY)
		}
	}
}

func TestBlockedRunNoticeNamesActionPolicyScopeAndConsequence(t *testing.T) {
	t.Parallel()

	notice := blockedRunNotice(agent.RunResult{
		BlockedWorkID: "bw_123",
		Target: memory.TargetResolution{
			TargetID:    "ssh:production-web-2",
			TargetKind:  memory.TargetKindSSH,
			PrimaryName: "production-web-2",
		},
		BlockedApproval: &agent.BlockedApproval{
			Action:           "sudo systemctl restart nginx",
			Reason:           "service mutation requires explicit operator approval",
			ActionKind:       "shell_execute",
			Host:             "production-web-2",
			Principal:        "deploy",
			WorkingDirectory: "/srv/app",
		},
	})
	got := strings.Join([]string{notice.Action, notice.Reason, notice.Scope, notice.Effect, notice.Approve}, "\n")
	for _, want := range []string{
		"sudo systemctl restart nginx",
		"service mutation requires explicit operator approval",
		"production-web-2 | exact command | principal deploy | cwd /srv/app | approve once",
		"Approve once + retry. No action has run.",
		"cvkeharness commands approve-work bw_123",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected blocked notice to contain %q, got %q", want, got)
		}
	}
}

func TestSummarizeRunResultAggregatesPhasesAndTools(t *testing.T) {
	t.Parallel()

	startedAt := time.Now().Add(-3 * time.Second)
	finishedAt := startedAt.Add(2500 * time.Millisecond)
	summary := summarizeRunResult(agent.RunResult{
		Run: state.RunRecord{
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			Phases: []state.PhaseRecord{
				{
					Provider:          "openrouter",
					ActualModel:       "model-a",
					PromptTokens:      100,
					CompletionTokens:  25,
					TotalTokens:       125,
					CachedTokens:      10,
					CachedTokensKnown: true,
				},
				{
					Provider:         "openrouter",
					ActualModel:      "model-b",
					PromptTokens:     40,
					CompletionTokens: 15,
					TotalTokens:      55,
				},
			},
			Tools: []state.ToolOutcome{
				{ToolName: "shell_execute", Success: true},
				{ToolName: "memory_record_finding", Success: false},
			},
		},
	}, "")

	if summary.ExitReason != "Completed" {
		t.Fatalf("expected completed exit reason, got %q", summary.ExitReason)
	}
	if summary.Duration != 2500*time.Millisecond {
		t.Fatalf("expected duration to be preserved, got %s", summary.Duration)
	}
	if summary.TotalTokens != 180 || summary.PromptTokens != 140 || summary.CompletionTokens != 40 {
		t.Fatalf("unexpected token totals: %#v", summary)
	}
	if !summary.CachedTokensKnown || summary.CachedTokens != 10 {
		t.Fatalf("expected cached tokens, got %#v", summary)
	}
	if summary.ToolCalls != 2 || summary.SuccessfulTools != 1 || summary.FailedTools != 1 {
		t.Fatalf("unexpected tool counts: %#v", summary)
	}
	if len(summary.ModelsUsed) != 2 {
		t.Fatalf("expected 2 models, got %v", summary.ModelsUsed)
	}
}

func TestSummarizeRunResultCountsApprovalRequiredSeparately(t *testing.T) {
	t.Parallel()

	summary := summarizeRunResult(agent.RunResult{
		Run: state.RunRecord{
			TaskState: state.TaskStateBlockedWaitingUser,
			Tools: []state.ToolOutcome{{
				ToolName:     "shell_execute",
				PolicyDenied: true,
				DenialClass:  "approval_required",
			}},
		},
	}, "")

	if summary.ExitCode != 1 || summary.BlockedTools != 1 || summary.FailedTools != 0 {
		t.Fatalf("expected blocked tool and exit 1, got %#v", summary)
	}
	if summary.VerificationStatus != "stopped safely" {
		t.Fatalf("expected explicit verification stop, got %#v", summary)
	}
}

func TestRunTargetLabelExplainsTransport(t *testing.T) {
	t.Parallel()

	got := runTargetLabel(memory.TargetResolution{PrimaryName: "production-web-2", TargetKind: memory.TargetKindSSH})
	if got != "production-web-2 | remote via ssh" {
		t.Fatalf("unexpected target label %q", got)
	}
}

func TestSummarizeRunResultUsesInterruptReason(t *testing.T) {
	t.Parallel()

	summary := summarizeRunResult(agent.RunResult{}, "interrupt")
	if summary.ExitReason != "Interrupted" {
		t.Fatalf("expected interrupted exit reason, got %q", summary.ExitReason)
	}
}

func TestSummarizeRunResultUsesTaskState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state state.TaskState
		want  string
	}{
		{state: state.TaskStateCompleted, want: "Completed"},
		{state: state.TaskStateBlockedWaitingUser, want: "Approval required"},
		{state: state.TaskStateIncomplete, want: "Incomplete"},
		{state: state.TaskStateFailed, want: "Failed"},
	}
	for _, test := range tests {
		summary := summarizeRunResult(agent.RunResult{Run: state.RunRecord{TaskState: test.state}}, "")
		if summary.ExitReason != test.want {
			t.Fatalf("task state %q produced exit %q, want %q", test.state, summary.ExitReason, test.want)
		}
	}
}
