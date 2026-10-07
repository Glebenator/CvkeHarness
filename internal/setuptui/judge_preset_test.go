package setuptui

import (
	"testing"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setupflow"
	"github.com/coolcake/cvkeharness/securitypolicy"
)

func TestLLMJudgePresetOpensJudgeModelPicker(t *testing.T) {
	m := connectionSetupFixture()
	m.step = stepSafety
	for i, option := range setupflow.SafetyOptions() {
		if option.ID == "llm_judge" {
			m.cursor = i
		}
	}
	m = press(m, enterKey)
	if m.cfg.Security.Profile != securitypolicy.ProfileLLMJudge || m.modelPicker == nil || m.safetyRole() != config.RoleSafetyJudge {
		t.Fatal("judge preset did not open the judge picker")
	}
	m = finishPicker(m, config.RoleSafetyJudge, config.ModelBinding{Connection: "review", Model: "chosen-judge"})
	judge, err := m.cfg.ResolveRole(config.RoleSafetyJudge)
	if err != nil || judge.Model != "chosen-judge" || judge.ConnectionID != "review" || m.step != stepScan {
		t.Fatalf("judge selection lost: %+v %v; step=%v", judge, err, m.step)
	}
}
