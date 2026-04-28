package setupflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

const HostProfileFile = "host_profile.json"

type ProviderOption struct {
	ID          string
	Label       string
	Description string
}

type ModelOption struct {
	ID          string
	Description string
}

type ModelResult struct {
	Items     []ModelOption
	Live      bool
	Source    string
	Message   string
	Timestamp time.Time
}

type ToolStatus struct {
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Found   bool   `json:"found"`
}

type HostProfile struct {
	ScannedAt          time.Time    `json:"scanned_at"`
	OS                 string       `json:"os"`
	Arch               string       `json:"arch"`
	Hostname           string       `json:"hostname,omitempty"`
	Shell              string       `json:"shell,omitempty"`
	CPUs               int          `json:"cpus"`
	HomeDir            string       `json:"home_dir,omitempty"`
	MemoryBytes        uint64       `json:"memory_bytes,omitempty"`
	ConfigDirFreeBytes uint64       `json:"config_dir_free_bytes,omitempty"`
	InternetReachable  bool         `json:"internet_reachable"`
	ProviderReachable  bool         `json:"provider_reachable"`
	Tools              []ToolStatus `json:"tools"`
	Python             ToolStatus   `json:"python"`
	PackageManagers    []ToolStatus `json:"package_managers"`
	Warnings           []string     `json:"warnings,omitempty"`
}

type InstallPlan struct {
	Tool        string   `json:"tool"`
	Command     []string `json:"command"`
	Description string   `json:"description"`
	Available   bool     `json:"available"`
	Selected    bool     `json:"selected"`
}

type DaemonPlan struct {
	Supported      bool          `json:"supported"`
	Reason         string        `json:"reason,omitempty"`
	SystemService  bool          `json:"system_service"`
	User           string        `json:"user,omitempty"`
	Interval       time.Duration `json:"interval"`
	EnableLinger   bool          `json:"enable_linger"`
	EnableNow      bool          `json:"enable_now"`
	StartNow       bool          `json:"start_now"`
	Selected       bool          `json:"selected"`
	ReviewCommands [][]string    `json:"review_commands,omitempty"`
}

type SoulProfile struct {
	ID               string
	Label            string
	Description      string
	Tone             string
	Autonomy         string
	RiskPosture      string
	ExplanationDepth string
}

type FinalizeOptions struct {
	Config       *config.Config
	HostProfile  HostProfile
	InstallPlans []InstallPlan
	DaemonPlan   DaemonPlan
	SoulProfile  SoulProfile
	HostNotes    []string
	ApplyActions bool
}

type FinalizeResult struct {
	ConfigSaved      bool
	HostProfilePath  string
	SoulWritten      bool
	HostNotesWritten bool
	ActionOutput     []string
}

type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type RealRunner struct{}

