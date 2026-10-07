package config

import (
	"testing"

	"github.com/glebenator/cvkeharness/securitypolicy"
)

func TestLLMJudgePresetAndSelectedModelSurviveSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	if err := cfg.Security.ApplyProfile(securitypolicy.ProfileLLMJudge); err != nil {
		t.Fatal(err)
	}
	cfg.Connections = map[string]Connection{
		"local": {Provider: "lmstudio"},
		"judge": {Provider: "openrouter", APIKey: "test-key"},
	}
	cfg.Models = ModelRoles{
		Primary:     ModelBinding{Connection: "local", Model: "primary"},
		SafetyJudge: ModelBinding{Connection: "judge", Model: "chosen-judge"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := loaded.EffectiveSecurity()
	if err != nil || policy.Profile != securitypolicy.ProfileLLMJudge || loaded.SafetyMode != "llm_judge" || policy.Decision(securitypolicy.SettingFileCreate) != securitypolicy.DecisionLLMReview {
		t.Fatalf("judge preset changed after save: %+v %v", policy, err)
	}
	judge, err := loaded.ResolveRole(RoleSafetyJudge)
	if err != nil || judge.Model != "chosen-judge" || judge.ConnectionID != "judge" {
		t.Fatalf("judge binding changed: %+v %v", judge, err)
	}
}
