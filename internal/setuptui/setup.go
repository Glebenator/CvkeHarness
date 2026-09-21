package setuptui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setupflow"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
)

type step int

const (
	stepWelcome step = iota
	stepProvider
	stepCredentials
	stepModel
	stepSafety
	stepSecurityControls
	stepJudge
	stepScan
	stepDependencies
	stepDaemon
	stepCapabilities
	stepWebSearch
	stepRecommendations
	stepSoul
	stepNotes
	stepReview
	stepDone
)

type inputMode int

const (
	inputNone inputMode = iota
	inputOpenRouterKey
	inputOpenAIKey
	inputTavilyKey
	inputLMStudioURL
	inputCustomModel
	inputJudgeModel
	inputDaemonUser
	inputHostNotes
)

type modelResultMsg struct{ result setupflow.ModelResult }
type credentialMsg struct {
	label string
	err   error
}
type scanMsg struct{ profile setupflow.HostProfile }
type recommendationsMsg struct {
	items []string
	err   error
}
type saveMsg struct {
	result setupflow.FinalizeResult
	err    error
}

type webSearchOption struct {
	action string
	row    row
}

type setupModel struct {
	cfg               *config.Config
	step              step
	width             int
	height            int
	cursor            int
	input             textinput.Model
	inputMode         inputMode
	message           string
	errMessage        string
	pendingCredential string
	pendingProvider   string
	validating        bool
	modelsLoading     bool
	models            setupflow.ModelResult
	scanning          bool
	scanComplete      bool
	hostProfile       setupflow.HostProfile
	installPlan       setupflow.InstallPlan
	daemonPlan        setupflow.DaemonPlan
	recommending      bool
	recommendations   []string
	acceptedRecs      []string
	soulProfile       setupflow.SoulProfile
	hostNotes         string
	applyActions      bool
	safetyAdvanced    bool
	securityCustomize bool
	yoloConfirm       bool
	capAdvanced       bool
	saving            bool
	saveResult        setupflow.FinalizeResult
}

// Run starts the full-screen setup wizard.
func Run() error {
	cfg, err := setupflow.LoadWizardConfig()
	if err != nil {
		return fmt.Errorf("load setup configuration: %w", err)
	}
	m := setupModel{
		cfg:         cfg,
		step:        stepWelcome,
		width:       90,
		height:      28,
		soulProfile: setupflow.DefaultSoulProfile(),
		daemonPlan:  setupflow.DetectDaemonPlan(setupflow.RealRunner{}, ""),
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func (m setupModel) Init() tea.Cmd { return nil }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case modelResultMsg:
		m.modelsLoading = false
		m.models = msg.result
		m.cursor = m.preferredCursor()
		return m, nil
	case credentialMsg:
		m.validating = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			return m, nil
		}
		m.cfg.SetAPIKey(m.pendingProvider, m.pendingCredential)
		if m.pendingProvider == "tavily" {
			enableWebSearchDefaults(m.cfg)
		}
		m.pendingCredential, m.pendingProvider = "", ""
		if msg.label != "" {
			m.message = "Credential validated: " + msg.label
		} else {
			m.message = "Credential validated"
		}
		return m.nextStep()
	case scanMsg:
		m.scanning = false
		m.scanComplete = true
		m.hostProfile = msg.profile
		m.installPlan = setupflow.PlanMissingPython(msg.profile)
		return m, nil
	case recommendationsMsg:
		m.recommending = false
		if msg.err != nil {
			m.message = "Agent review unavailable; showing local recommendations"
			m.recommendations = setupflow.GenerateRecommendations(m.cfg, m.hostProfile, m.installPlan, m.daemonPlan)
			return m, nil
		}
		m.recommendations = msg.items
		return m, nil
	case saveMsg:
		m.saving = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			if msg.result.ConfigSaved {
				m.saveResult = msg.result
				m.step = stepDone
				m.errMessage = "Configuration saved, but an optional action failed: " + msg.err.Error()
			}
			return m, nil
		}
		m.saveResult = msg.result
		m.step = stepDone
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.saving || m.validating || m.modelsLoading || m.scanning || m.recommending {
			return m, nil
		}
		if m.inputMode != inputNone {
			return m.updateInput(msg)
		}
		switch {
		case msg.String() == "ctrl+c", msg.String() == "q":
			return m, tea.Quit
		case msg.String() == "esc":
			return m.prevStep()
		case key.Matches(msg, key.NewBinding(key.WithKeys("up", "k"))):
			m.moveCursor(-1)
		case key.Matches(msg, key.NewBinding(key.WithKeys("down", "j"))):
			m.moveCursor(1)
		case key.Matches(msg, key.NewBinding(key.WithKeys("left", "h"))):
			if m.step == stepSecurityControls {
				m.cycleSecurityControl(-1)
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("right", "l"))):
			if m.step == stepSecurityControls {
				m.cycleSecurityControl(1)
			}
		case msg.String() == " ":
			if m.step == stepSecurityControls {
				m.cycleSecurityControl(1)
			}
		case msg.String() == "r":
			if m.step == stepSecurityControls {
				m.resetSecurityControl()
			}
		case msg.String() == "a":
			switch m.step {
			case stepSafety:
				m.securityCustomize = !m.securityCustomize
				if m.securityCustomize {
					m.message = "Per-control customization will be shown after profile selection"
				} else {
					m.message = "Using the selected profile without additional customization"
				}
			case stepScan:
				m.safetyAdvanced = !m.safetyAdvanced
				if m.safetyAdvanced {
					m.message = "Advanced Safety options enabled: dependency and daemon planning will be shown"
				} else {
					m.installPlan.Selected = false
					m.daemonPlan.Selected = false
					m.message = "Install and daemon plans cleared"
				}

			}
		case msg.String() == "n":
			if m.canAdvanceWithN() {
				if m.step == stepSecurityControls {
					return m.nextStep()
				}
				return m.activate()
			}
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			return m.activate()
		}
	}
	return m, nil
}