func (RealRunner) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (RealRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type Scanner struct {
	Runner CommandRunner
	Client *http.Client
	GOOS   string
	GOARCH string
}

func ProviderOptions() []ProviderOption {
	return []ProviderOption{
		{ID: "codex", Label: "Codex", Description: "ChatGPT subscription via Codex CLI login"},
		{ID: "openrouter", Label: "OpenRouter", Description: "Cloud API with many coding models"},
		{ID: "openai", Label: "OpenAI", Description: "Usage-based OpenAI API models"},
		{ID: "lmstudio", Label: "LM Studio", Description: "Local OpenAI-compatible inference"},
	}
}

func SafetyOptions() []ModelOption {
	return []ModelOption{
		{ID: tools.SafetyModeLLMJudge, Description: "Recommended: allowlist plus LLM judge for other commands"},
		{ID: tools.SafetyModeUserConfirmAll, Description: "Ask for user approval before every shell command"},
		{ID: tools.SafetyModeUnrestricted, Description: "Risky: run parsed shell commands without approval"},
	}
}

func SoulProfiles() []SoulProfile {
	return []SoulProfile{
		{ID: "balanced", Label: "Balanced", Description: "Clear, steady, and practical", Tone: "balanced", Autonomy: "balanced", RiskPosture: "standard", ExplanationDepth: "standard"},
		{ID: "concise", Label: "Concise", Description: "Short answers with proactive execution", Tone: "terse", Autonomy: "proactive", RiskPosture: "standard", ExplanationDepth: "brief"},
		{ID: "cautious", Label: "Cautious", Description: "Ask-first and conservative around risk", Tone: "balanced", Autonomy: "ask-first", RiskPosture: "conservative", ExplanationDepth: "standard"},
		{ID: "mentor", Label: "Mentor", Description: "More explanatory and teaching-oriented", Tone: "explanatory", Autonomy: "balanced", RiskPosture: "standard", ExplanationDepth: "detailed"},
	}
}

func DefaultSoulProfile() SoulProfile { return SoulProfiles()[0] }

func LoadWizardConfig() *config.Config {
	existing, _ := config.LoadConfig()
	cfg := config.DefaultConfig()
	if existing == nil {
		cfg.Normalize()
		return cfg
	}
	clone := *cfg
	clone.Provider = firstNonEmpty(existing.Provider, cfg.Provider)
	clone.DefaultModel = firstNonEmpty(existing.PrimaryModel(), cfg.PrimaryModel())
	clone.APIKeys = cloneStringMap(existing.APIKeys)
	clone.BaseURL = firstNonEmpty(existing.BaseURL, cfg.BaseURL)
	clone.SafetyMode = firstNonEmpty(existing.SafetyMode, cfg.SafetyMode)
	clone.SafetyModel = firstNonEmpty(existing.SafetyModel, cfg.SafetyModel)
	if existing.MaxTokens > 0 {
		clone.MaxTokens = existing.MaxTokens
	}
	if existing.MaxIterations > 0 {
		clone.MaxIterations = existing.MaxIterations
	}
	clone.LogLevel = firstNonEmpty(existing.LogLevel, cfg.LogLevel)
	clone.AllowedCommands = append([]string(nil), existing.AllowedCommands...)
	clone.RoutingEnabled = existing.RoutingEnabled
	clone.RoutingMode = firstNonEmpty(existing.RoutingMode, cfg.RoutingMode)
	clone.ApprovedModels = append([]string(nil), existing.ApprovedModels...)
	clone.FavoriteModels = append([]string(nil), existing.FavoriteModels...)
	clone.MemoryDir = firstNonEmpty(existing.MemoryDir, cfg.MemoryDir)
	clone.StateDBPath = firstNonEmpty(existing.StateDBPath, cfg.StateDBPath)
	clone.DebugPromptDumps = existing.DebugPromptDumps
	clone.PromptDumpDir = firstNonEmpty(existing.PromptDumpDir, cfg.PromptDumpDir)
	clone.MemoryMaxSnippets = existing.MemoryMaxSnippets
	clone.RoutingMinConfidence = existing.RoutingMinConfidence
	clone.SetupAgentMode = firstNonEmpty(existing.SetupAgentMode, cfg.SetupAgentMode)
	clone.CapabilityPolicy = existing.CapabilityPolicy
	clone.Normalize()
	return &clone
}

func SetDefaultModel(cfg *config.Config, model string) {
	model = config.NormalizeProviderModelID(cfg.Provider, model)
	cfg.DefaultModel = model
	EnsureDefaultApproved(cfg)
}

func EnsureDefaultApproved(cfg *config.Config) {
	providerName := strings.TrimSpace(cfg.Provider)
	model := strings.TrimSpace(cfg.PrimaryModel())
	if providerName == "" || model == "" {
		return
	}
	entry := providerName + "/" + model
	for _, existing := range cfg.ApprovedModels {
		if existing == entry {
			return
		}
	}
	cfg.ApprovedModels = append(cfg.ApprovedModels, entry)
}

func ValidateOpenRouterKey(ctx context.Context, key string) (string, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/auth/key", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Data struct {
			Label string `json:"label"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode == http.StatusUnauthorized {
		msg := firstNonEmpty(out.Error.Message, "unauthorized")
		return "", fmt.Errorf("invalid key: %s", msg)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from validation endpoint", resp.StatusCode)
	}
	return out.Data.Label, nil
}

func ValidateOpenAIKey(ctx context.Context, key string) error {
	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.openai.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid credential: %s", firstNonEmpty(out.Error.Message, "unauthorized"))
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error.Message != "" {
			return fmt.Errorf("unexpected status %d from validation endpoint: %s", resp.StatusCode, out.Error.Message)
		}
		return fmt.Errorf("unexpected status %d from validation endpoint", resp.StatusCode)
	}
	return nil
}

func CodexAuthSummary() (string, bool) {
	auth, err := provider.LoadCodexCLIAuth(provider.CodexAuthPath())
	if err != nil {
		return fmt.Sprintf("No Codex login found at %s", provider.CodexAuthPath()), false
	}
	if auth.AccountID != "" {
		return "Found Codex login for " + auth.AccountID, true
	}
	return "Found Codex login", true
}

func DetectLMStudio(ctx context.Context, baseURL string) bool {
	if baseURL == "" {
		baseURL = "http://localhost:1234/v1"
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func FetchModels(ctx context.Context, cfg *config.Config) ModelResult {
	switch cfg.Provider {
	case "codex":
		return fetchCodexModels(time.Now())
	case "openai":
		return fetchOpenAIModels(ctx, cfg.GetAPIKey("openai"))
	case "lmstudio":
		return fetchLMStudioModels(ctx, cfg.BaseURL)
	default:
		return fetchOpenRouterModels(ctx)
	}
}

func (s Scanner) Scan(ctx context.Context, providerName string) HostProfile {
	runner := s.Runner
	if runner == nil {
		runner = RealRunner{}
	}
	goos := firstNonEmpty(s.GOOS, runtime.GOOS)
	goarch := firstNonEmpty(s.GOARCH, runtime.GOARCH)
	hostname, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	profile := HostProfile{
		ScannedAt: time.Now().UTC(),
		OS:        goos,
		Arch:      goarch,
		Hostname:  hostname,
		Shell:     os.Getenv("SHELL"),
		CPUs:      runtime.NumCPU(),
		HomeDir:   home,
	}
	profile.MemoryBytes = probeMemoryBytes(ctx, runner, goos)
	profile.ConfigDirFreeBytes = probeDiskFreeBytes(ctx, runner, home)
	profile.Tools = probeTools(ctx, runner, []string{"go", "git", "python3", "python", "pip3", "pip", "uv", "node", "npm", "docker", "podman", "kubectl", "systemctl", "launchctl", "curl", "wget", "jq", "rg", "gh", "codex"})
	profile.PackageManagers = probeTools(ctx, runner, []string{"brew", "apt-get", "dnf", "yum", "pacman", "winget", "choco"})
	profile.Python = firstFound(profile.Tools, "python3", "python")
	if !profile.Python.Found {
		profile.Warnings = append(profile.Warnings, "Python was not found on PATH.")
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	profile.InternetReachable = probeURL(ctx, client, "https://example.com")
	profile.ProviderReachable = probeURL(ctx, client, providerProbeURL(providerName))
	return profile
}

func PlanMissingPython(profile HostProfile) InstallPlan {
	if profile.Python.Found {
		return InstallPlan{Tool: "python", Description: "Python is already available", Available: false}
	}
	for _, manager := range profile.PackageManagers {
		if !manager.Found {
			continue
		}
		switch manager.Name {
		case "brew":
			return InstallPlan{Tool: "python", Command: []string{"brew", "install", "python"}, Description: "Install Python with Homebrew", Available: true}
		case "apt-get":
			return InstallPlan{Tool: "python", Command: []string{"sudo", "apt-get", "install", "-y", "python3", "python3-pip", "python3-venv"}, Description: "Install Python with apt", Available: true}
		case "dnf":
			return InstallPlan{Tool: "python", Command: []string{"sudo", "dnf", "install", "-y", "python3", "python3-pip"}, Description: "Install Python with dnf", Available: true}
		case "yum":
			return InstallPlan{Tool: "python", Command: []string{"sudo", "yum", "install", "-y", "python3", "python3-pip"}, Description: "Install Python with yum", Available: true}
		case "pacman":
			return InstallPlan{Tool: "python", Command: []string{"sudo", "pacman", "-S", "--needed", "python", "python-pip"}, Description: "Install Python with pacman", Available: true}
		case "winget":
			return InstallPlan{Tool: "python", Command: []string{"winget", "install", "-e", "--id", "Python.Python.3.13"}, Description: "Install Python with winget", Available: true}
		}
	}
	return InstallPlan{Tool: "python", Description: "Python is missing and no supported package manager was detected", Available: false}
}

func DetectDaemonPlan(runner CommandRunner, goos string) DaemonPlan {
	if runner == nil {
		runner = RealRunner{}
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos != "linux" {
		return DaemonPlan{Supported: false, Reason: "systemd daemon install is currently supported only on Linux"}
	}
	if _, err := runner.LookPath("systemctl"); err != nil {
		return DaemonPlan{Supported: false, Reason: "systemctl was not found"}
	}
	plan := DaemonPlan{Supported: true, Interval: 30 * time.Second}
	plan.RefreshReviewCommands()
	return plan
}

func (p *DaemonPlan) RefreshReviewCommands() {
	if p.Interval <= 0 {
		p.Interval = 30 * time.Second
	}
	var install []string
	if p.SystemService {
		install = []string{"cvkeharness", "daemon", "install", "--system", "--user", p.User, "--interval", p.Interval.String()}
	} else {
		install = []string{"cvkeharness", "daemon", "install", "--interval", p.Interval.String()}
		if p.EnableLinger {
			install = append(install, "--enable-linger")
		}
	}
	cmds := [][]string{install}
	if p.EnableNow {
		cmds = append(cmds, daemonActionCommand("enable", *p))
	}
	if p.StartNow {
		cmds = append(cmds, daemonActionCommand("start", *p))
	}
	p.ReviewCommands = cmds
}

func GenerateRecommendations(cfg *config.Config, profile HostProfile, installPlan InstallPlan, daemonPlan DaemonPlan) []string {
	var out []string
	if cfg.SafetyMode == tools.SafetyModeUnrestricted {
		out = append(out, "Unrestricted shell mode is fastest, but it removes CvkeHarness' approval guard. Use it only on disposable or trusted environments.")
	}
	if !profile.Python.Found {
		out = append(out, "Install Python if you expect the agent to generate diagnostic scripts or data-processing helpers.")
	}
	if installPlan.Selected {
		out = append(out, "Review the Python install command before applying; setup will not run it until the final confirmation.")
	}
	if daemonPlan.Supported && !daemonPlan.Selected {
		out = append(out, "Install the scheduler daemon if you want CvkeHarness jobs to run while the TUI is closed.")
	}
	if !profile.InternetReachable {
		out = append(out, "Internet reachability failed; cloud providers may not work until network or DNS is fixed.")
	}
	if len(out) == 0 {
		out = append(out, "Configuration looks ready. Keep the default guided permissions until a workflow needs broader autonomy.")
	}
	return out
}

func AgentRecommendations(ctx context.Context, cfg *config.Config, profile HostProfile, installPlan InstallPlan, daemonPlan DaemonPlan) ([]string, error) {
	p, err := resolveProvider(cfg)
	if err != nil {
		return nil, err
	}
	prompt := strings.Join([]string{
		"You are helping configure CvkeHarness, a local-first DevOps and coding agent.",
		"Return 3 to 6 concise setup recommendations as plain bullet lines. Do not ask to run commands.",
		"",
		"Provider: " + cfg.Provider,
		"Model: " + cfg.PrimaryModel(),
		"Safety mode: " + cfg.SafetyMode,
		fmt.Sprintf("Host: %s/%s, CPUs=%d, Python=%v, internet=%v, provider endpoint=%v", profile.OS, profile.Arch, profile.CPUs, profile.Python.Found, profile.InternetReachable, profile.ProviderReachable),
		"Python install plan: " + installPlan.Description + " selected=" + strconv.FormatBool(installPlan.Selected),
		"Daemon supported=" + strconv.FormatBool(daemonPlan.Supported) + " selected=" + strconv.FormatBool(daemonPlan.Selected),
		"Capabilities: python_scripts=" + cfg.CapabilityPolicy.PythonScripts + ", diagnostics=" + cfg.CapabilityPolicy.AutonomousDiagnostics + ", network=" + cfg.CapabilityPolicy.NetworkProbes + ", installs=" + cfg.CapabilityPolicy.InstallMissingTools,
	}, "\n")
	resp, err := p.ChatCompletion(ctx, &provider.ChatRequest{
		Model:       cfg.PrimaryModel(),
		Messages:    []provider.Message{{Role: "user", Content: prompt}},
		Temperature: 0.2,
		MaxTokens:   500,
	})
	if err != nil {
		return nil, err
	}
	return parseRecommendationLines(resp.Message.Content), nil
}

func Finalize(ctx context.Context, opts FinalizeOptions) (FinalizeResult, error) {
	if opts.Config == nil {
		return FinalizeResult{}, fmt.Errorf("config is required")
	}
	cfg := opts.Config
	cfg.Normalize()
	EnsureDefaultApproved(cfg)
	if err := cfg.Save(); err != nil {
		return FinalizeResult{}, err
	}
	result := FinalizeResult{ConfigSaved: true}
	hostPath, err := WriteHostProfile(cfg.MemoryDir, opts.HostProfile)
	if err != nil {
		return result, err
	}
	result.HostProfilePath = hostPath
	wroteSoul, err := WriteSoul(cfg.MemoryDir, cfg.MemoryMaxSnippets, opts.SoulProfile)
	if err != nil {
		return result, err
	}
	result.SoulWritten = wroteSoul
	wroteHost, err := WriteHostNotes(cfg.MemoryDir, cfg.MemoryMaxSnippets, append(SummarizeHostProfile(opts.HostProfile), opts.HostNotes...))
	if err != nil {
		return result, err
	}
	result.HostNotesWritten = wroteHost
	if !opts.ApplyActions {
		return result, nil
	}
	runner := RealRunner{}
	for _, plan := range opts.InstallPlans {
		if !plan.Selected || !plan.Available || len(plan.Command) == 0 {
			continue
		}
		out, err := runner.Run(ctx, plan.Command[0], plan.Command[1:]...)
		result.ActionOutput = append(result.ActionOutput, strings.TrimSpace(out))
		if err != nil {
			return result, err
		}
	}
	if opts.DaemonPlan.Selected {
		opts.DaemonPlan.RefreshReviewCommands()
		exe, err := os.Executable()
		if err != nil {
			return result, err
		}
		for _, raw := range opts.DaemonPlan.ReviewCommands {
			cmd := append([]string(nil), raw...)
			if len(cmd) > 0 && cmd[0] == "cvkeharness" {
				cmd[0] = exe
			}
			out, err := runner.Run(ctx, cmd[0], cmd[1:]...)
			result.ActionOutput = append(result.ActionOutput, strings.TrimSpace(out))
			if err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func resolveProvider(cfg *config.Config) (provider.Provider, error) {
	switch cfg.Provider {
	case "codex":
		return provider.NewCodexFromCLIAuth(), nil
	case "openrouter":
		return provider.NewOpenRouter(cfg.GetAPIKey("openrouter")), nil
	case "openai":
		return provider.NewOpenAI(cfg.GetAPIKey("openai")), nil
	case "lmstudio":
		return provider.NewLMStudio(cfg.BaseURL), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}

func parseRecommendationLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 && strings.TrimSpace(raw) != "" {
		out = []string{strings.TrimSpace(raw)}
	}
	return out
}

func WriteHostProfile(memoryDir string, profile HostProfile) (string, error) {
	if memoryDir == "" {
		memoryDir = defaultHarnessDir()
	}
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(memoryDir, HostProfileFile)
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0644)
}

func SummarizeHostProfile(profile HostProfile) []string {
	var notes []string
	if profile.OS != "" {
		notes = append(notes, fmt.Sprintf("Setup detected host platform %s/%s with %d CPU cores.", profile.OS, profile.Arch, profile.CPUs))
	}
	if profile.Python.Found {
		notes = append(notes, fmt.Sprintf("Python is available at %s%s.", profile.Python.Path, versionSuffix(profile.Python.Version)))
	} else {
		notes = append(notes, "Python was not found on PATH during setup.")
	}
	if !profile.InternetReachable {
		notes = append(notes, "Setup could not confirm internet access from this host.")
	}
	return notes
}

func WriteSoul(memoryDir string, maxSnippets int, profile SoulProfile) (bool, error) {
	manager := memory.NewManager(memoryDir, nil, maxSnippets)
	if err := manager.EnsureFiles(); err != nil {
		return false, err
	}
	path := filepath.Join(memoryDir, memory.SoulFile)
	data, err := os.ReadFile(path)
	if err == nil {
		trimmed := strings.TrimSpace(string(data))
		if trimmed != "" && trimmed != "# Soul" {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if profile.ID == "" {
		profile = DefaultSoulProfile()
	}
	content := fmt.Sprintf(`# Soul

You are CvkeHarness, a local-first engineering agent for coding, debugging, systems work, and DevOps-style workflows.

## Purpose
Help the operator understand, repair, automate, and monitor local systems while respecting explicit safety and capability policy.

## Working Style
- Tone: %s
- Autonomy: %s
- Risk posture: %s
- Explanation depth: %s

## Boundaries
Prefer reversible actions, inspect before changing state, and surface uncertainty before risky operations.
`, profile.Tone, profile.Autonomy, profile.RiskPosture, profile.ExplanationDepth)
	return true, os.WriteFile(path, []byte(content), 0644)
}

func WriteHostNotes(memoryDir string, maxSnippets int, notes []string) (bool, error) {
	notes = dedupe(notes)
	if len(notes) == 0 {
		return false, nil
	}
	manager := memory.NewManager(memoryDir, nil, maxSnippets)
	return manager.SeedRuntimeHostNotes(context.Background(), notes)
}

func MaskSecret(value string) string {
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", 8) + value[len(value)-4:]
}

func CommandString(cmd []string) string {
	var parts []string
	for _, part := range cmd {
		if strings.ContainsAny(part, " \t\n\"'\\") {
			parts = append(parts, strconv.Quote(part))
		} else {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " ")
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
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

func probeTools(ctx context.Context, runner CommandRunner, names []string) []ToolStatus {
	out := make([]ToolStatus, 0, len(names))
	for _, name := range names {
		path, err := runner.LookPath(name)
		status := ToolStatus{Name: name, Path: path, Found: err == nil}
		if status.Found {
			status.Version = probeVersion(ctx, runner, name)
		}
		out = append(out, status)
	}
	return out
}

func probeVersion(ctx context.Context, runner CommandRunner, name string) string {
	args := []string{"--version"}
	if name == "python" || name == "python3" {
		args = []string{"--version"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := runner.Run(probeCtx, name, args...)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")[0])
	return line
}

func firstFound(tools []ToolStatus, names ...string) ToolStatus {
	for _, name := range names {
		for _, tool := range tools {
			if tool.Name == name && tool.Found {
				return tool
			}
		}
	}
	return ToolStatus{Name: firstNonEmpty(names...)}
}

func probeURL(ctx context.Context, client *http.Client, url string) bool {
	if url == "" {
		return false
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err = client.Do(req)
	}
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func providerProbeURL(providerName string) string {
	switch providerName {
	case "openai":
		return "https://api.openai.com/v1/models"
	case "openrouter":
		return "https://openrouter.ai/api/v1/models"
	case "lmstudio":
		return "http://localhost:1234/v1/models"
	default:
		return "https://api.openai.com"
	}
}

func probeMemoryBytes(ctx context.Context, runner CommandRunner, goos string) uint64 {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	switch goos {
	case "darwin":
		out, err := runner.Run(probeCtx, "sysctl", "-n", "hw.memsize")
		if err == nil {
			if n, parseErr := strconv.ParseUint(strings.TrimSpace(out), 10, 64); parseErr == nil {
				return n
			}
		}
	default:
		out, err := runner.Run(probeCtx, "free", "-b")
		if err == nil {
			return parseFreeMemoryBytes(out)
		}
	}
	return 0
}

func probeDiskFreeBytes(ctx context.Context, runner CommandRunner, path string) uint64 {
	if path == "" {
		path = "."
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := runner.Run(probeCtx, "df", "-k", path)
	if err != nil {
		return 0
	}
	return parseDFFreeBytes(out)
}

func parseFreeMemoryBytes(out string) uint64 {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimSuffix(fields[0], ":") == "Mem" {
			n, _ := strconv.ParseUint(fields[1], 10, 64)
			return n
		}
	}
	return 0
}

func parseDFFreeBytes(out string) uint64 {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0
	}
	kb, _ := strconv.ParseUint(fields[3], 10, 64)
	return kb * 1024
}

func daemonActionCommand(action string, plan DaemonPlan) []string {
	cmd := []string{"cvkeharness", "daemon", action}
	if plan.SystemService {
		cmd = append(cmd, "--system", "--user", plan.User)
	}
	return cmd
}

func versionSuffix(version string) string {
	if version == "" {
		return ""
	}
	return " (" + version + ")"
}

func defaultHarnessDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cvkeharness"
	}
	return filepath.Join(home, ".cvkeharness")
}

func dedupe(notes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, note := range notes {
		note = strings.Join(strings.Fields(strings.TrimSpace(note)), " ")
		if note == "" || seen[note] {
			continue
		}
		seen[note] = true
		out = append(out, note)
	}
	return out
}

func currentUserName() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

func sortModelOptions(items []ModelOption) []ModelOption {
	out := append([]ModelOption(nil), items...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
