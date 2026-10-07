package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/glebenator/cvkeharness/tools"
)

func activityTestTab() *chatTab {
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 2
	tab.messages = []liveChatMessage{
		{role: "user", content: "Check API health", turn: 1},
		{role: "assistant", content: "The API is healthy.", turn: 1},
		{role: "user", content: "What about the worker?", turn: 2},
	}
	tab.toolCalls = []liveToolCall{
		{id: "health", name: "shell_execute", command: "curl /health", status: "SUCCEEDED", output: "healthy", turn: 1},
		{id: "status", name: "shell_execute", command: "systemctl status api", status: "SUCCEEDED", output: "active", turn: 1},
		{id: "worker", name: "shell_execute", command: "workerctl status", status: "RUNNING", output: "starting", turn: 2},
	}
	tab.resizeActivity(48, 18)
	return tab
}

func TestActivityFollowsNewTurnsUntilOperatorPins(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	if !tab.activity.following || tab.activity.selectedTurn != 2 || tab.activity.selectedTool != 2 {
		t.Fatalf("latest activity was not selected: %#v", tab.activity)
	}
	tab.openActivity(1)
	tab.activeTurn = 3
	tab.messages = append(tab.messages, liveChatMessage{role: "user", content: "Check the queue", turn: 3})
	tab.toolCalls = append(tab.toolCalls, liveToolCall{id: "queue", command: "queuectl status", turn: 3})
	tab.refreshActivity()
	if tab.activity.following || tab.activity.selectedTurn != 1 || tab.activity.selectedTool != 0 {
		t.Fatalf("runtime activity stole the pinned selection: %#v", tab.activity)
	}
	tab.followLatestActivity()
	if !tab.activity.focused || !tab.activity.following || tab.activity.selectedTurn != 3 || tab.activity.selectedTool != 3 {
		t.Fatalf("follow latest failed to select current turn while preserving focus: %#v", tab.activity)
	}
	if !strings.Contains(ansi.Strip(tab.activityView()), "FOLLOWING LATEST") {
		t.Fatal("follow mode is not visible")
	}
}

func TestActivityArrowsAndWheelOnlyScrollTheirOwnViewport(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.toolCalls[0].output = strings.Repeat("output line\n", 80)
	tab.viewport.SetContent(strings.Repeat("conversation line\n", 80))
	tab.viewport.SetYOffset(9)
	tab.composerFocused = true
	tab.composer.Focus()
	tab.openActivity(1)
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	selected := tab.activity.selectedTool
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyDown})
	if tab.activity.viewport.YOffset != 1 || tab.activity.selectedTool != selected || tab.viewport.YOffset != 9 {
		t.Fatalf("arrow scrolled or selected the wrong target: activity=%d selected=%d chat=%d", tab.activity.viewport.YOffset, tab.activity.selectedTool, tab.viewport.YOffset)
	}
	tab.handleActivityMouse(tea.MouseMsg{X: 1, Y: 5, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if tab.activity.viewport.YOffset != 4 || tab.activity.selectedTool != selected || tab.viewport.YOffset != 9 {
		t.Fatal("wheel changed selection or conversation scroll")
	}
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEsc})
	if tab.activity.inspecting || !tab.activity.focused {
		t.Fatal("first escape should return to the list")
	}
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEsc})
	if tab.activity.focused || !tab.composerFocused || !tab.composer.Focused() || tab.viewport.YOffset != 9 {
		t.Fatal("second escape did not restore composer focus and chat scroll")
	}
}

func TestActivityManualInspectionSurvivesSuccessAndNewTool(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.toolCalls[2].output = strings.Repeat("before\n", 50)
	tab.openActivity(2)
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	tab.activity.viewport.SetYOffset(12)
	tab.toolCalls[2].status = "SUCCEEDED"
	tab.toolCalls[2].duration = 1500 * time.Millisecond
	tab.toolCalls[2].output += "completed\n"
	tab.toolCalls = append(tab.toolCalls, liveToolCall{id: "new", command: "new command", turn: 2})
	tab.refreshActivity()
	if !tab.activity.inspecting || tab.activity.selectedTool != 2 || tab.activity.viewport.YOffset != 12 {
		t.Fatalf("completion or new call disturbed open output: %#v", tab.activity)
	}
	tab.activity.viewport.GotoTop()
	if !strings.Contains(ansi.Strip(tab.activityView()), "SUCCEEDED") {
		t.Fatal("open detail did not receive its updated status")
	}
}