func (m setupModel) canAdvanceWithN() bool {
	switch m.step {
	case stepCredentials:
		switch m.cfg.Provider {
		case "antigravity":
			_, err := provider.LoadAntigravityAuth(provider.AntigravityAuthPath())
			return err == nil
		case "codex":
			_, ok := setupflow.CodexAuthSummary()
			return ok
		case "openai":
			return strings.TrimSpace(m.cfg.GetAPIKey("openai")) != ""
		case "lmstudio":
			return true
		default:
			return strings.TrimSpace(m.cfg.GetAPIKey("openrouter")) != ""
		}
	case stepModel, stepJudge:
		return !m.modelsLoading
	case stepScan:
		return !m.scanning
	case stepRecommendations:
		return false
	case stepWebSearch:
		return m.cfg == nil || !m.cfg.WebSearch.Enabled || strings.TrimSpace(m.cfg.TavilyAPIKey()) != ""
	case stepReview:
		return false
	default:
		return true
	}
}

func (m setupModel) View() string {
	if m.step == stepDone {
		return m.frame("Setup complete", m.viewDone())
	}
	return m.frame(m.stepTitle(), m.stepView())
}

func (m setupModel) stepView() string {
	switch m.step {
	case stepWelcome:
		return m.viewWelcome()
	case stepProvider:
		return m.viewProvider()
	case stepCredentials:
		return m.viewCredentials()
	case stepModel:
		return m.viewModel()
	case stepSafety:
		return m.viewSafety()
	case stepSecurityControls:
		return m.viewSecurityControls()
	case stepJudge:
		return m.viewModel()
	case stepScan:
		return m.viewScan()
	case stepDependencies:
		return m.viewDependencies()
	case stepDaemon:
		return m.viewDaemon()
	case stepCapabilities:
		return m.viewCapabilities()
	case stepWebSearch:
		return m.viewWebSearch()
	case stepRecommendations:
		return m.viewRecommendations()
	case stepSoul:
		return m.viewSoul()
	case stepNotes:
		return m.viewNotes()
	case stepReview:
		return m.viewReview()
	default:
		return ""
	}
}

func (m setupModel) viewWelcome() string {
	return m.paragraph(
		"Connect a provider, choose your models and security profile, then save to start using CvkeHarness.",
		"Host checks, web search, installs, and guidance are optional. Nothing is saved until you confirm the review.",
	) + "\n" + m.renderList([]row{{"Continue to Connect", "Choose the model connection used for conversations and actions"}})
}

func (m setupModel) viewProvider() string {
	var rows []row
	for _, opt := range setupflow.ProviderOptions() {
		rows = append(rows, row{opt.ID, opt.Description})
	}
	return m.renderList(rows)
}

func (m setupModel) viewCredentials() string {
	if m.validating {
		return line("Validating credential...")
	}
	switch m.inputMode {
	case inputOpenRouterKey:
		return m.viewInputPrompt("API key input active: OpenRouter", "Paste your OpenRouter API key. Characters are hidden; press enter to validate or esc to cancel.", "OpenRouter API key")
	case inputOpenAIKey:
		return m.viewInputPrompt("API key input active: OpenAI", "Paste your OpenAI API key. Characters are hidden; press enter to validate or esc to cancel.", "OpenAI API key")
	case inputLMStudioURL:
		return m.viewInputPrompt("Base URL input active", "Enter the local OpenAI-compatible base URL; press enter to save or esc to cancel.", "LM Studio URL")
	}
	switch m.cfg.Provider {
	case "antigravity":
		status := "Run cvkeharness antigravity login in another terminal."
		if _, err := provider.LoadAntigravityAuth(provider.AntigravityAuthPath()); err == nil {
			status = "Found personal Google login (unofficial integration)."
		}
		return m.paragraph(status) + "\n" + m.renderList([]row{{"Use Google login", "Check saved credentials and continue"}})
	case "codex":
		summary, ok := setupflow.CodexAuthSummary()
		status := summary
		if !ok {
			status += " · run `codex login` first"
		}
		return m.paragraph(status) + "\n" + m.renderList([]row{{"Use this Codex login", "Continue with Codex CLI authentication"}, {"Check again", "Re-read the Codex auth cache"}})
	case "openai":
		return m.apiKeyView("openai", "OpenAI API key")
	case "lmstudio":
		base := m.cfg.BaseURL
		if base == "" {
			base = "http://localhost:1234/v1"
		}
		desc := "Use " + base
		return m.renderList([]row{{"Use local server", desc}, {"Enter URL", "Set a custom OpenAI-compatible base URL"}})
	default:
		return m.apiKeyView("openrouter", "OpenRouter API key")
	}
}

func (m setupModel) apiKeyView(providerName, label string) string {
	keyValue := m.cfg.GetAPIKey(providerName)
	rows := []row{{"Enter key", "Paste and validate a new " + label}}
	if keyValue != "" {
		rows = append([]row{{"Reuse existing key", setupflow.MaskSecret(keyValue)}}, rows...)
	}
	return m.renderList(rows)
}

