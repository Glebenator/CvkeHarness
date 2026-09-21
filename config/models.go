package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Connection is a reusable endpoint and credential configuration. Its stable
// identity is the key in Config.Connections, independent of its provider kind.
type Connection struct {
	Name     string `yaml:"name,omitempty"`
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url,omitempty"`
	APIKey   string `yaml:"api_key,omitempty"`
	AuthFile string `yaml:"auth_file,omitempty"`
}

func (c Connection) DisplayName(id string) string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return id
}

type ModelRole string

const (
	RolePrimary     ModelRole = "primary"
	RoleSafetyJudge ModelRole = "safety_judge"
	RoleClassifier  ModelRole = "classifier"
	RoleVerifier    ModelRole = "verifier"
	RolePlanning    ModelRole = "planning"
	RoleExecution   ModelRole = "execution"
	RoleCuration    ModelRole = "curation"
)

// ModelBinding stores the provider-native model ID without parsing its slashes.
// A binding either selects a connection/model pair or inherits another role.
type ModelBinding struct {
	Connection string    `yaml:"connection,omitempty"`
	Model      string    `yaml:"model,omitempty"`
	Inherit    ModelRole `yaml:"inherit,omitempty"`
}

func (b ModelBinding) IsZero() bool { return b.Connection == "" && b.Model == "" && b.Inherit == "" }

type ModelRoles struct {
	Primary     ModelBinding `yaml:"primary,omitempty"`
	SafetyJudge ModelBinding `yaml:"safety_judge,omitempty"`
	Classifier  ModelBinding `yaml:"classifier,omitempty"`
	Verifier    ModelBinding `yaml:"verifier,omitempty"`
	Planning    ModelBinding `yaml:"planning,omitempty"`
	Execution   ModelBinding `yaml:"execution,omitempty"`
	Curation    ModelBinding `yaml:"curation,omitempty"`
}

type ResolvedModel struct {
	ConnectionID string
	Connection   Connection
	Model        string
	// LegacyConnection marks a connection synthesized from the old global
	// provider fields. Explicit connections always retain their own identity,
	// even when their stable ID happens to match their provider kind.
	LegacyConnection bool
}

func ModelRoleList() []ModelRole {
	return []ModelRole{RolePrimary, RoleSafetyJudge, RoleClassifier, RoleVerifier, RolePlanning, RoleExecution, RoleCuration}
}

func (c *Config) explicitRole(role ModelRole) ModelBinding {
	switch role {
	case RolePrimary:
		return c.Models.Primary
	case RoleSafetyJudge:
		return c.Models.SafetyJudge
	case RoleClassifier:
		return c.Models.Classifier
	case RoleVerifier:
		return c.Models.Verifier
	case RolePlanning:
		return c.Models.Planning
	case RoleExecution:
		return c.Models.Execution
	case RoleCuration:
		return c.Models.Curation
	}
	return ModelBinding{}
}

// RoleBinding returns an explicit binding or the backwards-compatible default.
// Legacy fields are read lazily so older setup and API callers remain supported.
func (c *Config) RoleBinding(role ModelRole) ModelBinding {
	if c == nil {
		return ModelBinding{}
	}
	if b := c.explicitRole(role); !b.IsZero() {
		return b
	}
	switch role {
	case RolePrimary:
		model := c.DefaultModel
		if model == "" {
			model = c.Model
		}
		return ModelBinding{Connection: c.Provider, Model: NormalizeProviderModelID(c.Provider, model)}
	case RoleSafetyJudge:
		if c.Models.Primary.IsZero() && c.SafetyModel != "" && c.SafetyModel != c.DefaultModel && c.SafetyModel != c.Model {
			return ModelBinding{Connection: c.Provider, Model: NormalizeProviderModelID(c.Provider, c.SafetyModel)}
		}
		return ModelBinding{Inherit: RolePrimary}
	case RoleClassifier:
		return ModelBinding{Inherit: RoleSafetyJudge}
	case RoleVerifier:
		return ModelBinding{Inherit: RoleExecution}
	case RolePlanning, RoleExecution, RoleCuration:
		var legacy string
		switch role {
		case RolePlanning:
			legacy = c.PlanningModel
		case RoleExecution:
			legacy = c.ExecutionModel
		case RoleCuration:
			legacy = c.CurationModel
		}
		if c.Models.Primary.IsZero() && strings.TrimSpace(legacy) != "" {
			providerName, model := parseLegacyModel(legacy, c.Provider)
			return ModelBinding{Connection: providerName, Model: model}
		}
		return ModelBinding{Inherit: RolePrimary}
	}
	return ModelBinding{}
}

