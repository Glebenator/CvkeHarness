package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func navigationKey(m model, msg tea.KeyMsg) model { next, _ := m.Update(msg); return next.(model) }
func navigationText(m model, value string) model {
	return navigationKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
}

func TestNavigationCancelPreservesEditorAndIgnoresOldInputEvents(t *testing.T) {
	m := recoveryModel()
	m.activeTab = tabConfig
	cfg := m.tabs[tabConfig].(*configTab)
	cfg.cursor = 1
	cfg.beginEdit()
	cfg.input.SetValue("pending-provider")
	cfg.dirty = true
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.navigation.open {
		t.Fatal("switcher did not open inside editor")
	}
	epoch := m.navigationEpoch
	m = navigationText(m, "Runs")
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.navigation.open || !cfg.editing || cfg.input.Value() != "pending-provider" || !cfg.dirty {
		t.Fatal("cancel changed pending edit")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	next, _ := m.Update(navigationInputMsg{epoch: epoch, msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("stale")}})
	if next.(model).navigation.input.Value() != "" {
		t.Fatal("old switcher input leaked into new switcher")
	}
}
func TestSwitcherNavigatesWithoutRunningAJobAndResumesDraft(t *testing.T) {
	m := recoveryModel()
	job := m.tabs[tabJobs].(*jobsTab)
	job.initCreateInputs()
	job.createName.SetValue("Retained job")
	job.createStep = createStepPrompt
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	m = navigationText(m, "Continue job draft")
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.navigation.open || m.activeTab != tabJobs || job.mode != jobsModeCreate || job.createName.Value() != "Retained job" || job.createStep != createStepPrompt || job.creating {
		t.Fatal("draft action lost input or submitted work")
	}
}
func TestWorkspaceHistorySupportsBackForwardAndBranching(t *testing.T) {
	m := recoveryModel()
	m.switchTab(tabJobs)
	m.switchTab(tabChat)
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	if m.activeTab != tabJobs {
		t.Fatal("back did not return to jobs")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	if m.activeTab != tabChat {
		t.Fatal("forward did not restore chat")
	}
	m.navigateHistory(-1)
	m.switchTab(tabRuns)
	m.navigateHistory(1)
	if m.activeTab != tabRuns || len(m.navigationHistory) != 3 {
		t.Fatal("new navigation retained stale forward branch")
	}
}
func TestTabClickUsesRenderedBoundsAndDoesNotBlurCurrentChat(t *testing.T) {
	for _, width := range []int{80, 100, 120} {
		m := recoveryModel()
		m.width = width
		m.activeTab = tabChat
		chat := m.tabs[tabChat].(*chatTab)
		chat.composerFocused = true
		chat.composer.Focus()
		x := 0
		for i, part := range m.tabSegments() {
			if i == tabChat {
				break
			}
			x += lipgloss.Width(part)
		}
		next, _ := m.Update(tea.MouseMsg{X: x + 1, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
		m = next.(model)
		if !chat.composerFocused {
			t.Fatal("clicking active tab stole editor focus")
		}
		next, _ = m.Update(tea.MouseMsg{X: 1, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
		if next.(model).activeTab != tabOverview {
			t.Fatalf("tab hitbox wrong at width %d", width)
		}
	}
}
func TestSwitcherKeepsBackgroundCompletionsAndFitsAt80(t *testing.T) {
	m := recoveryModel()
	chat := m.tabs[tabChat].(*chatTab)
	chat.starting = true
	chat.pendingPrompt = "Retained prompt"
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	next, _ := m.Update(chatSessionReadyMsg{err: errors.New("fixture unavailable")})
	m = next.(model)
	if !m.navigation.open || chat.starting || chat.composer.Value() != "Retained prompt" {
		t.Fatal("switcher swallowed background completion")
	}
	for i := 0; i < 9; i++ {
		m = navigationKey(m, tea.KeyMsg{Type: tea.KeyDown})
		view := m.View()
		if strings.Count(view, "\n")+1 > 24 {
			t.Fatal("switcher height overflow")
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > 80 {
				t.Fatalf("switcher width overflow: %q", line)
			}
		}
	}
}
func TestNavigationNoMatchDoesNotNavigateOrLoseFilter(t *testing.T) {
	m := recoveryModel()
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	m = navigationText(m, "no matching workspace")
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.navigation.open || m.activeTab != tabOverview || !strings.Contains(m.View(), "No matches") {
		t.Fatal("empty results activated an action")
	}
}

func TestSwitcherRanksWorkspaceNamesBeforeDescriptions(t *testing.T) {
	m := recoveryModel()
	m.openNavigation()
	m.navigation.input.SetValue("job")
	matches := m.navigationActions()
	if len(matches) == 0 || matches[0].title != "Jobs" {
		t.Fatal("description match outranked workspace name")
	}
	m.navigation.input.SetValue("new job")
	matches = m.navigationActions()
	if len(matches) != 1 || matches[0].action != "job" {
		t.Fatal("new job alias missing")
	}
}
