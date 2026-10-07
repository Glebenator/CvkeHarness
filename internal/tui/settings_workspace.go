package tui

import (
	"reflect"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/glebenator/cvkeharness/config"
	"github.com/glebenator/cvkeharness/internal/modelui"
)

type settingsSection int

const (
	settingsModels settingsSection = iota
	settingsConnections
	settingsSecurity
	settingsRuntime
)

var settingsSections = []string{"Models", "Connections", "Security", "Runtime"}

func (t *configTab) workspaceHints() []string {
	if t.sidebarFocused {
		return []string{renderKeyHint("↑↓", "section"), renderKeyHint("→/enter", "content"), renderKeyHint("[/]", "section")}
	}
	hints := []string{renderKeyHint("←", "sections"), renderKeyHint("↑↓", "move"), renderKeyHint("enter", "edit"), renderKeyHint("s", "save")}
	if t.section == settingsModels {
		hints = append(hints, renderKeyHint("a", "advanced"), renderKeyHint("c", "connections"))
	} else if t.section == settingsConnections {
		hints = append(hints, renderKeyHint("a", "add"), renderKeyHint("d", "remove unused"))
	}
	return hints
}

func (t *configTab) setSection(section settingsSection) {
	t.section = section
	t.securityOpen = section == settingsSecurity
	t.pendingProfile = ""
	t.resetAllPending = false
}

func (t *configTab) updateSectionNavigation(msg tea.KeyMsg, svc *Service) (bool, tea.Cmd) {
	if msg.String() == "tab" || msg.String() == "shift+tab" {
		t.sidebarFocused = !t.sidebarFocused
		if t.sidebarFocused {
			t.pendingProfile = ""
			t.resetAllPending = false
		}
		return true, nil
	}
	if msg.String() == "left" && !t.securityOpen {
		t.sidebarFocused = true
		return true, nil
	}
	if t.sidebarFocused {
		switch msg.String() {
		case "up", "k":
			t.setSection(settingsSection((int(t.section) + len(settingsSections) - 1) % len(settingsSections)))
		case "down", "j":
			t.setSection(settingsSection((int(t.section) + 1) % len(settingsSections)))
		case "right", "enter":
			t.sidebarFocused = false
		case "[", "]", "c", "m":
			// Retain the existing section shortcuts in either pane.
		default:
			return true, nil
		}
		if msg.String() != "[" && msg.String() != "]" && msg.String() != "c" && msg.String() != "m" {
			return true, nil
		}
	}
	switch msg.String() {
	case "[":
		t.setSection(settingsSection((int(t.section) + len(settingsSections) - 1) % len(settingsSections)))
	case "]":
		t.setSection(settingsSection((int(t.section) + 1) % len(settingsSections)))
	case "c":
		t.setSection(settingsConnections)
	case "m":
		t.setSection(settingsModels)
	default:
		return false, nil
	}
	return true, nil
}

func (t *configTab) modelRoles() []config.ModelRole {
	roles := []config.ModelRole{config.RolePrimary, config.RoleSafetyJudge, config.RoleSafetyAdvisor}
	if t.advancedModels {
		roles = append(roles, config.RoleClassifier, config.RoleVerifier, config.RolePlanning, config.RoleExecution, config.RoleCuration)
	}
	return roles
}

func (t *configTab) openRolePicker(role config.ModelRole) tea.Cmd {
	t.cfg.EnsureModelBindings()
	t.modelPicker = modelui.NewPicker(t.cfg, role)
	t.message, t.saveErr = "", ""
	return t.modelPicker.Init()
}

func (t *configTab) openConnectionEditor(id string) tea.Cmd {
	connection := config.Connection{Provider: "openrouter"}
	if id != "" {
		connection = t.cfg.Connections[id]
	}
	t.connectionEditor = modelui.NewConnectionEditor(id, connection, t.cfg.ConnectionIDs())
	t.connectionEditor.SetUsage(t.connectionRoles(id))
	t.message, t.saveErr = "", ""
	return t.connectionEditor.Init()
}

// Children only return drafts. Applying a child result never persists, changes
// another role, or constructs a runtime client.
func (t *configTab) updateModelChildren(msg tea.Msg) (bool, tea.Cmd) {
	switch result := msg.(type) {
	case modelui.PickerResultMsg:
		if t.modelPicker == nil || result.PickerID != t.modelPicker.ID() {
			return true, nil
		}
		t.modelPicker = nil
		if !result.Cancelled {
			if t.cfg.RoleBinding(result.Role) != result.Binding {
				t.cfg.SetRoleBinding(result.Role, result.Binding)
				t.dirty = true
			}
			t.message = modelui.RoleLabel(result.Role) + " updated; save to apply to new sessions"
		}
		return true, nil
	case modelui.ConnectionResultMsg:
		if t.connectionEditor == nil || result.EditorID != t.connectionEditor.ID() {
			return true, nil
		}
		t.connectionEditor = nil
		if !result.Cancelled {
			previous, existed := t.cfg.Connections[result.ID]
			if !existed || !reflect.DeepEqual(previous, result.Connection) {
				t.cfg.Connections[result.ID] = result.Connection
				t.dirty = true
			}
			t.message = result.Connection.DisplayName(result.ID) + " updated; save to apply"
		}
		return true, nil
	}
	if modelui.IsMessage(msg) {
		if t.modelPicker != nil {
			return true, t.modelPicker.Update(msg)
		}
		if t.connectionEditor != nil {
			return true, t.connectionEditor.Update(msg)
		}
		return true, nil
	}
	return false, nil
}

