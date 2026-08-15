package cli

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRunSurfacePrintsQuietBriefBlockedRun(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	surface := NewRunSurfaceWithOptions(&out, RunSurfaceOptions{Mode: "plain", Width: 100})
	surface.PrintRunHeader(RunHeader{
		Task:   "Restart nginx on production-web-2 after checking its config",
		Target: "production-web-2 | remote via ssh",
		Model:  "openrouter/qwen3-coder",
	})
	surface.PrintApprovalRequired(ApprovalNotice{
		Action:  "sudo systemctl restart nginx",
		Reason:  "Service mutation requires explicit operator approval.",
		Scope:   "production-web-2 | exact command | approve once",
		Effect:  "Approve once + retry. No action has run.",
		Approve: "cvkeharness commands approve-work bw_123",
	})
	summary := RunSummary{
		Duration:           3200 * time.Millisecond,
		ExitReason:         "Approval required",
		ExitCode:           1,
		Target:             "production-web-2 | remote via ssh",
		ModelsUsed:         []string{"openrouter/qwen3-coder"},
		ToolCalls:          2,
		SuccessfulTools:    1,
		BlockedTools:       1,
		VerificationStatus: "stopped safely",
	}
	surface.PrintRunSummary(summary)
	surface.PrintAnswer("nginx configuration is valid. The service was not restarted because the protected action requires approval.", false)
	surface.PrintRunReceipt(summary)

	got := out.String()
	for _, want := range []string{
		"$ cvkeharness run",
		"Task      Restart nginx on production-web-2",
		"Target    production-web-2 | remote via ssh",
		"APPROVAL REQUIRED",
		"Action    sudo systemctl restart nginx",
		"Effect    Approve once + retry. No action has run.",
		"Approve   cvkeharness commands approve-work bw_123",
		"BLOCKED   exit 1 | waiting for operator | 3.2s",
		"ANSWER    nginx configuration is valid.",
		"target production-web-2 | remote via ssh",
		"tools 1/2 done (1 blocked)",
		"verify stopped safely",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected quiet brief to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "\033[") {
		t.Fatalf("plain output must not contain ANSI escapes: %q", got)
	}
}

func TestRunSurfaceRichUsesRestrainedANSI(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	surface := NewRunSurfaceWithOptions(&out, RunSurfaceOptions{Mode: "rich", Width: 100})
	surface.PrintRunHeader(RunHeader{Task: "Inspect disk pressure", Target: "web-2 | remote via ssh", Model: "openrouter/model-a"})
	surface.PrintRunSummary(RunSummary{Duration: 1500 * time.Millisecond, ExitReason: "Completed", ExitCode: 0})

	got := out.String()
	if !strings.Contains(got, "\033[") {
		t.Fatalf("rich output should use ANSI styling: %q", got)
	}
	plain := ansiPattern.ReplaceAllString(got, "")
	for _, want := range []string{"Task      Inspect disk pressure", "COMPLETED exit 0"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("styled output lost explicit status %q: %q", want, plain)
		}
	}
}

func TestRunSurfaceRichRendersMarkdownAnswer(t *testing.T) {
	t.Parallel()

	const answer = `You have **142 GiB free** on the data volume.

### Largest opportunities

| Area | Space | Notes |
| --- | ---: | --- |
| LM Studio models | **47 GiB** | Best target |
| Build artifacts | ` + "`7.4 GiB`" + ` | Regenerates |

- Review models first
- Keep the active version`

	var out strings.Builder
	surface := NewRunSurfaceWithOptions(&out, RunSurfaceOptions{Mode: "rich", Width: 100})
	surface.PrintAnswer(answer, false)

	rendered := out.String()
	plain := ansiPattern.ReplaceAllString(rendered, "")
	for _, want := range []string{
		"ANSWER    You have 142 GiB free on the data volume.",
		"Largest opportunities",
		"LM Studio models",
		"47 GiB",
		"Build artifacts",
		"7.4 GiB",
		"• Review models first",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected rendered Markdown to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, raw := range []string{"**142 GiB free**", "### Largest", "| ---", "`7.4 GiB`"} {
		if strings.Contains(plain, raw) {
			t.Fatalf("expected Markdown syntax %q to be consumed, got:\n%s", raw, rendered)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected rich Markdown to contain ANSI styling, got:\n%s", rendered)
	}
	for _, line := range strings.Split(plain, "\n") {
		if line != strings.TrimRightFunc(line, func(r rune) bool { return r == ' ' || r == '\t' }) {
			t.Fatalf("rendered Markdown has noisy trailing whitespace: %q", line)
		}
	}
	assertRunSurfaceWidth(t, rendered, 100)
}

func TestRunSurfacePlainKeepsMarkdownAndANSIOut(t *testing.T) {
	t.Parallel()

	const answer = "### Result\n\nThe check is **healthy**.\x1b]52;c;dGVybWluYWwtY2xpcGJvYXJk\a"
	var out strings.Builder
	NewRunSurfaceWithOptions(&out, RunSurfaceOptions{Mode: "plain", Width: 80}).PrintAnswer(answer, false)

	got := out.String()
	for _, want := range []string{"ANSWER    ### Result", "**healthy**"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain output must preserve Markdown text %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("plain output must remain ANSI-free: %q", got)
	}
	if strings.ContainsAny(got, "\x1b\a") || strings.Contains(got, "dGVybWluYWwtY2xpcGJvYXJk") {
		t.Fatalf("plain output must strip injected terminal controls: %q", got)
	}
}

func TestRunSurfaceMarkdownWrapsAtSupportedWidths(t *testing.T) {
	t.Parallel()

	const answer = `### Largest opportunities

| Area | Space | Notes |
| --- | ---: | --- |
| LM Studio models | **47 GiB** | Several large overlapping model variants |
| Docker data | **15 GiB** | Downloaded models plus the virtual machine disk |
| Xcode build artifacts | **7.4 GiB** | Safe build output that regenerates |

- Review before deleting anything
- Prefer application-managed cleanup`

	for _, width := range []int{80, 100, 120} {
		width := width
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			var out strings.Builder
			NewRunSurfaceWithOptions(&out, RunSurfaceOptions{Mode: "rich", Width: width}).PrintAnswer(answer, false)

			rendered := out.String()
			plain := ansiPattern.ReplaceAllString(rendered, "")
			for _, want := range []string{"Largest opportunities", "LM Studio models", "Docker data", "Xcode build artifacts", "• Review before deleting"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("width %d lost %q:\n%s", width, want, rendered)
				}
			}
			assertRunSurfaceWidth(t, rendered, width)
		})
	}
}

