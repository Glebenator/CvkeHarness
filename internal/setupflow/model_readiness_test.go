package setupflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/config"
)

func TestReadinessChecksIndependentJudgeLogin(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Provider, cfg.DefaultModel, cfg.SafetyModel = "lmstudio", "local-primary", "local-primary"
	cfg.Normalize()
	cfg.EnsureModelBindings()
	login := filepath.Join(t.TempDir(), "auth.json")
	cfg.Connections["judge-account"] = config.Connection{Provider: "codex", AuthFile: login}
	cfg.SetRoleBinding(config.RoleSafetyJudge, config.ModelBinding{Connection: "judge-account", Model: "judge-native"})
	if err := ValidateReady(cfg); err == nil || !strings.Contains(err.Error(), "judge-account") {
		t.Fatalf("missing independent judge login was accepted: %v", err)
	}
	if err := os.WriteFile(login, []byte(`{"tokens":{"access_token":"synthetic-test-token","account_id":"test-account"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReady(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultApprovalKeepsExplicitConnectionIdentity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Connections = map[string]config.Connection{"lmstudio": {Provider: "lmstudio"}}
	cfg.Models.Primary = config.ModelBinding{Connection: "lmstudio", Model: "native/model"}
	cfg.EnsureModelBindings()
	cfg.ApprovedModels = nil
	EnsureDefaultApproved(cfg)
	EnsureDefaultApproved(cfg)
	if len(cfg.ApprovedModels) != 1 || cfg.ApprovedModels[0] != "lmstudio::lmstudio/native/model" {
		t.Fatalf("approval lost connection identity or duplicated: %v", cfg.ApprovedModels)
	}
}
