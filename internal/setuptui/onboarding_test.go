package setuptui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setupflow"
	"github.com/coolcake/cvkeharness/securitypolicy"
)

func press(m setupModel, key tea.KeyMsg) setupModel {
	next, _ := m.Update(key)
	return next.(setupModel)
}

var enterKey = tea.KeyMsg{Type: tea.KeyEnter}

func TestCodexSetupRequiresLoginAndUsesProviderJudge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Point login lookup only at this test's synthetic cache.
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-test"))
	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{cfg: cfg, step: stepProvider, cursor: 0}
	m = press(m, enterKey)
	m = press(m, enterKey)
	if m.step != stepCredentials || m.errMessage == "" {
		t.Fatal("missing Codex login was accepted")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.step != stepCredentials {
		t.Fatal("N bypassed missing login")
	}
	if err := os.MkdirAll(filepath.Join(home, "codex-test"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "codex-test", "auth.json"), []byte(`{"tokens":{"access_token":"synthetic-test-token","account_id":"test-account"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m = press(m, enterKey)
	if m.step != stepModel || !m.modelsLoading {
		t.Fatal("login should start model loading")
	}
	next, _ := m.Update(modelResultMsg{result: setupflow.ModelResult{Items: []setupflow.ModelOption{{ID: "codex-primary"}, {ID: "codex-judge"}, {ID: "[ custom model ]"}}}})
	m = press(next.(setupModel), enterKey)
	if cfg.SafetyModel != "codex-primary" {
		t.Fatalf("wrong default judge %q", cfg.SafetyModel)
	}
	m = press(m, enterKey)
	if m.step != stepJudge {
		t.Fatalf("missing explicit judge step: %v", m.step)
	}
	m.cursor = 1
	m = press(m, enterKey)
	if cfg.SafetyModel != "codex-judge" {
		t.Fatalf("judge choice ignored: %q", cfg.SafetyModel)
	}
	m = press(m, enterKey) // skip scan
	if m.step != stepCapabilities {
		t.Fatalf("scan wasn't optional: %v", m.step)
	}
	m = press(m, enterKey) // recommended optional settings
	if m.step != stepReview || m.itemCount() != 1 {
		t.Fatal("review should only offer save without selected actions")
	}
	if !strings.Contains(m.viewReview(), "codex-judge") {
		t.Fatal("judge omitted from review")
	}
}

func TestFailedCredentialNeverReplacesSavedCredential(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SetAPIKey("openrouter", "previous-key")
	m := setupModel{cfg: cfg, step: stepCredentials}
	m = m.beginInput(inputOpenRouterKey, "key", "bad-new-key", true)
	m = press(m, enterKey)
	if cfg.GetAPIKey("openrouter") != "previous-key" {
		t.Fatal("unvalidated key replaced saved credential")
	}
	next, _ := m.Update(credentialMsg{err: errors.New("rejected")})
	m = next.(setupModel)
	if cfg.GetAPIKey("openrouter") != "previous-key" || m.step != stepCredentials {
		t.Fatal("rejected key was retained")
	}
}

func TestSetupBusyInputCannotSkipOrSaveTwice(t *testing.T) {
	for _, state := range []string{"loading", "saving", "validating", "scanning", "recommending"} {
		cfg := config.DefaultConfig()
		m := setupModel{cfg: cfg, step: stepReview, modelsLoading: state == "loading", saving: state == "saving", validating: state == "validating", scanning: state == "scanning", recommending: state == "recommending"}
		for _, key := range []tea.KeyMsg{enterKey, {Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune{'n'}}} {
			next, cmd := m.Update(key)
			if cmd != nil || next.(setupModel).step != stepReview {
				t.Fatalf("input escaped %s state", state)
			}
		}
	}
}

func TestSkipClearsPreviouslySelectedInstall(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{cfg: cfg, step: stepDependencies, installPlan: setupflow.InstallPlan{Available: true, Selected: true}, cursor: 0}
	m = press(m, enterKey)
	if m.installPlan.Selected {
		t.Fatal("Skip retained install selection")
	}
}

func TestContinuingExistingProfilePreservesOverrides(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Normalize()
	if err := cfg.Security.SetOverride(securitypolicy.SettingNetworkAccess, "deny"); err != nil {
		t.Fatal(err)
	}
	cfg.Normalize()
	before, _ := cfg.EffectiveSecurity()
	m := setupModel{cfg: cfg, step: stepSafety, cursor: 1}
	m = press(m, enterKey)
	after, _ := m.cfg.EffectiveSecurity()
	if before.Hash != after.Hash {
		t.Fatal("continuing same profile dropped existing overrides")
	}
}

func TestBlankCustomModelStaysInEditor(t *testing.T) {
	m := setupModel{cfg: config.DefaultConfig(), step: stepModel}
	m = m.beginInput(inputCustomModel, "Model ID", "", false)
	m = press(m, enterKey)
	if m.step != stepModel || m.inputMode != inputCustomModel || m.errMessage == "" {
		t.Fatal("blank model advanced setup")
	}
}

func TestRerunPreservesModelsMissingFromCatalog(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultModel = "configured-primary"
	cfg.SafetyModel = "configured-judge"
	for _, step := range []step{stepModel, stepJudge} {
		m := setupModel{cfg: cfg, step: step, models: setupflow.ModelResult{Items: []setupflow.ModelOption{{ID: "other-model"}, {ID: "[ custom model ]"}}}}
		m.cursor = m.preferredCursor()
		m = press(m, enterKey)
		if cfg.DefaultModel != "configured-primary" || cfg.SafetyModel != "configured-judge" {
			t.Fatal("continuing setup replaced configured models")
		}
	}
}

func TestUnsupportedDaemonPlanningIsSkipped(t *testing.T) {
	m := setupModel{cfg: config.DefaultConfig(), step: stepDependencies, safetyAdvanced: true}
	m = press(m, enterKey)
	if m.step != stepCapabilities {
		t.Fatal("unsupported daemon added an unnecessary screen")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.step != stepDependencies {
		t.Fatal("back navigation opened unsupported daemon")
	}
}

func TestOptionalActionFailureDoesNotInviteDuplicateExecution(t *testing.T) {
	m := setupModel{cfg: config.DefaultConfig(), step: stepReview, saving: true}
	next, _ := m.Update(saveMsg{result: setupflow.FinalizeResult{ConfigSaved: true}, err: errors.New("install failed")})
	m = next.(setupModel)
	if m.step != stepDone || !strings.Contains(m.errMessage, "Configuration saved") {
		t.Fatal("partial action failure was presented as an unsaved setup")
	}
}
