package setuptui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/modelui"
)

func (m *setupModel) ensureConnections() {
	if m.cfg == nil {
		m.cfg = config.DefaultConfig()
	}
	m.cfg.EnsureModelBindings()
}

func (m setupModel) connectionRows() []row {
	var rows []row
	for _, id := range m.cfg.ConnectionIDs() {
		connection, err := m.cfg.ConnectionByID(id)
		if err != nil {
			continue
		}
		detail := connection.Provider
		if connection.BaseURL != "" {
			detail += " · " + connection.BaseURL
		}
		if (connection.Provider == "openai" || connection.Provider == "openrouter") && connection.APIKey == "" {
			detail += " · API key needed"
		}
		rows = append(rows, row{connection.DisplayName(id), detail})
	}
	return append(rows, row{"Add connection", "Name a provider connection, then choose its model"})
}

func (m setupModel) viewConnections() string {
	rows := m.connectionRows()
	visible := max(1, (m.height-14)/3)
	start, end := listWindow(m.cursor, len(rows), visible)
	window := m
	window.cursor -= start
	return m.paragraph("Choose a connection for Primary. Credentials and endpoints stay with each named connection.", "Enter chooses · e edits · a adds a connection") + "\n" + window.renderList(rows[start:end])
}

func (m setupModel) beginConnectionEditor(id string) (setupModel, tea.Cmd) {
	m.ensureConnections()
	connection := config.Connection{}
	if id != "" {
		var err error
		connection, err = m.cfg.ConnectionByID(id)
		if err != nil {
			m.errMessage = err.Error()
			return m, nil
		}
	}
	m.connectionEditor = modelui.NewConnectionEditor(id, connection, m.cfg.ConnectionIDs())
	m.connectionEditor.SetUsage(modelui.ConnectionUsage(m.cfg, id))
	m.step = stepCredentials
	m.message, m.errMessage = "", ""
	return m, m.connectionEditor.Init()
}

func (m setupModel) beginModelPicker(role config.ModelRole, connectionID string) (setupModel, tea.Cmd) {
	m.ensureConnections()
	pickerConfig := m.cfg.Clone()
	if role == config.RolePrimary && connectionID != "" {
		binding := pickerConfig.RoleBinding(role)
		if binding.Connection != connectionID {
			binding = config.ModelBinding{Connection: connectionID}
		}
		pickerConfig.SetRoleBinding(role, binding)
	}
	m.modelPicker = modelui.NewPicker(pickerConfig, role)
	m.message, m.errMessage = "", ""
	if role == config.RolePrimary {
		m.step = stepModel
	} else {
		m.step = stepJudge
	}
	return m, m.modelPicker.Init()
}

func (m setupModel) acceptConnection(msg modelui.ConnectionResultMsg) (setupModel, tea.Cmd) {
	if m.connectionEditor == nil || msg.EditorID != m.connectionEditor.ID() {
		return m, nil
	}
	m.connectionEditor = nil
	m.step = stepProvider
	if msg.Cancelled {
		m.cursor = m.preferredCursor()
		return m, nil
	}
	m.ensureConnections()
	m.cfg.Connections[msg.ID] = msg.Connection
	m.connectionID = msg.ID
	return m.beginModelPicker(config.RolePrimary, msg.ID)
}

func (m setupModel) acceptModel(msg modelui.PickerResultMsg) (setupModel, tea.Cmd) {
	if m.modelPicker == nil || msg.PickerID != m.modelPicker.ID() {
		return m, nil
	}
	role := config.RolePrimary
	if m.step == stepJudge {
		role = m.safetyRole()
	}
	if msg.Role != role {
		return m, nil
	}
	m.modelPicker = nil
	if msg.Cancelled {
		if role == config.RolePrimary {
			m.step = stepProvider
			m.cursor = m.preferredCursor()
			return m, nil
		}
		return m.prevStep()
	}
	m.ensureConnections()
	m.cfg.SetRoleBinding(role, msg.Binding)
	if role == config.RolePrimary {
		m.connectionID = msg.Binding.Connection
	}
	return m.nextStep()
}

func (m setupModel) modelRoleSummary(role config.ModelRole) string {
	binding := m.cfg.RoleBinding(role)
	resolved, err := m.cfg.ResolveRole(role)
	if err != nil {
		return "Not selected"
	}
	text := fmt.Sprintf("%s · %s · %s", resolved.Connection.DisplayName(resolved.ConnectionID), resolved.Connection.Provider, resolved.Model)
	if binding.Inherit != "" {
		text = "Same as " + strings.ReplaceAll(string(binding.Inherit), "_", " ") + " → " + text
	}
	return text
}

func (m setupModel) primaryProvider() string {
	if resolved, err := m.cfg.ResolveRole(config.RolePrimary); err == nil {
		return resolved.Connection.Provider
	}
	return m.cfg.Provider
}

func (m setupModel) modelChildView() string {
	width := max(20, m.width-4)
	height := max(6, m.height-4)
	var body string
	if m.connectionEditor != nil {
		body = m.connectionEditor.View(width, height)
	} else if m.modelPicker != nil {
		body = m.modelPicker.View(width, height)
	}
	return "\n  " + styleTitle.Render("CvkeHarness") + styleMuted.Render("  GUIDED SETUP  ") + styleStep.Render(stageLabels[m.stage()]) + "\n\n" + body
}

func (m setupModel) safetyRole() config.ModelRole {
	if m.cfg.SafetyMode == "llm_advisor" {
		return config.RoleSafetyAdvisor
	}
	return config.RoleSafetyJudge
}

func (m setupModel) usesSafetyModel() bool {
	return m.cfg.SafetyMode == "llm_judge" || m.cfg.SafetyMode == "llm_advisor"
}