func TestRunMarkdownStripsInjectedTerminalControls(t *testing.T) {
	t.Parallel()

	input := "safe\x1b]52;c;dGVybWluYWwtY2xpcGJvYXJk\a **text**"
	rendered := renderRunMarkdownWith(input, 40, func(content string, _ int) (string, error) {
		if strings.ContainsAny(content, "\x1b\a") || strings.Contains(content, "dGVybWluYWwtY2xpcGJvYXJk") {
			t.Fatalf("renderer received terminal control content: %q", content)
		}
		return content, nil
	})
	if rendered != "safe **text**" {
		t.Fatalf("expected sanitized text, got %q", rendered)
	}
}

func TestRunMarkdownFallbackIsWidthSafe(t *testing.T) {
	t.Parallel()

	rendered := renderRunMarkdownWith("## Still readable\n\n- an-extremely-long-unbroken-fallback-value-0123456789", 18, func(string, int) (string, error) {
		return "", errors.New("renderer unavailable")
	})
	for _, want := range []string{"## Still readable", "- an-extremely", "0123456789"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected fallback to preserve %q, got:\n%s", want, rendered)
		}
	}
	assertRunSurfaceWidth(t, rendered, 18)
}

func TestRunSurfaceWrapsAtSupportedWidths(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 120} {
		width := width
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			var out strings.Builder
			surface := NewRunSurfaceWithOptions(&out, RunSurfaceOptions{Mode: "rich", Width: width})
			surface.PrintRunHeader(RunHeader{
				Task:   "Restart nginx on production-web-2 after checking its configuration and report the exact outcome",
				Target: "production-web-2 | remote via ssh",
				Model:  "openrouter/very-long-execution-model-name",
			})
			surface.PrintApprovalRequired(ApprovalNotice{
				Action:  "sudo systemctl restart nginx after validating the exact service configuration",
				Reason:  "Service mutation requires explicit operator approval under the selected production safety profile.",
				Scope:   "production-web-2 | exact command | principal deploy | cwd /srv/application/current | approve once",
				Effect:  "Approve once + retry. No action has run.",
				Approve: "cvkeharness commands approve-work bw_very_long_identifier_for_width_testing",
			})
			summary := RunSummary{
				Duration:           3200 * time.Millisecond,
				ExitReason:         "Approval required",
				ExitCode:           1,
				ModelsUsed:         []string{"openrouter/very-long-execution-model-name x2"},
				ToolCalls:          3,
				SuccessfulTools:    2,
				BlockedTools:       1,
				VerificationStatus: "stopped safely after protected action",
			}
			surface.PrintRunSummary(summary)
			surface.PrintAnswer("The configuration check succeeded, but the requested service restart did not run because approval is required.", false)
			surface.PrintRunReceipt(summary)

			plain := ansiPattern.ReplaceAllString(out.String(), "")
			assertRunSurfaceWidth(t, out.String(), width)
			for _, line := range strings.Split(plain, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "·") || strings.HasPrefix(trimmed, "|") {
					t.Fatalf("width %d wrapped with orphan separator: %q", width, line)
				}
			}
		})
	}
}

func assertRunSurfaceWidth(t *testing.T, rendered string, width int) {
	t.Helper()
	for lineNo, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d overflow: got %d cells, limit %d\n%s", lineNo+1, got, width, line)
		}
	}
}

func TestRunSurfacePrintInfoUsesExplicitStatus(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	NewRunSurface(&out).PrintInfo("Interrupt", []string{"Stopping the current run and waiting for cleanup..."})
	if got := out.String(); !strings.Contains(got, "INTERRUPT\nStopping the current run") {
		t.Fatalf("unexpected info output %q", got)
	}
}

func TestRunSurfaceDoesNotMislabelTerminationAsCompletion(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	NewRunSurface(&out).PrintRunSummary(RunSummary{ExitReason: "Terminated", ExitCode: 1})
	got := out.String()
	if !strings.Contains(got, "TERMINATED") || strings.Contains(got, "COMPLETED") {
		t.Fatalf("unexpected termination close-out %q", got)
	}
}
