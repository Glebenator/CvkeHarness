package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/glebenator/cvkeharness/agent"
	"github.com/glebenator/cvkeharness/memory"
	"github.com/glebenator/cvkeharness/state"
	"github.com/glebenator/cvkeharness/tools"
)

func chatActivityViewForTest(tab *chatTab, turn int) string {
	tab.resizeActivity(78, 80)
	tab.openActivity(turn)
	tab.refreshActivity()
	return tab.activityView()
}

func TestChatTranscriptKeepsActivityWithOriginatingTurn(t *testing.T) {
	t.Parallel()
	for _, firstResponse := range []string{"FIRST RESPONSE", ""} {
		t.Run(fmt.Sprintf("response=%q", firstResponse), func(t *testing.T) {
			t.Parallel()
			tab := newChatTab().(*chatTab)
			tab.activeTurn = 1
			tab.messages = []liveChatMessage{{role: "user", content: "FIRST PROMPT", turn: 1}}
			tab.toolCalls = []liveToolCall{{name: "first_turn_tool", command: "printf first", status: "SUCCEEDED", turn: 1}}
			tab.applyTurnResult(agent.ChatTurnResult{TaskState: state.TaskStateCompleted, Output: firstResponse}, nil)
			tab.activeTurn = 2
			tab.messages = append(tab.messages, liveChatMessage{role: "user", content: "SECOND PROMPT", turn: 2})
			tab.applyTurnResult(agent.ChatTurnResult{TaskState: state.TaskStateCompleted, Output: "SECOND RESPONSE"}, nil)
			tab.viewport.Height = 100
			tab.refreshViewport()
			view := ansi.Strip(tab.viewport.View())
			promptAt, activityAt, laterPromptAt := strings.Index(view, "FIRST PROMPT"), strings.Index(view, "View 1 tool call"), strings.Index(view, "SECOND PROMPT")
			if promptAt < 0 || activityAt < 0 || laterPromptAt < 0 || !(promptAt < activityAt && activityAt < laterPromptAt) {
				t.Fatalf("first-turn activity escaped its originating prompt:\n%s", view)
			}
			if firstResponse != "" && activityAt > strings.Index(view, firstResponse) {
				t.Fatalf("expected activity before its turn's final answer:\n%s", view)
			}
			if strings.Count(view, "View 1 tool call") != 1 || strings.Contains(view, "first_turn_tool") || strings.Contains(view, "printf first") {
				t.Fatalf("expected one compact activity link, with raw tool detail outside chat:\n%s", view)
			}
		})
	}
}

func TestChatTranscriptActivityLinksUseRenderedPhysicalRows(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 2
	tab.messages = []liveChatMessage{
		{role: "user", content: "FIRST PROMPT", turn: 1},
		{role: "assistant", content: "First response\n\n" + strings.Repeat("Paragraph with enough content to occupy a complete line.\n\n", 8), turn: 1},
		{role: "user", content: "SECOND PROMPT", turn: 2},
	}
	tab.toolCalls = []liveToolCall{{name: "first", status: "SUCCEEDED", turn: 1}, {name: "second", status: "SUCCEEDED", turn: 2}}
	for _, width := range []int{44, 78} {
		tab.viewport.Width = width
		tab.viewport.Height = 200
		tab.refreshViewport()
		tab.viewport.GotoTop()
		rows := strings.Split(ansi.Strip(tab.viewport.View()), "\n")
		var actual []int
		for row, text := range rows {
			if strings.Contains(text, "View 1 tool call") {
				actual = append(actual, row)
			}
		}
		if len(actual) != 2 || len(tab.chatTurnLinks) != 2 {
			t.Fatalf("width %d: expected two rendered activity links, rows=%v mapping=%v", width, actual, tab.chatTurnLinks)
		}
		for i, physicalRow := range actual {
			if turn := tab.chatTurnLinks[physicalRow]; turn != i+1 {
				t.Fatalf("width %d: physical row %d belongs to turn %d, mapping says %d; all links=%v", width, physicalRow, i+1, turn, tab.chatTurnLinks)
			}
		}
	}
}

