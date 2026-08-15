package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coolcake/cvkeharness/securitypolicy"
	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Provider string `yaml:"provider"`
	// APIKeys stores credentials keyed by provider name (e.g. "openrouter",
	// "anthropic"). All providers are preserved so a user switching providers
	// never has to re-enter a key they already validated.
	APIKeys                 map[string]string         `yaml:"api_keys,omitempty"`
	BaseURL                 string                    `yaml:"base_url,omitempty"` // Used for local providers (e.g. LM Studio)
	Model                   string                    `yaml:"model,omitempty"`    // legacy read compatibility only
	DefaultModel            string                    `yaml:"default_model,omitempty"`
	PlanningModel           string                    `yaml:"planning_model,omitempty"`
	ExecutionModel          string                    `yaml:"execution_model,omitempty"`
	CurationModel           string                    `yaml:"curation_model,omitempty"`
	SafetyMode              string                    `yaml:"safety_mode,omitempty"`
	SafetyModel             string                    `yaml:"safety_model"`
	MaxTokens               int                       `yaml:"max_tokens"`
	MaxIterations           int                       `yaml:"max_iterations"`
	LogLevel                string                    `yaml:"log_level"`
	AllowedCommands         []string                  `yaml:"allowed_commands"`
	RoutingEnabled          bool                      `yaml:"routing_enabled,omitempty"`
	RoutingMode             string                    `yaml:"routing_mode,omitempty"`
	ApprovedModels          []string                  `yaml:"approved_models,omitempty"`
	FavoriteModels          []string                  `yaml:"favorite_models,omitempty"`
	MemoryDir               string                    `yaml:"memory_dir,omitempty"`
	StateDBPath             string                    `yaml:"state_db_path,omitempty"`
	DebugPromptDumps        bool                      `yaml:"debug_prompt_dumps,omitempty"`
	PromptDumpDir           string                    `yaml:"prompt_dump_dir,omitempty"`
	PromptDumpRetentionDays int                       `yaml:"prompt_dump_retention_days,omitempty"`
	RoutingMinConfidence    float64                   `yaml:"routing_min_confidence,omitempty"`
	SetupAgentMode          string                    `yaml:"setup_agent_mode,omitempty"`
	CapabilityPolicy        CapabilityPolicy          `yaml:"capability_policy,omitempty"`
	WebSearch               WebSearchConfig           `yaml:"web_search,omitempty"`
	Security                *securitypolicy.Selection `yaml:"security,omitempty"`
}

// CapabilityPolicy captures durable user preferences collected during setup.
type CapabilityPolicy struct {
	PythonScripts         string `yaml:"python_scripts,omitempty"`
	ScriptWriteDir        string `yaml:"script_write_dir,omitempty"`
	AutonomousDiagnostics string `yaml:"autonomous_diagnostics,omitempty"`
	NetworkProbes         string `yaml:"network_probes,omitempty"`
	InstallMissingTools   string `yaml:"install_missing_tools,omitempty"`
}

// WebSearchConfig controls optional external web research tools.
type WebSearchConfig struct {
	Enabled         bool     `yaml:"enabled,omitempty"`
	Provider        string   `yaml:"provider,omitempty"`
	MaxResults      int      `yaml:"max_results,omitempty"`
	SearchDepth     string   `yaml:"search_depth,omitempty"`
	MaxFetchedChars int      `yaml:"max_fetched_chars,omitempty"`
	AllowedDomains  []string `yaml:"allowed_domains,omitempty"`
	BlockedDomains  []string `yaml:"blocked_domains,omitempty"`
}

// GetAPIKey returns the stored credential for the given provider, or "" if none.
func (c *Config) GetAPIKey(provider string) string {
	if c.APIKeys == nil {
		return ""
	}
	return c.APIKeys[provider]
}

// SetAPIKey stores a credential for the given provider.
func (c *Config) SetAPIKey(provider, key string) {
	if c.APIKeys == nil {
		c.APIKeys = make(map[string]string)
	}
	c.APIKeys[provider] = key
}

