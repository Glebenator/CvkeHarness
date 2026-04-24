package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Provider string `yaml:"provider"`
	// APIKeys stores credentials keyed by provider name (e.g. "openrouter",
	// "anthropic"). All providers are preserved so a user switching providers
	// never has to re-enter a key they already validated.
	APIKeys              map[string]string `yaml:"api_keys,omitempty"`
	BaseURL              string            `yaml:"base_url,omitempty"` // Used for local providers (e.g. LM Studio)
	Model                string            `yaml:"model,omitempty"`    // legacy read compatibility only
	DefaultModel         string            `yaml:"default_model,omitempty"`
	PlanningModel        string            `yaml:"planning_model,omitempty"`
	ExecutionModel       string            `yaml:"execution_model,omitempty"`
	CurationModel        string            `yaml:"curation_model,omitempty"`
	SafetyMode           string            `yaml:"safety_mode,omitempty"`
	SafetyModel          string            `yaml:"safety_model"`
	MaxTokens            int               `yaml:"max_tokens"`
	MaxIterations        int               `yaml:"max_iterations"`
	LogLevel             string            `yaml:"log_level"`
	AllowedCommands      []string          `yaml:"allowed_commands"`
	RoutingEnabled       bool              `yaml:"routing_enabled,omitempty"`
	RoutingMode          string            `yaml:"routing_mode,omitempty"`
	ApprovedModels       []string          `yaml:"approved_models,omitempty"`
	FavoriteModels       []string          `yaml:"favorite_models,omitempty"`
	MemoryDir            string            `yaml:"memory_dir,omitempty"`
	StateDBPath          string            `yaml:"state_db_path,omitempty"`
	MemoryMaxSnippets    int               `yaml:"memory_max_snippets,omitempty"`
	RoutingMinConfidence float64           `yaml:"routing_min_confidence,omitempty"`
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
	if c.MemoryMaxSnippets <= 0 {
		c.MemoryMaxSnippets = 3
	}
	if c.RoutingMinConfidence <= 0 {
		c.RoutingMinConfidence = 0.55
	}
	if c.MemoryDir == "" {
		c.MemoryDir = defaultHarnessPath("memory")
	}
	if c.StateDBPath == "" {
		c.StateDBPath = defaultHarnessPath("state.db")
	}
	if len(c.ApprovedModels) == 0 && c.DefaultModel != "" && c.Provider != "" {
		c.ApprovedModels = []string{c.Provider + "/" + c.DefaultModel}
	}
	c.ApprovedModels = normalizeModelRefList(c.ApprovedModels)
	c.FavoriteModels = normalizeModelRefList(c.FavoriteModels)
	ensureAllowedCommand(c, "echo")
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
	// Keep reading legacy `model`, but stop actively persisting it.
	c.Model = ""

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// DefaultConfig provides sensible defaults for a new setup.
func DefaultConfig() *Config {
	return &Config{
		Provider:             "openrouter",
		DefaultModel:         "anthropic/claude-sonnet-4.6",
		SafetyMode:           "llm_judge",
		SafetyModel:          "x-ai/grok-4.1-fast",
		MaxTokens:            4096,
		MaxIterations:        25,
		LogLevel:             "off",
		RoutingMode:          "auto_within_policy",
		MemoryDir:            defaultHarnessPath(""),
		StateDBPath:          defaultHarnessPath("state.db"),
		MemoryMaxSnippets:    3,
		RoutingMinConfidence: 0.55,
		ApprovedModels:       []string{"openrouter/anthropic/claude-sonnet-4.6"},
		AllowedCommands: []string{
			"df", "echo", "free", "uptime", "ps", "netstat", "systemctl", "journalctl",
		},
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
