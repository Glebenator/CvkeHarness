package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
)

func TestSettingsReviewUnfinishedConnectionEditSurvivesQuitFromOtherTab(t *testing.T) {
	m := recoveryModel()
	m.reduceMotion = true
	m.activeTab = tabConfig
	tab := m.tabs[tabConfig].(*configTab)
	tab.openConnectionEditor(tab.cfg.ConnectionIDs()[0])
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = navigationText(m, "Pending connection name")
	if !tab.connectionEditor.Dirty() {
		t.Fatal("unfinished field edit was not recognized")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	m = navigationText(m, "Overview")
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(model)
	if cmd != nil {
		if _, quitting := cmd().(tea.QuitMsg); quitting {
			t.Fatal("q discarded the unfinished connection field after switching tabs")
		}
	}
	if !m.confirmQuit && m.activeTab != tabConfig {
		t.Fatal("quit neither protected the pending draft nor returned to its editor")
	}
	if tab.connectionEditor == nil || !strings.Contains(tab.View(80, 20), "Pending connection name") {
		t.Fatal("pending connection field was lost")
	}
}

func TestSettingsReviewIncompleteBindingStillProtectsConnection(t *testing.T) {
	m := recoveryModel()
	tab := m.tabs[tabConfig].(*configTab)
	id := tab.cfg.ConnectionIDs()[0]
	tab.cfg.SetRoleBinding(config.RolePrimary, config.ModelBinding{Connection: id})
	tab.setSection(settingsConnections)
	tab.updateWorkspace(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}, m.svc)
	if _, exists := tab.cfg.Connections[id]; !exists {
		t.Fatal("repair draft removed the connection referenced by an incomplete Primary binding")
	}
	tab.openConnectionEditor(id)
	tab.connectionEditor.Update(tea.KeyMsg{Type: tea.KeyDown})
	tab.connectionEditor.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(tab.connectionEditor.View(80, 20), "add a new connection") {
		t.Fatal("incomplete role binding allowed its connection provider to change")
	}
}

func TestSettingsReviewRemovedLegacyConnectionDoesNotReturnAfterSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Provider, cfg.DefaultModel = "lmstudio", "native/model"
	cfg.BaseURL = "http://127.0.0.1:1234/v1"
	cfg.APIKeys = map[string]string{"openai": "synthetic-unused-key"}
	svc := NewService(cfg, nil, nil, nil, nil)
	tab := newConfigTab().(*configTab)
	tab.Init(svc)
	tab.setSection(settingsConnections)
	for i, id := range tab.cfg.ConnectionIDs() {
		if id == "openai" {
			tab.connectionCursor = i
		}
	}
	tab.updateWorkspace(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}, svc)
	if _, exists := tab.cfg.Connections["openai"]; exists {
		t.Fatal("unused legacy connection was not removed from the draft")
	}
	cmd := tab.saveSettings(svc)
	if cmd == nil {
		t.Fatalf("could not save repaired draft: %s", tab.saveErr)
	}
	result := cmd().(configSavedMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	loaded, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for id, connection := range loaded.Connections {
		if connection.Provider == "openai" {
			t.Fatalf("deleted unused connection returned after save as %q", id)
		}
	}
}

func TestSettingsRuntimeInputCompletionKeepsItsEditor(t *testing.T) {
	m := recoveryModel()
	tab := m.tabs[tabConfig].(*configTab)
	for i, field := range tab.fields {
		if field.Label == "Memory Dir" {
			tab.cursor = i
			break
		}
	}
	tab.beginEdit()
	tab.input.SetValue("")
	completion := configInputCmd(func() tea.Msg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted/path")}
	}, tab.inputEpoch)()
	// Completion arrives while a different workspace owns keyboard focus.
	m.activeTab = tabOverview
	next, _ := m.Update(completion)
	m = next.(model)
	if tab.input.Value() != "pasted/path" || m.activeTab != tabOverview {
		t.Fatal("input completion was lost or moved workspace focus")
	}
	tab.updateEditor(tea.KeyMsg{Type: tea.KeyEsc})
	tab.beginEdit()
	tab.input.SetValue("new draft")
	m.Update(completion)
	if tab.input.Value() != "new draft" {
		t.Fatal("late input completion changed a new editor")
	}
}

func TestSettingsRuntimeInputPreservesBatchedCommands(t *testing.T) {
	command := configInputCmd(tea.Batch(
		func() tea.Msg { return "clipboard" },
		func() tea.Msg { return "cursor" },
	), 8)
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("input effects were hidden from the event loop")
	}
	for _, child := range batch {
		msg, ok := child().(configInputMsg)
		if !ok || msg.epoch != 8 {
			t.Fatal("input effect lost its originating editor")
		}
	}
}
