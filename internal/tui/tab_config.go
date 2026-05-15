package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/tools"
)

type configSavedMsg struct {
	err error
}

type configFieldKind int

const (
	configFieldSelect configFieldKind = iota
	configFieldText
	configFieldNumber
	configFieldToggle
)

type configField struct {
	Label       string
	Description string
	Kind        configFieldKind
	Options     []string
	Get         func(*config.Config) string
	Set         func(*config.Config, string)
}

type configTab struct {
	cfg     *config.Config
	cursor  int
	scroll  int
	loaded  bool
	dirty   bool
	message string
	saveErr string
	editing bool
	editIdx int
	input   textinput.Model
	fields  []configField
}

func newConfigTab() tabModel {
	return &configTab{}
}

func (t *configTab) Init(svc *Service) tea.Cmd {
	cfg := cloneTUIConfig(svc.Config())
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	cfg.Normalize()
	t.cfg = cfg
	t.fields = configFields()
	if svc.SetupMode() {
		t.dirty = true
		t.message = "First-run setup: review values, then press s to save"
	}
	t.loaded = true
	return nil
}

func (t *configTab) Consuming() bool { return t.editing }

func (t *configTab) StatusHints() []string {
	if t.editing {
		return []string{
			renderKeyHint("enter", "apply"),
			renderKeyHint("esc", "cancel"),
		}
	}
	hints := []string{
		renderKeyHint("↑↓", "move"),
		renderKeyHint("enter", "edit"),
		renderKeyHint("s", "save"),
		renderKeyHint("r", "reset"),
	}
	if t.dirty {
		hints = append(hints, styleWarning.Render("unsaved"))
	}
	return hints
}

func (t *configTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case configSavedMsg:
		if msg.err != nil {
			t.saveErr = msg.err.Error()
			t.message = ""
			return t, nil
		}
		t.dirty = false
		t.saveErr = ""
		t.message = "Configuration saved"
		t.cfg = cloneTUIConfig(svc.Config())
		return t, nil
	case tea.KeyMsg:
		if t.editing {
			return t.updateEditor(msg)
		}
		return t.updateList(msg, svc)
	}
	return t, nil
}

func (t *configTab) updateList(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Down):
		if t.cursor < len(t.fields)-1 {
			t.cursor++
		}
	case key.Matches(msg, keys.Up):
		if t.cursor > 0 {
			t.cursor--
		}
	case key.Matches(msg, keys.Enter):
		t.beginEdit()
	case msg.String() == "s":
		cfg := cloneTUIConfig(t.cfg)
		return t, func() tea.Msg {
			return configSavedMsg{err: svc.SaveConfig(cfg)}
		}
	case msg.String() == "r":
		t.cfg = cloneTUIConfig(svc.Config())
		if t.cfg == nil {
			t.cfg = config.DefaultConfig()
		}
		t.cfg.Normalize()
		t.dirty = false
		t.message = "Reset to saved values"
		t.saveErr = ""
	}
	return t, nil
}

func (t *configTab) beginEdit() {
	if len(t.fields) == 0 || t.cfg == nil {
		return
	}
	field := t.fields[t.cursor]
	if field.Kind == configFieldToggle {
		current := strings.EqualFold(field.Get(t.cfg), "true")
		field.Set(t.cfg, strconv.FormatBool(!current))
		t.dirty = true
		t.message = field.Label + " updated"
		t.saveErr = ""
		return
	}
	if field.Kind == configFieldSelect {
		current := field.Get(t.cfg)
		next := nextOption(field.Options, current)
		field.Set(t.cfg, next)
		t.dirty = true
		t.message = field.Label + " set to " + next
		t.saveErr = ""
		return
	}

	t.editIdx = t.cursor
	t.input = textinput.New()
	t.input.SetValue(field.Get(t.cfg))
	t.input.Placeholder = field.Label
	t.input.CharLimit = 512
	t.input.Width = 52
	t.input.Focus()
	t.editing = true
	t.message = ""
	t.saveErr = ""
}

func (t *configTab) updateEditor(msg tea.KeyMsg) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		t.editing = false
		return t, nil
	case key.Matches(msg, keys.Enter):
		field := t.fields[t.editIdx]
		value := strings.TrimSpace(t.input.Value())
		if field.Kind == configFieldNumber {
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				t.saveErr = field.Label + " must be a positive number"
				return t, nil
			}
			value = strconv.Itoa(n)
		}
		field.Set(t.cfg, value)
		t.cfg.Normalize()
		t.dirty = true
		t.editing = false
		t.message = field.Label + " updated"
		t.saveErr = ""
		return t, nil
	}

	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	return t, cmd
}

