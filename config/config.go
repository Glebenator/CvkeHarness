package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Provider        string   `yaml:"provider"`
	APIKey          string   `yaml:"api_key,omitempty"`  // Used for OpenRouter
	BaseURL         string   `yaml:"base_url,omitempty"` // Used for LM Studio
	Model           string   `yaml:"model"`
	MaxTokens       int      `yaml:"max_tokens"`
	MaxIterations   int      `yaml:"max_iterations"`
	LogLevel        string   `yaml:"log_level"`
	AllowedCommands []string `yaml:"allowed_commands"`
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

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// DefaultConfig provides sensible defaults for a new setup.
func DefaultConfig() *Config {
	return &Config{
		Provider:      "openrouter",
		Model:         "anthropic/claude-3.5-sonnet",
		MaxTokens:     4096,
		MaxIterations: 15,
		LogLevel:      "info",
		AllowedCommands: []string{
			"df", "free", "uptime", "ps", "netstat", "systemctl", "journalctl",
		},
	}
}