func (m setupModel) modelOptions() []setupflow.ModelOption {
	if len(m.models.Items) == 0 && m.step != stepJudge {
		return nil
	}
	items := append([]setupflow.ModelOption(nil), m.models.Items...)
	current := m.cfg.PrimaryModel()
	if m.step == stepJudge {
		items = []setupflow.ModelOption{{ID: m.cfg.PrimaryModel(), Description: "Use the primary model (recommended)"}}
		for _, item := range m.models.Items {
			if item.ID != m.cfg.PrimaryModel() {
				items = append(items, item)
			}
		}
		current = m.cfg.SafetyModel
	}
	for _, item := range items {
		if item.ID == current {
			return items
		}
	}
	if current != "" {
		items = append([]setupflow.ModelOption{{ID: current, Description: "Configured model; not listed in this catalog"}}, items...)
	}
	return items
}

func (m setupModel) viewModel() string {
	if m.inputMode == inputCustomModel || m.inputMode == inputJudgeModel {
		return m.viewInputPrompt("Custom model input active", "Enter a model ID supported by this provider; press enter to save or esc to cancel.", "Model ID")
	}
	if m.modelsLoading {
		return line("Fetching models...")
	}
	items := m.modelOptions()
	if len(items) == 0 {
		return line("Press enter to fetch available models.")
	}
	status := "offline fallback"
	if m.models.Live {
		status = "live models"
		if m.models.Source == "codex-cache" {
			status = "recent account cache"
		}
	} else if m.models.Source == "codex-cache" {
		status = "cached choices; refresh in Codex if needed"
	}
	intro := "Source: " + status + " · " + m.models.Source
	if m.step == stepJudge {
		intro = "The judge reviews proposed actions using your " + m.cfg.Provider + " connection. Choose a model available through that provider."
	}
	// Reserve room for the title, status, and footer at 80x24 too.
	visible := max(1, (m.height-15)/3)
	start, end := listWindow(m.cursor, len(items), visible)
	window := m
	window.cursor -= start
	position := fmt.Sprintf("Models %d-%d of %d", start+1, end, len(items))
	return m.paragraph(intro, position) + "\n" + window.renderList(modelRows(items[start:end]))
}

func (m setupModel) viewSafety() string {
	effective, _ := m.cfg.EffectiveSecurity()
	custom := "Press A to customize individual controls after choosing a profile."
	if m.securityCustomize {
		custom = "Individual control customization is enabled; press A to use the profile unchanged."
	}
	return m.paragraph(
		"Choose a security profile. Reasonable is the default: reads run, mutations ask, and credential or raw-device access stays blocked.",
		custom+" Current policy: "+effective.Summary()+".",
	) + "\n" + m.renderList(modelRows(setupflow.SafetyOptions()))
}

func (m setupModel) viewSecurityControls() string {
	effective, err := m.cfg.EffectiveSecurity()
	if err != nil {
		return m.paragraph("Security policy is invalid: " + err.Error())
	}
	catalog := securitypolicy.Catalog()
	rows := make([]row, 0, len(catalog))
	for _, setting := range catalog {
		origin := effective.Origins[setting.ID]
		rows = append(rows, row{setting.Label, effective.Value(setting.ID) + " · " + origin + " · " + setting.Description})
	}
	visible := max(4, m.height-14)
	start, end := listWindow(m.cursor, len(rows), visible)
	window := m
	window.cursor = m.cursor - start
	return m.paragraph(
		"Customize any control with Left/Right or Space. Press R to reset the selected control to the profile value.",
		"Effective: "+effective.Summary()+" · policy "+effective.Hash,
	) + "\n" + window.renderList(rows[start:end])
}

func (m setupModel) viewScan() string {
	if m.scanning {
		return line("Scanning this system...")
	}
	if !m.scanComplete {
		return m.paragraph(
			"Optional: inspect installed tools and host details. The scan runs version checks and sends connectivity probes; it is not needed for chat or run.",
		) + "\n" + m.renderList([]row{{"Skip host scan", "Continue without scanning or planning installs"}, {"Scan this host", "Check tools and connectivity; A reveals install planning"}})
	}
	rows := []string{
		fmt.Sprintf("Platform: %s/%s", m.hostProfile.OS, m.hostProfile.Arch),
		fmt.Sprintf("CPU cores: %d", m.hostProfile.CPUs),
		fmt.Sprintf("Memory: %s", bytesText(m.hostProfile.MemoryBytes)),
		fmt.Sprintf("Config disk free: %s", bytesText(m.hostProfile.ConfigDirFreeBytes)),
		fmt.Sprintf("Python: %s", foundText(m.hostProfile.Python)),
		fmt.Sprintf("Internet: %s", boolText(m.hostProfile.InternetReachable)),
		fmt.Sprintf("Provider endpoint: %s", boolText(m.hostProfile.ProviderReachable)),
	}
	advanced := "Optional install and daemon planning will be skipped"
	if m.safetyAdvanced {
		advanced = "Optional install and daemon planning will be shown"
	}
	rows = append(rows, "Next: "+advanced)
	return m.paragraph(rows...) + "\n" + m.renderList([]row{{"Continue", "Accept this read-only scan summary"}})
}

func (m setupModel) viewDependencies() string {
	if m.hostProfile.Python.Found {
		return m.paragraph("Python is available: "+foundText(m.hostProfile.Python)) + "\n" + m.renderList([]row{{"Continue", "No install needed"}})
	}
	if !m.installPlan.Available {
		return m.paragraph(m.installPlan.Description) + "\n" + m.renderList([]row{{"Skip", "Continue without installing Python"}})
	}
	return m.paragraph("Python was not found. Setup can plan an install, but it will only run after final confirmation.", "Command: "+setupflow.CommandString(m.installPlan.Command)) + "\n" + m.renderList([]row{{"Skip", "Continue without installing Python"}, {"Plan install", m.installPlan.Description}})
}

