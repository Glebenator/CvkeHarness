package setuptui

import (
	"testing"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setupflow"
)

func TestAdvisorSetupOpensIndependentModelPicker(t *testing.T) {
	m := connectionSetupFixture()
	m.step = stepSafety
	for i, option := range setupflow.SafetyOptions() {
		if option.ID == "llm_advisor" {
			m.cursor = i
		}
	}
	m = press(m, enterKey)
	if m.cfg.SafetyMode != "llm_advisor" || m.modelPicker == nil || m.safetyRole() != config.RoleSafetyAdvisor {
		t.Fatal("advisor selection did not open advisor picker")
	}
	m = finishPicker(m, config.RoleSafetyAdvisor, config.ModelBinding{Connection: "review", Model: "advisor-native"})
	if m.step != stepScan {
		t.Fatal("advisor picker did not advance setup")
	}
	resolved, err := m.cfg.ResolveRole(config.RoleSafetyAdvisor)
	if err != nil || resolved.Model != "advisor-native" || resolved.ConnectionID != "review" {
		t.Fatalf("wrong advisor: %+v %v", resolved, err)
	}
	judge, _ := m.cfg.ResolveRole(config.RoleSafetyJudge)
	if judge.Model != "org/primary" {
		t.Fatal("advisor changed judge binding")
	}
}
