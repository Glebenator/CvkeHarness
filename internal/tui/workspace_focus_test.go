package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestWorkspaceArrowsAndShortcutsRequireTopBarFocus(t *testing.T) {
	for active := 0; active < tabCount; active++ {
		m := recoveryModel()
		m.activeTab = active
		for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight, tea.KeyTab, tea.KeyShiftTab} {
			m = navigationKey(m, tea.KeyMsg{Type: key})
			if m.activeTab != active || m.topBarFocused {
				t.Fatalf("tab %d: %s escaped the workspace", active, key)
			}
		}
		m = navigationText(m, "1")
		if m.activeTab != active {
			t.Fatal("numeric shortcut escaped focused workspace")
		}
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
		if !m.topBarFocused {
			t.Fatalf("tab %d: Escape did not focus top bar", active)
		}
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyRight})
		if m.activeTab != (active+1)%tabCount || !m.topBarFocused {
			t.Fatal("top bar selection did not wrap/stay unfocused")
		}
		m = navigationText(m, "2")
		m = navigationText(m, "n")
		jobs := m.tabs[tabJobs].(*jobsTab)
		if jobs.mode != jobsModeList {
			t.Fatal("unfocused Jobs accepted an action")
		}
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
		if m.topBarFocused || jobs.mode != jobsModeList {
			t.Fatal("Enter must only focus the workspace")
		}
		m = navigationText(m, "n")
		if jobs.mode != jobsModeCreate {
			t.Fatal("focused Jobs did not accept an action")
		}
	}
}

func TestSettingsSidebarNavigationPreservesSelectionAndDraft(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := recoveryModel()
		m.width, m.activeTab = width, tabConfig
		cfg := m.tabs[tabConfig].(*configTab)
		cfg.roleCursor = 1
		cfg.dirty = true
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyLeft})
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyDown})
		if !cfg.sidebarFocused || cfg.section != settingsConnections || cfg.roleCursor != 1 {
			t.Fatal("sidebar arrows did not change section independently of content")
		}
		m = navigationText(m, "a")
		if cfg.connectionEditor != nil {
			t.Fatal("sidebar shortcut edited unfocused content")
		}
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cfg.sidebarFocused || cfg.connectionEditor != nil {
			t.Fatal("Enter should only focus content from the sidebar")
		}
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
		if !m.topBarFocused || cfg.section != settingsConnections || !cfg.dirty {
			t.Fatal("Escape reset section or discarded settings")
		}
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyLeft})
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyUp})
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyRight})
		if cfg.sidebarFocused || cfg.section != settingsModels || cfg.roleCursor != 1 {
			t.Fatal("returning to Models lost selection")
		}
		for _, focused := range []bool{false, true} {
			m.topBarFocused = focused
			for _, line := range strings.Split(m.View(), "\n") {
				if lipgloss.Width(line) > width {
					t.Fatalf("focus UI overflows %d columns: %q", width, line)
				}
			}
		}
	}
}

func TestEscapeClosesNestedSurfacesBeforeTopBar(t *testing.T) {
	for _, setup := range []func(model){
		func(m model) { m.tabs[tabOverview].(*overviewTab).detail = true },
		func(m model) { m.tabs[tabJobs].(*jobsTab).mode = jobsModeDetail },
		func(m model) { m.tabs[tabRuns].(*runsTab).expanded = true },
	} {
		m := recoveryModel()
		setup(m)
		for i := tabOverview; i <= tabRuns; i++ {
			m.activeTab = i
			if !m.workspaceHandlesEscape() {
				continue
			}
			m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.topBarFocused || m.workspaceHandlesEscape() {
				t.Fatal("first Escape did not close the nested detail")
			}
			m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
			if !m.topBarFocused {
				t.Fatal("second Escape did not focus top bar")
			}
		}
	}
}

