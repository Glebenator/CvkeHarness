package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/memory"
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

func TestLiveChatOrdersToolActivityBeforeHighlightedResponse(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.activeTurn = 1
	tab.messages = append(tab.messages, liveChatMessage{role: "user", content: "check the service", turn: 1})
	tab.applyRuntimeEvent(tools.Event{
		Type:       tools.EventToolCallStarted,
		ToolCallID: "call-1",
		ToolName:   "shell_execute",
		Command:    "systemctl status web",
	})
	tab.applyTurnResult(agent.ChatTurnResult{
		Output:        "The web service is healthy.",
		TaskState:     state.TaskStateCompleted,
		CurationError: errors.New("memory unavailable"),
		Tools: []state.ToolOutcome{{
			ToolName: "shell_execute",
			Command:  "systemctl status web",
			Success:  true,
		}},
	}, nil)
	tab.refreshViewport()

	view := tab.viewport.View()
	toolAt := strings.Index(view, "shell_execute")
	warningAt := strings.Index(view, "Memory curation warning")
	responseAt := strings.Index(view, "RESPONSE")
	answerAt := strings.Index(view, "The web service is healthy.")
	if toolAt < 0 || warningAt < 0 || responseAt < 0 || answerAt < 0 || !(toolAt < responseAt && warningAt < responseAt && responseAt < answerAt) {
		t.Fatalf("expected all turn activity before a highlighted final response, got:\n%s", view)
	}
	if !strings.Contains(view, "systemctl status web") || !strings.Contains(view, "╭") {
		t.Fatalf("expected collapsed command preview and bordered response, got:\n%s", view)
	}
}

func TestLiveChatToolDisclosureTogglesOnlySelectedCall(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.toolCalls = []liveToolCall{
		{name: "shell_execute", command: "first", status: "SUCCEEDED"},
		{name: "shell_execute", command: "second", status: "SUCCEEDED"},
	}
	tab.toolCursor = 1
	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, nil)

	if tab.toolCalls[0].expanded || !tab.toolCalls[1].expanded {
		t.Fatalf("expected only the selected tool disclosure to open, got %#v", tab.toolCalls)
	}
}

func TestLiveChatAlwaysNamesOffscreenSelectedToolAndSpaceAction(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.toolCalls = []liveToolCall{
		{name: "first_tool", status: "SUCCEEDED"},
		{name: "dangerous_tool", status: "SUCCEEDED"},
	}
	tab.toolCursor = 1
	tab.resize(80, 16)
	tab.viewport.SetYOffset(0)

	view := tab.View(80, 16)
	for _, want := range []string{"TOOL 2/2", "dangerous_tool", "Space: open"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected persistent selection strip to contain %q, got:\n%s", want, view)
		}
	}

	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, nil)
	view = tab.View(80, 16)
	for _, want := range []string{"Space: close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected selection strip after disclosure to contain %q, got:\n%s", want, view)
		}
	}
}

func TestLiveChatSelectedToolRowUsesCompactPositionMarker(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.toolCalls = []liveToolCall{
		{name: "first", status: "SUCCEEDED"},
		{name: "shell_execute", status: "RUNNING"},
	}
	tab.toolCursor = 1
	tab.resize(80, 20)

	view := tab.viewport.View()
	if !strings.Contains(view, "▸ 2/2") {
		t.Fatalf("expected selected row to have a compact position marker, got:\n%s", view)
	}
	if strings.Contains(view, "SELECTED") {
		t.Fatalf("expected selected row not to spend width on a SELECTED label, got:\n%s", view)
	}
}

func TestLiveChatSelectingOffscreenToolFocusesItsTranscriptArea(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	for i := 0; i < 12; i++ {
		tab.toolCalls = append(tab.toolCalls, liveToolCall{
			name:   fmt.Sprintf("tool_%02d", i+1),
			status: "SUCCEEDED",
		})
	}
	tab.toolCursor = 7
	tab.resize(80, 16)
	tab.viewport.SetYOffset(0)

	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeyDown}, nil)
	selectedLine := tab.toolLineStarts[tab.toolCursor]
	if selectedLine < tab.viewport.YOffset || selectedLine >= tab.viewport.YOffset+tab.viewport.Height {
		t.Fatalf("expected offscreen selection line %d in focused viewport [%d,%d)", selectedLine, tab.viewport.YOffset, tab.viewport.YOffset+tab.viewport.Height)
	}
	if tab.viewport.YOffset >= selectedLine {
		t.Fatalf("expected context above selected line %d, got viewport offset %d", selectedLine, tab.viewport.YOffset)
	}
	if tab.viewport.YOffset+tab.viewport.Height <= selectedLine+1 {
		t.Fatalf("expected context below selected line %d, got viewport [%d,%d)", selectedLine, tab.viewport.YOffset, tab.viewport.YOffset+tab.viewport.Height)
	}
	if view := tab.viewport.View(); !strings.Contains(view, "tool_09") || !strings.Contains(view, "▸ 9/12") {
		t.Fatalf("expected focused viewport to draw attention to selected tool, got:\n%s", view)
	}
}

