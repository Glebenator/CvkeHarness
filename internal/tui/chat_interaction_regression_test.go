package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func interactionRegressionTab() *chatTab {
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 2
	tab.messages = []liveChatMessage{
		{role: "user", content: "old prompt", turn: 1},
		{role: "assistant", content: "old response", turn: 1},
		{role: "user", content: "new current prompt", turn: 2},
	}
	tab.toolCalls = []liveToolCall{{
		name: "old_tool", status: "SUCCEEDED", output: strings.Repeat("old output line\n", 30), turn: 1,
	}}
	tab.composerFocused = true
	tab.composer.Focus()
	tab.composer.SetValue("preserved draft")
	return tab
}

func TestChatNarrowActivityBackClickRestoresComposerAcrossRelease(t *testing.T) {
	t.Parallel()
	tab := interactionRegressionTab()
	tab.resize(80, 30)
	tab.openActivity(1)
	y := 2 + strings.Count(tab.liveHeader(80), "\n") + tab.activity.backStart
	for _, action := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease} {
		tab.Update(tea.MouseMsg{X: 4, Y: y, Button: tea.MouseButtonLeft, Action: action}, nil, 80, 30)
	}
	if tab.activity.focused || !tab.composerFocused || !tab.composer.Focused() {
		t.Fatalf("Back click release changed the restored keyboard focus: activity=%t composer=%t textarea=%t", tab.activity.focused, tab.composerFocused, tab.composer.Focused())
	}
	if got := tab.composer.Value(); got != "preserved draft" {
		t.Fatalf("Back click changed the preserved draft: %q", got)
	}
}

func TestChatClickVisibleOutputRefocusesInspector(t *testing.T) {
	t.Parallel()
	tab := interactionRegressionTab()
	tab.resize(140, 30)
	tab.openActivity(1)
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	tab.closeActivity()
	if !tab.composerFocused || !tab.activity.inspecting || tab.activity.focused {
		t.Fatal("expected visible inspector to retain output while keyboard focus returns to composer")
	}
	_, mainWidth, _ := liveChatColumns(140)
	y := 2 + strings.Count(tab.liveHeader(140), "\n") + len(tab.activity.header) + 8
	for _, action := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease} {
		tab.Update(tea.MouseMsg{X: mainWidth + 4, Y: y, Button: tea.MouseButtonLeft, Action: action}, nil, 140, 30)
	}
	if !tab.activity.focused || !tab.activity.inspecting || tab.composerFocused || tab.composer.Focused() {
		t.Fatalf("clicking the visible output did not focus its inspector: activity=%t inspecting=%t composer=%t", tab.activity.focused, tab.activity.inspecting, tab.composerFocused)
	}
	if tab.activity.selectedTurn != 1 || tab.activity.selectedTool != 0 || tab.composer.Value() != "preserved draft" {
		t.Fatalf("refocusing output changed selection or draft: turn=%d tool=%d draft=%q", tab.activity.selectedTurn, tab.activity.selectedTool, tab.composer.Value())
	}
}

func TestChatHistoricalActivityCannotApproveHiddenCurrentWorkOrEditDraft(t *testing.T) {
	t.Parallel()
	tab := interactionRegressionTab()
	tab.resize(80, 30)
	tab.pendingApproval = &pendingChatApproval{workID: "current-work", summary: "current action", reason: "requires approval"}
	tab.status = "APPROVAL REQUIRED"
	tab.openActivity(1)
	tab.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := tab.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, nil, 80, 30)
	if cmd != nil || tab.approvalInFlight || tab.approvalWorkID != "" || tab.status != "APPROVAL REQUIRED" {
		t.Fatalf("a in historical activity authorized hidden current work: command=%t inFlight=%t workID=%q status=%q", cmd != nil, tab.approvalInFlight, tab.approvalWorkID, tab.status)
	}
	if tab.pendingApproval == nil || tab.pendingApproval.workID != "current-work" {
		t.Fatal("inspecting history changed the pending current approval")
	}
	tab.Update(tea.KeyMsg{Type: tea.KeyCtrlJ}, nil, 80, 30)
	if got := tab.composer.Value(); got != "preserved draft" {
		t.Fatalf("an Activity key edited the hidden composer draft: %q", got)
	}
}