func (c *Config) SetRoleBinding(role ModelRole, binding ModelBinding) {
	switch role {
	case RolePrimary:
		c.Models.Primary = binding
	case RoleSafetyJudge:
		c.Models.SafetyJudge = binding
	case RoleClassifier:
		c.Models.Classifier = binding
	case RoleVerifier:
		c.Models.Verifier = binding
	case RolePlanning:
		c.Models.Planning = binding
	case RoleExecution:
		c.Models.Execution = binding
	case RoleCuration:
		c.Models.Curation = binding
	}
}

func (c *Config) ResolveRole(role ModelRole) (ResolvedModel, error) {
	return c.resolveRole(role, map[ModelRole]bool{})
}

func (c *Config) resolveRole(role ModelRole, seen map[ModelRole]bool) (ResolvedModel, error) {
	if c == nil {
		return ResolvedModel{}, fmt.Errorf("configuration is required")
	}
	if seen[role] {
		return ResolvedModel{}, fmt.Errorf("model role inheritance cycle at %s", role)
	}
	seen[role] = true
	b := c.RoleBinding(role)
	if b.Inherit != "" {
		if b.Connection != "" || b.Model != "" {
			return ResolvedModel{}, fmt.Errorf("%s must inherit or select a connection and model, not both", role)
		}
		return c.resolveRole(b.Inherit, seen)
	}
	if strings.TrimSpace(b.Connection) == "" || strings.TrimSpace(b.Model) == "" {
		return ResolvedModel{}, fmt.Errorf("choose a connection and model for %s", role)
	}
	connection, err := c.ConnectionByID(b.Connection)
	if err != nil {
		return ResolvedModel{}, fmt.Errorf("%s: %w", role, err)
	}
	_, explicitConnection := c.Connections[b.Connection]
	return ResolvedModel{ConnectionID: b.Connection, Connection: connection, Model: b.Model, LegacyConnection: !explicitConnection}, nil
}

// VerifierInheritsExecution preserves verification of the actual routed model,
// rather than resolving the static execution default before routing occurs.
func (c *Config) VerifierInheritsExecution() bool {
	seen := map[ModelRole]bool{}
	for role := RoleVerifier; !seen[role]; {
		seen[role] = true
		b := c.RoleBinding(role)
		if b.Inherit == RoleExecution {
			return true
		}
		if b.Inherit == "" {
			return false
		}
		role = b.Inherit
	}
	return false
}

func supportedProvider(name string) bool {
	switch name {
	case "codex", "openrouter", "openai", "lmstudio", "antigravity":
		return true
	}
	return false
}

func parseLegacyModel(raw, defaultProvider string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "openrouter/auto" || raw == "openrouter/free" {
		return "openrouter", raw
	}
	if prefix, model, found := strings.Cut(raw, "/"); found && supportedProvider(prefix) {
		return prefix, model
	}
	return defaultProvider, raw
}

func (c *Config) ConnectionByID(id string) (Connection, error) {
	if c == nil {
		return Connection{}, fmt.Errorf("configuration is required")
	}
	if connection, ok := c.Connections[id]; ok {
		return connection, nil
	}
	if len(c.Connections) != 0 || !supportedProvider(id) {
		return Connection{}, fmt.Errorf("unknown connection %q", id)
	}
	connection := Connection{Provider: id, APIKey: c.GetAPIKey(id)}
	if id == "lmstudio" {
		connection.BaseURL = c.BaseURL
	}
	return connection, nil
}

