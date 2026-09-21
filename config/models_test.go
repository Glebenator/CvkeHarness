package config

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSaveNamedModelsPersistsOneCanonicalSchemaAndAllSavedCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.SetAPIKey("openrouter", "old-router-key")
	cfg.SetAPIKey("tavily", "search-key")
	cfg.Connections = map[string]Connection{"subscription": {Provider: "codex"}, "review": {Provider: "openrouter", APIKey: "review-key"}}
	cfg.Models = ModelRoles{Primary: ModelBinding{Connection: "subscription", Model: "native-primary"}, SafetyJudge: ModelBinding{Connection: "review", Model: "vendor/judge"}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"provider", "base_url", "model", "default_model", "safety_model", "planning_model", "execution_model", "curation_model"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("stale model field persisted: %s", key)
		}
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	primary, err := loaded.ResolveRole(RolePrimary)
	if err != nil || primary.ConnectionID != "subscription" || primary.Model != "native-primary" {
		t.Fatalf("primary changed: %+v %v", primary, err)
	}
	judge, err := loaded.ResolveRole(RoleSafetyJudge)
	if err != nil || judge.ConnectionID != "review" || judge.Connection.APIKey != "review-key" {
		t.Fatalf("judge changed: %+v %v", judge, err)
	}
	if loaded.Connections["openrouter"].APIKey != "old-router-key" || loaded.GetAPIKey("tavily") != "search-key" || loaded.GetAPIKey("openrouter") != "" {
		t.Fatal("saved credential migration lost or duplicated keys")
	}
	if cfg.Provider != "" || cfg.GetAPIKey("openrouter") != "" || cfg.Connections["openrouter"].APIKey != "old-router-key" {
		t.Fatal("saved caller does not match canonical persisted configuration")
	}
	delete(cfg.Connections, "openrouter")
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded.Connections["openrouter"]; exists {
		t.Fatal("saving again resurrected a removed unused connection")
	}
}

func TestExplicitMigrationPreventsCredentialResurrectionBeforeFirstSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.SetAPIKey("openrouter", "old-key")
	cfg.SetAPIKey("openai", "unused-key")
	cfg.BaseURL = "http://localhost:9999/v1"
	cfg.EnsureModelBindings()
	delete(cfg.Connections, "openai")
	delete(cfg.Connections, "lmstudio")
	rotated := cfg.Connections["openrouter"]
	rotated.APIKey = "new-key"
	cfg.Connections["openrouter"] = rotated
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Connections["openrouter"].APIKey != "new-key" {
		t.Fatal("key rotation reverted")
	}
	for _, id := range []string{"openai", "lmstudio", "openrouter-saved"} {
		if _, exists := loaded.Connections[id]; exists {
			t.Fatalf("migration resurrected removed credential/endpoint %s", id)
		}
	}
}

func TestLegacyModelBindingsMigrateWithoutRewiring(t *testing.T) {
	cfg := &Config{Provider: "codex", DefaultModel: "gpt-codex", SafetyModel: "small-codex", PlanningModel: "openrouter/vendor/planner", ExecutionModel: "openrouter/auto", APIKeys: map[string]string{"openrouter": "test-key", "tavily": "search-key"}, BaseURL: "http://localhost:4567/v1"}
	cfg.EnsureModelBindings()
	for role, want := range map[ModelRole]ModelBinding{
		RolePrimary:     {Connection: "codex", Model: "gpt-codex"},
		RoleSafetyJudge: {Connection: "codex", Model: "small-codex"},
		RolePlanning:    {Connection: "openrouter", Model: "vendor/planner"},
		RoleExecution:   {Connection: "openrouter", Model: "openrouter/auto"},
		RoleClassifier:  {Inherit: RoleSafetyJudge},
		RoleVerifier:    {Inherit: RoleExecution},
	} {
		if got := cfg.RoleBinding(role); got != want {
			t.Errorf("%s: got %+v, want %+v", role, got, want)
		}
	}
	if cfg.Connections["openrouter"].APIKey != "test-key" || cfg.GetAPIKey("tavily") != "search-key" {
		t.Fatal("migration lost saved credentials")
	}
	cfg.SetRoleBinding(RolePrimary, ModelBinding{Connection: "openrouter", Model: "new/primary"})
	judge, err := cfg.ResolveRole(RoleSafetyJudge)
	if err != nil || judge.ConnectionID != "codex" || judge.Model != "small-codex" {
		t.Fatalf("primary edit rewired fixed judge: %+v, %v", judge, err)
	}
}

