package setuptui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setupflow"
)

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
	if updated.step != stepWebSearch {
		t.Fatalf("expected enter to advance to web search, got %v", updated.step)
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
