package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/state"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOverviewOpensAttentionItemAndShowsAccurateTotals(t *testing.T) {
	tab := newOverviewTab().(*overviewTab)
	tab.Update(overviewDataMsg{at: time.Now(), totals: state.ActivityTotals{Runs: 105, SuccessfulRuns: 100, ChatSessions: 7}, runs: []state.RunSummary{{ID: 99, Task: "Failed inspection"}}}, nil, 80, 20)
	if !strings.Contains(tab.View(80, 20), "105 runs") {
		t.Fatal("incorrect activity total")
	}
	_, cmd := tab.Update(tea.KeyMsg{Type: tea.KeyEnter}, nil, 80, 20)
	nav := cmd().(navigateMsg)
	if nav.tab != tabRuns || nav.run.ID != 99 {
		t.Fatal("attention action did not select the failed run")
	}
}
func TestDatabaseReadFailureIsNotAnEmptyOverview(t *testing.T) {
	db := state.Open(filepath.Join(t.TempDir(), "state.db"))
	db.Close()
	svc := NewService(config.DefaultConfig(), db, nil, nil, nil)
	msg := loadOverviewData(svc).(overviewDataMsg)
	if msg.err == nil {
		t.Fatal("read failure discarded")
	}
	tab := newOverviewTab().(*overviewTab)
	tab.Update(msg, svc, 80, 20)
	if !strings.Contains(tab.View(80, 20), "Activity unavailable") {
		t.Fatal("error looks like an empty profile")
	}
}
