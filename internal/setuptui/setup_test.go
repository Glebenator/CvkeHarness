package setuptui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setupflow"
	"github.com/coolcake/cvkeharness/securitypolicy"
)

func TestSetupSurfacesOfflineModelsAndSaveFailure(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:   cfg,
		step:  stepModel,
		width: 80,
		models: setupflow.ModelResult{
			Items:  []setupflow.ModelOption{{ID: "offline/model", Description: "cached fallback"}},
			Live:   false,
			Source: "offline defaults",
		},
	}
	if view := m.View(); !strings.Contains(view, "offline fallback") {
		t.Fatalf("expected explicit offline model state, got:\n%s", view)
	}

	m.step = stepReview
	m.saving = true
	next, _ := m.Update(saveMsg{err: errors.New("disk full")})
	updated := next.(setupModel)
	if updated.saving || !strings.Contains(updated.errMessage, "disk full") {
		t.Fatalf("expected save failure state, got saving=%v error=%q", updated.saving, updated.errMessage)
	}
}

func TestSetupUsesFourGroupedStages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		step  step
		stage setupStage
	}{
		{stepWelcome, stageConnect},
		{stepModel, stageConnect},
		{stepSafety, stageSafety},
		{stepSecurityControls, stageSafety},
		{stepDaemon, stageSafety},
		{stepCapabilities, stageCapabilities},
		{stepNotes, stageCapabilities},
		{stepReview, stageReady},
		{stepDone, stageReady},
	}
	for _, tc := range cases {
		m := setupModel{step: tc.step}
		if got := m.stage(); got != tc.stage {
			t.Fatalf("step %v mapped to stage %v, want %v", tc.step, got, tc.stage)
		}
	}
	if len(stageOrder) != 4 {
		t.Fatalf("expected four setup stages, got %d", len(stageOrder))
	}
}

func TestSetupSecurityProfilesAndProgressiveCustomization(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{cfg: cfg, step: stepSafety, cursor: 1}
	if got := setupflow.SafetyOptions(); len(got) != 5 || got[1].ID != string(securitypolicy.ProfileReasonable) {
		t.Fatalf("unexpected security profiles: %#v", got)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	custom := next.(setupModel)
	if !custom.securityCustomize {
		t.Fatal("expected security customization to be enabled")
	}
	next, _ = custom.Update(tea.KeyMsg{Type: tea.KeyEnter})
	custom = next.(setupModel)
	if custom.step != stepSecurityControls {
		t.Fatalf("expected security controls step, got %v", custom.step)
	}
	before, _ := custom.cfg.EffectiveSecurity()
	next, _ = custom.Update(tea.KeyMsg{Type: tea.KeyRight})
	custom = next.(setupModel)
	after, _ := custom.cfg.EffectiveSecurity()
	first := securitypolicy.Catalog()[0]
	if before.Value(first.ID) == after.Value(first.ID) || after.Origins[first.ID] != "override" {
		t.Fatalf("control did not become an override: before=%q after=%q origin=%q", before.Value(first.ID), after.Value(first.ID), after.Origins[first.ID])
	}
}

func TestSetupYOLORequiresExplicitSecondConfirmation(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{cfg: cfg, step: stepSafety, cursor: 4}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	first := next.(setupModel)
	if first.step != stepSafety || !first.yoloConfirm || first.cfg.Security.Profile == securitypolicy.ProfileYOLO {
		t.Fatalf("first Enter should only arm YOLO confirmation: %#v", first)
	}
	if !strings.Contains(first.message, "does not bypass") && !strings.Contains(first.message, "OS and provider") {
		t.Fatalf("YOLO consequence copy missing: %q", first.message)
	}
	next, _ = first.Update(tea.KeyMsg{Type: tea.KeyEnter})
	confirmed := next.(setupModel)
	if confirmed.cfg.Security.Profile != securitypolicy.ProfileYOLO || confirmed.step != stepScan {
		t.Fatalf("YOLO confirmation did not apply: profile=%q step=%v", confirmed.cfg.Security.Profile, confirmed.step)
	}
}

func TestSetupAdvancedProgressiveDisclosure(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{cfg: cfg, step: stepScan, scanComplete: true}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(setupModel).step; got != stepCapabilities {
		t.Fatalf("safe default should skip install and daemon screens, got %v", got)
	}

	m.step = stepScan
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	advanced := next.(setupModel)
	if !advanced.safetyAdvanced {
		t.Fatal("expected advanced safety options to be enabled")
	}
	next, _ = advanced.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(setupModel).step; got != stepDependencies {
		t.Fatalf("advanced safety should reveal dependency planning, got %v", got)
	}
}

func TestSetupRendererDoesNotOverflowRepresentativeWidths(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	for _, width := range []int{80, 100, 120} {
		for current := stepWelcome; current <= stepDone; current++ {
			m := setupModel{
				cfg:         cfg,
				step:        current,
				width:       width,
				height:      30,
				soulProfile: setupflow.DefaultSoulProfile(),
				models: setupflow.ModelResult{Items: []setupflow.ModelOption{{
					ID:          "provider/model-with-a-long-but-realistic-identifier",
					Description: "A representative model description that must wrap without clipping",
				}}},
			}
			view := m.View()
			for lineNo, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("width %d step %v overflow on line %d: got %d\n%s", width, current, lineNo+1, got, line)
				}
			}
		}
	}
}