func (m setupModel) viewDaemon() string {
	if m.inputMode == inputDaemonUser {
		return m.viewInputPrompt("System service user input active", "Enter the Linux user that should own the scheduler service; press enter to continue or esc to cancel.", "System service user")
	}
	if !m.daemonPlan.Supported {
		return m.paragraph("Scheduler daemon install is unavailable: "+m.daemonPlan.Reason) + "\n" + m.renderList([]row{{"Continue", "Skip daemon setup"}})
	}
	return m.paragraph("The scheduler daemon runs CvkeHarness jobs while the TUI is closed.") + "\n" + m.renderList([]row{
		{"Skip", "Do not install the daemon now"},
		{"Install user service", "Install current-user systemd service"},
		{"Install and start", "Install, enable, and start the user service"},
		{"System service", "Install a system service for a target user"},
	})
}

func (m setupModel) viewCapabilities() string {
	return m.paragraph(
		"Your security profile already controls scripts, diagnostics, network access, and installs.",
		"Web search and personalized guidance are optional. You can configure them later by running setup again.",
	) + "\n" + m.renderList([]row{
		{"Continue to review", "Keep optional features as configured"},
		{"Configure optional features", "Web search, setup suggestions, and guidance"},
	})
}

func (m setupModel) viewWebSearch() string {
	if m.validating {
		return line("Validating Tavily credential...")
	}
	if m.inputMode == inputTavilyKey {
		return m.viewInputPrompt("API key input active: Tavily", "Paste your Tavily API key. Characters are hidden; press enter to validate or esc to cancel.", "Tavily API key")
	}
	status := "disabled"
	if m.cfg.WebSearch.Enabled {
		status = "enabled"
	}
	return m.paragraph(
		"Web search is optional and read-only. It lets the agent look up public current documentation, release notes, issues, and error messages through Tavily.",
		"Status: "+status+". Never put secrets, private hostnames, or internal URLs into web search requests.",
	) + "\n" + m.renderList(webSearchRows(m.webSearchOptions()))
}

func (m setupModel) viewRecommendations() string {
	if m.recommending {
		return line("Asking the configured model for setup recommendations...")
	}
	if len(m.recommendations) == 0 {
		return m.paragraph("Optional suggestions use a model request and may incur provider charges.") + "\n" + m.renderList([]row{{"Skip suggestions", "Continue without a model request"}, {"Generate suggestions", "Send the setup summary to your provider"}})
	}
	return m.paragraph("Choose what to do with these recommendations. Accepting appends them to machine notes; skipping leaves them out.") + "\n" +
		m.paragraph(m.recommendations...) + "\n" +
		m.renderList([]row{
			{"Accept into notes", "Save these recommendations in guidance.md"},
			{"Edit before saving", "Open the machine notes editor with these prefilled"},
			{"Regenerate", "Ask the model again"},
			{"Skip", "Do not save these recommendations"},
		})
}

func (m setupModel) viewSoul() string {
	var rows []row
	for _, profile := range setupflow.SoulProfiles() {
		rows = append(rows, row{profile.Label, profile.Description})
	}
	return m.renderList(rows)
}

func (m setupModel) viewNotes() string {
	if m.inputMode == inputHostNotes {
		return m.viewInputPrompt("Machine notes input active", "Add durable local quirks as a short semicolon-separated note; press enter to save or esc to cancel.", "Machine notes")
	}
	value := strings.TrimSpace(m.hostNotes)
	if value == "" {
		value = "No machine notes yet"
	}
	return m.paragraph(value) + "\n" + m.renderList([]row{{"Edit notes", "Add durable host quirks"}, {"Skip", "Continue without extra notes"}})
}

func (m setupModel) viewReview() string {
	if m.saving {
		return m.paragraph("Saving configuration and local setup artifacts. External actions run only when the second option was explicitly selected.")
	}
	lines := []string{
		"Provider: " + m.cfg.Provider,
		"Model: " + m.cfg.PrimaryModel(),
		"Security: " + securitySummary(m.cfg),
		"Judge model: " + m.cfg.SafetyModel,
		"Web search: " + boolText(m.cfg.WebSearch.Enabled),
	}
	if m.installPlan.Selected {
		lines = append(lines, "Selected action: "+setupflow.CommandString(m.installPlan.Command))
	}
	if m.daemonPlan.Selected {
		m.daemonPlan.RefreshReviewCommands()
		for _, cmd := range m.daemonPlan.ReviewCommands {
			lines = append(lines, "Selected action: "+setupflow.CommandString(cmd))
		}
	}
	rows := []row{{"Save configuration", "Finish setup and enable run and console"}}
	if m.hasActions() {
		rows[0] = row{"Save configuration only", "Leave the selected install and daemon actions unapplied"}
		rows = append(rows, row{"Save and apply selected actions", "Also execute the commands listed above"})
	}
	return m.paragraph(lines...) + "\n" + m.renderList(rows)
}

func (m setupModel) viewDone() string {
	lines := []string{"Configuration saved."}
	if m.saveResult.HostProfilePath != "" {
		lines = append(lines, "Host profile: "+m.saveResult.HostProfilePath)
	}
	if m.saveResult.SoulWritten {
		lines = append(lines, "Prepared guidance.md.")
	}
	if m.saveResult.HostNotesWritten {
		lines = append(lines, "Updated guidance.md.")
	}
	for _, out := range m.saveResult.ActionOutput {
		if strings.TrimSpace(out) != "" {
			lines = append(lines, out)
		}
	}
	lines = append(lines, "Next: cvkeharness console", "Or: cvkeharness run \"describe your task\"", "Press Enter or q to exit.")
	return m.paragraph(lines...)
}

