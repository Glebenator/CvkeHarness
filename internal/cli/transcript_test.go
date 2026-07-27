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
	if !strings.Contains(got, "--- shell: command ---") {
		t.Fatalf("expected shell header, got %q", got)
	}
	if !strings.Contains(got, "$ echo hello") {
		t.Fatalf("expected command line, got %q", got)
	}
	if !strings.Contains(got, "| hello\n| world\n") {
		t.Fatalf("expected streamed output with gutter, got %q", got)
	}
	if !strings.Contains(got, "[done] exit 0 in 1.5s") {
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
	if !strings.Contains(got, "shell") {
		t.Fatalf("expected rich shell section line, got %q", got)
	}
	if !strings.Contains(got, "approval") {
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

	if _, err := renderer.Write([]byte("time=2026-04-20T14:28:03Z level=INFO msg=\"executing shell command\" iteration=5\n")); err != nil {
		t.Fatalf("Write returned unexpected error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "time=") {
		t.Fatalf("expected log formatter to strip time field, got %q", got)
	}
	if !strings.Contains(got, "[log] level=INFO msg=\"executing shell command\" iteration=5") {
		t.Fatalf("expected log lane prefix, got %q", got)
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
	if !strings.Contains(got, "[memory] execution memory injected: guidance.md: 120 chars; targets.md: 48 chars") {
		t.Fatalf("expected memory injection event to be visible, got %q", got)
	}
}
