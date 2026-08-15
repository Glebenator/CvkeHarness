package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/securitypolicy"
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
	configFieldSecurity
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
	cfg             *config.Config
	cursor          int
	scroll          int
	loaded          bool
	dirty           bool
	message         string
	saveErr         string
	editing         bool
	editIdx         int
	input           textinput.Model
	fields          []configField
	securityOpen    bool
	securityCursor  int
	pendingProfile  securitypolicy.Profile
	resetAllPending bool
}

func newConfigTab() tabModel {
	return &configTab{}
}

func (t *configTab) Init(svc *Service) tea.Cmd {
	// Tick refreshes call Init on the active tab. Never replace an in-progress
	// editor with the last saved config; that silently discards user changes.
	if t.loaded && (t.dirty || t.editing || t.securityOpen) {
		return nil
	}
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

func (t *configTab) Consuming() bool { return t.editing || t.securityOpen }

func (t *configTab) StatusHints() []string {
	if t.editing {
		return []string{
			renderKeyHint("enter", "apply"),
			renderKeyHint("esc", "cancel"),
		}
	}
	if t.securityOpen {
		hints := []string{
			renderKeyHint("↑↓", "move"),
			renderKeyHint("←→", "change"),
			renderKeyHint("r", "reset"),
			renderKeyHint("R", "reset all"),
			renderKeyHint("s", "save"),
			renderKeyHint("esc", "back"),
		}
		if t.dirty {
			hints = append(hints, styleWarning.Render("unsaved"))
		}
		return hints
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
		if t.securityOpen {
			return t.updateSecurity(msg, svc)
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

func (t *configTab) updateSecurity(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	catalog := securitypolicy.Catalog()
	count := len(catalog) + 1
	switch {
	case key.Matches(msg, keys.Back):
		t.securityOpen = false
		t.pendingProfile = ""
		t.resetAllPending = false
		t.message = "Security editor closed; press s to save pending changes"
	case key.Matches(msg, keys.Down):
		t.securityCursor = (t.securityCursor + 1) % count
		t.pendingProfile = ""
		t.resetAllPending = false
	case key.Matches(msg, keys.Up):
		t.securityCursor = (t.securityCursor - 1 + count) % count
		t.pendingProfile = ""
		t.resetAllPending = false
	case key.Matches(msg, keys.Left):
		t.cycleSecurityValue(-1)
	case key.Matches(msg, keys.Right), msg.String() == " ":
		t.cycleSecurityValue(1)
	case key.Matches(msg, keys.Enter):
		if t.securityCursor == 0 {
			if t.pendingProfile == "" {
				t.pendingProfile = nextSecurityProfile(t.cfg.Security.Profile, 1)
				t.message = profileConfirmation(t.pendingProfile, len(t.cfg.Security.Overrides))
				return t, nil
			}
			_ = t.cfg.Security.ApplyProfile(t.pendingProfile)
			t.cfg.Normalize()
			t.dirty = true
			t.message = "Applied " + strings.ReplaceAll(string(t.pendingProfile), "_", " ") + " and cleared previous overrides"
			t.pendingProfile = ""
			return t, nil
		}
		t.cycleSecurityValue(1)
	case msg.String() == "r":
		if t.securityCursor > 0 {
			setting := catalog[t.securityCursor-1]
			t.cfg.Security.ClearOverride(setting.ID)
			t.cfg.Normalize()
			t.dirty = true
			t.message = setting.Label + " reset to profile value"
		}
	case msg.String() == "R":
		if len(t.cfg.Security.Overrides) == 0 {
			t.message = "No security overrides to reset"
			return t, nil
		}
		if !t.resetAllPending {
			t.resetAllPending = true
			t.message = "Press R again to clear all security overrides"
			return t, nil
		}
		t.cfg.Security.Overrides = nil
		t.cfg.Normalize()
		t.dirty = true
		t.resetAllPending = false
		t.message = "All security controls reset to the selected profile"
	case msg.String() == "s":
		cfg := cloneTUIConfig(t.cfg)
		return t, func() tea.Msg { return configSavedMsg{err: svc.SaveConfig(cfg)} }
	}
	return t, nil
}

func (t *configTab) cycleSecurityValue(delta int) {
	if t.cfg == nil || t.cfg.Security == nil {
		return
	}
	if t.securityCursor == 0 {
		current := t.cfg.Security.Profile
		if t.pendingProfile != "" {
			current = t.pendingProfile
		}
		t.pendingProfile = nextSecurityProfile(current, delta)
		t.message = profileConfirmation(t.pendingProfile, len(t.cfg.Security.Overrides))
		return
	}
	catalog := securitypolicy.Catalog()
	setting := catalog[t.securityCursor-1]
	effective, err := t.cfg.EffectiveSecurity()
	if err != nil {
		t.saveErr = err.Error()
		return
	}
	next := securitypolicy.NextValue(setting, effective.Value(setting.ID), delta)
	if err := t.cfg.Security.SetOverride(setting.ID, next); err != nil {
		t.saveErr = err.Error()
		return
	}
	t.cfg.Normalize()
	t.dirty = true
	t.resetAllPending = false
	t.message = setting.Label + " set to " + next + " (override)"
}

func nextSecurityProfile(current securitypolicy.Profile, delta int) securitypolicy.Profile {
	profiles := securitypolicy.Profiles()
	for index, profile := range profiles {
		if profile.ID == current {
			return profiles[(index+delta+len(profiles))%len(profiles)].ID
		}
	}
	return securitypolicy.ProfileReasonable
}

func profileConfirmation(profile securitypolicy.Profile, overrides int) string {
	name := strings.ReplaceAll(string(profile), "_", " ")
	if profile == securitypolicy.ProfileYOLO {
		return fmt.Sprintf("YOLO disables CvkeHarness approval and deletion gates. Press Enter to apply and clear %d override(s)", overrides)
	}
	return fmt.Sprintf("Press Enter to apply %s and clear %d override(s)", name, overrides)
}

func (t *configTab) beginEdit() {
	if len(t.fields) == 0 || t.cfg == nil {
		return
	}
	field := t.fields[t.cursor]
	if field.Kind == configFieldSecurity {
		t.securityOpen = true
		t.securityCursor = 0
		t.pendingProfile = ""
		t.resetAllPending = false
		t.message = "Security changes apply to new sessions after saving"
		return
	}
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
	if t.securityOpen {
		return t.viewSecurity(width, height)
	}
	return t.viewSettings(width, height)
}

func (t *configTab) viewSettings(width, height int) string {
	var b strings.Builder
	col := width - 4
	if col < 40 {
		col = 40
	}

	b.WriteString(renderPageHeader("Settings", "provider, security, memory, and runtime behavior", width))
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

func (t *configTab) viewSecurity(width, height int) string {
	var b strings.Builder
	effective, err := t.cfg.EffectiveSecurity()
	if err != nil {
		return renderPageHeader("Settings / Security", "invalid policy", width) + "  " + styleError.Render(err.Error())
	}
	b.WriteString(renderPageHeader("Settings / Security", "profiles and per-control overrides", width))
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Effective", effective.Summary()))
	b.WriteString("    ")
	b.WriteString(renderKeyValue("Policy", effective.Hash))
	b.WriteString("\n")
	for _, line := range wrapText("Profile changes require Enter and clear overrides. YOLO does not bypass OS or provider protections.", maxInt(width-6, 24)) {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if t.message != "" {
		for _, line := range wrapText(t.message, maxInt(width-6, 24)) {
			b.WriteString("  ")
			b.WriteString(styleAccent.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if t.saveErr != "" {
		for _, line := range wrapText(t.saveErr, maxInt(width-6, 24)) {
			b.WriteString("  ")
			b.WriteString(styleError.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	header := padRight("", 3) + padRight("Area", 20) + "  " + padRight("Control", 24) + "  " + padRight("Value", 16) + "  Source"
	b.WriteString(renderTableHeader(width, header))
	total := len(securitypolicy.Catalog()) + 1
	listHeight := height - 11
	if listHeight < 4 {
		listHeight = 4
	}
	start, end := listWindow(t.securityCursor, total, listHeight)
	for index := start; index < end; index++ {
		selected := index == t.securityCursor
		var area, label, value, origin string
		if index == 0 {
			area, label, value, origin = "Preset", "Security profile", string(t.cfg.Security.Profile), "selected"
			if t.pendingProfile != "" {
				value = string(t.pendingProfile) + " ?"
				origin = "pending"
			}
		} else {
			setting := securitypolicy.Catalog()[index-1]
			area, label = setting.Category, setting.Label
			value = effective.Value(setting.ID)
			origin = effective.Origins[setting.ID]
		}
		row := padRight(truncate(area, 20), 20) + "  " + padRight(truncate(label, 24), 24) + "  " + padRight(truncate(value, 16), 16) + "  " + origin
		b.WriteString("  ")
		if selected {
			b.WriteString(renderSelectableRow(row, true))
		} else {
			b.WriteString("  " + styleBase.Render(row))
		}
		b.WriteString("\n")
	}
	if hint := scrollHints(start, end, total); hint != "" {
		b.WriteString("  ")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	if t.securityCursor > 0 {
		setting := securitypolicy.Catalog()[t.securityCursor-1]
		b.WriteString("\n")
		for _, line := range wrapText(setting.Description, maxInt(width-6, 24)) {
			b.WriteString("  ")
			b.WriteString(styleMuted.Render(line))
			b.WriteString("\n")
		}
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
			Label:       "Security",
			Description: "Open profiles and all individually overridable runtime controls",
			Kind:        configFieldSecurity,
			Get: func(c *config.Config) string {
				effective, err := c.EffectiveSecurity()
				if err != nil {
					return "invalid"
				}
				return effective.Summary()
			},
			Set: func(*config.Config, string) {},
		},
		{
			Label:       "Advisory Model",
			Description: "Secondary model used only for controls marked llm_review",
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
			Label:       "Prompt Dump Retention",
			Description: "Days to keep debug prompt dumps before pruning",
			Kind:        configFieldNumber,
			Get:         func(c *config.Config) string { return strconv.Itoa(c.PromptDumpRetentionDays) },
			Set: func(c *config.Config, v string) {
				if parsed, err := strconv.Atoi(v); err == nil {
					c.PromptDumpRetentionDays = parsed
				}
			},
		},
		{
			Label:       "Memory Dir",
			Description: "Directory for guidance, target, playbook, finding, and caution memory",
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
	return cfg.Clone()
}

func maskTUISecret(value string) string {
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", 8) + value[len(value)-4:]
}