func (t *configTab) updateWorkspace(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	switch msg.String() {
	case "s":
		return t, t.saveSettings(svc)
	case "r":
		return t.updateList(msg, svc)
	case "a":
		if t.section == settingsConnections {
			return t, t.openConnectionEditor("")
		}
		t.advancedModels = !t.advancedModels
		t.roleCursor = minInt(t.roleCursor, len(t.modelRoles()))
	}
	if t.section == settingsModels {
		roles := t.modelRoles()
		switch msg.String() {
		case "down", "j":
			t.roleCursor = minInt(t.roleCursor+1, len(roles))
		case "up", "k":
			t.roleCursor = maxInt(t.roleCursor-1, 0)
		case "enter":
			if t.roleCursor == len(roles) {
				t.advancedModels = !t.advancedModels
				t.roleCursor = minInt(t.roleCursor, len(t.modelRoles()))
			} else {
				return t, t.openRolePicker(roles[t.roleCursor])
			}
		}
	} else if t.section == settingsConnections {
		ids := t.cfg.ConnectionIDs()
		t.connectionCursor = clamp(t.connectionCursor, 0, len(ids))
		switch msg.String() {
		case "down", "j":
			t.connectionCursor = minInt(t.connectionCursor+1, len(ids))
		case "up", "k":
			t.connectionCursor = maxInt(t.connectionCursor-1, 0)
		case "enter":
			id := ""
			if t.connectionCursor < len(ids) {
				id = ids[t.connectionCursor]
			}
			return t, t.openConnectionEditor(id)
		case "d":
			if t.connectionCursor < len(ids) {
				id := ids[t.connectionCursor]
				if uses := t.connectionRoles(id); len(uses) != 0 {
					t.saveErr = "Reassign " + strings.Join(uses, ", ") + " before removing this connection"
				} else {
					delete(t.cfg.Connections, id)
					t.dirty = true
					t.message = "Connection removed from draft; r restores saved settings"
				}
			}
		}
	}
	return t, nil
}

func (t *configTab) saveSettings(svc *Service) tea.Cmd {
	cfg := t.cfg.Clone()
	if err := cfg.ValidateConnection(); err != nil {
		t.saveErr = err.Error()
		return nil
	}
	t.saving = true
	return func() tea.Msg { return configSavedMsg{err: svc.SaveConfig(cfg)} }
}

func (t *configTab) workspaceHeader(title, subtitle string, width int) []string {
	state := "Settings saved"
	if t.dirty {
		state = "Unsaved changes · s Save"
	}
	lines := []string{styleBright.Render(title), styleMuted.Render(subtitle), "", styleMuted.Render(state)}
	if t.configurationOnly {
		lines = append(lines, styleMuted.Render("Configuration only · reopen console after saving"))
	}
	if t.message != "" {
		lines = append(lines, styleSuccess.Render(t.message))
	}
	if t.saveErr != "" {
		lines = append(lines, styleError.Render(t.saveErr))
	}
	return append(lines, "", horizontalRule(maxInt(width-4, 1)))
}

func (t *configTab) viewModelRoles(width, height int) string {
	lines := t.workspaceHeader("Models", "Assign a connection and model to each role.", width)
	roles := t.modelRoles()
	// Each role occupies three physical rows. Window by selection so advanced
	// roles remain reachable in a short terminal rather than clipped away.
	slots := maxInt((height-len(lines)-4)/3, 1)
	start, end := listWindow(t.roleCursor, len(roles)+1, slots)
	for i := start; i < end; i++ {
		if i == len(roles) {
			label := "▸ Advanced model roles"
			if t.advancedModels {
				label = "▾ Hide advanced model roles"
			}
			lines = append(lines, renderSelectableRow(label, i == t.roleCursor && !t.sidebarFocused), "", "")
			continue
		}
		role := roles[i]
		label := modelui.RoleLabel(role)
		binding := t.cfg.RoleBinding(role)
		if binding.Inherit != "" {
			label += " · Uses " + modelui.RoleLabel(binding.Inherit)
		}
		value := "Choose a connection and model"
		if resolved, err := t.cfg.ResolveRole(role); err == nil {
			value = resolved.Connection.DisplayName(resolved.ConnectionID) + " / " + resolved.Model
			if role == config.RoleVerifier && t.cfg.VerifierInheritsExecution() {
				value = "Follows the model actually used for execution"
			}
		}
		lines = append(lines, renderSelectableRow(label, i == t.roleCursor && !t.sidebarFocused), "  "+styleMuted.Render(value), "")
	}
	if hint := scrollHints(start, end, len(roles)+1); hint != "" {
		lines = append(lines, hint)
	}
	if t.roleCursor < len(roles) {
		lines = append(lines, styleMuted.Render(modelRoleDescription(roles[t.roleCursor])))
	}
	lines = append(lines, "Different roles can use different providers.", "c Connections · a Advanced · s Save")
	return settingsFit(lines, width, height)
}