func (t *configTab) View(width, height int) string {
	if !t.loaded {
		return styleMuted.Render("  Loading...")
	}

	if t.editing {
		return t.viewEditor(width)
	}
	return t.viewSettings(width, height)
}

func (t *configTab) viewSettings(width, height int) string {
	var b strings.Builder
	col := width - 4
	if col < 40 {
		col = 40
	}

	b.WriteString(renderPageHeader("Settings", "provider, safety, memory, and runtime behavior", width))
	b.WriteString("  ")
	b.WriteString(styleMuted.Render("State "))
	if t.dirty {
		b.WriteString(renderStatusBadge("unsaved", false))
	} else {
		b.WriteString(renderStatusBadge("saved", true))
	}
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(styleMuted.Render("Press s to write ~/.cvkeharness/config.yaml. Select fields cycle values or open an editor."))
	b.WriteString("\n\n")

	if t.message != "" {
		b.WriteString("  ")
		b.WriteString(styleSuccess.Render(t.message))
		b.WriteString("\n\n")
	}
	if t.saveErr != "" {
		b.WriteString("  ")
		b.WriteString(styleError.Render(t.saveErr))
		b.WriteString("\n\n")
	}

	header := padRight("", 3) + padRight("Setting", 20) + "  " + padRight("Value", 34) + "  " + "Notes"
	b.WriteString(renderTableHeader(width, header))

	listHeight := height - 9
	if listHeight < 4 {
		listHeight = 4
	}
	start, end := listWindow(t.cursor, len(t.fields), listHeight)
	t.scroll = start

	for i := start; i < end; i++ {
		b.WriteString("  ")
		b.WriteString(t.renderFieldRow(i, col, i == t.cursor))
		b.WriteString("\n")
	}
	if hint := scrollHints(start, end, len(t.fields)); hint != "" {
		b.WriteString("  ")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	return b.String()
}

func (t *configTab) renderFieldRow(idx, col int, selected bool) string {
	field := t.fields[idx]
	value := field.Get(t.cfg)
	if strings.Contains(strings.ToLower(field.Label), "key") && value != "" {
		value = maskTUISecret(value)
	}
	row := padRight(field.Label, 20) + "  " + padRight(truncate(value, 34), 34) + "  " + truncate(field.Description, maxInt(col-60, 12))
	if selected {
		return renderSelectableRow(row, true)
	}
	return "  " + styleBase.Render(row)
}

func (t *configTab) viewEditor(width int) string {
	field := t.fields[t.editIdx]
	var b strings.Builder
	b.WriteString(renderPageHeader(field.Label, "edit setting", width))
	b.WriteString("  ")
	b.WriteString(styleMuted.Render(field.Description))
	b.WriteString("\n\n  ")
	b.WriteString(styleInputPrompt.Render("▸ "))
	b.WriteString(styleInputActive.Render(t.input.View()))
	b.WriteString("\n\n  ")
	b.WriteString(styleMuted.Render("enter applies, esc cancels"))
	if t.saveErr != "" {
		b.WriteString("\n\n  ")
		b.WriteString(styleError.Render(t.saveErr))
	}
	return b.String()
}

func configFields() []configField {
	return []configField{
		{
			Label:       "Provider",
			Description: "Runtime provider for new runs and chat sessions",
			Kind:        configFieldSelect,
			Options:     []string{"codex", "openrouter", "openai", "lmstudio"},
			Get:         func(c *config.Config) string { return c.Provider },
			Set:         func(c *config.Config, v string) { c.Provider = v },
		},
		{
			Label:       "Default Model",
			Description: "Primary model used when routing is disabled or undecided",
			Kind:        configFieldText,
			Get:         func(c *config.Config) string { return c.PrimaryModel() },
			Set:         func(c *config.Config, v string) { c.DefaultModel = v },
		},
		{
			Label:       "Connection URL",
			Description: "Base URL for local/OpenAI-compatible providers",
			Kind:        configFieldText,
			Get:         func(c *config.Config) string { return c.BaseURL },
			Set:         func(c *config.Config, v string) { c.BaseURL = v },
		},
		{
			Label:       "Provider API Key",
			Description: "Stored under the currently selected provider",
			Kind:        configFieldText,
			Get:         func(c *config.Config) string { return c.GetAPIKey(c.Provider) },
			Set:         func(c *config.Config, v string) { c.SetAPIKey(c.Provider, v) },
		},
		{
			Label:       "Safety Mode",
			Description: "Command approval gate for non-allowlisted shell commands",
			Kind:        configFieldSelect,
			Options:     []string{tools.SafetyModeLLMJudge, tools.SafetyModeUserConfirmAll, tools.SafetyModeUserConfirm, tools.SafetyModeUnrestricted},
			Get:         func(c *config.Config) string { return c.SafetyMode },
			Set:         func(c *config.Config, v string) { c.SafetyMode = v },
		},
		{
			Label:       "Safety Model",
			Description: "Judge model used when safety mode is llm_judge",
			Kind:        configFieldText,
			Get:         func(c *config.Config) string { return c.SafetyModel },
			Set:         func(c *config.Config, v string) { c.SafetyModel = v },
		},
		{
			Label:       "Routing Enabled",
			Description: "Let the router choose approved models per phase",
			Kind:        configFieldToggle,
			Get:         func(c *config.Config) string { return strconv.FormatBool(c.RoutingEnabled) },
			Set: func(c *config.Config, v string) {
				c.RoutingEnabled = strings.EqualFold(v, "true")
				if c.RoutingEnabled {
					c.RoutingMode = "auto_within_policy"
				} else {
					c.RoutingMode = "disabled"
				}
			},
		},
		{
			Label:       "Max Tokens",
			Description: "Maximum response tokens for model calls",
			Kind:        configFieldNumber,
			Get:         func(c *config.Config) string { return fmt.Sprintf("%d", c.MaxTokens) },
			Set: func(c *config.Config, v string) {
				if n, err := strconv.Atoi(v); err == nil {
					c.MaxTokens = n
				}
			},
		},
		{
			Label:       "Max Iterations",
			Description: "Maximum tool/model loop iterations per run",
			Kind:        configFieldNumber,
			Get:         func(c *config.Config) string { return fmt.Sprintf("%d", c.MaxIterations) },
			Set: func(c *config.Config, v string) {
				if n, err := strconv.Atoi(v); err == nil {
					c.MaxIterations = n
				}
			},
		},
		{
			Label:       "Log Level",
			Description: "Runtime logging verbosity",
			Kind:        configFieldSelect,
			Options:     []string{"off", "error", "warn", "info", "debug"},
			Get:         func(c *config.Config) string { return c.LogLevel },
			Set:         func(c *config.Config, v string) { c.LogLevel = v },
		},
		{
			Label:       "Prompt Dumps",
			Description: "Debug: save full model prompts as Markdown and HTML",
			Kind:        configFieldToggle,
			Get:         func(c *config.Config) string { return strconv.FormatBool(c.DebugPromptDumps) },
			Set:         func(c *config.Config, v string) { c.DebugPromptDumps = strings.EqualFold(v, "true") },
		},
		{
			Label:       "Prompt Dump Dir",
			Description: "Directory for full prompt dump artifacts",
			Kind:        configFieldText,
			Get:         func(c *config.Config) string { return c.PromptDumpDir },
			Set:         func(c *config.Config, v string) { c.PromptDumpDir = v },
		},
		{
			Label:       "Memory Dir",
			Description: "Directory for soul, host, target, and playbook memory",
			Kind:        configFieldText,
			Get:         func(c *config.Config) string { return c.MemoryDir },
			Set:         func(c *config.Config, v string) { c.MemoryDir = v },
		},
		{
			Label:       "State DB",
			Description: "SQLite state path for runs, jobs, approvals, and chat",
			Kind:        configFieldText,
			Get:         func(c *config.Config) string { return c.StateDBPath },
			Set:         func(c *config.Config, v string) { c.StateDBPath = v },
		},
	}
}

func nextOption(options []string, current string) string {
	if len(options) == 0 {
		return current
	}
	for i, option := range options {
		if option == current {
			return options[(i+1)%len(options)]
		}
	}
	return options[0]
}

func cloneTUIConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	out := *cfg
	if cfg.APIKeys != nil {
		out.APIKeys = make(map[string]string, len(cfg.APIKeys))
		for k, v := range cfg.APIKeys {
			out.APIKeys[k] = v
		}
	}
	out.AllowedCommands = append([]string(nil), cfg.AllowedCommands...)
	out.ApprovedModels = append([]string(nil), cfg.ApprovedModels...)
	out.FavoriteModels = append([]string(nil), cfg.FavoriteModels...)
	out.WebSearch.AllowedDomains = append([]string(nil), cfg.WebSearch.AllowedDomains...)
	out.WebSearch.BlockedDomains = append([]string(nil), cfg.WebSearch.BlockedDomains...)
	return &out
}

func maskTUISecret(value string) string {
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", 8) + value[len(value)-4:]
}
