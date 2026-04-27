package tui

import (
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestChatDetailRendersFullEvidence(t *testing.T) {
	t.Parallel()

	tab := &chatTab{
		loaded:   true,
		expanded: true,
		detail: state.ChatSessionDetail{
			Session: state.ChatSessionSummary{
				ID:          20,
				Provider:    "codex",
				PinnedModel: "gpt-5.3-codex",
				TurnCount:   1,
			},
			Turns: []state.ChatTurn{{
				ID:                         7,
				TurnIndex:                  0,
				UserInput:                  "run a speed test",
				TaskClass:                  core.TaskClassGeneral,
				RequestedModel:             "gpt-5.3-codex",
				ActualModel:                "gpt-5.3-codex",
				FinalOutput:                "Speed test results:\nPing: 20.352 ms\nDownload: needle_result_visible",
				ErrorMessage:               "verification failed with needle_error_visible",
				VerificationStatus:         "incomplete",
				VerificationReason:         "The assistant omitted the tool output.",
				VerificationMissingActions: "Show the exact command and output.",
			}},
			Messages: []state.ChatMessage{{
				TurnID:        7,
				Role:          "assistant",
				ToolName:      "shell",
				ToolArguments: `{"command":"go test ./..."}`,
			}},
			ToolsByTurnID: map[int64][]state.ToolOutcome{
				7: {{
					Phase:      core.PhaseChat,
					Provider:   "codex",
					Model:      "gpt-5.3-codex",
					ToolName:   "shell",
					Toolset:    "shell_execute",
					Arguments:  `{"command":"go test ./..."}`,
					Command:    "go test ./...",
					Success:    true,
					DurationMs: 523,
				}},
			},
		},
	}

	view := tab.View(100, 40)
	for _, want := range []string{
		"needle_result_visible",
		"needle_error_visible",
		"Verification:",
		"Tools: 1 persisted outcome",
		"go test ./...",
		"Transcript tool evidence:",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected chat detail to contain %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "more lines") {
		t.Fatalf("chat detail should render evidence directly instead of collapsed line counts:\n%s", view)
	}
}