// TavilyAPIKey returns the configured Tavily key, falling back to TAVILY_API_KEY.
func (c *Config) TavilyAPIKey() string {
	if key := strings.TrimSpace(c.GetAPIKey("tavily")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("TAVILY_API_KEY"))
}

// PrimaryModel returns the configured default model with legacy fallback.
func (c *Config) PrimaryModel() string {
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	return c.Model
}

// NormalizeProviderModelID canonicalizes configured model IDs that may have
// been entered in provider/model form even though the runtime expects the raw
// provider-native model identifier. For example, with provider=openrouter, both
// "google/gemma-4-31b-it:free" and "openrouter/google/gemma-4-31b-it:free"
// should execute as "google/gemma-4-31b-it:free". Special OpenRouter model
// IDs such as "openrouter/auto" and "openrouter/free" are preserved.
func NormalizeProviderModelID(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return model
	}

	prefix := provider + "/"
	if !strings.HasPrefix(model, prefix) {
		return model
	}

	if provider == "openrouter" && (model == "openrouter/auto" || model == "openrouter/free") {
		return model
	}

	return strings.TrimPrefix(model, prefix)
}

// Normalize applies defaults and backward-compatible migrations in memory.
func (c *Config) Normalize() {
	// Preserve support for older configs that only set `model`, but treat
	// `default_model` as the canonical field going forward.
	if c.DefaultModel == "" {
		c.DefaultModel = c.Model
	}
	c.DefaultModel = NormalizeProviderModelID(c.Provider, c.DefaultModel)
	c.Model = NormalizeProviderModelID(c.Provider, c.Model)

	// Fallback for safety model if not found
	if c.SafetyModel == "" {
		c.SafetyModel = "x-ai/grok-4.1-fast"
	}
	c.SafetyModel = NormalizeProviderModelID(c.Provider, c.SafetyModel)
	if c.SafetyMode == "" {
		c.SafetyMode = "llm_judge"
	}
	if c.RoutingMode == "" {
		c.RoutingMode = "auto_within_policy"
	}
	if c.RoutingMinConfidence <= 0 {
		c.RoutingMinConfidence = 0.55
	}
	if c.SetupAgentMode == "" {
		c.SetupAgentMode = "guided"
	}
	if c.MemoryDir == "" {
		c.MemoryDir = defaultHarnessPath("memory")
	}
	if c.StateDBPath == "" {
		c.StateDBPath = defaultHarnessPath("state.db")
	}
	if c.PromptDumpDir == "" {
		c.PromptDumpDir = defaultHarnessPath("prompt_dumps")
	}
	if c.PromptDumpRetentionDays <= 0 {
		c.PromptDumpRetentionDays = 7
	}
	if c.CapabilityPolicy.PythonScripts == "" {
		c.CapabilityPolicy.PythonScripts = "ask"
	}
	if c.CapabilityPolicy.ScriptWriteDir == "" {
		c.CapabilityPolicy.ScriptWriteDir = defaultHarnessPath("scripts")
	}
	if c.CapabilityPolicy.AutonomousDiagnostics == "" {
		c.CapabilityPolicy.AutonomousDiagnostics = "ask"
	}
	if c.CapabilityPolicy.NetworkProbes == "" {
		c.CapabilityPolicy.NetworkProbes = "ask"
	}
	if c.CapabilityPolicy.InstallMissingTools == "" {
		c.CapabilityPolicy.InstallMissingTools = "ask"
	}
	if c.Security == nil {
		c.Security = c.migrateLegacySecurity()
	}
	c.Security.Normalize()
	c.projectLegacySecurity()
	c.normalizeWebSearch()
	if len(c.ApprovedModels) == 0 && c.DefaultModel != "" && c.Provider != "" {
		c.ApprovedModels = []string{c.Provider + "/" + c.DefaultModel}
	}
	c.ApprovedModels = normalizeModelRefList(c.ApprovedModels)
	c.FavoriteModels = normalizeModelRefList(c.FavoriteModels)
	ensureAllowedCommand(c, "echo")
}

