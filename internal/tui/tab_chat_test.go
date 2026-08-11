package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

type fakeLiveChatSession struct {
	selection core.RoutingSelection
	result    agent.ChatTurnResult
	err       error
	prompt    string
	closed    string
}

func (f *fakeLiveChatSession) Selection() core.RoutingSelection { return f.selection }

func (f *fakeLiveChatSession) Turn(_ context.Context, prompt string) (agent.ChatTurnResult, error) {
	f.prompt = prompt
	return f.result, f.err
}

func (f *fakeLiveChatSession) Close(_ context.Context, reason string) { f.closed = reason }

func TestLiveChatCommandsReuseInjectedRuntime(t *testing.T) {
	t.Parallel()

	session := &fakeLiveChatSession{
		selection: core.RoutingSelection{Requested: core.NewModelRef("codex", "gpt-test")},
		result: agent.ChatTurnResult{
			Output:    "done",
			TaskState: state.TaskStateCompleted,
		},
	}
	svc := &Service{}
	svc.SetChatStarter(func(context.Context, tools.EventObserver) (LiveChatSession, error) {
		return session, nil
	})

	ready := startLiveChatCmd(svc, channelEventObserver{ch: make(chan tools.Event, 1)})().(chatSessionReadyMsg)
	if ready.err != nil || ready.session != session {
		t.Fatalf("unexpected session start: %#v", ready)
	}
	done := runChatTurnCmd(context.Background(), ready.session, "check service")().(chatTurnDoneMsg)
	if done.err != nil || done.result.Output != "done" || session.prompt != "check service" {
		t.Fatalf("unexpected in-process turn: %#v prompt=%q", done, session.prompt)
	}
}

func TestLiveChatClassifiesStartupFailures(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unsupported provider \"other\"": "Provider unavailable",
		"missing openai API key":         "Missing or invalid credentials",
		"connection refused":             "Provider appears offline",
	}
	for input, want := range cases {
		if got := classifyChatStartError(errors.New(input)); !strings.Contains(got, want) {
			t.Fatalf("classifyChatStartError(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLiveChatRuntimeEventsExposeExplicitToolStatus(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.applyRuntimeEvent(tools.Event{
		Type:       tools.EventToolCallStarted,
		ToolCallID: "call-1",
		ToolName:   "shell_execute",
		Command:    "systemctl status web",
	})
	tab.applyRuntimeEvent(tools.Event{
		Type:       tools.EventToolCallFinished,
		ToolCallID: "call-1",
		ToolName:   "shell_execute",
		Success:    true,
	})
	if len(tab.toolCalls) != 1 || tab.toolCalls[0].status != "SUCCEEDED" {
		t.Fatalf("expected one successful explicit tool state, got %#v", tab.toolCalls)
	}
	tab.toolCalls[0].expanded = true
	tab.refreshViewport()
	if view := tab.viewport.View(); !strings.Contains(view, "SUCCEEDED") {
		t.Fatalf("expected text status in tool rendering, got:\n%s", view)
	}
}

func TestLiveChatSurfacesApprovalAndInterruptionStates(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.applyTurnResult(agent.ChatTurnResult{
		TaskState: state.TaskStateBlockedWaitingUser,
	}, nil)
	if tab.status != "APPROVAL REQUIRED" || !strings.Contains(tab.statusDetail, "persisted") {
		t.Fatalf("expected explicit persisted approval state, got %q %q", tab.status, tab.statusDetail)
	}

	tab.stopping = true
	tab.applyTurnResult(agent.ChatTurnResult{TaskState: state.TaskStateIncomplete}, nil)
	if tab.status != "INTERRUPTED" {
		t.Fatalf("expected interrupted state, got %q", tab.status)
	}
}

func TestLiveChatRendererDoesNotOverflowRepresentativeWidths(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 120} {
		tab := newChatTab().(*chatTab)
		tab.session = &fakeLiveChatSession{
			selection: core.RoutingSelection{Requested: core.NewModelRef("openrouter", "anthropic/claude-sonnet-test")},
		}
		tab.messages = []liveChatMessage{{
			role:    "assistant",
			content: strings.Repeat("A responsive conversation line with concrete verification context. ", 5),
		}}
		tab.applyRuntimeEvent(tools.Event{
			Type:         tools.EventToolCallFinished,
			ToolCallID:   "call-1",
			ToolName:     "shell_execute",
			Command:      "go test ./...",
			Success:      false,
			ErrorMessage: "representative tool failure with enough text to wrap safely",
		})
		view := tab.View(width, 30)
		for lineNo, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d overflow on line %d: got %d\n%s", width, lineNo+1, got, line)
			}
		}
	}
}

func TestDashboardChatSurfaceDoesNotOverflowAtEightyColumns(t *testing.T) {
	t.Parallel()

	tab := newChatTab()
	m := model{
		width:     80,
		height:    28,
		activeTab: tabChat,
	}
	m.tabs[tabChat] = tab
	view := m.View()
	for lineNo, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 80 {
			t.Fatalf("dashboard overflow on line %d: got %d\n%s", lineNo+1, got, line)
		}
	}
}

func TestChatDetailRendersFullEvidence(t *testing.T) {
	t.Parallel()

	tab := &chatTab{
		loaded:   true,
		history:  true,
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