func TestSetupModelNavigatesFromWelcomeToProvider(t *testing.T) {
	t.Parallel()

	m := setupModel{
		cfg:         config.DefaultConfig(),
		step:        stepWelcome,
		soulProfile: setupflow.DefaultSoulProfile(),
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected no command when leaving welcome")
	}
	updated := next.(setupModel)
	if updated.step != stepProvider {
		t.Fatalf("expected provider step, got %v", updated.step)
	}
	if updated.cursor != 1 {
		t.Fatalf("expected current default provider openrouter to be focused, got cursor %d", updated.cursor)
	}
}

func TestSetupModelCyclesCapabilityPolicy(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:  cfg,
		step: stepCapabilities,
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := next.(setupModel)
	if updated.cfg.CapabilityPolicy.PythonScripts != "allow" {
		t.Fatalf("expected python policy to cycle to allow, got %#v", updated.cfg.CapabilityPolicy)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = next.(setupModel)
	if updated.cfg.CapabilityPolicy.PythonScripts != "ask" {
		t.Fatalf("expected python policy to cycle back to ask, got %#v", updated.cfg.CapabilityPolicy)
	}
}

func TestSetupModelEnterOnCapabilitiesKeepsDefaultsAndAdvances(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:  cfg,
		step: stepCapabilities,
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(setupModel)
	if updated.step != stepReview {
		t.Fatalf("expected safe default to advance to review with optional capability screens hidden, got %v", updated.step)
	}
	if updated.cfg.CapabilityPolicy.PythonScripts != "ask" ||
		updated.cfg.CapabilityPolicy.NetworkProbes != "ask" ||
		updated.cfg.CapabilityPolicy.InstallMissingTools != "ask" {
		t.Fatalf("expected enter to preserve default ask policies, got %#v", updated.cfg.CapabilityPolicy)
	}
}

func TestSetupModelWebSearchCanBeSkipped(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:  cfg,
		step: stepWebSearch,
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(setupModel)
	if updated.step != stepRecommendations {
		t.Fatalf("expected skip to advance to recommendations, got %v", updated.step)
	}
	if updated.cfg.WebSearch.Enabled {
		t.Fatalf("expected web search to remain disabled")
	}
}

func TestSetupModelWebSearchEnablesExistingTavilyKey(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	cfg.SetAPIKey("tavily", "tvly-test")
	m := setupModel{
		cfg:  cfg,
		step: stepWebSearch,
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(setupModel)
	if updated.step != stepRecommendations {
		t.Fatalf("expected enabled web search to advance to recommendations, got %v", updated.step)
	}
	if !updated.cfg.WebSearch.Enabled {
		t.Fatalf("expected web search to be enabled")
	}
	if updated.cfg.WebSearch.Provider != "tavily" {
		t.Fatalf("expected tavily provider, got %q", updated.cfg.WebSearch.Provider)
	}
}

func TestSetupModelWebSearchPromptsForTavilyKey(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:    cfg,
		step:   stepWebSearch,
		cursor: 1,
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected no command while opening key input")
	}
	updated := next.(setupModel)
	if updated.inputMode != inputTavilyKey {
		t.Fatalf("expected Tavily key input mode, got %v", updated.inputMode)
	}
	view := updated.stepView()
	if !strings.Contains(view, "API key input active: Tavily") || !strings.Contains(view, "Tavily API key") {
		t.Fatalf("expected Tavily key input prompt, got %q", view)
	}
	if strings.Contains(view, "Leave web_search disabled") {
		t.Fatalf("expected active input prompt to replace option list, got %q", view)
	}
}

func TestSetupModelPlansPythonInstallSelection(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:  cfg,
		step: stepDependencies,
		hostProfile: setupflow.HostProfile{
			Python: setupflow.ToolStatus{Name: "python3"},
		},
		installPlan: setupflow.InstallPlan{
			Tool:      "python",
			Command:   []string{"brew", "install", "python"},
			Available: true,
		},
		cursor: 1,
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(setupModel)
	if !updated.installPlan.Selected {
		t.Fatalf("expected install plan to be selected")
	}
	if updated.step != stepDaemon {
		t.Fatalf("expected daemon step after dependency selection, got %v", updated.step)
	}
}

func TestSetupModelRecommendationsCanBeSkipped(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:             cfg,
		step:            stepRecommendations,
		recommendations: []string{"Use Python pinning."},
		cursor:          3,
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(setupModel)
	if updated.step != stepSoul {
		t.Fatalf("expected skip to advance to soul step, got %v", updated.step)
	}
	if updated.hostNotes != "" {
		t.Fatalf("expected skipped recommendations not to become host notes, got %q", updated.hostNotes)
	}
}

func TestSetupModelRecommendationsCanBeAcceptedIntoNotes(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Normalize()
	m := setupModel{
		cfg:             cfg,
		step:            stepRecommendations,
		recommendations: []string{"Use Python pinning."},
		cursor:          0,
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(setupModel)
	if updated.step != stepSoul {
		t.Fatalf("expected accept to advance to soul step, got %v", updated.step)
	}
	if !strings.Contains(updated.hostNotes, "Python pinning") {
		t.Fatalf("expected accepted recommendation in host notes, got %q", updated.hostNotes)
	}
}
