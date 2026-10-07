package modelui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/glebenator/cvkeharness/config"
)

type ConnectionResultMsg struct {
	EditorID   uint64
	OriginalID string
	ID         string
	Connection config.Connection
	Cancelled  bool
}

type ConnectionEditor struct {
	id                     uint64
	inputEpoch             uint64
	originalID             string
	draft                  config.Connection
	original               config.Connection
	existingIDs            []string
	usage                  []string
	cursor, providerCursor int
	mode, field, err       string
	input                  textinput.Model
}

func NewConnectionEditor(id string, connection config.Connection, existingIDs []string) *ConnectionEditor {
	e := &ConnectionEditor{id: nextID.Add(1), originalID: id, draft: connection, existingIDs: append([]string(nil), existingIDs...)}
	if e.draft.Name == "" && id != "" {
		e.draft.Name = id
	}
	e.original = e.draft
	e.input = textinput.New()
	e.input.CharLimit = 4096
	return e
}
func (e *ConnectionEditor) ID() uint64    { return e.id }
func (e *ConnectionEditor) Init() tea.Cmd { return nil }
func (e *ConnectionEditor) inputCmd(cmd tea.Cmd) tea.Cmd {
	return scopedInputCmd(cmd, e.id, e.inputEpoch)
}

// Dirty also includes an unfinished field edit, before Enter keeps it in the
// connection draft. Navigation may preserve this editor while it is hidden.
func (e *ConnectionEditor) Dirty() bool {
	if e.draft != e.original {
		return true
	}
	if e.mode != "input" {
		return false
	}
	var current string
	switch e.field {
	case "Name":
		current = e.draft.Name
	case "Endpoint":
		current = e.draft.BaseURL
	case "API key":
		current = e.draft.APIKey
	case "Login file":
		current = e.draft.AuthFile
	}
	return strings.TrimSpace(e.input.Value()) != current
}

// SetUsage makes the effects of editing a reused connection visible. Provider
// changes require a new connection so existing model IDs retain their meaning.
func (e *ConnectionEditor) SetUsage(labels []string) { e.usage = append([]string(nil), labels...) }
func (e *ConnectionEditor) StatusHints() []string {
	switch e.mode {
	case "input":
		return []string{"Enter keep edit", "Esc cancel edit"}
	case "provider":
		return []string{"↑↓ browse", "Enter choose provider", "Esc back"}
	}
	return []string{"↑↓ select field", "Enter edit / keep", "Esc cancel"}
}
func (e *ConnectionEditor) fields() []string {
	fields := []string{"Name", "Provider"}
	switch e.draft.Provider {
	case "openai", "openrouter", "lmstudio":
		fields = append(fields, "Endpoint", "API key")
	case "codex":
		fields = append(fields, "Login file")
	}
	return append(fields, "Keep connection")
}

var providerIDs = []string{"codex", "openrouter", "openai", "lmstudio"}
var providerDescriptions = map[string]string{
	"codex":      "Reuse a local Codex login",
	"openrouter": "Cloud API with an API key",
	"openai":     "OpenAI API with an API key",
	"lmstudio":   "Local or remote OpenAI-compatible server",
}