func TestActivitySelectionIsExplicitAndBackRestoresListOffset(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	for i := 0; i < 12; i++ {
		tab.toolCalls = append(tab.toolCalls, liveToolCall{id: fmt.Sprint(i), command: fmt.Sprintf("command %d", i), status: "SUCCEEDED", turn: 1})
	}
	tab.resizeActivity(32, 12)
	tab.openActivity(1)
	for i := 0; i < 7; i++ {
		tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	}
	start := tab.activity.rowStarts[tab.activity.selectedTool]
	end := tab.activity.rowEnds[tab.activity.selectedTool]
	offset := tab.activity.viewport.YOffset
	if start < offset || end >= offset+tab.activity.viewport.Height {
		t.Fatal("explicit next-tool selection did not reveal the selected row")
	}
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEsc})
	if tab.activity.viewport.YOffset != offset {
		t.Fatalf("back lost list scroll: got %d want %d", tab.activity.viewport.YOffset, offset)
	}
	if !tab.handleActivityKey(tea.KeyMsg{Type: tea.KeySpace}) || tab.activity.inspecting {
		t.Fatal("space must be consumed without opening tool details")
	}
}

func TestActivityClickUsesPhysicalRowsWithWrappedPrompt(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.messages[0].content = "Inspect 日本語 API health\nand explain the result across multiple lines."
	tab.resizeActivity(23, 18)
	tab.openActivity(1)
	start := tab.activity.rowStarts[1]
	tab.activity.viewport.SetYOffset(start)
	before := ansi.Strip(tab.activity.viewport.View())
	if !strings.Contains(before, "systemctl status api") {
		t.Fatalf("test row not visible: %q", before)
	}
	tab.handleActivityMouse(tea.MouseMsg{
		X: 3, Y: len(tab.activity.header) + start - tab.activity.viewport.YOffset + 1,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
	})
	if !tab.activity.inspecting || tab.activity.selectedTool != 1 {
		t.Fatalf("click failed to open second tool's physical command row: %#v", tab.activity)
	}
}

func TestActivityOutputSanitizationVerificationAndWidth(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.toolCalls[0].command = "printf '\u001b[31mhello\u001b[0m'\nsecond line"
	tab.toolCalls[0].output = "\u001b[2Jwide 日本語 日本語 output\r\n\tcontinued\x00"
	tab.toolCalls[0].err = "failure\nsecond error line"
	tab.toolCalls[0].duration = 24 * time.Millisecond
	tab.verifierActivity[1] = liveVerificationActivity{turn: 1, VerificationActivity: tools.VerificationActivity{
		Phase: tools.VerificationPhaseCompleted, Status: "satisfied", Final: true,
		Reason: "independent health probe passed", CapabilitiesEvaluated: true,
	}}
	tab.resizeActivity(23, 90)
	tab.openActivity(1)
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	rendered := tab.activityView()
	plain := ansi.Strip(rendered)
	for _, want := range []string{"COMMAND", "RAW OUTPUT", "ERROR", "DURATION", "VERIFICATION", "SATISFIED", "Capabilities:"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("detail missing %q:\n%s", want, plain)
		}
	}
	if strings.ContainsAny(plain, "\x00\r\t") || strings.Contains(rendered, "\u001b[2J") {
		t.Fatal("raw output retained terminal control characters")
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := ansi.StringWidth(line); width > 23 {
			t.Fatalf("activity overflows by %d cells: %q", width-23, line)
		}
	}
}

func TestActivityEmptyTurnAndTurnNavigation(t *testing.T) {
	t.Parallel()
	tab := newChatTab().(*chatTab)
	tab.resizeActivity(40, 15)
	if !strings.Contains(ansi.Strip(tab.activityView()), "No activity yet") {
		t.Fatal("missing initial empty state")
	}
	tab.messages = []liveChatMessage{{role: "user", content: "Hello", turn: 1}, {role: "user", content: "Next", turn: 2}}
	tab.activeTurn = 2
	tab.openActivity(1)
	if !strings.Contains(ansi.Strip(tab.activityView()), "No tool calls in this turn.") {
		t.Fatal("empty selected turn shows stale tools")
	}
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if tab.activity.selectedTurn != 2 || tab.activity.following {
		t.Fatal("next turn selection should be explicitly pinned")
	}
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if tab.activity.selectedTurn != 1 {
		t.Fatal("previous turn selection did not restore first turn")
	}
}