func (m setupModel) viewInputPrompt(title, detail, label string) string {
	return m.paragraph(title, detail) + "\n" + m.renderInputField(label)
}

func (m setupModel) activate() (setupModel, tea.Cmd) {
	m.errMessage = ""
	m.message = ""
	switch m.step {
	case stepWelcome:
		return m.nextStep()
	case stepProvider:
		opts := setupflow.ProviderOptions()
		if m.cursor >= 0 && m.cursor < len(opts) {
			setupflow.SelectProvider(m.cfg, opts[m.cursor].ID)
			m.models = setupflow.ModelResult{}
			m.recommendations = nil
		}
		m.cursor = 0
		return m.nextStep()
	case stepCredentials:
		return m.activateCredentials()
	case stepModel, stepJudge:
		if len(m.modelOptions()) == 0 {
			m.modelsLoading = true
			return m, fetchModelsCmd(m.cfg)
		}
		item := m.modelOptions()[m.cursor]
		if item.ID == "[ custom model ]" {
			mode, current := inputCustomModel, m.cfg.PrimaryModel()
			if m.step == stepJudge {
				mode, current = inputJudgeModel, m.cfg.SafetyModel
			}
			return m.beginInput(mode, "Model ID", current, false), nil
		}
		if m.step == stepJudge {
			m.cfg.SafetyModel = config.NormalizeProviderModelID(m.cfg.Provider, item.ID)
		} else {
			setupflow.SetDefaultModel(m.cfg, item.ID)
		}
		return m.nextStep()
	case stepSafety:
		opts := setupflow.SafetyOptions()
		profile := securitypolicy.Profile(opts[m.cursor].ID)
		if profile == securitypolicy.ProfileYOLO && !m.yoloConfirm {
			m.yoloConfirm = true
			m.message = "YOLO disables CvkeHarness approval and deletion guards. Press Enter again to confirm; OS and provider protections still apply."
			return m, nil
		}
		if profile != m.cfg.Security.Profile {
			if err := m.cfg.Security.ApplyProfile(profile); err != nil {
				m.errMessage = err.Error()
				return m, nil
			}
		}
		m.cfg.Normalize()
		m.yoloConfirm = false
		return m.nextStep()
	case stepSecurityControls:
		return m.nextStep()
	case stepScan:
		if !m.scanComplete && m.cursor == 0 {
			m.installPlan.Selected, m.daemonPlan.Selected = false, false
			m.safetyAdvanced = false
			return m.nextStep()
		}
		if !m.scanComplete {
			m.scanning = true
			return m, scanCmd(m.cfg.Provider)
		}
		return m.nextStep()
	case stepDependencies:
		m.installPlan.Selected = !m.hostProfile.Python.Found && m.installPlan.Available && m.cursor == 1
		return m.nextStep()
	case stepDaemon:
		return m.activateDaemon()
	case stepCapabilities:
		m.capAdvanced = m.cursor == 1
		return m.nextStep()
	case stepWebSearch:
		return m.activateWebSearch()
	case stepRecommendations:
		if len(m.recommendations) == 0 {
			if m.cursor == 0 {
				return m.nextStep()
			}
			m.recommending = true
			return m, recommendationsCmd(m)
		}
		switch m.cursor {
		case 0:
			m.acceptedRecs = append([]string(nil), m.recommendations...)
			m.appendHostNotes(m.recommendations)
			return m.nextStep()
		case 1:
			m.appendHostNotes(m.recommendations)
			return m.beginInput(inputHostNotes, "Machine notes", m.hostNotes, false), nil
		case 2:
			m.recommendations = nil
			m.acceptedRecs = nil
			m.recommending = true
			return m, recommendationsCmd(m)
		default:
			m.acceptedRecs = nil
			return m.nextStep()
		}
	case stepSoul:
		profiles := setupflow.SoulProfiles()
		if m.cursor >= 0 && m.cursor < len(profiles) {
			m.soulProfile = profiles[m.cursor]
		}
		return m.nextStep()
	case stepNotes:
		if m.cursor == 0 {
			return m.beginInput(inputHostNotes, "Machine notes", m.hostNotes, false), nil
		}
		return m.nextStep()
	case stepReview:
		if err := setupflow.ValidateReady(m.cfg); err != nil {
			m.errMessage = err.Error()
			return m, nil
		}
		m.applyActions = m.hasActions() && m.cursor == 1
		m.saving = true
		return m, saveCmd(m)
	case stepDone:
		return m, tea.Quit
	}
	return m, nil
}

func (m setupModel) activateCredentials() (setupModel, tea.Cmd) {
	switch m.cfg.Provider {
	case "antigravity":
		if _, err := provider.LoadAntigravityAuth(provider.AntigravityAuthPath()); err != nil {
			m.message = err.Error()
			return m, nil
		}
		return m.nextStep()
	case "codex":
		if m.cursor == 1 {
			m.message = "Codex auth cache refreshed"
			return m, nil
		}
		if _, ok := setupflow.CodexAuthSummary(); !ok {
			m.errMessage = "Run 'codex login' in another terminal, then choose Check again."
			return m, nil
		}
		return m.nextStep()
	case "openai":
		if m.cfg.GetAPIKey("openai") != "" && m.cursor == 0 {
			return m.nextStep()
		}
		return m.beginInput(inputOpenAIKey, "OpenAI API key", "", true), nil
	case "lmstudio":
		if m.cursor == 0 {
			if m.cfg.BaseURL == "" {
				m.cfg.BaseURL = "http://localhost:1234/v1"
			}
			return m.nextStep()
		}
		return m.beginInput(inputLMStudioURL, "LM Studio URL", firstNonEmpty(m.cfg.BaseURL, "http://localhost:1234/v1"), false), nil
	default:
		if m.cfg.GetAPIKey("openrouter") != "" && m.cursor == 0 {
			return m.nextStep()
		}
		return m.beginInput(inputOpenRouterKey, "OpenRouter API key", "", true), nil
	}
}

