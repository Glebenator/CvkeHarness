package tui

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/state"
)

func TestJobsDetailKeepsPageHeader(t *testing.T) {
	t.Parallel()

	tab := &jobsTab{
		jobs: []state.ScheduledJob{{
			ID:           "job_123",
			Name:         "Speedtest every 2 hours to speedtest.md",
			ScheduleKind: "every",
			ScheduleSpec: "2h",
			Prompt:       "Run a network speed test and append a markdown entry.",
			Enabled:      true,
		}},
		cursor: 0,
		mode:   jobsModeDetail,
		loaded: true,
	}

	view := tab.View(100, 30)
	for _, want := range []string{
		"Jobs",
		"scheduled agent work and run history",
		"Speedtest every 2 hours to speedtest.md",
		"Run History",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected jobs detail to contain %q, got:\n%s", want, view)
		}
	}
}

func TestJobDraftValidatesScheduleAndSurvivesFailure(t *testing.T) {
	tab := newJobsTab().(*jobsTab)
	tab.initCreateInputs()
	tab.createName.SetValue("Inspect staging")
	tab.createStep = createStepSpec
	tab.createSpec.SetValue("tomorrow")
	tab.updateCreate(tea.KeyMsg{Type: tea.KeyEnter}, nil)
	if tab.createStep != createStepSpec || tab.createError == "" {
		t.Fatal("invalid schedule advanced")
	}
	tab.createSpec.SetValue("30m")
	tab.updateCreate(tea.KeyMsg{Type: tea.KeyEnter}, nil)
	if tab.createStep != createStepPrompt || len(tab.preview) != 3 {
		t.Fatal("valid interval lacks next-run preview")
	}
	tab.createPrompt.SetValue("Inspect only.\nReport findings.")
	tab.updateCreate(tea.KeyMsg{Type: tea.KeyEnter}, nil)
	tab.creating = true
	tab.Update(jobActionMsg{created: true, err: errors.New("database unavailable")}, nil, 80, 20)
	if tab.mode != jobsModeCreate || tab.creating || tab.createError == "" || !strings.Contains(tab.createPrompt.Value(), "Report findings") {
		t.Fatal("failed creation lost draft")
	}
	tab.updateCreate(tea.KeyMsg{Type: tea.KeyCtrlB}, nil)
	if tab.createStep != createStepPrompt {
		t.Fatal("cannot revise prompt")
	}
	tab.updateCreate(tea.KeyMsg{Type: tea.KeyEsc}, nil)
	tab.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}, nil)
	if tab.createName.Value() != "Inspect staging" || tab.createStep != createStepPrompt {
		t.Fatal("reopening reset draft")
	}
}

func TestJobReviewCanReachEntirePrompt(t *testing.T) {
	tab := newJobsTab().(*jobsTab)
	tab.initCreateInputs()
	tab.createStep = createStepConfirm
	tab.createName.SetValue("Review test")
	tab.createSpec.SetValue("1h")
	tab.validateSchedule()
	tab.createPrompt.SetValue(strings.Repeat("Review all workers carefully. ", 50) + "END-OF-PROMPT")
	tab.createScroll = 10000
	view := tab.viewCreate(80, 20)
	if !strings.Contains(strings.ReplaceAll(view, "\n", ""), "END-OF-PROMPT") {
		t.Fatalf("review lost prompt tail: %s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("review overflow: %q", line)
		}
	}
}
