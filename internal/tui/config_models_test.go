package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/modelui"
	"strings"
	"testing"
)

func TestEveryRoleUsesSharedPickerAndStagesOnlyItsBinding(t *testing.T) {
	for _, role := range config.ModelRoleList() {
		t.Run(string(role), func(t *testing.T) {
			m := recoveryModel()
			tab := m.tabs[tabConfig].(*configTab)
			tab.cfg.Connections["judge-server"] = config.Connection{Name: "Judge server", Provider: "lmstudio", BaseURL: "http://127.0.0.1:2345/v1"}
			before := tab.cfg.Clone()
			tab.openRolePicker(role)
			id := tab.modelPicker.ID()
			tab.Init(m.svc)
			if tab.modelPicker == nil || tab.modelPicker.ID() != id {
				t.Fatal("refresh discarded picker")
			}
			binding := config.ModelBinding{Connection: "judge-server", Model: "vendor/native/model"}
			tab.Update(modelui.PickerResultMsg{PickerID: id, Role: role, Binding: binding}, m.svc, 80, 20)
			if tab.modelPicker != nil || !tab.dirty || tab.cfg.RoleBinding(role) != binding {
				t.Fatal("selection not staged")
			}
			for _, other := range config.ModelRoleList() {
				if other != role && tab.cfg.RoleBinding(other) != before.RoleBinding(other) {
					t.Fatalf("changed unrelated role %s", other)
				}
			}
			if _, ok := m.svc.Config().Connections["judge-server"]; ok {
				t.Fatal("draft saved prematurely")
			}
		})
	}
}

func TestSharedPickerCancelAndStaleResultDoNotChangeDraft(t *testing.T) {
	m := recoveryModel()
	tab := m.tabs[tabConfig].(*configTab)
	before := tab.cfg.RoleBinding(config.RolePrimary)
	tab.openRolePicker(config.RolePrimary)
	old := tab.modelPicker.ID()
	tab.Update(modelui.PickerResultMsg{PickerID: old, Cancelled: true}, m.svc, 80, 20)
	tab.openRolePicker(config.RolePrimary)
	current := tab.modelPicker.ID()
	tab.Update(modelui.PickerResultMsg{PickerID: old, Role: config.RolePrimary, Binding: config.ModelBinding{Connection: "bad", Model: "stale"}}, m.svc, 80, 20)
	if tab.cfg.RoleBinding(config.RolePrimary) != before || tab.modelPicker.ID() != current {
		t.Fatal("cancelled picker changed new draft")
	}
}

func TestSettingsWorkspaceRowsRemainVisibleAtTerminalSizes(t *testing.T) {
	m := recoveryModel()
	tab := m.tabs[tabConfig].(*configTab)
	tab.advancedModels = true
	for _, size := range [][2]int{{80, 20}, {100, 26}, {120, 30}, {144, 36}} {
		for _, section := range []settingsSection{settingsModels, settingsConnections} {
			tab.setSection(section)
			tab.roleCursor = len(tab.modelRoles()) - 1
			view := tab.View(size[0], size[1])
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatalf("height overflow at %v", size)
			}
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("width overflow at %v: %q", size, line)
				}
			}
			if section == settingsModels && !strings.Contains(view, modelui.RoleLabel(config.RoleCuration)) {
				t.Fatalf("selected advanced role hidden at %v", size)
			}
		}
	}
}

func TestCannotRemoveConnectionUsedByInheritedRole(t *testing.T) {
	m := recoveryModel()
	tab := m.tabs[tabConfig].(*configTab)
	tab.setSection(settingsConnections)
	ids := tab.cfg.ConnectionIDs()
	id := ids[0]
	tab.updateWorkspace(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, m.svc)
	if _, ok := tab.cfg.Connections[id]; !ok || tab.saveErr == "" {
		t.Fatal("removed a connection with active roles")
	}
}