func (m setupModel) activateWebSearch() (setupModel, tea.Cmd) {
	opts := m.webSearchOptions()
	if m.cursor < 0 || m.cursor >= len(opts) {
		m.cursor = 0
	}
	switch opts[m.cursor].action {
	case "enable_existing", "enable_env":
		enableWebSearchDefaults(m.cfg)
		return m.nextStep()
	case "enter_key":
		return m.beginInput(inputTavilyKey, "Tavily API key", "", true), nil
	default:
		m.cfg.WebSearch.Enabled = false
		return m.nextStep()
	}
}

func (m setupModel) webSearchOptions() []webSearchOption {
	configKey := ""
	enabled := false
	if m.cfg != nil {
		configKey = strings.TrimSpace(m.cfg.GetAPIKey("tavily"))
		enabled = m.cfg.WebSearch.Enabled
	}
	envKey := strings.TrimSpace(os.Getenv("TAVILY_API_KEY"))

	if enabled && configKey == "" && envKey == "" {
		return []webSearchOption{
			{action: "enter_key", row: row{"Enter Tavily key", "Required before enabled web search can be saved"}},
			{action: "disable", row: row{"Disable web search", "Leave external research tools unavailable"}},
		}
	}

	var opts []webSearchOption
	switch {
	case configKey != "":
		label := "Enable existing key"
		if enabled {
			label = "Keep enabled"
		}
		opts = append(opts, webSearchOption{
			action: "enable_existing",
			row:    row{label, "Use stored Tavily key " + setupflow.MaskSecret(configKey)},
		})
	case envKey != "":
		label := "Use TAVILY_API_KEY"
		if enabled {
			label = "Keep env key"
		}
		opts = append(opts, webSearchOption{
			action: "enable_env",
			row:    row{label, "Enable web search using the environment credential"},
		})
	}

	if len(opts) == 0 && !enabled {
		opts = append(opts, webSearchOption{
			action: "disable",
			row:    row{"Skip", "Leave web_search disabled"},
		})
	}
	opts = append(opts, webSearchOption{
		action: "enter_key",
		row:    row{"Enter Tavily key", "Validate and save a Tavily API key"},
	})
	if enabled || configKey != "" || envKey != "" {
		label := "Skip"
		desc := "Leave web_search disabled"
		if enabled {
			label = "Disable web search"
			desc = "Turn off external research tools"
		}
		opts = append(opts, webSearchOption{
			action: "disable",
			row:    row{label, desc},
		})
	}
	return opts
}

func webSearchRows(opts []webSearchOption) []row {
	rows := make([]row, 0, len(opts))
	for _, opt := range opts {
		rows = append(rows, opt.row)
	}
	return rows
}

func enableWebSearchDefaults(cfg *config.Config) {
	if cfg == nil {
		return
	}
	cfg.WebSearch.Enabled = true
	cfg.WebSearch.Provider = "tavily"
	if cfg.WebSearch.MaxResults <= 0 {
		cfg.WebSearch.MaxResults = 5
	}
	if cfg.WebSearch.MaxResults > 10 {
		cfg.WebSearch.MaxResults = 10
	}
	if strings.TrimSpace(cfg.WebSearch.SearchDepth) == "" {
		cfg.WebSearch.SearchDepth = "basic"
	}
	if cfg.WebSearch.MaxFetchedChars <= 0 {
		cfg.WebSearch.MaxFetchedChars = 12000
	}
	if cfg.WebSearch.MaxFetchedChars > 30000 {
		cfg.WebSearch.MaxFetchedChars = 30000
	}
}

func (m setupModel) activateDaemon() (setupModel, tea.Cmd) {
	if !m.daemonPlan.Supported || m.cursor == 0 {
		m.daemonPlan.Selected = false
		return m.nextStep()
	}
	m.daemonPlan.Selected = true
	m.daemonPlan.SystemService = false
	m.daemonPlan.EnableLinger = false
	m.daemonPlan.EnableNow = false
	m.daemonPlan.StartNow = false
	if m.cursor == 2 {
		m.daemonPlan.EnableNow = true
		m.daemonPlan.StartNow = true
	}
	if m.cursor == 3 {
		m.daemonPlan.SystemService = true
		return m.beginInput(inputDaemonUser, "System service user", m.daemonPlan.User, false), nil
	}
	m.daemonPlan.RefreshReviewCommands()
	return m.nextStep()
}

func (m setupModel) beginInput(mode inputMode, placeholder, value string, secret bool) setupModel {
	m.inputMode = mode
	m.input = textinput.New()
	m.input.Placeholder = placeholder
	m.input.SetValue(value)
	m.input.CharLimit = 1024
	m.input.Width = 72
	if secret {
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '*'
	}
	m.input.Focus()
	return m
}

