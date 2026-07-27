package setuptui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setupflow"
)

type step int

const (
	stepWelcome step = iota
	stepProvider
	stepCredentials
	stepModel
	stepSafety
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
	cfg             *config.Config
	step            step
	width           int
	height          int
	cursor          int
	input           textinput.Model
	inputMode       inputMode
	message         string
	errMessage      string
	validating      bool
	modelsLoading   bool
	models          setupflow.ModelResult
	scanning        bool
	scanComplete    bool
	hostProfile     setupflow.HostProfile
	installPlan     setupflow.InstallPlan
	daemonPlan      setupflow.DaemonPlan
	recommending    bool
	recommendations []string
	acceptedRecs    []string
	soulProfile     setupflow.SoulProfile
	hostNotes       string
	applyActions    bool
	saving          bool
	saveResult      setupflow.FinalizeResult
}

// Run starts the full-screen setup wizard.
func Run() error {
	cfg := setupflow.LoadWizardConfig()
	m := setupModel{
		cfg:         cfg,
		step:        stepWelcome,
		width:       90,
		height:      28,
		soulProfile: setupflow.DefaultSoulProfile(),
		daemonPlan:  setupflow.DetectDaemonPlan(setupflow.RealRunner{}, ""),
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
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
		m.cursor = 0
		return m, nil
	case credentialMsg:
		m.validating = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			return m, nil
		}
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
			return m, nil
		}
		m.saveResult = msg.result
		m.step = stepDone
		return m, nil
	case tea.KeyMsg:
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
			if m.step == stepCapabilities {
				m.cycleCapability(-1)
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("right", "l"))):
			if m.step == stepCapabilities {
				m.cycleCapability(1)
			}
		case msg.String() == " ":
			if m.step == stepCapabilities {
				m.cycleCapability(1)
			}
		case msg.String() == "n":
			if m.canAdvanceWithN() {
				return m.nextStep()
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
	case stepModel:
		return len(m.models.Items) > 0 && strings.TrimSpace(m.cfg.PrimaryModel()) != ""
	case stepScan:
		return m.scanComplete
	case stepRecommendations:
		return len(m.recommendations) > 0 && !m.recommending
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
	return paragraph(
		"CvkeHarness is a local-first engineering agent for coding, debugging, systems work, and DevOps workflows. It connects a model provider to guarded shell tools, durable memory, scheduled jobs, and an operations TUI.",
		"Setup will configure a provider, choose models, scan this machine, pick a safety level, collect capability preferences, optionally enable public web search, and optionally install the scheduler daemon.",
	) + "\n" + m.renderList([]row{{"Start setup", "Begin guided configuration"}})
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
	case "codex":
		summary, ok := setupflow.CodexAuthSummary()
		status := summary
		if !ok {
			status += " · run `codex login` first"
		}
		return paragraph(status) + "\n" + m.renderList([]row{{"Use this Codex login", "Continue with Codex CLI authentication"}, {"Check again", "Re-read the Codex auth cache"}})
	case "openai":
		return m.apiKeyView("openai", "OpenAI API key")
	case "lmstudio":
		base := m.cfg.BaseURL
		if base == "" {
			base = "http://localhost:1234/v1"
		}
		found := setupflow.DetectLMStudio(context.Background(), base)
		desc := "Use " + base
		if found {
			desc += " · server detected"
		}
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

func (m setupModel) viewModel() string {
	if m.inputMode == inputCustomModel {
		return m.viewInputPrompt("Custom model input active", "Enter the provider-native model ID; press enter to save or esc to cancel.", "Model ID")
	}
	if m.modelsLoading {
		return line("Fetching models...")
	}
	if len(m.models.Items) == 0 {
		return line("Press enter to fetch available models.")
	}
	status := "offline fallback"
	if m.models.Live {
		status = "live models"
	}
	var rows []row
	for _, item := range m.models.Items {
		rows = append(rows, row{item.ID, item.Description})
	}
	return paragraph("Source: "+status+" · "+m.models.Source) + "\n" + m.renderList(rows)
}

func (m setupModel) viewSafety() string {
	return paragraph("Choose the command safety posture. You can change this later in config.") + "\n" + m.renderList(modelRows(setupflow.SafetyOptions()))
}

func (m setupModel) viewScan() string {
	if m.scanning {
		return line("Scanning this system...")
	}
	if !m.scanComplete {
		return line("Press enter to scan OS, installed tools, Python readiness, and network reachability.")
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
	return paragraph(rows...) + "\n" + m.renderList([]row{{"Continue", "Use this scan summary"}})
}

func (m setupModel) viewDependencies() string {
	if m.hostProfile.Python.Found {
		return paragraph("Python is available: "+foundText(m.hostProfile.Python)) + "\n" + m.renderList([]row{{"Continue", "No install needed"}})
	}
	if !m.installPlan.Available {
		return paragraph(m.installPlan.Description) + "\n" + m.renderList([]row{{"Skip", "Continue without installing Python"}})
	}
	return paragraph("Python was not found. Setup can plan an install, but it will only run after final confirmation.", "Command: "+setupflow.CommandString(m.installPlan.Command)) + "\n" + m.renderList([]row{{"Skip", "Continue without installing Python"}, {"Plan install", m.installPlan.Description}})
}

func (m setupModel) viewDaemon() string {
	if m.inputMode == inputDaemonUser {
		return m.viewInputPrompt("System service user input active", "Enter the Linux user that should own the scheduler service; press enter to continue or esc to cancel.", "System service user")
	}
	if !m.daemonPlan.Supported {
		return paragraph("Scheduler daemon install is unavailable: "+m.daemonPlan.Reason) + "\n" + m.renderList([]row{{"Continue", "Skip daemon setup"}})
	}
	return paragraph("The scheduler daemon runs CvkeHarness jobs while the TUI is closed.") + "\n" + m.renderList([]row{
		{"Skip", "Do not install the daemon now"},
		{"Install user service", "Install current-user systemd service"},
		{"Install and start", "Install, enable, and start the user service"},
		{"System service", "Install a system service for a target user"},
	})
}

func (m setupModel) viewCapabilities() string {
	rows := []row{
		{"Python scripts", m.cfg.CapabilityPolicy.PythonScripts},
		{"Autonomous diagnostics", m.cfg.CapabilityPolicy.AutonomousDiagnostics},
		{"Network probes", m.cfg.CapabilityPolicy.NetworkProbes},
		{"Install missing tools", m.cfg.CapabilityPolicy.InstallMissingTools},
	}
	return paragraph("Use left/right or space to change a policy. Press enter to keep the current values and continue. `ask` is the recommended default.") + "\n" + m.renderList(rows)
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
	return paragraph(
		"Web search is optional and read-only. It lets the agent look up public current documentation, release notes, issues, and error messages through Tavily.",
		"Status: "+status+". Never put secrets, private hostnames, or internal URLs into web search requests.",
	) + "\n" + m.renderList(webSearchRows(m.webSearchOptions()))
}

func (m setupModel) viewRecommendations() string {
	if m.recommending {
		return line("Asking the configured model for setup recommendations...")
	}
	if len(m.recommendations) == 0 {
		return line("Press enter to generate guided setup recommendations.")
	}
	return paragraph("Choose what to do with these recommendations. Accepting appends them to machine notes; skipping leaves them out.") + "\n" +
		paragraph(m.recommendations...) + "\n" +
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
	return paragraph(value) + "\n" + m.renderList([]row{{"Edit notes", "Add durable host quirks"}, {"Skip", "Continue without extra notes"}})
}

func (m setupModel) viewReview() string {
	if m.saving {
		return line("Saving setup...")
	}
	lines := []string{
		"Provider: " + m.cfg.Provider,
		"Model: " + m.cfg.PrimaryModel(),
		"Safety: " + m.cfg.SafetyMode,
		"Python: " + foundText(m.hostProfile.Python),
		"Capability policy: scripts=" + m.cfg.CapabilityPolicy.PythonScripts + ", installs=" + m.cfg.CapabilityPolicy.InstallMissingTools,
		"Web search: " + boolText(m.cfg.WebSearch.Enabled),
	}
	if m.installPlan.Selected {
		lines = append(lines, "Will run: "+setupflow.CommandString(m.installPlan.Command))
	}
	if m.daemonPlan.Selected {
		m.daemonPlan.RefreshReviewCommands()
		for _, cmd := range m.daemonPlan.ReviewCommands {
			lines = append(lines, "Will run: "+setupflow.CommandString(cmd))
		}
	}
	return paragraph(lines...) + "\n" + m.renderList([]row{{"Save only", "Write config, memory, and host profile"}, {"Save and apply actions", "Also run selected install/daemon commands"}})
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
	lines = append(lines, "Press q to exit.")
	return paragraph(lines...)
}

func (m setupModel) viewInputPrompt(title, detail, label string) string {
	return paragraph(title, detail) + "\n" + m.renderInputField(label)
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
			m.cfg.Provider = opts[m.cursor].ID
			m.models = setupflow.ModelResult{}
			m.recommendations = nil
		}
		m.cursor = 0
		return m.nextStep()
	case stepCredentials:
		return m.activateCredentials()
	case stepModel:
		if len(m.models.Items) == 0 {
			m.modelsLoading = true
			return m, fetchModelsCmd(m.cfg)
		}
		item := m.models.Items[m.cursor]
		if item.ID == "[ custom model ]" {
			return m.beginInput(inputCustomModel, "Model ID", m.cfg.PrimaryModel(), false), nil
		}
		setupflow.SetDefaultModel(m.cfg, item.ID)
		return m.nextStep()
	case stepSafety:
		opts := setupflow.SafetyOptions()
		m.cfg.SafetyMode = opts[m.cursor].ID
		return m.nextStep()
	case stepScan:
		if !m.scanComplete {
			m.scanning = true
			return m, scanCmd(m.cfg.Provider)
		}
		return m.nextStep()
	case stepDependencies:
		if !m.hostProfile.Python.Found && m.installPlan.Available && m.cursor == 1 {
			m.installPlan.Selected = true
			m.cfg.CapabilityPolicy.InstallMissingTools = "allow"
		}
		return m.nextStep()
	case stepDaemon:
		return m.activateDaemon()
	case stepCapabilities:
		return m.nextStep()
	case stepWebSearch:
		return m.activateWebSearch()
	case stepRecommendations:
		if len(m.recommendations) == 0 {
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
		m.applyActions = m.cursor == 1
		m.saving = true
		return m, saveCmd(m)
	case stepDone:
		return m, tea.Quit
	}
	return m, nil
}

func (m setupModel) activateCredentials() (setupModel, tea.Cmd) {
	switch m.cfg.Provider {
	case "codex":
		if m.cursor == 1 {
			m.message = "Codex auth cache refreshed"
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
		m.inputMode = inputNone
		switch mode {
		case inputOpenRouterKey:
			m.cfg.SetAPIKey("openrouter", value)
			m.validating = true
			return m, validateOpenRouterCmd(value)
		case inputOpenAIKey:
			m.cfg.SetAPIKey("openai", value)
			m.validating = true
			return m, validateOpenAICmd(value)
		case inputTavilyKey:
			m.cfg.SetAPIKey("tavily", value)
			enableWebSearchDefaults(m.cfg)
			m.validating = true
			return m, validateTavilyCmd(value)
		case inputLMStudioURL:
			m.cfg.BaseURL = value
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
	if m.step < stepReview {
		m.step++
		m.cursor = 0
	}
	return m, nil
}

func (m setupModel) prevStep() (setupModel, tea.Cmd) {
	if m.step > stepWelcome && !m.saving && !m.scanning && !m.validating {
		m.step--
		m.cursor = 0
	}
	return m, nil
}

func (m *setupModel) moveCursor(delta int) {
	count := m.itemCount()
	if count <= 0 {
		return
	}
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
	case stepModel:
		return len(m.models.Items)
	case stepSafety:
		return len(setupflow.SafetyOptions())
	case stepScan:
		return 1
	case stepRecommendations:
		if len(m.recommendations) == 0 {
			return 1
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
		return 4
	case stepWebSearch:
		return len(m.webSearchOptions())
	case stepSoul:
		return len(setupflow.SoulProfiles())
	case stepNotes:
		return 2
	case stepReview:
		return 2
	}
	return 0
}

func (m *setupModel) cycleCapability(delta int) {
	values := []string{"ask", "allow", "deny"}
	cycle := func(current string) string {
		idx := 0
		for i, value := range values {
			if value == current {
				idx = i
				break
			}
		}
		return values[(idx+delta+len(values))%len(values)]
	}
	switch m.cursor {
	case 0:
		m.cfg.CapabilityPolicy.PythonScripts = cycle(m.cfg.CapabilityPolicy.PythonScripts)
	case 1:
		m.cfg.CapabilityPolicy.AutonomousDiagnostics = cycle(m.cfg.CapabilityPolicy.AutonomousDiagnostics)
	case 2:
		m.cfg.CapabilityPolicy.NetworkProbes = cycle(m.cfg.CapabilityPolicy.NetworkProbes)
	case 3:
		m.cfg.CapabilityPolicy.InstallMissingTools = cycle(m.cfg.CapabilityPolicy.InstallMissingTools)
	}
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