func modelRoleDescription(role config.ModelRole) string {
	switch role {
	case config.RolePrimary:
		return "Default model for conversation, planning, and execution."
	case config.RoleSafetyJudge:
		return "Advisory reviews for security controls set to LLM review."
	case config.RoleSafetyAdvisor:
		return "Explains pending actions and recommends approve or reject in LLM advisor mode; you decide."
	case config.RoleClassifier:
		return "Classifies tasks while LLM judge mode is active."
	case config.RoleVerifier:
		return "Checks the evidence that a task was completed."
	case config.RolePlanning:
		return "Creates plans when phase routing is enabled."
	case config.RoleExecution:
		return "Performs agent work in runs and console chat."
	case config.RoleCuration:
		return "Used by LLM memory curators; structured curation is local."
	}
	return ""
}

func (t *configTab) connectionRoles(id string) []string {
	return modelui.ConnectionUsage(t.cfg, id)
}

func (t *configTab) hasPendingEdit() bool {
	if t.connectionEditor != nil && t.connectionEditor.Dirty() {
		return true
	}
	if t.modelPicker != nil && t.modelPicker.Dirty() {
		return true
	}
	return t.editing && t.editIdx >= 0 && t.editIdx < len(t.fields) && t.input.Value() != t.fields[t.editIdx].Get(t.cfg)
}

func (t *configTab) viewConnections(width, height int) string {
	lines := t.workspaceHeader("Connections", "Save provider access once; reuse it across roles.", width)
	ids := t.cfg.ConnectionIDs()
	slots := maxInt((height-len(lines)-4)/3, 1)
	t.connectionCursor = clamp(t.connectionCursor, 0, len(ids))
	start, end := listWindow(t.connectionCursor, len(ids)+1, slots)
	for i := start; i < end; i++ {
		if i == len(ids) {
			lines = append(lines, renderSelectableRow("+ Add connection", i == t.connectionCursor && !t.sidebarFocused), "", "")
			continue
		}
		id := ids[i]
		connection, _ := t.cfg.ConnectionByID(id)
		detail := connection.Provider
		if connection.BaseURL != "" {
			detail += " · " + connection.BaseURL
		} else if connection.Provider == "codex" {
			detail += " · local login file"
		} else if connection.APIKey != "" {
			detail += " · API key stored"
		} else if connection.Provider != "lmstudio" {
			detail += " · API key missing"
		}
		uses := "Not assigned"
		if roles := t.connectionRoles(id); len(roles) != 0 {
			uses = "Used by " + strings.Join(roles, ", ")
		}
		lines = append(lines, renderSelectableRow(connection.DisplayName(id)+" · "+id, i == t.connectionCursor && !t.sidebarFocused), "  "+styleMuted.Render(detail), "  "+styleMuted.Render(uses))
	}
	if hint := scrollHints(start, end, len(ids)+1); hint != "" {
		lines = append(lines, hint)
	}
	lines = append(lines, "", "Editing a connection affects every role that uses it.", "a Add · Enter Edit · s Save · r Reset draft")
	return settingsFit(lines, width, height)
}

func settingsFit(lines []string, width, height int) string {
	for i := range lines {
		lines[i] = "  " + truncate(lines[i], maxInt(width-4, 1))
	}
	return strings.Join(lines[:minInt(len(lines), maxInt(height, 1))], "\n")
}

func (t *configTab) viewWorkspace(width, height int) string {
	if width < 100 {
		var sections []string
		for i, name := range settingsSections {
			if settingsSection(i) == t.section {
				if t.sidebarFocused {
					name = styleSelectedRow.Render("▸ " + name)
				} else {
					name = styleBright.Render("• " + name)
				}
			}
			sections = append(sections, name)
		}
		return "  " + truncate(strings.Join(sections, " · "), maxInt(width-4, 1)) + "\n" + t.viewSettingsContent(width, maxInt(height-1, 1))
	}
	sideWidth := 18
	var nav []string
	for i, name := range settingsSections {
		selected := settingsSection(i) == t.section
		if selected && !t.sidebarFocused {
			nav = append(nav, "  "+styleBright.Render("• "+name), "")
		} else {
			nav = append(nav, "  "+renderSelectableRow(name, selected), "")
		}
	}
	if t.sidebarFocused {
		nav = append(nav, "  ↑↓ section", "  →/Enter content")
	} else if t.securityOpen {
		nav = append(nav, "  Tab sections")
	} else {
		nav = append(nav, "  ←/Tab sections")
	}
	side := lipgloss.NewStyle().Width(sideWidth).Height(height).Render(strings.Join(nav, "\n"))
	divider := strings.TrimSuffix(strings.Repeat(styleMuted.Render("│")+"\n", height), "\n")
	content := t.viewSettingsContent(width-sideWidth-1, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, side, divider, content)
}