func TestNamedModelBindingsPreserveRawIDsAndConnectionIsolation(t *testing.T) {
	cfg := &Config{Connections: map[string]Connection{
		"subscription": {Name: "My subscription", Provider: "codex"},
		"review":       {Provider: "openrouter", APIKey: "review-key"},
		"laptop":       {Provider: "lmstudio", BaseURL: "http://localhost:1234/v1", APIKey: "laptop-key"},
		"server":       {Provider: "lmstudio", BaseURL: "https://models.example/v1", APIKey: "server-key"},
	}, Models: ModelRoles{Primary: ModelBinding{Connection: "subscription", Model: "gpt-codex"}, SafetyJudge: ModelBinding{Connection: "review", Model: "openrouter/auto"}, Planning: ModelBinding{Connection: "server", Model: "org/group/model"}}}
	cfg.Normalize()
	if err := cfg.ValidateConnection(); err != nil {
		t.Fatal(err)
	}
	judge, err := cfg.ResolveRole(RoleClassifier)
	if err != nil || judge.ConnectionID != "review" || judge.Model != "openrouter/auto" {
		t.Fatalf("classifier inheritance: %+v %v", judge, err)
	}
	plan, err := cfg.ResolveRole(RolePlanning)
	if err != nil || plan.Model != "org/group/model" {
		t.Fatalf("native ID changed: %+v %v", plan, err)
	}
	view, err := cfg.ConnectionConfig("server")
	if err != nil || view.Provider != "lmstudio" || view.BaseURL != "https://models.example/v1" || view.GetAPIKey("lmstudio") != "server-key" {
		t.Fatalf("wrong catalog connection: %+v %v", view, err)
	}
	view.SetAPIKey("lmstudio", "edited")
	clone := cfg.Clone()
	clone.Connections["server"] = Connection{Provider: "openai"}
	if cfg.Connections["server"].APIKey != "server-key" {
		t.Fatal("connection copy mutated source")
	}
	if !cfg.VerifierInheritsExecution() {
		t.Fatal("verifier must follow actual execution")
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Config
	if err := yaml.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	roundTrip.Normalize()
	got, err := roundTrip.ResolveRole(RolePlanning)
	if err != nil || got != plan {
		t.Fatalf("round trip changed binding: %+v %v", got, err)
	}
}

func TestModelBindingValidationRejectsAmbiguousOrMissingAssignments(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"cycle":              func(c *Config) { c.Models.Primary = ModelBinding{Inherit: RoleSafetyJudge} },
		"missing connection": func(c *Config) { c.Models.SafetyJudge = ModelBinding{Connection: "deleted", Model: "judge"} },
		"mixed inheritance": func(c *Config) {
			c.Models.SafetyJudge = ModelBinding{Inherit: RolePrimary, Connection: "local", Model: "judge"}
		},
		"unsupported endpoint": func(c *Config) {
			c.Connections["local"] = Connection{Provider: "codex", BaseURL: "http://localhost:1234/v1"}
		},
		"embedded credential": func(c *Config) {
			c.Connections["local"] = Connection{Provider: "lmstudio", BaseURL: "https://secret:password@example.com/v1"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{Connections: map[string]Connection{"local": {Provider: "lmstudio"}}, Models: ModelRoles{Primary: ModelBinding{Connection: "local", Model: "model"}}}
			mutate(cfg)
			if err := cfg.ValidateModelRoles(false); err == nil {
				t.Fatal("invalid binding accepted")
			} else if strings.Contains(err.Error(), "password") {
				t.Fatal("validation leaked URL credential")
			}
		})
	}
}

func TestLegacyMutationRemainsSupportedUntilExplicitMigration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "lmstudio"
	cfg.DefaultModel = "new-local-model"
	cfg.SafetyModel = "new-judge"
	cfg.Normalize()
	got, err := cfg.ResolveRole(RolePrimary)
	if err != nil || got.Model != "new-local-model" || got.Connection.Provider != "lmstudio" {
		t.Fatalf("legacy config shadowed: %+v %v", got, err)
	}
}