func (e *ConnectionEditor) Update(msg tea.Msg) tea.Cmd {
	if input, ok := msg.(inputMsg); ok {
		if input.id != e.id || input.epoch != e.inputEpoch || e.mode != "input" {
			return nil
		}
		msg = input.msg
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if e.mode == "input" {
			var cmd tea.Cmd
			e.input, cmd = e.input.Update(msg)
			return e.inputCmd(cmd)
		}
		return nil
	}
	if e.mode == "input" {
		switch key.String() {
		case "esc":
			e.inputEpoch++
			e.mode = ""
			e.input.Blur()
			e.err = ""
			return nil
		case "enter":
			e.inputEpoch++
			value := strings.TrimSpace(e.input.Value())
			switch e.field {
			case "Name":
				e.draft.Name = value
			case "Endpoint":
				e.draft.BaseURL = value
			case "API key":
				e.draft.APIKey = value
			case "Login file":
				e.draft.AuthFile = value
			}
			e.mode = ""
			e.input.Blur()
			e.err = ""
			return nil
		}
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(key)
		return e.inputCmd(cmd)
	}
	if e.mode == "provider" {
		switch key.String() {
		case "esc":
			e.mode = ""
			return nil
		case "up":
			e.providerCursor = max(0, e.providerCursor-1)
		case "down":
			e.providerCursor = min(len(providerIDs)-1, e.providerCursor+1)
		case "enter":
			provider := providerIDs[e.providerCursor]
			if provider != e.draft.Provider {
				// Never send the previous provider's secret to a newly selected
				// provider. Cancelling the outer editor retains the saved draft.
				e.draft.Provider = provider
				e.draft.APIKey = ""
				e.draft.BaseURL = ""
				e.draft.AuthFile = ""
			}
			e.mode = ""
			e.err = ""
		}
		return nil
	}
	fields := e.fields()
	switch key.String() {
	case "esc":
		return e.finish(true)
	case "up":
		e.cursor = max(0, e.cursor-1)
	case "down":
		e.cursor = min(len(fields)-1, e.cursor+1)
	case "enter":
		field := fields[e.cursor]
		if field == "Keep connection" {
			return e.accept()
		}
		if field == "Provider" {
			if len(e.usage) > 0 {
				e.err = "Used by " + strings.Join(e.usage, ", ") + "; add a new connection to change provider."
				return nil
			}
			e.mode = "provider"
			e.providerCursor = 0
			for i, id := range providerIDs {
				if id == e.draft.Provider {
					e.providerCursor = i
				}
			}
			return nil
		}
		e.field = field
		e.inputEpoch++
		e.mode = "input"
		e.input = textinput.New()
		e.input.CharLimit = 4096
		e.input.Placeholder = field
		switch field {
		case "Name":
			e.input.SetValue(e.draft.Name)
		case "Endpoint":
			e.input.SetValue(e.draft.BaseURL)
			e.input.Placeholder = e.defaultEndpoint()
		case "API key":
			e.input.SetValue(e.draft.APIKey)
			e.input.EchoMode = textinput.EchoPassword
			e.input.EchoCharacter = '•'
		case "Login file":
			e.input.SetValue(e.draft.AuthFile)
			e.input.Placeholder = "Leave blank to use the provider's default login"
		}
		e.input.CursorEnd()
		return e.inputCmd(e.input.Focus())
	}
	return nil
}
func (e *ConnectionEditor) accept() tea.Cmd {
	if strings.TrimSpace(e.draft.Name) == "" {
		e.err = "Give this connection a name"
		return nil
	}
	if err := config.ValidateConnectionDefinition(e.draft); err != nil {
		e.err = err.Error()
		return nil
	}
	return e.finish(false)
}
func (e *ConnectionEditor) finish(cancelled bool) tea.Cmd {
	e.inputEpoch++
	id := e.originalID
	if id == "" {
		base := connectionSlug(e.draft.Name)
		if base == "" {
			base = "connection"
		}
		id = base
		for i := 2; containsID(e.existingIDs, id); i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
	}
	result := ConnectionResultMsg{EditorID: e.id, OriginalID: e.originalID, ID: id, Connection: e.draft, Cancelled: cancelled}
	return func() tea.Msg { return result }
}
func (e *ConnectionEditor) defaultEndpoint() string {
	switch e.draft.Provider {
	case "lmstudio":
		return "http://localhost:1234/v1"
	case "openai":
		return "https://api.openai.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	}
	return ""
}
func (e *ConnectionEditor) value(field string) string {
	switch field {
	case "Name":
		if e.draft.Name == "" {
			return "Give this connection a name"
		}
		return e.draft.Name
	case "Provider":
		if e.draft.Provider == "" {
			return "Choose a provider"
		}
		return e.draft.Provider
	case "Endpoint":
		if e.draft.BaseURL == "" {
			return e.defaultEndpoint() + " (default)"
		}
		return e.draft.BaseURL
	case "API key":
		if e.draft.APIKey != "" {
			return "Configured · hidden"
		}
		if e.draft.Provider == "lmstudio" {
			return "Optional · not configured"
		}
		return "Not configured"
	case "Login file":
		if e.draft.AuthFile == "" {
			return "Use provider's default login file"
		}
		return e.draft.AuthFile
	case "Keep connection":
		return "Return with this draft; save after review"
	}
	return ""
}
func (e *ConnectionEditor) View(width, height int) string {
	w := max(1, width-4)
	title := "New connection"
	if e.originalID != "" {
		title = "Edit connection"
	}
	lines := []string{"  " + titleStyle.Render(title)}
	footer := wrapped(strings.Join(e.StatusHints(), "  "), w)
	var errors []string
	if e.err != "" {
		errors = wrapped(clean(e.err), w)
	}
	if len(e.usage) > 0 {
		lines = append(lines, "  "+mutedStyle.Render(clipped("Used by "+strings.Join(e.usage, ", "), w)))
	}
	if e.mode == "provider" {
		lines = append(lines, "  "+mutedStyle.Render("Choose provider"), "")
		start, end := window(e.providerCursor, len(providerIDs), max(1, height-len(lines)-3))
		for i := start; i < end; i++ {
			label := providerIDs[i]
			if label == e.draft.Provider {
				label += "  ✓ current"
			}
			lines = append(lines, "  "+selectedRow(label, i == e.providerCursor, w))
		}
		lines = append(lines, "  "+mutedStyle.Render(clipped(providerDescriptions[providerIDs[e.providerCursor]], w)))
	} else if e.mode == "input" {
		lines = append(lines, "", "  "+bodyStyle.Render(e.field))
		e.input.Width = max(1, w-2)
		lines = append(lines, "  "+e.input.View())
		if e.field == "API key" {
			lines = append(lines, "  "+mutedStyle.Render(clipped("Hidden while editing. Empty clears the stored key.", w)))
		}
	} else {
		fields := e.fields()
		e.cursor = min(e.cursor, len(fields)-1)
		lines = append(lines, "  "+mutedStyle.Render(clipped("Connection details are saved only after review.", w)), "")
		slots := max(1, (height-len(lines)-len(footer)-len(errors)-1)/2)
		start, end := window(e.cursor, len(fields), slots)
		for i := start; i < end; i++ {
			lines = append(lines, "  "+selectedRow(fields[i], i == e.cursor, w), "    "+mutedStyle.Render(clipped(e.value(fields[i]), max(1, w-2))))
		}
	}
	for _, line := range errors {
		lines = append(lines, "  "+errorStyle.Render(line))
	}
	for _, line := range footer {
		lines = append(lines, "  "+mutedStyle.Render(line))
	}
	return fit(lines, width, height)
}
func connectionSlug(value string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(value) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteRune('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
func containsID(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}
