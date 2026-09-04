package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/state"
	"strings"
	"testing"
)

func TestRunAnswerIsCompleteAndSelectedRecordStaysStable(t *testing.T) {
	tab := newRunsTab().(*runsTab)
	tab.runs = []state.RunSummary{{ID: 42, Task: "Original task", FinalOutput: strings.Repeat("Long answer. ", 100) + "FINAL_SENTINEL", Success: true}}
	tab.loaded = true
	tab.updateList(tea.KeyMsg{Type: tea.KeyEnter})
	tab.Update(runsDataMsg{runs: []state.RunSummary{{ID: 43, Task: "New task"}}}, nil, 80, 20)
	tab.scroll = 1 << 20
	view := tab.viewDetail(80, 20)
	compact := strings.ReplaceAll(view, "\n", "")
	if !strings.Contains(compact, "FINAL_SENTINEL") || tab.selected.ID != 42 {
		t.Fatal("answer truncated or selection changed on refresh")
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("overflow: %q", line)
		}
	}
}

func TestRunTargetFilterResetsPageAndRejectsStaleResults(t *testing.T) {
	tab := newRunsTab().(*runsTab)
	tab.targets = []string{"target-prod"}
	tab.offset, tab.cursor = 25, 3
	tab.runs = []state.RunSummary{{ID: 42}}
	tab.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}, nil, 80, 20)
	if !tab.target.Unknown || tab.offset != 0 || tab.cursor != 0 {
		t.Fatalf("target change failed: %+v", tab)
	}
	tab.Update(runsDataMsg{runs: []state.RunSummary{{ID: 43}}, status: "all"}, nil, 80, 20)
	if tab.runs[0].ID != 42 {
		t.Fatal("stale target response replaced history")
	}
	tab.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}, nil, 80, 20)
	if tab.target.ID != "target-prod" || tab.target.Unknown {
		t.Fatal("recorded target missing from filter")
	}
	tab.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}, nil, 80, 20)
	if tab.target != (state.RunTargetFilter{}) {
		t.Fatal("cannot return to all targets")
	}
}