func TestLiveChatFocusFramesExpandedToolBlockWithContextOnBothSides(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	for i := 0; i < 8; i++ {
		tab.toolCalls = append(tab.toolCalls, liveToolCall{
			name:     fmt.Sprintf("tool_%02d", i+1),
			status:   "SUCCEEDED",
			expanded: i == 3,
			command:  "printf hello",
			output:   "hello",
		})
	}
	tab.toolCursor = 2
	tab.resize(100, 24)
	tab.viewport.SetYOffset(0)

	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeyDown}, nil)
	start := tab.toolLineStarts[tab.toolCursor]
	end := tab.toolLineEnds[tab.toolCursor]
	visibleEnd := tab.viewport.YOffset + tab.viewport.Height - 1
	if tab.viewport.YOffset >= start {
		t.Fatalf("expected transcript context above expanded block [%d,%d], got offset %d", start, end, tab.viewport.YOffset)
	}
	if visibleEnd <= end {
		t.Fatalf("expected transcript context below expanded block [%d,%d], got visible end %d", start, end, visibleEnd)
	}
}

func TestLiveChatArrowKeysSelectToolsAndScrollAtBoundaries(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.toolCalls = []liveToolCall{
		{name: "first", status: "SUCCEEDED"},
		{name: "second", status: "SUCCEEDED"},
		{name: "third", status: "SUCCEEDED"},
	}
	tab.toolCursor = 2
	tab.resize(80, 16)
	tab.viewport.GotoBottom()

	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeyUp}, nil)
	if tab.toolCursor != 1 {
		t.Fatalf("expected Up to select the previous tool, got %d", tab.toolCursor)
	}
	selectedLine := tab.toolLineStarts[tab.toolCursor]
	if selectedLine < tab.viewport.YOffset || selectedLine >= tab.viewport.YOffset+tab.viewport.Height {
		t.Fatalf("expected selected tool line %d to remain visible in viewport [%d,%d)", selectedLine, tab.viewport.YOffset, tab.viewport.YOffset+tab.viewport.Height)
	}

	tab.toolCursor = 0
	tab.refreshViewport()
	tab.viewport.SetYOffset(2)
	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeyUp}, nil)
	if tab.toolCursor != 0 || tab.viewport.YOffset != 1 {
		t.Fatalf("expected Up at first tool to scroll upward, cursor=%d offset=%d", tab.toolCursor, tab.viewport.YOffset)
	}

	tab.toolCursor = len(tab.toolCalls) - 1
	tab.refreshViewport()
	tab.viewport.SetYOffset(0)
	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeyDown}, nil)
	if tab.toolCursor != len(tab.toolCalls)-1 || tab.viewport.YOffset != 1 {
		t.Fatalf("expected Down at last tool to scroll downward, cursor=%d offset=%d", tab.toolCursor, tab.viewport.YOffset)
	}
}

func TestLiveChatSpaceReachesFocusedComposer(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.toolCalls = []liveToolCall{{
		name: "shell_execute", status: "SUCCEEDED",
	}}
	tab.composerFocused = true
	tab.composer.Focus()
	tab.composer.SetValue("hello")
	tab.composer.CursorEnd()

	_, _ = tab.updateLive(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, nil)

	if got := tab.composer.Value(); got != "hello " {
		t.Fatalf("expected Space to remain normal composer input, got %q", got)
	}
	if tab.toolCalls[0].expanded {
		t.Fatal("expected composer Space not to toggle tool disclosure")
	}
}

