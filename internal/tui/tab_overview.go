package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/state"
)

type overviewDataMsg struct {
	cfg      *config.Config
	runs     []state.RunSummary
	jobs     []state.ScheduledJob
	sessions []state.ChatSessionSummary
	allRuns  []state.RunSummary
	totals   state.ActivityTotals
	blocked  []state.BlockedWork
	health   []state.SchedulerHealth
	setup    bool
	err      error
	at       time.Time
}
type overviewItem struct {
	label   string
	tab     int
	run     *state.RunSummary
	jobID   string
	blocked *state.BlockedWork
}
type overviewTab struct {
	cfg      *config.Config
	runs     []state.RunSummary
	jobs     []state.ScheduledJob
	sessions []state.ChatSessionSummary
	allRuns  []state.RunSummary
	totals   state.ActivityTotals
	loaded   bool
	setup    bool
	err      error
	at       time.Time
	items    []overviewItem
	cursor   int
	detail   bool
	scroll   int
	notice   string
}

func newOverviewTab() tabModel { return &overviewTab{} }
func (t *overviewTab) Init(svc *Service) tea.Cmd {
	return func() tea.Msg { return loadOverviewData(svc) }
}
func (t *overviewTab) Consuming() bool { return false }
func (t *overviewTab) StatusHints() []string {
	if t.detail {
		return []string{renderKeyHint("esc", "back"), renderKeyHint("c", "chat"), renderKeyHint("↑↓", "scroll")}
	}
	return []string{renderKeyHint("enter", "open"), renderKeyHint("c", "chat"), renderKeyHint("s", "settings"), renderKeyHint("v", "check config"), renderKeyHint("r", "retry")}
}
func (t *overviewTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case overviewDataMsg:
		t.loaded = true
		t.err = msg.err
		if msg.err != nil {
			return t, nil
		}
		t.cfg = msg.cfg
		t.runs = msg.runs
		t.jobs = msg.jobs
		t.sessions = msg.sessions
		t.allRuns = msg.allRuns
		t.totals = msg.totals
		t.setup = msg.setup
		t.at = msg.at
		// Keep the selected item stable as new work arrives in the attention queue.
		selected := ""
		if t.cursor < len(t.items) {
			selected = t.items[t.cursor].label
		}
		t.items = nil
		for i := range msg.blocked {
			work := msg.blocked[i]
			t.items = append(t.items, overviewItem{label: "Approval waiting: " + work.Task, blocked: &work})
		}
		for i := range msg.runs {
			run := msg.runs[i]
			if !run.Success {
				t.items = append(t.items, overviewItem{label: runStateLabel(run) + ": " + run.Task, tab: tabRuns, run: &run})
			}
		}
		health := map[string]state.SchedulerHealth{}
		for _, h := range msg.health {
			health[h.JobID] = h
		}
		sort.SliceStable(t.jobs, func(i, j int) bool { return t.jobs[i].NextRunAt.Before(t.jobs[j].NextRunAt) })
		for _, job := range t.jobs {
			h := health[job.ID]
			label := ""
			switch {
			case h.StaleClaim:
				label = "Stale claim: "
			case h.Blocked:
				label = "Blocked job: "
			case h.Overdue:
				label = "Overdue job: "
			}
			if label != "" {
				t.items = append(t.items, overviewItem{label: label + job.Name, tab: tabJobs, jobID: job.ID})
			}
		}
		for _, job := range t.jobs {
			if job.Enabled && !job.NextRunAt.IsZero() {
				t.items = append(t.items, overviewItem{label: "Next " + job.NextRunAt.Local().Format("Jan 2 15:04 MST") + ": " + job.Name, tab: tabJobs, jobID: job.ID})
			}
		}
		for i := range msg.runs {
			run := msg.runs[i]
			if run.Success {
				t.items = append(t.items, overviewItem{label: "Completed: " + run.Task, tab: tabRuns, run: &run})
			}
		}
		t.cursor = clamp(t.cursor, 0, maxInt(len(t.items)-1, 0))
		for i, item := range t.items {
			if item.label == selected {
				t.cursor = i
				break
			}
		}
	case tea.KeyMsg:
		if t.detail {
			switch msg.String() {
			case "c":
				return t, func() tea.Msg { return navigateMsg{tab: tabChat} }
			case "esc":
				t.detail = false
			case "down", "j":
				t.scroll++
			case "up", "k":
				t.scroll = maxInt(0, t.scroll-1)
			case "pgdown":
				t.scroll += height - 2
			case "pgup":
				t.scroll = maxInt(0, t.scroll-height+2)
			}
			return t, nil
		}
		switch msg.String() {
		case "r":
			return t, t.Init(svc)
		case "s":
			return t, func() tea.Msg { return navigateMsg{tab: tabConfig} }
		case "c":
			return t, func() tea.Msg { return navigateMsg{tab: tabChat} }
		case "v":
			t.notice = "Configuration checks passed locally. Send a task in Chat to check the connection."
			if t.cfg == nil {
				t.notice = "Open Settings to configure a provider."
			} else if err := t.cfg.Validate(); err != nil {
				t.notice = err.Error()
			} else if (t.cfg.Provider == "openai" || t.cfg.Provider == "openrouter") && t.cfg.GetAPIKey(t.cfg.Provider) == "" {
				t.notice = "Provider API key missing. Press s to open Settings."
			}
		case "down", "j":
			t.cursor = minInt(t.cursor+1, maxInt(len(t.items)-1, 0))
		case "up", "k":
			t.cursor = maxInt(t.cursor-1, 0)
		case "enter":
			if len(t.items) == 0 {
				return t, func() tea.Msg { return navigateMsg{tab: tabChat} }
			}
			item := t.items[t.cursor]
			if item.blocked != nil {
				t.detail = true
				t.scroll = 0
				return t, nil
			}
			return t, func() tea.Msg { return navigateMsg{tab: item.tab, run: item.run, jobID: item.jobID} }
		}
	}
	return t, nil
}
func (t *overviewTab) View(width, height int) string {
	header := renderPageHeader("Overview", "attention and next actions", width)
	if !t.loaded {
		return header + "  Loading activity…"
	}
	if t.err != nil {
		return header + "  " + wrapDisplay("Activity unavailable: "+t.err.Error()+". Press r to retry.", width-4)
	}
	if t.detail && len(t.items) > 0 && t.items[t.cursor].blocked != nil {
		work := t.items[t.cursor].blocked
		body := fmt.Sprintf("Approval waiting\n\nTask: %s\nReason: %s\nWork ID: %s\n\nFor an active chat, open Chat to inspect the exact action and approve once.\nFor persisted work, review it with the commands workflow before granting approval.\n\nCLI: cvkeharness commands approve-work %s\nThis action is never approved automatically from Overview.", work.Task, work.BlockedReason, work.ID, work.ID)
		return header + scrollBody(body, width, height-4, &t.scroll)
	}
	if t.setup || t.totals.Runs+t.totals.ChatSessions == 0 {
		header += "  Get started\n  s Connect provider in Settings · v Check configuration locally\n  c Start first task in Chat\n\n"
	}
	if t.notice != "" {
		header += "  " + strings.ReplaceAll(wrapDisplay(t.notice, width-4), "\n", "\n  ") + "\n\n"
	}
	header += fmt.Sprintf("  All history: %d runs · %s success\n  %d chat sessions · %d jobs · updated %s\n\n", t.totals.Runs, successRate(t.totals.Runs, t.totals.SuccessfulRuns), t.totals.ChatSessions, t.totals.Jobs, t.at.Local().Format("15:04:05"))
	if len(t.items) == 0 {
		return header + "  No work needs attention. Press c to start a task."
	}
	header += "  Attention, upcoming jobs, and recent outcomes\n"
	available := maxInt(height-strings.Count(header, "\n")-1, 1)
	start, end := listWindow(t.cursor, len(t.items), available)
	for i := start; i < end; i++ {
		header += "  " + renderSelectableRow(truncate(t.items[i].label, width-6), i == t.cursor) + "\n"
	}
	header += "  " + scrollHints(start, end, len(t.items))
	return header
}

func scrollBody(body string, width, height int, scroll *int) string {
	lines := strings.Split(wrapDisplay(body, maxInt(width-4, 20)), "\n")
	count := maxInt(height-1, 1)
	*scroll = clamp(*scroll, 0, maxInt(len(lines)-count, 0))
	end := minInt(*scroll+count, len(lines))
	return "  " + strings.Join(lines[*scroll:end], "\n  ") + "\n  " + scrollHints(*scroll, end, len(lines))
}