// Validate rejects invalid security settings instead of silently falling back
// to a weaker profile.
func (c *Config) Validate() error {
	if c.Security == nil {
		return fmt.Errorf("security configuration is required")
	}
	return c.Security.Validate()
}

// EffectiveSecurity returns the single resolved policy used by runtimes.
func (c *Config) EffectiveSecurity() (securitypolicy.EffectivePolicy, error) {
	if c == nil {
		return securitypolicy.EffectivePolicy{}, fmt.Errorf("configuration is required")
	}
	return securitypolicy.Resolve(c.Security)
}

// Clone returns a deep copy suitable for an immutable session snapshot.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := *c
	out.Security = c.Security.Clone()
	if c.APIKeys != nil {
		out.APIKeys = make(map[string]string, len(c.APIKeys))
		for key, value := range c.APIKeys {
			out.APIKeys[key] = value
		}
	}
	out.AllowedCommands = append([]string(nil), c.AllowedCommands...)
	out.ApprovedModels = append([]string(nil), c.ApprovedModels...)
	out.FavoriteModels = append([]string(nil), c.FavoriteModels...)
	out.WebSearch.AllowedDomains = append([]string(nil), c.WebSearch.AllowedDomains...)
	out.WebSearch.BlockedDomains = append([]string(nil), c.WebSearch.BlockedDomains...)
	return &out
}

// ConfigPath returns the path to the config file (~/.cvkeharness/config.yaml).
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cvkeharness", "config.yaml"), nil
}

// EnsureConfigPath creates the ~/.cvkeharness directory if it doesn't exist.
func EnsureConfigPath() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0755)
}

// LoadConfig reads the configuration from disk.
func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found. Please run 'cvkeharness setup'")
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	cfg.Normalize()

	return &cfg, nil
}

