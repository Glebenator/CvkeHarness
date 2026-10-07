package modelui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/glebenator/cvkeharness/config"
	"github.com/glebenator/cvkeharness/internal/modelcatalog"
)

type PickerResultMsg struct {
	PickerID  uint64
	Role      config.ModelRole
	Binding   config.ModelBinding
	Cancelled bool
}
type catalogMsg struct {
	id, epoch  uint64
	connection string
	result     modelcatalog.ModelResult
}
type pickerRow struct {
	label, description, model string
	inherit                   config.ModelRole
	custom                    bool
}

type Picker struct {
	id, epoch                        uint64
	inputEpoch                       uint64
	cfg                              *config.Config
	role                             config.ModelRole
	original                         config.ModelBinding
	connection                       string
	search, custom, connectionSearch textinput.Model
	mode                             string
	cursor, connectionCursor         int
	loading                          bool
	touched, selectedInitial         bool
	result                           modelcatalog.ModelResult
	err                              string
	cancel                           context.CancelFunc
	loader                           func(context.Context, config.Connection) modelcatalog.ModelResult
}

func NewPicker(cfg *config.Config, role config.ModelRole) *Picker {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	draft := cfg.Clone()
	draft.EnsureModelBindings()
	p := &Picker{id: nextID.Add(1), cfg: draft, role: role, original: draft.RoleBinding(role), loader: modelcatalog.Fetch}
	p.search = textinput.New()
	p.search.Placeholder = "Search model ID or name"
	p.search.CharLimit = 256
	p.search.Focus()
	p.connectionSearch = textinput.New()
	p.connectionSearch.Placeholder = "Search connections"
	p.connectionSearch.CharLimit = 256
	p.custom = textinput.New()
	p.custom.Placeholder = "Exact model ID"
	p.custom.CharLimit = 512
	p.connection = p.original.Connection
	if resolved, err := draft.ResolveRole(role); err == nil {
		p.connection = resolved.ConnectionID
	}
	if p.connection == "" {
		if ids := draft.ConnectionIDs(); len(ids) > 0 {
			p.connection = ids[0]
		}
	}
	return p
}
func (p *Picker) ID() uint64                   { return p.id }
func (p *Picker) Init() tea.Cmd                { return tea.Batch(p.inputCmd(textinput.Blink), p.load()) }
func (p *Picker) inputCmd(cmd tea.Cmd) tea.Cmd { return scopedInputCmd(cmd, p.id, p.inputEpoch) }

// Dirty reports a pending model choice, not a transient catalog search.
func (p *Picker) Dirty() bool {
	initialConnection := p.original.Connection
	if resolved, err := p.cfg.ResolveRole(p.role); err == nil {
		initialConnection = resolved.ConnectionID
	}
	if p.connection != initialConnection {
		return true
	}
	return p.mode == "custom" && strings.TrimSpace(p.custom.Value()) != p.original.Model
}
func (p *Picker) StatusHints() []string {
	switch p.mode {
	case "custom":
		return []string{"Enter choose model", "Esc back"}
	case "connections":
		return []string{"Type to filter", "↑↓ browse", "Enter choose connection", "Esc back"}
	default:
		return []string{"↑↓ browse", "Enter choose", "Ctrl+K connections", "Ctrl+R reload", "Esc cancel"}
	}
}
func (p *Picker) load() tea.Cmd {
	if p.cancel != nil {
		p.cancel()
	}
	p.epoch++
	epoch, id, connectionID := p.epoch, p.id, p.connection
	connection, err := p.cfg.ConnectionByID(connectionID)
	if err != nil {
		p.loading = false
		p.err = "Choose a connection with Ctrl+K"
		return nil
	}
	p.loading = true
	p.err = ""
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	p.cancel = cancel
	loader := p.loader
	return func() tea.Msg {
		defer cancel()
		return catalogMsg{id: id, epoch: epoch, connection: connectionID, result: loader(ctx, connection)}
	}
}
func (p *Picker) Update(msg tea.Msg) tea.Cmd {
	if input, ok := msg.(inputMsg); ok {
		if input.id != p.id || input.epoch != p.inputEpoch {
			return nil
		}
		msg = input.msg
	}
	if result, ok := msg.(catalogMsg); ok {
		if result.id != p.id || result.epoch != p.epoch || result.connection != p.connection {
			return nil
		}
		p.loading = false
		p.result = result.result
		rows := p.rows()
		p.cursor = min(p.cursor, len(rows)-1)
		if !p.selectedInitial && !p.touched && strings.TrimSpace(p.search.Value()) == "" {
			for i, row := range rows {
				if (p.original.Inherit != "" && row.inherit == p.original.Inherit) || (p.original.Connection == p.connection && row.model != "" && row.model == p.original.Model) {
					p.cursor = i
					break
				}
			}
		}
		p.selectedInitial = true
		return nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return p.updateInput(msg)
	}
	if p.mode == "custom" {
		return p.updateCustom(key)
	}
	if p.mode == "connections" {
		return p.updateConnections(key)
	}
	switch key.String() {
	case "esc":
		return p.finish(p.original, true)
	case "ctrl+k":
		p.touched = true
		p.inputEpoch++
		p.mode = "connections"
		p.connectionCursor = 0
		p.search.Blur()
		return p.inputCmd(p.connectionSearch.Focus())
	case "ctrl+r":
		return p.load()
	case "up":
		p.touched = true
		p.cursor = max(0, p.cursor-1)
		return nil
	case "down":
		p.touched = true
		p.cursor = min(len(p.rows())-1, p.cursor+1)
		return nil
	case "pgup":
		p.touched = true
		p.cursor = max(0, p.cursor-8)
		return nil
	case "pgdown":
		p.touched = true
		p.cursor = min(len(p.rows())-1, p.cursor+8)
		return nil
	case "enter":
		p.touched = true
		rows := p.rows()
		if len(rows) == 0 {
			return nil
		}
		row := rows[p.cursor]
		if row.custom {
			p.inputEpoch++
			p.mode = "custom"
			value := strings.TrimSpace(p.search.Value())
			if value == "" && p.original.Connection == p.connection {
				value = p.original.Model
			}
			p.custom.SetValue(value)
			p.custom.CursorEnd()
			p.search.Blur()
			return p.inputCmd(p.custom.Focus())
		}
		if row.inherit != "" {
			return p.choose(config.ModelBinding{Inherit: row.inherit})
		}
		return p.choose(config.ModelBinding{Connection: p.connection, Model: row.model})
	}
	return p.updateInput(key)
}