func (m *setupModel) appendHostNotes(notes []string) {
	var existing []string
	if strings.TrimSpace(m.hostNotes) != "" {
		existing = append(existing, splitNotes(m.hostNotes)...)
	}
	seen := make(map[string]bool, len(existing)+len(notes))
	for _, note := range existing {
		seen[note] = true
	}
	for _, note := range notes {
		note = strings.Join(strings.Fields(strings.TrimSpace(note)), " ")
		if note == "" || seen[note] {
			continue
		}
		existing = append(existing, note)
		seen[note] = true
	}
	m.hostNotes = strings.Join(existing, "; ")
}

func (m setupModel) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputMode = inputNone
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		mode := m.inputMode
		if value == "" && mode != inputHostNotes {
			m.errMessage = "Enter a value or press Esc to cancel."
			return m, nil
		}
		m.inputMode = inputNone
		switch mode {
		case inputOpenRouterKey:
			m.pendingProvider, m.pendingCredential = "openrouter", value
			m.validating = true
			return m, validateOpenRouterCmd(value)
		case inputOpenAIKey:
			m.pendingProvider, m.pendingCredential = "openai", value
			m.validating = true
			return m, validateOpenAICmd(value)
		case inputTavilyKey:
			m.pendingProvider, m.pendingCredential = "tavily", value
			m.validating = true
			return m, validateTavilyCmd(value)
		case inputLMStudioURL:
			u, err := url.Parse(value)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				m.inputMode = inputLMStudioURL
				m.errMessage = "Enter an http or https URL with a host."
				return m, nil
			}
			m.cfg.BaseURL = value
			return m.nextStep()
		case inputJudgeModel:
			m.cfg.SafetyModel = config.NormalizeProviderModelID(m.cfg.Provider, value)
			return m.nextStep()
		case inputCustomModel:
			setupflow.SetDefaultModel(m.cfg, value)
			return m.nextStep()
		case inputDaemonUser:
			if value == "" {
				m.errMessage = "System service user is required"
				return m, nil
			}
			m.daemonPlan.User = value
			m.daemonPlan.RefreshReviewCommands()
			return m.nextStep()
		case inputHostNotes:
			m.hostNotes = value
			return m.nextStep()
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m setupModel) nextStep() (setupModel, tea.Cmd) {
	switch {
	case m.step == stepSafety && !m.securityCustomize:
		m.step = stepJudge
		if m.cfg.SafetyMode != "llm_judge" {
			m.step = stepScan
		}
		m.cursor = m.preferredCursor()
		return m, nil
	case m.step == stepSecurityControls && m.cfg.SafetyMode != "llm_judge":
		m.step = stepScan
		m.cursor = 0
		return m, nil
	case m.step == stepScan && !m.safetyAdvanced:
		m.step = stepCapabilities
		m.cursor = 0
		return m, nil
	case m.step == stepDependencies && !m.daemonPlan.Supported:
		m.step = stepCapabilities
		m.cursor = 0
		return m, nil
	case m.step == stepCapabilities && !m.capAdvanced:
		m.step = stepReview
		m.cursor = 0
		return m, nil
	}
	if m.step < stepReview {
		m.step++
		m.cursor = m.preferredCursor()
		if m.step == stepModel && len(m.models.Items) == 0 {
			m.modelsLoading = true
			return m, fetchModelsCmd(m.cfg)
		}
	}
	return m, nil
}

func (m setupModel) prevStep() (setupModel, tea.Cmd) {
	if m.step > stepWelcome && !m.saving && !m.scanning && !m.validating {
		switch {
		case m.step == stepJudge && !m.securityCustomize:
			m.step = stepSafety
		case m.step == stepScan && m.cfg.SafetyMode != "llm_judge":
			m.step = stepSafety
			if m.securityCustomize {
				m.step = stepSecurityControls
			}
		case m.step == stepCapabilities && !m.safetyAdvanced:
			m.step = stepScan
		case m.step == stepCapabilities && !m.daemonPlan.Supported:
			m.step = stepDependencies
		case m.step == stepReview && !m.capAdvanced:
			m.step = stepCapabilities
		default:
			m.step--
		}
		m.cursor = m.preferredCursor()
	}
	return m, nil
}

func (m setupModel) preferredCursor() int {
	switch m.step {
	case stepProvider:
		for i, option := range setupflow.ProviderOptions() {
			if option.ID == m.cfg.Provider {
				return i
			}
		}
	case stepModel, stepJudge:
		selected := m.cfg.PrimaryModel()
		if m.step == stepJudge {
			selected = m.cfg.SafetyModel
		}
		for i, option := range m.modelOptions() {
			if option.ID == selected {
				return i
			}
		}
	case stepSafety:
		profile := securitypolicy.ProfileReasonable
		if m.cfg.Security != nil {
			profile = m.cfg.Security.Profile
		}
		for i, option := range setupflow.SafetyOptions() {
			if option.ID == string(profile) {
				return i
			}
		}
	case stepSoul:
		for i, profile := range setupflow.SoulProfiles() {
			if profile.ID == m.soulProfile.ID {
				return i
			}
		}
	}
	return 0
}

func (m *setupModel) moveCursor(delta int) {
	count := m.itemCount()
	if count <= 0 {
		return
	}
	m.yoloConfirm = false
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = count - 1
	}
	if m.cursor >= count {
		m.cursor = 0
	}
}