// Save writes the configuration to disk.
func (c *Config) Save() error {
	if err := EnsureConfigPath(); err != nil {
		return err
	}

	path, err := ConfigPath()
	if err != nil {
		return err
	}

	c.Normalize()
	if err := c.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	// Keep reading legacy `model`, but stop actively persisting it.
	c.Model = ""

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

// DefaultConfig provides sensible defaults for a new setup.
func DefaultConfig() *Config {
	return &Config{
		Provider:                "openrouter",
		DefaultModel:            "anthropic/claude-sonnet-4.6",
		SafetyMode:              "llm_judge",
		SafetyModel:             "x-ai/grok-4.1-fast",
		MaxTokens:               4096,
		MaxIterations:           25,
		LogLevel:                "off",
		RoutingMode:             "auto_within_policy",
		MemoryDir:               defaultHarnessPath(""),
		StateDBPath:             defaultHarnessPath("state.db"),
		PromptDumpDir:           defaultHarnessPath("prompt_dumps"),
		PromptDumpRetentionDays: 7,
		RoutingMinConfidence:    0.55,
		SetupAgentMode:          "guided",
		Security:                securitypolicy.DefaultSelection(),
		CapabilityPolicy: CapabilityPolicy{
			PythonScripts:         "ask",
			ScriptWriteDir:        defaultHarnessPath("scripts"),
			AutonomousDiagnostics: "ask",
			NetworkProbes:         "ask",
			InstallMissingTools:   "ask",
		},
		WebSearch: WebSearchConfig{
			Enabled:         false,
			Provider:        "tavily",
			MaxResults:      5,
			SearchDepth:     "basic",
			MaxFetchedChars: 12000,
		},
		ApprovedModels: []string{"openrouter/anthropic/claude-sonnet-4.6"},
		AllowedCommands: []string{
			"df", "echo", "free", "uptime", "ps", "netstat", "systemctl", "journalctl",
		},
	}
}

func (c *Config) migrateLegacySecurity() *securitypolicy.Selection {
	selection := securitypolicy.DefaultSelection()
	switch c.SafetyMode {
	case "user_confirm_all":
		selection.Profile = securitypolicy.ProfileExtraStrict
	case "unrestricted":
		selection.Profile = securitypolicy.ProfileYOLO
	}
	legacyOverrides := map[string]string{
		securitypolicy.SettingScriptExecution: c.CapabilityPolicy.PythonScripts,
		securitypolicy.SettingReadCommands:    c.CapabilityPolicy.AutonomousDiagnostics,
		securitypolicy.SettingNetworkAccess:   c.CapabilityPolicy.NetworkProbes,
		securitypolicy.SettingPackageChanges:  c.CapabilityPolicy.InstallMissingTools,
	}
	for id, value := range legacyOverrides {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || value == "ask" {
			continue
		}
		_ = selection.SetOverride(id, value)
	}
	return selection
}

func (c *Config) projectLegacySecurity() {
	effective, err := securitypolicy.Resolve(c.Security)
	if err != nil {
		return
	}
	switch effective.Profile {
	case securitypolicy.ProfileExtraStrict:
		c.SafetyMode = "user_confirm_all"
	case securitypolicy.ProfileYOLO:
		c.SafetyMode = "unrestricted"
	default:
		c.SafetyMode = "llm_judge"
	}
	c.CapabilityPolicy.PythonScripts = legacyDecision(effective.Decision(securitypolicy.SettingScriptExecution))
	c.CapabilityPolicy.AutonomousDiagnostics = legacyDecision(effective.Decision(securitypolicy.SettingReadCommands))
	c.CapabilityPolicy.NetworkProbes = legacyDecision(effective.Decision(securitypolicy.SettingNetworkAccess))
	c.CapabilityPolicy.InstallMissingTools = legacyDecision(effective.Decision(securitypolicy.SettingPackageChanges))
}

func legacyDecision(decision securitypolicy.Decision) string {
	switch decision {
	case securitypolicy.DecisionAllow:
		return "allow"
	case securitypolicy.DecisionDeny:
		return "deny"
	default:
		return "ask"
	}
}

func defaultHarnessPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	base := filepath.Join(home, ".cvkeharness")
	if name == "" || name == "memory" {
		return base
	}
	return filepath.Join(base, name)
}

func ensureAllowedCommand(cfg *Config, command string) {
	for _, item := range cfg.AllowedCommands {
		if item == command {
			return
		}
	}
	cfg.AllowedCommands = append(cfg.AllowedCommands, command)
}

func (c *Config) normalizeWebSearch() {
	if c.WebSearch.Provider == "" {
		c.WebSearch.Provider = "tavily"
	}
	c.WebSearch.Provider = strings.ToLower(strings.TrimSpace(c.WebSearch.Provider))
	if c.WebSearch.MaxResults <= 0 {
		c.WebSearch.MaxResults = 5
	}
	if c.WebSearch.MaxResults > 10 {
		c.WebSearch.MaxResults = 10
	}
	if c.WebSearch.SearchDepth == "" {
		c.WebSearch.SearchDepth = "basic"
	}
	c.WebSearch.SearchDepth = strings.ToLower(strings.TrimSpace(c.WebSearch.SearchDepth))
	if c.WebSearch.MaxFetchedChars <= 0 {
		c.WebSearch.MaxFetchedChars = 12000
	}
	if c.WebSearch.MaxFetchedChars > 30000 {
		c.WebSearch.MaxFetchedChars = 30000
	}
	c.WebSearch.AllowedDomains = normalizeStringList(c.WebSearch.AllowedDomains)
	c.WebSearch.BlockedDomains = normalizeStringList(c.WebSearch.BlockedDomains)
}

func normalizeStringList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		item = strings.TrimPrefix(item, ".")
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func normalizeModelRefList(items []string) []string {
	if len(items) == 0 {
		return nil
	}

	out := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
