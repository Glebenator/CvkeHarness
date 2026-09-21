package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func paneChatFixture(width, height int) *chatTab {
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 2
	tab.messages = []liveChatMessage{
		{role: "user", content: "Check the API", turn: 1},
		{role: "assistant", content: strings.Repeat("A detailed response with multiple physical lines.\n\n", 10), turn: 1},
		{role: "user", content: "Check the worker", turn: 2},
		{role: "assistant", content: "The worker is healthy.", turn: 2},
	}
	tab.toolCalls = []liveToolCall{
		{name: "shell_execute", command: "systemctl status api", status: "SUCCEEDED", output: strings.Repeat("first output line\n", 40), turn: 1},
		{name: "shell_execute", command: "workerctl status", status: "SUCCEEDED", output: strings.Repeat("second output line\n", 40), turn: 2},
	}
	tab.resize(width, height)
	return tab
}

func TestChatPaneLayoutsFitTerminalAndKeepComposer(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 20}, {100, 26}, {120, 20}, {144, 36}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			tab := paneChatFixture(size[0], size[1])
			for _, mode := range []string{"chat", "activity", "output"} {
				if mode == "activity" {
					tab.openActivity(1)
				} else if mode == "output" {
					tab.updateLive(tea.KeyMsg{Type: tea.KeyEnter}, nil)
				}
				view := tab.View(size[0], size[1])
				rows := strings.Split(view, "\n")
				if len(rows) > size[1] {
					t.Fatalf("%s exceeds height: %d > %d\n%s", mode, len(rows), size[1], view)
				}
				for row, line := range rows {
					if w := ansi.StringWidth(line); w > size[0] {
						t.Fatalf("%s row %d exceeds width: %d > %d: %q", mode, row, w, size[0], line)
					}
				}
				if (size[0] >= 120 || mode == "chat") && !strings.Contains(view, "Message") {
					t.Fatalf("%s hid the composer:\n%s", mode, view)
				}
				if mode != "chat" && !strings.Contains(view, "ACTIVITY / TURN 1") {
					t.Fatalf("missing selected turn in %s:\n%s", mode, view)
				}
			}
		})
	}
}

func TestChatWheelRoutesToPaneUnderPointerWithoutChangingFocus(t *testing.T) {
	t.Parallel()
	tab := paneChatFixture(120, 30)
	tab.openActivity(1)
	tab.updateLive(tea.KeyMsg{Type: tea.KeyEnter}, nil)
	tab.closeActivity()
	tab.composerFocused = true
	tab.composer.Focus()
	tab.viewport.SetYOffset(5)
	tab.activity.viewport.SetYOffset(5)
	header := 2 + strings.Count(tab.liveHeader(120), "\n")
	wheel := tea.MouseMsg{X: 118, Y: header + len(tab.activity.header) + 1, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	tab.Update(wheel, nil, 120, 30)
	if tab.viewport.YOffset != 5 || tab.activity.viewport.YOffset != 2 || !tab.composerFocused || tab.activity.focused {
		t.Fatalf("wheel over activity changed wrong pane/focus: chat=%d activity=%d composer=%t activityFocus=%t", tab.viewport.YOffset, tab.activity.viewport.YOffset, tab.composerFocused, tab.activity.focused)
	}
	wheel.X, wheel.Y = 3, header+2
	tab.Update(wheel, nil, 120, 30)
	if tab.viewport.YOffset != 2 || tab.activity.viewport.YOffset != 2 || !tab.composerFocused {
		t.Fatalf("wheel over chat changed wrong pane/focus: chat=%d activity=%d composer=%t", tab.viewport.YOffset, tab.activity.viewport.YOffset, tab.composerFocused)
	}
	wheel.Y = header + tab.viewport.Height + 4
	tab.Update(wheel, nil, 120, 30)
	if tab.viewport.YOffset != 2 || tab.activity.viewport.YOffset != 2 {
		t.Fatal("wheel over composer scrolled a reading pane")
	}
}

func TestChatActivityLinkClickUsesPhysicalPositionAfterMultilineReply(t *testing.T) {
	t.Parallel()
	tab := paneChatFixture(100, 30)
	link := -1
	for row, turn := range tab.chatTurnLinks {
		if turn == 2 {
			link = row
		}
	}
	if link < 0 {
		t.Fatal("second turn has no activity link")
	}
	tab.viewport.SetYOffset(link - 2)
	before := tab.viewport.YOffset
	y := 2 + strings.Count(tab.liveHeader(100), "\n") + 1 + link - before
	tab.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}, nil, 100, 30)
	if !tab.activity.focused || tab.activity.selectedTurn != 2 || tab.viewport.YOffset != before {
		t.Fatalf("activity link targeted wrong turn or moved chat: focused=%t turn=%d offset=%d want=%d", tab.activity.focused, tab.activity.selectedTurn, tab.viewport.YOffset, before)
	}
}

func TestChatActivityRoundTripRestoresDraftAndCtrlEndResumesLatest(t *testing.T) {
	t.Parallel()
	tab := paneChatFixture(80, 24)
	tab.composerFocused = true
	tab.composer.Focus()
	tab.composer.SetValue("keep this draft")
	tab.openActivity(1)
	tab.updateLive(tea.KeyMsg{Type: tea.KeyEnter}, nil)
	tab.updateLive(tea.KeyMsg{Type: tea.KeyEsc}, nil)
	tab.updateLive(tea.KeyMsg{Type: tea.KeyEsc}, nil)
	if tab.activity.focused || !tab.composerFocused || tab.composer.Value() != "keep this draft" {
		t.Fatal("inspection round trip lost draft/focus")
	}
	tab.viewport.GotoTop()
	tab.updateLive(tea.KeyMsg{Type: tea.KeyCtrlEnd}, nil)
	if !tab.viewport.AtBottom() || !tab.activity.following || tab.activity.selectedTurn != 2 || tab.composer.Value() != "keep this draft" {
		t.Fatal("Ctrl+End did not restore latest reading state while preserving draft")
	}
}

func TestChatOpeningActivityAtBottomAlwaysUsesLatestTurn(t *testing.T) {
	t.Parallel()
	for _, width := range []int{80, 100, 120} {
		tab := paneChatFixture(width, 36)
		tab.toolCalls = tab.toolCalls[:1]
		tab.refreshViewport()
		tab.viewport.GotoBottom()
		tab.updateLive(tea.KeyMsg{Type: tea.KeyCtrlT}, nil)
		if tab.activity.selectedTurn != 2 || tab.activity.selectedTool != -1 {
			t.Fatalf("width %d: opening latest activity showed stale tools: turn=%d tool=%d", width, tab.activity.selectedTurn, tab.activity.selectedTool)
		}
	}
}

func TestChatFooterKeepsActivityAndLatestControlsAt80Columns(t *testing.T) {
	t.Parallel()
	m := recoveryModel()
	m.width, m.height, m.activeTab = 80, 24, tabChat
	footer := ansi.Strip(m.renderStatusBar())
	for _, key := range []string{"ctrl+t", "ctrl+end", "enter"} {
		if !strings.Contains(footer, key) {
			t.Fatalf("narrow chat footer hides essential %s control: %q", key, footer)
		}
	}
}
