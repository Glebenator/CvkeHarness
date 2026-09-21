package setuptui

import (
	"errors"
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

func TestFailedOptionalCredentialNeverReplacesSavedCredential(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SetAPIKey("tavily", "previous-key")
	m := setupModel{cfg: cfg, step: stepWebSearch}
	m = m.beginInput(inputTavilyKey, "key", "bad-new-key", true)
	m = press(m, enterKey)
	if cfg.GetAPIKey("tavily") != "previous-key" {
		t.Fatal("unvalidated key replaced saved credential")
	}
	next, _ := m.Update(credentialMsg{err: errors.New("rejected")})
	m = next.(setupModel)
	if cfg.GetAPIKey("tavily") != "previous-key" || m.step != stepWebSearch {
		t.Fatal("rejected key was retained")
	}
}

func TestSetupBusyInputCannotSkipOrSaveTwice(t *testing.T) {
	for _, state := range []string{"saving", "validating", "scanning", "recommending"} {
		cfg := config.DefaultConfig()
		m := setupModel{cfg: cfg, step: stepReview, saving: state == "saving", validating: state == "validating", scanning: state == "scanning", recommending: state == "recommending"}
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
