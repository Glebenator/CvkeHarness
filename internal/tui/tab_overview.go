package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/recovery"
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
	recovery []state.RecoveryOperation
	batches  []state.RecoveryBatch
	health   []state.SchedulerHealth
	setup    bool
	err      error
	at       time.Time
}
type overviewItem struct {
	label    string
	group    string
	title    string
	meta     string
	tab      int
	run      *state.RunSummary
	jobID    string
	blocked  *state.BlockedWork
	recovery *state.RecoveryOperation
	batch    *state.RecoveryBatch
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
		for i := range msg.batches {
			b := msg.batches[i]
			if b.Status == recovery.Committed || b.Status == recovery.Recovered {
				continue
			}
			t.items = append(t.items, overviewItem{label: "Fleet " + b.ID, title: "Fleet: " + strings.ReplaceAll(b.Status, "_", " "), group: "Needs attention", meta: b.ID + " · Inspect rollout evidence", batch: &b})
		}
		for i := range msg.recovery {
			op := msg.recovery[i]
			if op.Status == "committed" || op.Status == "recovered" || op.Status == "preparation_failed" || op.Status == "validation_failed" {
				continue
			}
			t.items = append(t.items, overviewItem{label: "Recovery " + op.ID, title: "Recovery: " + strings.ReplaceAll(op.Status, "_", " "), group: "Needs attention", meta: op.ID + " · Inspect recorded change", recovery: &op})
		}
		for i := range msg.blocked {
			work := msg.blocked[i]
			t.items = append(t.items, overviewItem{label: "Approval waiting: " + work.Task, group: "Needs attention", title: work.Task, meta: "APPROVAL REQUIRED · Review the exact action", blocked: &work})
		}
		for i := range msg.runs {
			run := msg.runs[i]
			if !run.Success {
				t.items = append(t.items, overviewItem{label: runStateLabel(run) + ": " + run.Task, group: "Needs attention", title: run.Task, meta: runStateLabel(run) + " · " + timeAgo(run.StartedAt) + " · Open result", tab: tabRuns, run: &run})
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
				t.items = append(t.items, overviewItem{label: label + job.Name, group: "Needs attention", title: job.Name, meta: strings.TrimSuffix(label, ": ") + " · Open job", tab: tabJobs, jobID: job.ID})
			}
		}
		for _, job := range t.jobs {
			if job.Enabled && !job.NextRunAt.IsZero() {
				t.items = append(t.items, overviewItem{label: "Next " + job.NextRunAt.Local().Format("Jan 2 15:04 MST") + ": " + job.Name, group: "Scheduled next", title: job.Name, meta: job.NextRunAt.Local().Format("Mon Jan 2 · 15:04 MST") + " · Open job", tab: tabJobs, jobID: job.ID})
			}
		}
		for i := range msg.runs {
			run := msg.runs[i]
			if run.Success {
				t.items = append(t.items, overviewItem{label: "Completed: " + run.Task, group: "Recent results", title: run.Task, meta: "Completed · verification: " + firstNonEmptyText(run.VerificationStatus, "not run") + " · " + timeAgo(run.StartedAt), tab: tabRuns, run: &run})
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
			if item.blocked != nil || item.recovery != nil || item.batch != nil {
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
	if t.detail && len(t.items) > 0 && t.items[t.cursor].batch != nil {
		b := t.items[t.cursor].batch
		body := fmt.Sprintf("Fleet recovery batch\n\nID: %s\nController: %s\nRecorded status: %s\n\n%s\n\n", b.ID, b.Target, b.Status, b.Problem)
		var plan recovery.FleetPlan
		var outcomes []recovery.FleetOutcome
		if json.Unmarshal(b.Manifest, &plan) == nil && json.Unmarshal(b.Progress, &outcomes) == nil {
			body += fmt.Sprintf("Impact: %d hosts, %d files, %d original+candidate bytes, %d services. Serial dispatch; maximum %d repairs per target.\n\n", plan.Impact.Hosts, plan.Impact.Files, plan.Impact.Bytes, plan.Impact.Services, plan.Limits.MaxRepairAttempts)
			for i, en := range plan.Entries {
				if i < len(outcomes) {
					body += fmt.Sprintf("%s: %s; dispatched %t; repairs %d\n", en.Host.Config.Name, outcomes[i].State, outcomes[i].Dispatched, outcomes[i].Repairs)
				}
			}
		}
		body += fmt.Sprintf("\nA dispatched batch cannot be applied again. Reconcile only inspects remote journals. Recovery restores dispatched operations in reverse order and preserves conflicts.\n\nCLI: cvkeharness recovery fleet inspect %s\nCLI: cvkeharness recovery fleet reconcile %s\n\nUse recovery fleet recover with the exact reviewed digest. Overview never dispatches, retries or restores automatically.", b.ID, b.ID)
		return header + scrollBody(body, width, height-4, &t.scroll)
	}
	if t.detail && len(t.items) > 0 && t.items[t.cursor].recovery != nil {
		op := t.items[t.cursor].recovery
		body := fmt.Sprintf("Recovery operation\n\nID: %s\nRecorded status: %s\nExecutor: %s\nUpdated: %s\n\n%s\n\nInspect the exact file manifest before applying or restoring. An interrupted operation may have changed some files. Recovery never force-overwrites a conflicting destination.\n\nCLI: cvkeharness recovery inspect %s\nCLI: cvkeharness recovery reconcile %s\n\nTo restore, use recovery recover with the reviewed digest from inspect. No model or provider is required.", op.ID, op.Status, op.Target, op.UpdatedAt.Local().Format(time.RFC3339), op.Problem, op.ID, op.ID)
		var plan recovery.Manifest
		if json.Unmarshal(op.Manifest, &plan) == nil && plan.SSH != nil {
			ssh := plan.SSH
			body = fmt.Sprintf("Guarded SSH change\n\nID: %s\nRecorded status: %s\nSSH service: %s\nPort: %d -> %d\n\n%s\n\nThe target-local watchdog attempts restoration of unconfirmed changes after %d seconds from arming. Inspect the recorded deadline and current outcome. Failed automatic restoration stops for explicit operator recovery. Confirmation requires a new authenticated connection to the proposed port; an existing multiplexed connection cannot confirm.\n\nCLI: cvkeharness recovery inspect %s\nFrom the new connection: cvkeharness recovery ssh confirm %s --confirm REVIEWED_DIGEST\n\nFor explicit restoration, use recovery recover with the reviewed digest. Overview never confirms or restores automatically.", op.ID, op.Status, ssh.Config.Name, ssh.OldPort, ssh.NewPort, op.Problem, ssh.Config.ConfirmationSeconds, op.ID, op.ID)
		}
		return header + scrollBody(body, width, height-4, &t.scroll)
	}
	if t.setup || t.totals.Runs+t.totals.ChatSessions == 0 {
		header += "  " + styleTitle.Render("Your operations workspace") + "\n"
		header += "  " + styleMuted.Render("Connect a provider, then describe your first task.") + "\n\n"
		header += "  " + renderKeyHint("s", "Set up provider") + "\n  " + renderKeyHint("v", "Check local configuration") + "\n  " + renderKeyHint("c", "Start a conversation") + "\n\n"
	}
	if t.notice != "" {
		header += "  " + styleBase.Render(strings.ReplaceAll(wrapDisplay(t.notice, width-4), "\n", "\n  ")) + "\n\n"
	}
	if len(t.items) == 0 {
		return header + renderEmptyState("You're ready to start", "Tasks and scheduled work will appear here.", "c", "Open Chat")
	}
	header += "  " + styleMuted.Render(fmt.Sprintf("%d runs · %s succeeded · %d chats · %d jobs", t.totals.Runs, successRate(t.totals.Runs, t.totals.SuccessfulRuns), t.totals.ChatSessions, t.totals.Jobs)) + "\n\n"
	var rows []string
	group := ""
	selectedStart, selectedEnd := 0, 0
	for i, item := range t.items {
		sectionStart := len(rows)
		if item.group != group {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			count := 0
			for _, candidate := range t.items {
				if candidate.group == item.group {
					count++
				}
			}
			rows = append(rows, strings.TrimSuffix(renderGroupLabel(item.group, count), "\n"))
			group = item.group
		}
		if i == t.cursor {
			selectedStart = sectionStart
		}
		rows = append(rows, strings.Split(strings.TrimSuffix(renderListEntry(firstNonEmptyText(item.title, item.label), item.meta, i == t.cursor, width), "\n"), "\n")...)
		if i == t.cursor {
			selectedEnd = len(rows)
		}
	}
	available := maxInt(height-strings.Count(header, "\n")-1, 1)
	start := maxInt(0, selectedEnd-available)
	if selectedStart < start {
		start = selectedStart
	}
	end := minInt(len(rows), start+available)
	header += strings.Join(rows[start:end], "\n") + "\n  " + scrollHints(start, end, len(rows))
	return header
}

func scrollBody(body string, width, height int, scroll *int) string {
	lines := strings.Split(wrapDisplay(body, maxInt(width-4, 20)), "\n")
	count := maxInt(height-1, 1)
	*scroll = clamp(*scroll, 0, maxInt(len(lines)-count, 0))
	end := minInt(*scroll+count, len(lines))
	return "  " + strings.Join(lines[*scroll:end], "\n  ") + "\n  " + scrollHints(*scroll, end, len(lines))
}