func (m setupModel) itemCount() int {
	switch m.step {
	case stepWelcome:
		return 1
	case stepProvider:
		return len(setupflow.ProviderOptions())
	case stepCredentials:
		switch m.cfg.Provider {
		case "antigravity":
			return 1
		case "codex", "lmstudio":
			return 2
		case "openai":
			if m.cfg.GetAPIKey("openai") != "" {
				return 2
			}
			return 1
		default:
			if m.cfg.GetAPIKey("openrouter") != "" {
				return 2
			}
			return 1
		}
	case stepModel, stepJudge:
		return len(m.modelOptions())
	case stepSafety:
		return len(setupflow.SafetyOptions())
	case stepSecurityControls:
		return len(securitypolicy.Catalog())
	case stepScan:
		if m.scanComplete {
			return 1
		}
		return 2
	case stepRecommendations:
		if len(m.recommendations) == 0 {
			return 2
		}
		return 4
	case stepDependencies:
		if !m.hostProfile.Python.Found && m.installPlan.Available {
			return 2
		}
		return 1
	case stepDaemon:
		if m.daemonPlan.Supported {
			return 4
		}
		return 1
	case stepCapabilities:
		return 2
	case stepWebSearch:
		return len(m.webSearchOptions())
	case stepSoul:
		return len(setupflow.SoulProfiles())
	case stepNotes:
		return 2
	case stepReview:
		if m.hasActions() {
			return 2
		}
		return 1
	}
	return 0
}

func (m setupModel) hasActions() bool {
	return m.installPlan.Selected || m.daemonPlan.Selected
}

func (m *setupModel) cycleSecurityControl(delta int) {
	if m.cfg == nil || m.cfg.Security == nil {
		return
	}
	catalog := securitypolicy.Catalog()
	if m.cursor < 0 || m.cursor >= len(catalog) {
		return
	}
	effective, err := m.cfg.EffectiveSecurity()
	if err != nil {
		m.errMessage = err.Error()
		return
	}
	setting := catalog[m.cursor]
	value := securitypolicy.NextValue(setting, effective.Value(setting.ID), delta)
	if err := m.cfg.Security.SetOverride(setting.ID, value); err != nil {
		m.errMessage = err.Error()
		return
	}
	m.cfg.Normalize()
	m.message = setting.Label + " set to " + value + " (override)"
}

func (m *setupModel) resetSecurityControl() {
	if m.cfg == nil || m.cfg.Security == nil {
		return
	}
	catalog := securitypolicy.Catalog()
	if m.cursor < 0 || m.cursor >= len(catalog) {
		return
	}
	setting := catalog[m.cursor]
	m.cfg.Security.ClearOverride(setting.ID)
	m.cfg.Normalize()
	m.message = setting.Label + " reset to profile value"
}

func securitySummary(cfg *config.Config) string {
	if cfg == nil {
		return "unavailable"
	}
	effective, err := cfg.EffectiveSecurity()
	if err != nil {
		return "invalid: " + err.Error()
	}
	return effective.Summary() + " · policy " + effective.Hash
}

func listWindow(cursor, total, viewportHeight int) (start, end int) {
	if total <= 0 {
		return 0, 0
	}
	if viewportHeight <= 0 || viewportHeight >= total {
		return 0, total
	}
	half := viewportHeight / 2
	start = cursor - half
	if start < 0 {
		start = 0
	}
	end = start + viewportHeight
	if end > total {
		end = total
		start = end - viewportHeight
	}
	return start, end
}

func fetchModelsCmd(cfg *config.Config) tea.Cmd {
	copyCfg := *cfg
	if cfg.APIKeys != nil {
		copyCfg.APIKeys = make(map[string]string, len(cfg.APIKeys))
		for k, v := range cfg.APIKeys {
			copyCfg.APIKeys[k] = v
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return modelResultMsg{result: setupflow.FetchModels(ctx, &copyCfg)}
	}
}

func validateOpenRouterCmd(key string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		label, err := setupflow.ValidateOpenRouterKey(ctx, key)
		return credentialMsg{label: label, err: err}
	}
}

func validateOpenAICmd(key string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return credentialMsg{err: setupflow.ValidateOpenAIKey(ctx, key)}
	}
}

func validateTavilyCmd(key string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return credentialMsg{label: "Tavily", err: setupflow.ValidateTavilyKey(ctx, key)}
	}
}

func scanCmd(providerName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		return scanMsg{profile: (setupflow.Scanner{}).Scan(ctx, providerName)}
	}
}

func recommendationsCmd(m setupModel) tea.Cmd {
	cfg := *m.cfg
	if m.cfg.APIKeys != nil {
		cfg.APIKeys = make(map[string]string, len(m.cfg.APIKeys))
		for k, v := range m.cfg.APIKeys {
			cfg.APIKeys[k] = v
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		items, err := setupflow.AgentRecommendations(ctx, &cfg, m.hostProfile, m.installPlan, m.daemonPlan)
		return recommendationsMsg{items: items, err: err}
	}
}

func saveCmd(m setupModel) tea.Cmd {
	cfg := *m.cfg
	if m.cfg.APIKeys != nil {
		cfg.APIKeys = make(map[string]string, len(m.cfg.APIKeys))
		for k, v := range m.cfg.APIKeys {
			cfg.APIKeys[k] = v
		}
	}
	hostNotes := splitNotes(m.hostNotes)
	installPlans := []setupflow.InstallPlan{m.installPlan}
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result, err := setupflow.Finalize(ctx, setupflow.FinalizeOptions{
			Config:       &cfg,
			HostProfile:  m.hostProfile,
			InstallPlans: installPlans,
			DaemonPlan:   m.daemonPlan,
			SoulProfile:  m.soulProfile,
			HostNotes:    hostNotes,
			ApplyActions: m.applyActions,
		})
		return saveMsg{result: result, err: err}
	}
}

func splitNotes(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