func TestChatDoesNotKeepOldToolSelectorAboveComposer(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 2
	tab.messages = []liveChatMessage{
		{role: "user", content: "FIRST PROMPT", turn: 1},
		{role: "assistant", content: "FIRST RESPONSE", turn: 1},
		{role: "user", content: "SECOND PROMPT", turn: 2},
		{role: "assistant", content: strings.Repeat("Later conversation line.\n\n", 20), turn: 2},
	}
	tab.toolCalls = []liveToolCall{{name: "old_tool_name", status: "SUCCEEDED", turn: 1}}
	tab.resize(80, 24)
	tab.viewport.GotoBottom()
	tab.composerFocused = true
	tab.composer.Focus()
	view := ansi.Strip(tab.View(80, 24))
	for _, unwanted := range []string{"old_tool_name", "TOOL 1/1", "Space: open", "Space: close"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("old tool selection leaked into the current chat view (%q):\n%s", unwanted, view)
		}
	}
	if !strings.Contains(view, "Message") {
		t.Fatalf("expected composer to remain present:\n%s", view)
	}
}

func TestChatArrowKeysScrollWithoutSelectingTools(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 1
	tab.messages = []liveChatMessage{{role: "user", content: strings.Repeat("Conversation line\n", 30), turn: 1}}
	tab.toolCalls = []liveToolCall{{name: "first", status: "SUCCEEDED", turn: 1}, {name: "second", status: "SUCCEEDED", turn: 1}}
	tab.resize(80, 24)
	tab.refreshActivity()
	tab.composerFocused = false
	tab.composer.Blur()
	tab.viewport.SetYOffset(5)
	selected := tab.activity.selectedTool
	for _, step := range []struct {
		key  tea.KeyType
		want int
	}{{tea.KeyDown, 6}, {tea.KeyUp, 5}} {
		tab.updateLive(tea.KeyMsg{Type: step.key}, nil)
		if tab.viewport.YOffset != step.want || tab.activity.selectedTool != selected || tab.activity.focused {
			t.Fatalf("arrow changed activity selection or failed to scroll one row: offset=%d want=%d tool=%d was=%d activityFocused=%t", tab.viewport.YOffset, step.want, tab.activity.selectedTool, selected, tab.activity.focused)
		}
	}
}

func TestChatSpaceOutsideComposerDoesNotOpenToolOutput(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 1
	tab.messages = []liveChatMessage{{role: "user", content: strings.Repeat("Conversation line\n", 20), turn: 1}}
	tab.toolCalls = []liveToolCall{{name: "shell_execute", output: "RAW OUTPUT MARKER", status: "SUCCEEDED", turn: 1}}
	tab.resize(80, 24)
	tab.composerFocused = false
	tab.composer.Blur()
	tab.viewport.SetYOffset(3)
	before := tab.viewport.View()
	tab.updateLive(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, nil)
	if tab.activity.focused || tab.activity.inspecting || tab.viewport.YOffset != 3 || tab.viewport.View() != before {
		t.Fatalf("Space changed chat or opened activity: focused=%t inspecting=%t offset=%d", tab.activity.focused, tab.activity.inspecting, tab.viewport.YOffset)
	}
}

func TestChatCompletionKeepsManualPositionAndFollowsLatestAtBottom(t *testing.T) {
	t.Parallel()
	for _, following := range []bool{false, true} {
		t.Run(fmt.Sprintf("following=%t", following), func(t *testing.T) {
			t.Parallel()
			tab := newChatTab().(*chatTab)
			tab.activeTurn = 1
			tab.running = true
			tab.messages = []liveChatMessage{{role: "user", content: strings.Repeat("Previous conversation line\n", 30), turn: 1}}
			tab.resize(80, 24)
			if following {
				tab.viewport.GotoBottom()
			} else {
				tab.viewport.SetYOffset(3)
			}
			before := tab.viewport.YOffset
			tab.Update(chatTurnDoneMsg{result: agent.ChatTurnResult{TaskState: state.TaskStateCompleted, Output: "A complete final response."}}, nil, 80, 24)
			if following {
				if !tab.viewport.AtBottom() || !strings.Contains(tab.viewport.View(), "A complete final response.") {
					t.Fatalf("expected the latest conversation to remain visible:\n%s", tab.viewport.View())
				}
			} else if tab.viewport.YOffset != before {
				t.Fatalf("completion stole manual scroll position: before=%d after=%d", before, tab.viewport.YOffset)
			}
		})
	}
}

