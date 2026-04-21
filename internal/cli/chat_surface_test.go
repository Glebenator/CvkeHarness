package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/tools"
)

func TestChatSurfaceStreamsShellOutputInPlainMode(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	surface := NewChatSurface(&out)

	surface.Observe(tools.Event{
		Type:       tools.EventShellCommandStarted,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Command:    "echo hello",
	})
	surface.Observe(tools.Event{
		Type:       tools.EventShellOutput,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Output:     "hello\nworld",
	})
	surface.Observe(tools.Event{
		Type:          tools.EventShellCommandFinished,
		ToolName:      "shell_execute",
		ToolCallID:    "call-1",
		Success:       true,
		ExitCode:      0,
		ExitCodeKnown: true,
		Duration:      1500 * time.Millisecond,
	})

	got := out.String()
	if !strings.Contains(got, "Shell:\n  $ echo hello\n") {
		t.Fatalf("expected shell header and command, got %q", got)
	}
	if !strings.Contains(got, "  | hello\n  | world\n") {
		t.Fatalf("expected streamed shell output lines, got %q", got)
	}
	if !strings.Contains(got, "  done: exit 0 in 1.5s\n") {
		t.Fatalf("expected shell completion line, got %q", got)
	}
}

func TestChatSurfaceStreamsShellOutputInRichMode(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	surface := &ChatSurface{
		out:    &out,
		width:  96,
		rich:   true,
		shells: make(map[string]*shellRenderState),
	}

	surface.Observe(tools.Event{
		Type:       tools.EventShellCommandStarted,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Command:    "echo hello",
	})
	surface.Observe(tools.Event{
		Type:         tools.EventShellApproval,
		ToolName:     "shell_execute",
		ToolCallID:   "call-1",
		ApprovalMode: "user_confirm",
	})
	surface.Observe(tools.Event{
		Type:       tools.EventShellOutput,
		ToolName:   "shell_execute",
		ToolCallID: "call-1",
		Output:     "hello\n",
	})

	got := out.String()
	if !strings.Contains(got, "\033[") {
		t.Fatalf("expected rich chat surface to include ANSI codes, got %q", got)
	}
	if !strings.Contains(got, "• Shell") {
		t.Fatalf("expected rich chat surface shell title, got %q", got)
	}
	if !strings.Contains(got, "$") {
		t.Fatalf("expected rich chat surface shell command line, got %q", got)
	}
	if !strings.Contains(got, "approval:") {
		t.Fatalf("expected rich chat surface approval metadata, got %q", got)
	}
	if !strings.Contains(got, "│") {
		t.Fatalf("expected rich chat surface gutter styling, got %q", got)
	}
}