func TestSettingsEditorRetainsArrowsAndEscapeCancellation(t *testing.T) {
	m := recoveryModel()
	m.activeTab = tabConfig
	cfg := m.tabs[tabConfig].(*configTab)
	cfg.setSection(settingsRuntime)
	for i, field := range cfg.fields {
		if field.Label == "Memory Dir" {
			cfg.cursor = i
		}
	}
	cfg.beginEdit()
	cfg.input.SetValue("abcd")
	cfg.input.CursorEnd()
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyLeft})
	m = navigationText(m, "X")
	if cfg.input.Value() != "abcXd" || m.topBarFocused || cfg.sidebarFocused {
		t.Fatal("editor did not receive local arrow movement")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cfg.editing || m.topBarFocused || cfg.section != settingsRuntime {
		t.Fatal("Escape did not cancel the editor in place")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.topBarFocused {
		t.Fatal("Escape from Runtime did not focus top bar")
	}
}

func TestChatFocusRoundTripPreservesDraftAndDoesNotSubmit(t *testing.T) {
	m := recoveryModel()
	m.activeTab = tabChat
	chat := m.tabs[tabChat].(*chatTab)
	chat.composerFocused = true
	chat.composer.Focus()
	chat.composer.SetValue("keep draft")
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.topBarFocused || chat.composer.Focused() {
		t.Fatal("Escape did not blur the composer and focus the top bar")
	}
	m = navigationText(m, "5")
	m = navigationText(m, "4")
	m = navigationText(m, "ignored")
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.topBarFocused || !chat.composerFocused || !chat.composer.Focused() || chat.composer.Value() != "keep draft" || chat.starting || chat.running {
		t.Fatal("focus round trip changed or submitted the draft")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if chat.composerFocused || chat.composer.Focused() || m.topBarFocused || chat.composer.Value() != "keep draft" {
		t.Fatal("Tab did not switch to reading without changing the draft")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if !chat.composerFocused || !chat.composer.Focused() || chat.composer.Value() != "keep draft" {
		t.Fatal("Shift+Tab did not restore composing")
	}
}

func TestEscapeLeavesRunningChatAndControlXInterruptsOnlyWhenFocused(t *testing.T) {
	m := recoveryModel()
	m.activeTab = tabChat
	chat := m.tabs[tabChat].(*chatTab)
	chat.running = true
	cancelled := false
	chat.cancelTurn = func() { cancelled = true }
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if !m.topBarFocused || cancelled || !chat.running {
		t.Fatal("unfocusing or typing into the top bar interrupted work")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if chat.composerFocused {
		t.Fatal("focusing a running chat enabled the composer")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if !cancelled || !chat.stopping {
		t.Fatal("Ctrl+X did not interrupt the focused chat")
	}
}

func TestSecurityArrowsStayLocalAndEscapeCancelsOnlyConfirmation(t *testing.T) {
	m := recoveryModel()
	m.activeTab = tabConfig
	cfg := m.tabs[tabConfig].(*configTab)
	cfg.setSection(settingsSecurity)
	profile := cfg.cfg.Security.Profile
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyRight})
	if cfg.pendingProfile == "" || m.activeTab != tabConfig || m.topBarFocused {
		t.Fatal("Security did not receive Right")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cfg.pendingProfile != "" || !cfg.securityOpen || m.topBarFocused || cfg.cfg.Security.Profile != profile {
		t.Fatal("Escape applied a pending profile or left Security")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyRight})
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if !cfg.sidebarFocused {
		t.Fatal("Tab did not focus sections from Security")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.topBarFocused || cfg.section != settingsSecurity {
		t.Fatal("Escape from Security sidebar did not focus top bar in place")
	}
}

func TestQuitFromTopBarReturnsFocusToUnfinishedSettingsEditor(t *testing.T) {
	m := recoveryModel()
	cfg := m.tabs[tabConfig].(*configTab)
	for i, field := range cfg.fields {
		if field.Label == "Memory Dir" {
			cfg.cursor = i
		}
	}
	cfg.setSection(settingsRuntime)
	cfg.beginEdit()
	cfg.input.SetValue("preserve this edit")
	m.topBarFocused = true
	m = navigationText(m, "q")
	if !m.confirmQuit {
		t.Fatal("quit did not protect the unfinished edit")
	}
	m = navigationText(m, "s")
	if m.confirmQuit || m.topBarFocused || m.activeTab != tabConfig || !cfg.editing || cfg.input.Value() != "preserve this edit" {
		t.Fatal("return to editor did not restore focus and draft")
	}
}

func TestChatCompletionCannotStealTopBarFocus(t *testing.T) {
	m := recoveryModel()
	m.activeTab, m.topBarFocused = tabChat, true
	chat := m.tabs[tabChat].(*chatTab)
	chat.running = true
	next, _ := m.Update(chatTurnDoneMsg{})
	m = next.(model)
	if !m.topBarFocused || chat.running || chat.composer.Focused() {
		t.Fatal("chat completion stole keyboard focus or failed to reconcile")
	}
	m = navigationText(m, "ignored")
	if chat.composer.Value() != "" {
		t.Fatal("background completion let input reach the unfocused composer")
	}
}
