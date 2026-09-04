package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/tools"
)

func recoveryModel() model {
	svc := NewService(config.DefaultConfig(), nil, nil, nil, nil)
	m := model{svc: svc, width: 80, height: 24, tabs: [tabCount]tabModel{newOverviewTab(), newJobsTab(), newRunsTab(), newChatTab(), newConfigTab()}}
	m.tabs[tabConfig].Init(svc)
	return m
}

func TestBackgroundChatFailureRestoresDraftOnEveryOtherTab(t *testing.T) {
	for _, active := range []int{tabOverview, tabJobs, tabRuns, tabConfig} {
		m := recoveryModel()
		m.activeTab = active
		chat := m.tabs[tabChat].(*chatTab)
		chat.starting = true
		chat.pendingPrompt = "Inspect staging without changing it"
		updated, _ := m.Update(chatSessionReadyMsg{err: errors.New("missing API key")})
		m = updated.(model)
		if chat.starting || chat.status != "UNAVAILABLE" || chat.composer.Value() != "Inspect staging without changing it" {
			t.Fatalf("tab %d lost completion/draft: %#v", active, chat)
		}
		if m.activeTab != active || !m.unread[tabChat] {
			t.Fatal("completion stole focus or did not signal attention")
		}
	}
}

func TestBackgroundSettingsSaveReconcilesDirtyState(t *testing.T) {
	m := recoveryModel()
	cfg := m.tabs[tabConfig].(*configTab)
	cfg.dirty = true
	updated, _ := m.Update(configSavedMsg{})
	m = updated.(model)
	if cfg.dirty || m.activeTab != tabOverview {
		t.Fatal("background save did not clear dirty state without stealing focus")
	}
}

func TestQuitPreservesSettingsUntilExplicitDiscard(t *testing.T) {
	m := recoveryModel()
	m.tabs[tabConfig].(*configTab).dirty = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(model)
	if cmd != nil || !m.confirmQuit {
		t.Fatal("quit discarded pending settings")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.confirmQuit || !m.tabs[tabConfig].(*configTab).dirty {
		t.Fatal("cancel lost edits")
	}
	m.confirmQuit = true
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("explicit discard did not exit")
	}
}

func TestAPIKeyEditorNeverRendersExistingKey(t *testing.T) {
	m := recoveryModel()
	cfg := m.tabs[tabConfig].(*configTab)
	cfg.cfg.SetAPIKey(cfg.cfg.Provider, "SYNTHETIC-SECRET-123456")
	for i, field := range cfg.fields {
		if field.Label == "Provider API Key" {
			cfg.cursor = i
		}
	}
	cfg.beginEdit()
	if strings.Contains(cfg.View(80, 20), "SYNTHETIC-SECRET") {
		t.Fatal("editor exposed credential")
	}
	cfg.updateEditor(tea.KeyMsg{Type: tea.KeyEnter})
	if cfg.cfg.GetAPIKey(cfg.cfg.Provider) != "SYNTHETIC-SECRET-123456" {
		t.Fatal("masked edit corrupted key")
	}
}

func TestHelpCanReachLastShortcutAt80x24(t *testing.T) {
	m := recoveryModel()
	m.showHelp = true
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(model)
	if !m.showHelp || !strings.Contains(m.View(), "Quit the dashboard") {
		t.Fatal("help cannot scroll to final group")
	}
}

func TestCompactChatShowsTargetSafetyAndVerification(t *testing.T) {
	tab := newChatTab().(*chatTab)
	tab.safety = "reasonable"
	tab.applyRuntimeEvent(tools.Event{Type: tools.EventTargetResolved, TargetID: "staging-api", Environment: "staging"})
	for _, width := range []int{80, 100, 120} {
		view := tab.View(width, 20)
		for _, want := range []string{"staging-api", "staging", "reasonable", "Verification:"} {
			if !strings.Contains(view, want) {
				t.Fatalf("width %d missing %s", width, want)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("width %d overflow: %q", width, line)
			}
		}
	}
}
