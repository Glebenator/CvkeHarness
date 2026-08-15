package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/tools"
)

func TestTranscriptRendererPlainShellFlow(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	renderer := NewTranscriptRenderer(&out, "plain")

	renderer.Observe(tools.Event{
		Type:       tools.EventShellCommandStarted,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Command:    "echo hello",
	})
	renderer.Observe(tools.Event{
		Type:       tools.EventShellOutput,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Output:     "hello\nworld",
	})
	renderer.Observe(tools.Event{
		Type:          tools.EventShellCommandFinished,
		ToolName:      "shell_execute",
		ToolCallID:    "call-1",
		Success:       true,
		ExitCode:      0,
		ExitCodeKnown: true,
		Duration:      1500 * time.Millisecond,
	})

	got := out.String()
	if strings.Contains(got, "\033[") {
		t.Fatalf("expected plain transcript to avoid ANSI codes, got %q", got)
	}
	if !strings.Contains(got, "RUNNING   Shell command") {
		t.Fatalf("expected explicit running status, got %q", got)
	}
	if !strings.Contains(got, "$ echo hello") {
		t.Fatalf("expected command line, got %q", got)
	}
	if !strings.Contains(got, "| hello\n| world\n") {
		t.Fatalf("expected streamed output with gutter, got %q", got)
	}
	if !strings.Contains(got, "DONE      exit 0 in 1.5s") {
		t.Fatalf("expected completion footer, got %q", got)
	}
}

func TestTranscriptRendererRichAddsStyling(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	renderer := NewTranscriptRenderer(&out, "rich")

	renderer.Observe(tools.Event{
		Type:       tools.EventShellCommandStarted,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Command:    "echo hello",
	})
	renderer.Observe(tools.Event{
		Type:         tools.EventShellApproval,
		ToolName:     "shell_execute",
		ToolCallID:   "call-1",
		ApprovalMode: "user_confirm",
	})
	renderer.Observe(tools.Event{
		Type:       tools.EventShellOutput,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Output:     "hello\n",
	})

	got := out.String()
	if !strings.Contains(got, "\033[") {
		t.Fatalf("expected rich transcript to include ANSI codes, got %q", got)
	}
	if !strings.Contains(got, "RUNNING") {
		t.Fatalf("expected rich shell status line, got %q", got)
	}
	if !strings.Contains(got, "APPROVED") {
		t.Fatalf("expected approval metadata line, got %q", got)
	}
	if !strings.Contains(got, "│ ") {
		t.Fatalf("expected rich renderer to use unicode gutter styling, got %q", got)
	}
}

func TestTranscriptRendererFormatsLogsSeparately(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	renderer := NewTranscriptRenderer(&out, "plain")

	if _, err := renderer.Write([]byte("time=2026-04-20T14:28:03Z level=WARN msg=\"network retry scheduled\" iteration=5\n")); err != nil {
		t.Fatalf("Write returned unexpected error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "time=") {
		t.Fatalf("expected log formatter to strip time field, got %q", got)
	}
	if !strings.Contains(got, "LOG       level=WARN msg=\"network retry scheduled\" iteration=5") {
		t.Fatalf("expected log lane prefix, got %q", got)
	}
}

func TestTranscriptRendererSuppressesLogsCoveredByStructuredEvents(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	renderer := NewTranscriptRenderer(&out, "plain")
	if _, err := renderer.Write([]byte("time=2026-04-20T14:28:03Z level=INFO msg=\"executing shell command\" iteration=5\n")); err != nil {
		t.Fatalf("Write returned unexpected error: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected structured-event duplicate to be quiet, got %q", got)
	}
}

func TestTranscriptRendererShowsMemoryInjection(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	renderer := NewTranscriptRenderer(&out, "plain")

	renderer.Observe(tools.Event{
		Type:   tools.EventMemoryInjected,
		Output: "execution memory injected: guidance.md: 120 chars; targets.md: 48 chars",
	})

	got := out.String()
	if !strings.Contains(got, "DONE      Load execution context | execution memory injected: guidance.md: 120 chars; targets.md: 48 chars") {
		t.Fatalf("expected memory injection event to be visible, got %q", got)
	}
}

func TestTranscriptRendererShowsVerificationLifecycle(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	renderer := NewTranscriptRenderer(&out, "plain")
	renderer.Observe(tools.Event{
		Type: tools.EventVerificationActivity,
		Verification: tools.VerificationActivity{
			Phase: tools.VerificationPhaseChecking,
		},
	})
	renderer.Observe(tools.Event{
		Type: tools.EventVerificationActivity,
		Verification: tools.VerificationActivity{
			Phase:  tools.VerificationPhaseCompleted,
			Status: "satisfied",
			Reason: "Evidence matches the requested read-only inspection",
			Final:  true,
		},
	})

	got := out.String()
	for _, want := range []string{
		"RUNNING   Verify completion",
		"VERIFIED  Evidence matches the requested read-only inspection",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected verification transcript to contain %q, got %q", want, got)
		}
	}
}

func TestTranscriptRendererShowsProtectedActionStopped(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	renderer := NewTranscriptRenderer(&out, "plain")
	renderer.Observe(tools.Event{Type: tools.EventApprovalRequired, BlockedWorkID: "bw_123"})
	if got := out.String(); !strings.Contains(got, "STOPPED   Protected action not executed") {
		t.Fatalf("expected blocked action status, got %q", got)
	}
}