func (c *Config) ConnectionIDs() []string {
	ids := map[string]bool{}
	for id := range c.Connections {
		ids[id] = true
	}
	if len(c.Connections) == 0 {
		if supportedProvider(c.Provider) {
			ids[c.Provider] = true
		}
		for id := range c.APIKeys {
			if supportedProvider(id) {
				ids[id] = true
			}
		}
		if c.BaseURL != "" {
			ids["lmstudio"] = true
		}
		for _, raw := range append(append([]string(nil), c.ApprovedModels...), c.FavoriteModels...) {
			providerName, _ := parseLegacyModel(raw, c.Provider)
			if supportedProvider(providerName) {
				ids[providerName] = true
			}
		}
		for _, role := range ModelRoleList() {
			if id := c.RoleBinding(role).Connection; id != "" {
				ids[id] = true
			}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// EnsureModelBindings materializes legacy settings once, preserving each role's
// prior selection and every saved provider credential. New bindings then win.
func (c *Config) EnsureModelBindings() {
	bindings := map[ModelRole]ModelBinding{}
	for _, role := range ModelRoleList() {
		bindings[role] = c.RoleBinding(role)
	}
	connections := map[string]Connection{}
	for _, id := range c.ConnectionIDs() {
		if connection, err := c.ConnectionByID(id); err == nil {
			connections[id] = connection
		}
	}
	c.Connections = connections
	for role, binding := range bindings {
		c.SetRoleBinding(role, binding)
	}
	if c.BaseURL != "" {
		if _, exists := c.Connections["lmstudio"]; !exists {
			c.Connections["lmstudio"] = Connection{Provider: "lmstudio", BaseURL: c.BaseURL}
		}
	}
	// Finish the explicit migration by removing the old sources. Otherwise a
	// later save could resurrect a removed endpoint or a rotated credential.
	c.Provider, c.BaseURL, c.DefaultModel = "", "", ""
	c.Model, c.SafetyModel = "", ""
	c.PlanningModel, c.ExecutionModel, c.CurationModel = "", "", ""
	for id, key := range c.APIKeys {
		if !supportedProvider(id) {
			continue
		}
		// Retain credentials for a previously configured provider even when
		// no role currently uses it. Never overwrite a connection's key.
		if connection, exists := c.Connections[id]; !exists {
			c.Connections[id] = Connection{Provider: id, APIKey: key}
		} else if connection.APIKey != key && key != "" {
			savedID := id + "-saved"
			for i := 2; ; i++ {
				if existing, exists := c.Connections[savedID]; !exists || (existing.Provider == id && existing.APIKey == key) {
					break
				}
				savedID = fmt.Sprintf("%s-saved-%d", id, i)
			}
			c.Connections[savedID] = Connection{Name: "Previously saved " + id, Provider: id, APIKey: key}
		}
		delete(c.APIKeys, id)
	}
}

// ConnectionConfig projects one connection for existing catalog and setup
// integrations. It is a deep copy and cannot mutate another role or connection.
func (c *Config) ConnectionConfig(id string) (*Config, error) {
	connection, err := c.ConnectionByID(id)
	if err != nil {
		return nil, err
	}
	view := c.Clone()
	view.Provider, view.BaseURL = connection.Provider, connection.BaseURL
	view.APIKeys = map[string]string{connection.Provider: connection.APIKey}
	view.Connections = nil
	view.Models = ModelRoles{}
	view.DefaultModel, view.Model, view.SafetyModel = "", "", ""
	return view, nil
}

func ValidateConnectionDefinition(connection Connection) error {
	if !supportedProvider(connection.Provider) {
		return fmt.Errorf("choose a supported provider")
	}
	if connection.BaseURL != "" {
		u, err := url.Parse(connection.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return fmt.Errorf("connection URL must be an http or https URL with a host and no embedded credentials")
		}
		if connection.Provider != "lmstudio" && connection.Provider != "openai" && connection.Provider != "openrouter" {
			return fmt.Errorf("%s does not support a custom endpoint", connection.Provider)
		}
	}
	if connection.AuthFile != "" && connection.Provider != "codex" && connection.Provider != "antigravity" {
		return fmt.Errorf("%s does not use a login file", connection.Provider)
	}
	return nil
}

func (c *Config) ValidateModelRoles(requireCredentials bool) error {
	for id, connection := range c.Connections {
		if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "/:\\ \t\n") {
			return fmt.Errorf("connection ID must be a nonempty name without spaces, slashes or colons")
		}
		if err := ValidateConnectionDefinition(connection); err != nil {
			return fmt.Errorf("connection %s: %w", id, err)
		}
	}
	for _, role := range ModelRoleList() {
		resolved, err := c.ResolveRole(role)
		if err != nil {
			return err
		}
		if err := ValidateConnectionDefinition(resolved.Connection); err != nil {
			return fmt.Errorf("%s: %w", role, err)
		}
		if requireCredentials && (resolved.Connection.Provider == "openrouter" || resolved.Connection.Provider == "openai") && strings.TrimSpace(resolved.Connection.APIKey) == "" {
			return fmt.Errorf("%s: %s API key is required for connection %s", role, resolved.Connection.Provider, resolved.ConnectionID)
		}
	}
	return nil
}
