package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	termansi "github.com/charmbracelet/x/ansi"
	"github.com/coolcake/cvkeharness/state"
	"github.com/muesli/termenv"
)

const representativeMarkdown = `# Deployment result

The service is **healthy** and the next command is ` + "`safe`" + `.

- First check
- Second check
  - Nested evidence
- [x] Verification recorded

> Keep the previous release available.

| Check | State |
| --- | --- |
| API | passing |

` + "```go" + `
fmt.Println("terminal-tail-marker-with-a-deliberately-long-unbroken-value-0123456789")
` + "```" + `

[Open the runbook](https://example.com/runbook).`

func TestMarkdownRendererSupportsRepresentativeAssistantOutput(t *testing.T) {
	t.Parallel()

	rendered := renderMarkdownWith(representativeMarkdown, 44, func(content string, width int) (string, error) {
		return renderGlamourMarkdownWithProfile(content, width, termenv.TrueColor)
	})
	plain := termansi.Strip(rendered)

	for _, want := range []string{
		"Deployment result",
		"healthy",
		"safe",
		"• First check",
		"• Nested evidence",
		"[✓] Verification recorded",
		"│ Keep the previous release available.",
		"Check",
		"State",
		"fmt.Println",
		"terminal-tail-marker",
		"Open the runbook",
		"https://example.com/runbook",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected rendered Markdown to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, raw := range []string{"**healthy**", "`safe`", "```go", "[Open the runbook]("} {
		if strings.Contains(plain, raw) {
			t.Fatalf("expected Markdown syntax %q to be rendered, got:\n%s", raw, rendered)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected true-color rendering to contain ANSI styling, got:\n%s", rendered)
	}
	assertRenderedWidth(t, rendered, 44)
}

func TestMarkdownHeadingsDoNotExposeSourceMarkers(t *testing.T) {
	t.Parallel()

	input := "## Health check: mostly healthy\n\n### One thing to note"
	rendered := renderMarkdownWith(input, 60, func(content string, width int) (string, error) {
		return renderGlamourMarkdownWithProfile(content, width, termenv.Ascii)
	})
	plain := termansi.Strip(rendered)
	for _, want := range []string{"Health check: mostly healthy", "One thing to note"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected rendered heading %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(plain, "##") {
		t.Fatalf("expected heading markers to be consumed, got:\n%s", rendered)
	}
}

func TestLiveResponseDoesNotPaintAConflictingOuterBackground(t *testing.T) {
	originalProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(originalProfile) })

	tab := newChatTab().(*chatTab)
	tab.messages = []liveChatMessage{{role: "assistant", content: "A plain response with **clean emphasis**."}}
	view := tab.View(80, 30)
	if strings.Contains(view, "48;2;41;37;36") {
		t.Fatalf("response container must not paint a background behind nested Markdown styles:\n%s", view)
	}
}

func TestMarkdownRendererStripsInjectedTerminalControls(t *testing.T) {
	t.Parallel()

	input := "safe\x1b]52;c;dGVybWluYWwtY2xpcGJvYXJk\a text"
	rendered := renderMarkdownWith(input, 40, func(content string, _ int) (string, error) {
		if strings.ContainsAny(content, "\x1b\a") || strings.Contains(content, "dGVybWluYWwtY2xpcGJvYXJk") {
			t.Fatalf("renderer received terminal control content: %q", content)
		}
		return content, nil
	})
	if rendered != "safe text" {
		t.Fatalf("expected sanitized text, got %q", rendered)
	}
}

func TestMarkdownRendererFallsBackToWidthSafePlainText(t *testing.T) {
	t.Parallel()

	input := "## Still readable\n\n- an-extremely-long-unbroken-fallback-value-0123456789"
	rendered := renderMarkdownWith(input, 18, func(string, int) (string, error) {
		return "", errors.New("renderer unavailable")
	})
	for _, want := range []string{"## Still readable", "- an-extremely", "0123456789"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected fallback to preserve %q, got:\n%s", want, rendered)
		}
	}
	assertRenderedWidth(t, rendered, 18)
}

func TestMarkdownRendererPreservesLeadingIndentedCode(t *testing.T) {
	t.Parallel()

	rendered := renderMarkdownWith("    echo preserved-indent", 40, func(content string, width int) (string, error) {
		return renderGlamourMarkdownWithProfile(content, width, termenv.Ascii)
	})
	if !strings.Contains(rendered, "echo preserved-indent") {
		t.Fatalf("expected leading indented code to remain renderable, got:\n%s", rendered)
	}
}

func TestLiveAndPersistedAssistantResponsesRenderMarkdown(t *testing.T) {
	t.Parallel()

	live := newChatTab().(*chatTab)
	live.messages = []liveChatMessage{{role: "assistant", content: representativeMarkdown}}
	liveView := live.View(80, 80)
	for _, want := range []string{"RESPONSE", "• First check", "[✓] Verification recorded", "terminal-tail-marker"} {
		if !strings.Contains(liveView, want) {
			t.Fatalf("expected live Markdown response to contain %q, got:\n%s", want, liveView)
		}
	}
	assertRenderedWidth(t, liveView, 80)

	history := newChatTab().(*chatTab)
	history.history = true
	history.expanded = true
	history.loaded = true
	history.detail = state.ChatSessionDetail{
		Session: state.ChatSessionSummary{ID: 7, Provider: "codex", PinnedModel: "gpt-test", TurnCount: 1},
		Turns: []state.ChatTurn{{
			ID:          11,
			TurnIndex:   0,
			UserInput:   "report the deployment",
			FinalOutput: representativeMarkdown,
			Success:     true,
		}},
	}
	historyView := history.View(80, 80)
	for _, want := range []string{"AI:", "• First check", "[✓] Verification recorded", "terminal-tail-marker"} {
		if !strings.Contains(historyView, want) {
			t.Fatalf("expected saved Markdown response to contain %q, got:\n%s", want, historyView)
		}
	}
	assertRenderedWidth(t, historyView, 80)
}

func TestMarkdownResponseDoesNotOverflowRepresentativeLayouts(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 120, 160} {
		tab := newChatTab().(*chatTab)
		tab.messages = []liveChatMessage{{role: "assistant", content: representativeMarkdown}}
		view := tab.View(width, 100)
		if !strings.Contains(view, "terminal-tail-marker") {
			t.Fatalf("width %d clipped the Markdown response:\n%s", width, view)
		}
		assertRenderedWidth(t, view, width)
	}
}

func assertRenderedWidth(t *testing.T, rendered string, width int) {
	t.Helper()
	for lineNo, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d overflow: got %d cells, limit %d\n%s", lineNo+1, got, width, line)
		}
	}
}