func TestActivityHeaderMouseActionsAndUnfocusedWheel(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.composerFocused = true
	tab.composer.Focus()
	tab.handleActivityMouse(tea.MouseMsg{X: 1, Y: 5, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if tab.activity.focused || !tab.composerFocused || tab.activity.following {
		t.Fatal("activity wheel must pin without stealing composer focus")
	}
	tab.openActivity(1)
	tab.handleActivityMouse(tea.MouseMsg{X: 1, Y: tab.activity.followStart, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !tab.activity.following || tab.activity.selectedTurn != 2 {
		t.Fatal("click on Follow latest header failed")
	}
	tab.handleActivityMouse(tea.MouseMsg{X: 1, Y: tab.activity.backStart, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if tab.activity.focused || !tab.composerFocused {
		t.Fatal("click on Back header failed to restore composer")
	}
}

func TestActivityMouseFocusAndBackProcessOnePhysicalClick(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.composerFocused = true
	tab.composer.Focus()
	index := tab.activity.selectedTool
	y := len(tab.activity.header) + tab.activity.rowStarts[index] - tab.activity.viewport.YOffset
	// Release-only input is used by some terminals and synthetic click paths.
	tab.handleActivityMouse(tea.MouseMsg{X: 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if !tab.activity.focused || !tab.activity.inspecting || tab.composerFocused || tab.composer.Focused() {
		t.Fatal("tool click did not transfer keyboard focus to the output")
	}
	y = tab.activity.backStart
	tab.handleActivityMouse(tea.MouseMsg{X: 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	tab.handleActivityMouse(tea.MouseMsg{X: 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if tab.activity.inspecting || !tab.activity.focused {
		t.Fatal("one Back click should return to list, without closing activity twice")
	}
	tab.closeActivity()
	if !tab.composerFocused {
		t.Fatal("mouse inspection lost the composer return focus")
	}
}

func TestActivityLongPromptsAndTinyPanesStayBounded(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.messages[0].content = strings.Repeat("A very long request with 日本語 text.\n", 100)
	for _, width := range []int{1, 8, 23, 48} {
		for _, height := range []int{1, 3, 8, 20} {
			tab.resizeActivity(width, height)
			tab.openActivity(1)
			view := tab.activityView()
			lines := strings.Split(view, "\n")
			if len(lines) > height {
				t.Fatalf("%dx%d pane rendered %d rows", width, height, len(lines))
			}
			for _, line := range lines {
				if actual := ansi.StringWidth(line); actual > width {
					t.Fatalf("%dx%d pane rendered %d-cell row %q", width, height, actual, line)
				}
			}
		}
	}
	tab.resizeActivity(48, 20)
	tab.openActivity(1)
	if !strings.Contains(ansi.Strip(tab.activityView()), "curl /health") {
		t.Fatal("long prompt buried the selected tool on opening activity")
	}
}

func TestActivityVisibleOutputClickRestoresKeyboardFocus(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.toolCalls[0].output = strings.Repeat("raw output line\n", 40)
	tab.composerFocused = true
	tab.composer.Focus()
	tab.openActivity(1)
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	tab.activity.viewport.SetYOffset(8)
	tab.closeActivity()
	if tab.activity.focused || !tab.composerFocused || !tab.activity.inspecting {
		t.Fatal("setup should leave the output visible without keyboard focus")
	}
	tab.handleActivityMouse(tea.MouseMsg{
		X: 2, Y: len(tab.activity.header) + 2,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease,
	})
	if !tab.activity.focused || tab.composerFocused || !tab.activity.inspecting || tab.activity.viewport.YOffset != 8 {
		t.Fatal("output click must restore activity focus while preserving output scroll")
	}
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyDown})
	if tab.activity.viewport.YOffset != 9 {
		t.Fatal("keyboard arrows did not reach the refocused output")
	}
	tab.closeActivity()
	if !tab.composerFocused {
		t.Fatal("refocusing output lost the composer return focus")
	}
}

func TestActivityConsumesUnsupportedKeysAndCannotApproveAnotherTurn(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	tab.pendingApproval = &pendingChatApproval{workID: "current-turn-approval", summary: "restart worker"}
	tab.openActivity(1)
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	if !tab.handleActivityKey(key) {
		t.Fatal("unsupported activity keys must not fall through to chat")
	}
	_, cmd := tab.updateLive(key, nil)
	if cmd != nil || tab.approvalInFlight || tab.approvalWorkID != "" {
		t.Fatal("historical activity key approved a different turn's hidden request")
	}
	if tab.pendingApproval == nil || tab.pendingApproval.workID != "current-turn-approval" || tab.activity.selectedTurn != 1 {
		t.Fatal("unsupported activity key changed approval or selected-turn context")
	}
}

func TestActivityHeaderAdvertisesOnlyFocusedKeyboardActions(t *testing.T) {
	t.Parallel()
	tab := activityTestTab()
	header := func() string { return ansi.Strip(strings.Join(tab.activity.header, "\n")) }
	if strings.Contains(header(), "Esc") || !strings.Contains(header(), "Click to focus activity") {
		t.Fatal("unfocused activity must invite focus without advertising Escape")
	}
	tab.handleActivityMouse(tea.MouseMsg{
		X: 1, Y: tab.activity.backStart,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease,
	})
	if !tab.activity.focused || !strings.Contains(header(), "Esc Back to chat") {
		t.Fatal("clicking the unfocused list header must focus activity")
	}
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(header(), "Esc Back to tools") {
		t.Fatal("focused output should advertise Escape to tools")
	}
	tab.closeActivity()
	if strings.Contains(header(), "Esc") || strings.Contains(header(), "· f Follow") || !strings.Contains(header(), "Back to tools") {
		t.Fatal("unfocused output should expose a click action without an Escape hint")
	}
	tab.handleActivityMouse(tea.MouseMsg{
		X: 1, Y: tab.activity.backStart,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease,
	})
	if tab.activity.inspecting || !strings.Contains(header(), "Click to focus activity") {
		t.Fatal("unfocused Back to tools header failed to return to the list")
	}
}