func (p *Picker) updateInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch p.mode {
	case "custom":
		p.custom, cmd = p.custom.Update(msg)
	case "connections":
		before := p.connectionSearch.Value()
		p.connectionSearch, cmd = p.connectionSearch.Update(msg)
		if before != p.connectionSearch.Value() {
			p.connectionCursor = 0
		}
	default:
		before := p.search.Value()
		p.search, cmd = p.search.Update(msg)
		if before != p.search.Value() {
			p.touched = true
			p.cursor = 0
		}
	}
	return p.inputCmd(cmd)
}
func (p *Picker) updateCustom(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		p.inputEpoch++
		p.mode = ""
		p.custom.Blur()
		p.err = ""
		return p.inputCmd(p.search.Focus())
	case "enter":
		model := strings.TrimSpace(p.custom.Value())
		if model == "" {
			p.err = "Enter a model ID or press Esc to go back"
			return nil
		}
		return p.choose(config.ModelBinding{Connection: p.connection, Model: model})
	}
	return p.updateInput(key)
}
func (p *Picker) updateConnections(key tea.KeyMsg) tea.Cmd {
	ids := p.connectionIDs()
	switch key.String() {
	case "esc", "ctrl+k":
		p.inputEpoch++
		p.mode = ""
		p.connectionSearch.Blur()
		return p.inputCmd(p.search.Focus())
	case "up":
		p.connectionCursor = max(0, p.connectionCursor-1)
		return nil
	case "down":
		p.connectionCursor = max(0, min(len(ids)-1, p.connectionCursor+1))
		return nil
	case "enter":
		if len(ids) == 0 {
			return nil
		}
		p.connection = ids[p.connectionCursor]
		p.inputEpoch++
		p.mode = ""
		p.result = modelcatalog.ModelResult{}
		p.search.SetValue("")
		p.cursor = 0
		p.connectionSearch.Blur()
		return tea.Batch(p.inputCmd(p.search.Focus()), p.load())
	}
	return p.updateInput(key)
}
func (p *Picker) choose(binding config.ModelBinding) tea.Cmd {
	draft := p.cfg.Clone()
	draft.SetRoleBinding(p.role, binding)
	if _, err := draft.ResolveRole(p.role); err != nil {
		p.err = err.Error()
		return nil
	}
	return p.finish(binding, false)
}
func (p *Picker) finish(binding config.ModelBinding, cancelled bool) tea.Cmd {
	if p.cancel != nil {
		p.cancel()
	}
	p.epoch++
	p.inputEpoch++
	result := PickerResultMsg{PickerID: p.id, Role: p.role, Binding: binding, Cancelled: cancelled}
	return func() tea.Msg { return result }
}
func (p *Picker) rows() []pickerRow {
	query := strings.ToLower(strings.TrimSpace(p.search.Value()))
	var rows []pickerRow
	if query == "" && p.role != config.RolePrimary {
		roles := []config.ModelRole{config.RolePrimary}
		if p.role == config.RoleClassifier {
			roles = append(roles, config.RoleSafetyJudge)
		}
		if p.role == config.RoleVerifier {
			roles = append(roles, config.RoleExecution)
		}
		if p.original.Inherit != "" && p.original.Inherit != p.role && !hasRole(roles, p.original.Inherit) {
			roles = append(roles, p.original.Inherit)
		}
		for _, role := range roles {
			resolved, err := p.cfg.ResolveRole(role)
			if err != nil {
				continue
			}
			description := resolved.Connection.DisplayName(resolved.ConnectionID) + " · " + resolved.Model
			label := "Use " + RoleLabel(role)
			if p.original.Inherit == role {
				label += "  ✓ current"
			}
			rows = append(rows, pickerRow{label: label, description: description, inherit: role})
		}
	}
	items := append([]modelcatalog.ModelOption(nil), p.result.Items...)
	current := ""
	if p.original.Connection == p.connection {
		current = p.original.Model
	}
	found := false
	for _, item := range items {
		found = found || item.ID == current
	}
	if current != "" && !found {
		items = append([]modelcatalog.ModelOption{{ID: current, Description: "Configured model; not listed in this catalog"}}, items...)
	}
	for _, item := range items {
		if item.ID == modelcatalog.CustomModelID || !strings.Contains(strings.ToLower(item.ID+" "+item.Description), query) {
			continue
		}
		label := item.ID
		if item.ID == current {
			label += "  ✓ current"
		}
		rows = append(rows, pickerRow{label: label, description: item.Description, model: item.ID})
	}
	return append(rows, pickerRow{label: "Enter a custom model ID…", description: "Use an exact provider-native ID", custom: true})
}
func (p *Picker) connectionIDs() []string {
	query := strings.ToLower(strings.TrimSpace(p.connectionSearch.Value()))
	var ids []string
	for _, id := range p.cfg.ConnectionIDs() {
		conn, _ := p.cfg.ConnectionByID(id)
		if strings.Contains(strings.ToLower(id+" "+conn.Name+" "+conn.Provider+" "+conn.BaseURL), query) {
			ids = append(ids, id)
		}
	}
	return ids
}
func (p *Picker) View(width, height int) string {
	w := max(1, width-4)
	var lines []string
	add := func(s string) { lines = append(lines, "  "+s) }
	footer := wrapped(strings.Join(p.StatusHints(), "  "), w)
	add(titleStyle.Render("Choose " + RoleLabel(p.role) + " model"))
	connection, _ := p.cfg.ConnectionByID(p.connection)
	add(mutedStyle.Render(clipped("Connection: "+connection.DisplayName(p.connection)+" · "+connection.Provider, w)))
	if p.mode == "connections" {
		return p.viewConnections(width, height)
	}
	if p.mode == "custom" {
		add("")
		add(bodyStyle.Render("Exact model ID"))
		p.custom.Width = max(1, w-2)
		add(p.custom.View())
		add(mutedStyle.Render(clipped("Slashes are preserved as part of the model ID.", w)))
	} else {
		source := "Loading model catalog…"
		if !p.loading {
			source = "Catalog: " + p.result.Source
			if p.result.Source == "codex-cache" {
				if p.result.Live {
					source = "Catalog: recent account cache"
				} else {
					source = "Catalog: account cache · may be outdated"
				}
			} else if !p.result.Live {
				source += " · fallback"
			}
			if !p.result.Timestamp.IsZero() {
				source += " · " + p.result.Timestamp.Local().Format("Jan 2 15:04")
			}
		}
		add(mutedStyle.Render(clipped(source, w)))
		if p.result.Message != "" && !p.loading {
			add(mutedStyle.Render(clipped(p.result.Message, w)))
		}
		p.search.Width = max(1, w-2)
		add(p.search.View())
		add("")
		rows := p.rows()
		p.cursor = max(0, min(p.cursor, len(rows)-1))
		slots := max(1, height-len(lines)-len(footer)-3)
		start, end := window(p.cursor, len(rows), slots)
		for i := start; i < end; i++ {
			add(selectedRow(rows[i].label, i == p.cursor, w))
		}
		add(mutedStyle.Render(clipped(rows[p.cursor].description, w)))
		add(mutedStyle.Render(fmt.Sprintf("%d choices · %d/%d", len(rows), p.cursor+1, len(rows))))
	}
	if p.err != "" {
		add(errorStyle.Render(clipped(p.err, w)))
	}
	for _, line := range footer {
		add(mutedStyle.Render(line))
	}
	return fit(lines, width, height)
}
func (p *Picker) viewConnections(width, height int) string {
	w := max(1, width-4)
	lines := []string{"  " + titleStyle.Render("Choose connection for "+RoleLabel(p.role))}
	p.connectionSearch.Width = max(1, w-2)
	lines = append(lines, "  "+p.connectionSearch.View(), "")
	ids := p.connectionIDs()
	slots := max(1, height-len(lines)-3)
	start, end := window(p.connectionCursor, len(ids), slots)
	for i := start; i < end; i++ {
		c, _ := p.cfg.ConnectionByID(ids[i])
		label := c.DisplayName(ids[i]) + " · " + c.Provider
		if ids[i] == p.connection {
			label += "  ✓ current"
		}
		lines = append(lines, "  "+selectedRow(label, i == p.connectionCursor, w))
	}
	if len(ids) == 0 {
		lines = append(lines, "  "+mutedStyle.Render(clipped("No connections match. Add one in Connections.", w)))
	}
	lines = append(lines, "  "+mutedStyle.Render(clipped("↑↓ browse  Enter choose  Esc back", w)))
	return fit(lines, width, height)
}
func hasRole(roles []config.ModelRole, wanted config.ModelRole) bool {
	for _, r := range roles {
		if r == wanted {
			return true
		}
	}
	return false
}