func TestLiveChatTurnCompletionDrainsAndRendersRawToolOutput(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.activeTurn = 1
	tab.running = true
	tab.eventWaitStop = make(chan struct{})
	tab.eventCh <- tools.Event{
		Type:       tools.EventToolCallStarted,
		ToolCallID: "call-raw",
		ToolName:   "shell_execute",
		Command:    "printf hello",
	}
	tab.eventCh <- tools.Event{
		Type:       tools.EventShellOutput,
		ToolCallID: "call-raw",
		ToolName:   "shell_execute",
		Output:     "partial",
	}
	tab.eventCh <- tools.Event{
		Type:       tools.EventToolCallFinished,
		ToolCallID: "call-raw",
		ToolName:   "shell_execute",
		Success:    true,
		Output:     "\x1b[32mhello from stdout\x1b[0m\nhello from stderr\n",
	}

	updated, _ := tab.Update(chatTurnDoneMsg{
		result: agent.ChatTurnResult{TaskState: state.TaskStateCompleted},
	}, nil, 100, 30)
	tab = updated.(*chatTab)
	if len(tab.toolCalls) != 1 {
		t.Fatalf("expected queued events to reconcile into one tool call, got %#v", tab.toolCalls)
	}
	tab.toolCalls[0].expanded = true
	tab.refreshViewport()
	view := tab.viewport.View()
	for _, want := range []string{"RAW OUTPUT", "stdout + stderr", "hello from stdout", "hello from stderr"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected expanded tool output to contain %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("expected terminal control sequences to be sanitized, got:\n%q", view)
	}
}

func TestLiveChatIgnoresMemoryEventsAsToolCalls(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.applyRuntimeEvent(tools.Event{
		Type:   tools.EventMemoryInjected,
		Output: "chat memory injected",
	})
	if len(tab.toolCalls) != 0 {
		t.Fatalf("expected memory event to leave tool rows empty, got %#v", tab.toolCalls)
	}
}

func TestLiveChatTurnResultReconcilesDroppedToolFinishEvent(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.applyRuntimeEvent(tools.Event{
		Type:       tools.EventToolCallStarted,
		ToolCallID: "call-1",
		ToolName:   "shell_execute",
		Command:    "systemctl status web",
	})
	tab.applyTurnResult(agent.ChatTurnResult{
		TaskState: state.TaskStateCompleted,
		Tools: []state.ToolOutcome{{
			ToolName:   "shell_execute",
			Command:    "systemctl status web",
			Success:    true,
			DurationMs: 125,
		}},
	}, nil)

	if len(tab.toolCalls) != 1 {
		t.Fatalf("expected final outcome to reconcile the live row, got %#v", tab.toolCalls)
	}
	if got := tab.toolCalls[0].status; got != "SUCCEEDED" {
		t.Fatalf("expected dropped finish event to be repaired from the turn result, got %q", got)
	}
}

func TestLiveChatTurnResultReconcilesNonShellToolWithoutLiveArguments(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.applyRuntimeEvent(tools.Event{
		Type:       tools.EventToolCallStarted,
		ToolCallID: "call-2",
		ToolName:   "memory_record_finding",
	})
	tab.applyTurnResult(agent.ChatTurnResult{
		TaskState: state.TaskStateCompleted,
		Tools: []state.ToolOutcome{{
			ToolName:  "memory_record_finding",
			Arguments: `{"finding":"service uses port 8080"}`,
			Success:   true,
		}},
		Observed: []memory.ObservedToolCall{{
			ToolName: "memory_record_finding",
			Result:   `{"recorded":true}`,
			Success:  true,
		}},
	}, nil)

	if len(tab.toolCalls) != 1 || tab.toolCalls[0].status != "SUCCEEDED" {
		t.Fatalf("expected final non-shell outcome to reconcile one live row, got %#v", tab.toolCalls)
	}
	if tab.toolCalls[0].command == "" {
		t.Fatal("expected final outcome arguments to fill the live tool detail")
	}
	if tab.toolCalls[0].output != `{"recorded":true}` {
		t.Fatalf("expected final observed result to preserve raw tool output, got %q", tab.toolCalls[0].output)
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

func TestLiveChatReflowsTranscriptAcrossContextPaneBreakpoint(t *testing.T) {
	t.Parallel()

	tab := newChatTab().(*chatTab)
	tab.messages = []liveChatMessage{{
		role: "assistant",
		content: "Docker is installed. I cannot verify whether it is running right now because " +
			"this turn has no command-execution access. final-tail-marker",
	}}

	for _, width := range []int{160, 119, 120, 160} {
		view := tab.View(width, 30)
		if !strings.Contains(view, "final-tail-marker") {
			t.Fatalf("width %d clipped response content while reflowing:\n%s", width, view)
		}
		for lineNo, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d overflow on line %d: got %d\n%s", width, lineNo+1, got, line)
			}
		}
		split, mainWidth, _ := liveChatColumns(width)
		wantViewportWidth := maxInt(width-4, 20)
		if split {
			wantViewportWidth = maxInt(mainWidth-2, 20)
		}
		if tab.viewport.Width != wantViewportWidth {
			t.Fatalf("width %d left viewport at %d, want %d", width, tab.viewport.Width, wantViewportWidth)
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
