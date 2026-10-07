package config

import (
	"testing"

	"github.com/coolcake/cvkeharness/securitypolicy"
)

func TestAdvisorModeAndIndependentModelSurviveSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.SafetyMode, cfg.Security = "llm_advisor", nil
	cfg.Normalize()
	if cfg.Security.Profile != securitypolicy.ProfileLLMAdvisor || cfg.SafetyMode != "llm_advisor" {
		t.Fatal("legacy mode lost during normalization")
	}
	cfg.Connections = map[string]Connection{"work": {Provider: "lmstudio"}, "advice": {Provider: "openai", APIKey: "test-key"}}
	cfg.Models = ModelRoles{Primary: ModelBinding{Connection: "work", Model: "primary"}, SafetyAdvisor: ModelBinding{Connection: "advice", Model: "selected-advisor"}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := loaded.ResolveRole(RoleSafetyAdvisor)
	if err != nil || resolved.ConnectionID != "advice" || resolved.Model != "selected-advisor" || loaded.SafetyMode != "llm_advisor" {
		t.Fatalf("advisor not persisted: %+v %v", resolved, err)
	}
	primary, _ := loaded.ResolveRole(RolePrimary)
	if primary.Model != "primary" {
		t.Fatal("advisor changed primary")
	}
	advisorPolicy, _ := loaded.EffectiveSecurity()
	reasonable, _ := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	for key, value := range reasonable.Values {
		if advisorPolicy.Value(key) != value {
			t.Fatalf("advisor weakened %s", key)
		}
	}
}
