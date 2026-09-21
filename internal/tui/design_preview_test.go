//go:build uxpreview

package tui

import (
	"encoding/json"
	"github.com/charmbracelet/x/ansi"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/internal/setupflow"
	"github.com/coolcake/cvkeharness/state"
	"github.com/muesli/termenv"
)

// TestExportConsolePreview renders the real components with synthetic data.
// It performs no provider calls, command execution, or configuration writes.
// CVKE_PREVIEW_OUTPUT is an explicit output path for this opt-in design utility.
func TestExportConsolePreview(t *testing.T) {
	path := os.Getenv("CVKE_PREVIEW_OUTPUT")
	if path == "" {
		t.Skip("set CVKE_PREVIEW_OUTPUT to export component previews")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	type frame struct {
		Scene         string
		Width, Height int
		Theme, Text   string
		Animation     []string
	}
	var frames []frame
	now := time.Date(2026, 9, 4, 15, 0, 0, 0, time.Local)
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		theme := "dark"
		if !dark {
			theme = "light"
		}
		for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 40}} {
			for _, scene := range []string{"Overview", "Jobs", "Runs", "Chat", "Settings", "Model picker", "New job", "Run detail", "Switcher", "Connecting"} {
				m := recoveryModel()
				m.width, m.height = size[0], size[1]
				m.svc.cfg.Provider = "openai"
				m.svc.cfg.DefaultModel = "gpt-5.4"
				m.tabs[tabConfig].Init(m.svc)
				m.tabs[tabChat].Init(m.svc)
				runs := []state.RunSummary{
					{ID: 124, Task: "Inspect staging API latency and worker health", Success: false, TaskState: state.TaskStateFailed, Provider: "openai", StartedAt: now.Add(-7 * time.Minute), FinishedAt: now, TargetID: "staging-api", TargetEnvironment: "staging", VerificationStatus: "unsatisfied", ErrorMessage: "One worker is not responding.", FinalOutput: "## Worker health\n\nTwo workers are healthy. The third did not respond within 30 seconds.\n\nNo changes were made. Inspect its service logs before restarting it."},
					{ID: 123, Task: "Check disk capacity across the production fleet", Success: true, Provider: "openai", StartedAt: now.Add(-30 * time.Minute), VerificationStatus: "satisfied"},
					{ID: 122, Task: "Review the latest deployment logs", Success: true, Provider: "openai", StartedAt: now.Add(-time.Hour), VerificationStatus: "satisfied"},
				}
				jobs := []state.ScheduledJob{{ID: "health", Name: "Morning service health", ScheduleKind: "cron", ScheduleSpec: "0 9 * * *", Enabled: true, NextRunAt: now.Add(time.Hour)}, {ID: "capacity", Name: "Weekly capacity review", ScheduleKind: "every", ScheduleSpec: "168h", Enabled: false}}
				o := m.tabs[tabOverview].(*overviewTab)
				o.Update(overviewDataMsg{cfg: m.svc.cfg, runs: runs, jobs: jobs, at: now, totals: state.ActivityTotals{Runs: 124, SuccessfulRuns: 120, ChatSessions: 18, Jobs: 2}}, m.svc, size[0], size[1]-4)
				j := m.tabs[tabJobs].(*jobsTab)
				j.loaded = true
				j.jobs = jobs
				r := m.tabs[tabRuns].(*runsTab)
				r.loaded = true
				r.runs = runs
				switch scene {
				case "Jobs":
					m.activeTab = tabJobs
				case "Runs":
					m.activeTab = tabRuns
				case "Chat":
					m.activeTab = tabChat
				case "Settings":
					m.activeTab = tabConfig
					m.tabs[tabConfig].(*configTab).cursor = 4
				case "Model picker":
					m.activeTab = tabConfig
					cfg := m.tabs[tabConfig].(*configTab)
					cfg.cfg.Provider = "codex"
					cfg.cursor = 1
					cfg.openModelPicker()
					cfg.acceptModels(setupflow.ModelResult{Source: "codex-cache", Live: true, Timestamp: now, Items: []setupflow.ModelOption{{ID: "gpt-5.4", Description: "General coding model"}, {ID: "example-fast", Description: "Example catalog description"}}})
				case "New job":
					m.activeTab = tabJobs
					j.initCreateInputs()
					j.mode = jobsModeCreate
					j.createStep = createStepPrompt
					j.createName.SetValue("Morning service health")
					j.createPrompt.SetValue("Inspect staging API and worker health.\nReport failures without changing services.")
				case "Switcher":
					m.openNavigation()
				case "Connecting":
					m.activeTab = tabChat
					chat := m.tabs[tabChat].(*chatTab)
					chat.starting = true
					chat.status = "CONNECTING"
					m.syncActivity()
				case "Run detail":
					m.activeTab = tabRuns
					r.expanded = true
					r.selected = &runs[0]
				}
				view := m.View()
				for n, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > size[0] {
						t.Errorf("%s %s %d columns line %d exceeds width: %q", scene, theme, size[0], n, line)
					}
				}
				if strings.Count(view, "\n")+1 > size[1] {
					t.Errorf("%s exceeds terminal height", scene)
				}
				item := frame{Scene: scene, Width: size[0], Height: size[1], Theme: theme, Text: view}
				if scene == "Connecting" {
					for i := range activityFrames {
						m.motionFrame = i
						m.syncActivity()
						item.Animation = append(item.Animation, m.View())
					}
				}
				frames = append(frames, item)
			}
		}
	}
	data, err := json.Marshal(frames)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