func TestChatNextTurnClearsFollowingActivityWithoutDeletingHistory(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.session = &fakeLiveChatSession{}
	tab.activeTurn = 1
	tab.messages = []liveChatMessage{{role: "user", content: "first request", turn: 1}, {role: "assistant", content: "first response", turn: 1}}
	tab.toolCalls = []liveToolCall{{name: "previous_tool", output: "previous output", status: "SUCCEEDED", turn: 1}}
	tab.resizeActivity(78, 30)
	tab.refreshActivity()
	if !tab.activity.following || !strings.Contains(tab.activityView(), "previous_tool") {
		t.Fatalf("expected activity to initially follow the latest turn:\n%s", tab.activityView())
	}
	tab.beginTurn("second request without a tool yet")
	defer tab.cancelTurn()
	view := tab.activityView()
	if tab.activity.selectedTurn != 2 || tab.activity.selectedTool != -1 || strings.Contains(view, "previous_tool") || strings.Contains(view, "previous output") {
		t.Fatalf("new turn retained previous tools in following activity: turn=%d tool=%d\n%s", tab.activity.selectedTurn, tab.activity.selectedTool, view)
	}
	if len(tab.toolCalls) != 1 || tab.toolCalls[0].turn != 1 {
		t.Fatalf("beginning a turn discarded historical tool evidence: %#v", tab.toolCalls)
	}
	if history := chatActivityViewForTest(tab, 1); !strings.Contains(history, "previous_tool") {
		t.Fatalf("previous tool is not available when reopening its originating turn:\n%s", history)
	}
}

func TestChatRuntimeEventsDoNotStealManualTranscriptPosition(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 1
	tab.running = true
	tab.messages = []liveChatMessage{{role: "user", content: strings.Repeat("Previous conversation line\n", 30), turn: 1}}
	tab.resize(80, 24)
	tab.viewport.SetYOffset(4)
	tab.Update(chatRuntimeEventMsg{event: tools.Event{Type: tools.EventToolCallStarted, ToolCallID: "current", ToolName: "shell_execute"}}, nil, 80, 24)
	if tab.viewport.YOffset != 4 {
		t.Fatalf("runtime activity stole manual conversation position: offset=%d", tab.viewport.YOffset)
	}
}

func TestChatCompletionPreservesOpenToolOutputAndItsScroll(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 1
	tab.running = true
	tab.messages = []liveChatMessage{{role: "user", content: "inspect the command output", turn: 1}}
	output := strings.Repeat("A line of tool output\n", 50)
	tab.toolCalls = []liveToolCall{{id: "running-call", name: "shell_execute", command: "printf rows", output: output, status: "RUNNING", turn: 1}}
	tab.resize(80, 24)
	tab.openActivity(1)
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	tab.activity.viewport.SetYOffset(8)
	before := tab.activity.viewport.YOffset
	tab.Update(chatTurnDoneMsg{result: agent.ChatTurnResult{
		TaskState: state.TaskStateCompleted,
		Output:    "The command completed successfully.",
		Tools: []state.ToolOutcome{{
			ToolName: "shell_execute", Command: "printf rows", Success: true,
		}},
		Observed: []memory.ObservedToolCall{{
			ToolName: "shell_execute", Command: "printf rows", Result: output + "Final output line\n", Success: true,
		}},
	}}, nil, 80, 24)
	if !tab.activity.inspecting || !tab.activity.focused || tab.activity.selectedTool != 0 || tab.activity.selectedTurn != 1 {
		t.Fatalf("successful completion closed or changed the inspected tool: %#v", tab.activity)
	}
	if tab.activity.viewport.YOffset != before {
		t.Fatalf("completion stole the output scroll position: before=%d after=%d", before, tab.activity.viewport.YOffset)
	}
	if tab.toolCalls[0].status != "SUCCEEDED" || !strings.Contains(tab.toolCalls[0].output, "Final output line") {
		t.Fatalf("preserving the inspector prevented final evidence reconciliation: %#v", tab.toolCalls[0])
	}
}
