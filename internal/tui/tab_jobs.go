package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/coolcake/cvkeharness/scheduler"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/state"
)

// ── messages ────────────────────────────────────────────────────────

type jobsDataMsg struct {
	jobs   []state.ScheduledJob
	health []state.SchedulerHealth
}

type jobRunsDataMsg struct {
	jobID string
	runs  []state.ScheduledJobRun
}

type jobActionMsg struct {
	action  string
	created bool
	err     error
}

// ── modes ───────────────────────────────────────────────────────────

type jobsMode int

const (
	jobsModeList   jobsMode = iota
	jobsModeDetail          // Viewing a job's run history
	jobsModeCreate          // Multi-step creation wizard
	jobsModeDelete          // Confirmation dialog
)

// ── create wizard steps ─────────────────────────────────────────────

type createStep int

const (
	createStepName    createStep = iota
	createStepKind               // schedule kind selection
	createStepSpec               // schedule spec input
	createStepPrompt             // agent prompt input
	createStepConfirm            // review and confirm
	createStepCount
)

var createStepLabels = [createStepCount]string{
	"Name",
	"Schedule Type",
	"Schedule",
	"Agent Prompt",
	"Review",
}

// ── tab model ───────────────────────────────────────────────────────

type jobsTab struct {
	jobs        []state.ScheduledJob
	healthByJob map[string]state.SchedulerHealth
	cursor      int
	mode        jobsMode
	loaded      bool
	message     string // Transient feedback message

	// Detail mode
	detailRuns   []state.ScheduledJobRun
	detailJobID  string
	detailScroll int

	// Create wizard
	createStep   createStep
	createName   textinput.Model
	createKind   int // 0=every, 1=cron, 2=at
	createSpec   textinput.Model
	createPrompt textarea.Model
	creating     bool
	draftReady   bool
	createScroll int
	preview      []time.Time
	createError  string

	// Delete confirmation
	deleteConfirm bool
}

var scheduleKinds = []struct {
	label string
	value string
	hint  string
}{
	{"Every interval", "every", "e.g. 30m, 1h, 24h"},
	{"Cron expression", "cron", "e.g. 0 */6 * * *"},
	{"One-time at", "at", "e.g. 2026-05-01T09:00:00Z"},
}

func newJobsTab() tabModel {
	return &jobsTab{}
}

func (t *jobsTab) Init(svc *Service) tea.Cmd {
	return func() tea.Msg { return loadJobsData(svc) }
}

func (t *jobsTab) Consuming() bool {
	return t.mode == jobsModeCreate || t.mode == jobsModeDelete
}

func (t *jobsTab) StatusHints() []string {
	switch t.mode {
	case jobsModeCreate:
		if t.creating {
			return []string{styleMuted.Render("Saving job…")}
		}
		hints := []string{renderKeyHint("enter", "continue"), renderKeyHint("ctrl+b", "back"), renderKeyHint("esc", "close draft")}
		if t.createStep == createStepPrompt {
			hints = append(hints, renderKeyHint("ctrl+j", "newline"))
		}
		if t.createStep == createStepConfirm {
			hints[0] = renderKeyHint("enter", "create")
			hints = append(hints, renderKeyHint("↑↓", "review"))
		}
		return hints
	case jobsModeDelete:
		return []string{
			renderKeyHint("y", "confirm"),
			renderKeyHint("n", "cancel"),
		}
	case jobsModeDetail:
		return []string{
			renderKeyHint("esc", "back"),
			renderKeyHint("↑↓", "scroll"),
		}
	default:
		if len(t.jobs) > 0 {
			return []string{
				renderKeyHint("n", "new"),
				renderKeyHint("r", "run"),
				renderKeyHint("p", "pause"),
				renderKeyHint("x", "del"),
				positionIndicator(t.cursor, len(t.jobs)),
			}
		}
		return []string{renderKeyHint("n", "new job")}
	}
}

func (t *jobsTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case jobsDataMsg:
		t.jobs = msg.jobs
		t.healthByJob = make(map[string]state.SchedulerHealth, len(msg.health))
		for _, item := range msg.health {
			t.healthByJob[item.JobID] = item
		}
		t.loaded = true
		if t.cursor >= len(t.jobs) && len(t.jobs) > 0 {
			t.cursor = len(t.jobs) - 1
		}
		return t, nil

	case jobRunsDataMsg:
		t.detailRuns = msg.runs
		t.detailJobID = msg.jobID
		return t, nil

	case jobActionMsg:
		if msg.created {
			t.creating = false
			if msg.err != nil {
				t.createError = msg.err.Error()
				t.mode = jobsModeCreate
				return t, nil
			}
			t.mode = jobsModeList
			t.draftReady = false
		}
		if msg.err != nil {
			t.message = styleError.Render("Error: " + msg.err.Error())
		} else {
			t.message = styleSuccess.Render("✓ " + msg.action)
		}
		return t, func() tea.Msg { return loadJobsData(svc) }

	case tea.KeyMsg:
		switch t.mode {
		case jobsModeList:
			return t.updateList(msg, svc)
		case jobsModeDetail:
			return t.updateDetail(msg, height)
		case jobsModeCreate:
			return t.updateCreate(msg, svc)
		case jobsModeDelete:
			return t.updateDelete(msg, svc)
		}
	}
	return t, nil
}

// ── list mode ───────────────────────────────────────────────────────

func (t *jobsTab) updateList(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	// Clear transient message on any navigation
	switch {
	case key.Matches(msg, keys.Down):
		t.message = ""
		if t.cursor < len(t.jobs)-1 {
			t.cursor++
		}
	case key.Matches(msg, keys.Up):
		t.message = ""
		if t.cursor > 0 {
			t.cursor--
		}
	case key.Matches(msg, keys.Enter):
		if len(t.jobs) > 0 {
			t.mode = jobsModeDetail
			t.detailScroll = 0
			job := t.jobs[t.cursor]
			return t, func() tea.Msg {
				ctx := context.Background()
				runs, _ := svc.ScheduledJobRuns(ctx, job.ID, 10)
				return jobRunsDataMsg{jobID: job.ID, runs: runs}
			}
		}
	case key.Matches(msg, keys.NewJob):
		t.mode = jobsModeCreate
		if !t.draftReady {
			t.initCreateInputs()
			t.createStep = createStepName
		}
		t.message = ""
		return t, t.focusCreateStep()
	case key.Matches(msg, keys.DeleteJob):
		if len(t.jobs) > 0 {
			t.mode = jobsModeDelete
			t.deleteConfirm = false
		}
	case key.Matches(msg, keys.RunJob):
		if len(t.jobs) > 0 {
			job := t.jobs[t.cursor]
			t.message = styleMuted.Render("Running " + job.Name + "…")
			return t, func() tea.Msg {
				ctx := context.Background()
				_, err := svc.RunJobNow(ctx, job.ID)
				label := "Triggered " + job.Name
				return jobActionMsg{action: label, err: err}
			}
		}
	case key.Matches(msg, keys.PauseJob):
		if len(t.jobs) > 0 {
			job := t.jobs[t.cursor]
			return t, func() tea.Msg {
				ctx := context.Background()
				_, err := svc.SetJobEnabled(ctx, job.ID, !job.Enabled)
				action := "Paused"
				if !job.Enabled {
					action = "Resumed"
				}
				return jobActionMsg{action: action + " " + job.Name, err: err}
			}
		}
	}
	return t, nil
}

// ── detail mode ─────────────────────────────────────────────────────

func (t *jobsTab) updateDetail(msg tea.KeyMsg, height int) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		t.mode = jobsModeList
		t.detailRuns = nil
	case key.Matches(msg, keys.Down):
		t.detailScroll++
	case key.Matches(msg, keys.Up):
		if t.detailScroll > 0 {
			t.detailScroll--
		}
	}
	return t, nil
}

// ── create wizard ───────────────────────────────────────────────────

func (t *jobsTab) initCreateInputs() {
	t.createName = textinput.New()
	t.createName.Placeholder = "Health check, Deploy staging…"
	t.createName.CharLimit = 80
	t.createName.Width = 50
	t.createName.PromptStyle = styleInputPrompt
	t.createName.TextStyle = styleInputActive
	t.createName.Prompt = "  › "

	t.createKind = 0

	t.createSpec = textinput.New()
	t.createSpec.CharLimit = 100
	t.createSpec.Width = 50
	t.createSpec.PromptStyle = styleInputPrompt
	t.createSpec.TextStyle = styleInputActive
	t.createSpec.Prompt = "  › "
	t.updateSpecPlaceholder()

	t.draftReady = true
	t.createPrompt = textarea.New()
	t.createPrompt.Placeholder = "Check if the API is responding and report status"
	t.createPrompt.CharLimit = 16 * 1024
	t.createPrompt.SetWidth(68)
	t.createPrompt.SetHeight(4)
	t.createPrompt.ShowLineNumbers = false
	t.createPrompt.Prompt = "  › "
}

func (t *jobsTab) updateSpecPlaceholder() {
	t.createSpec.Placeholder = scheduleKinds[t.createKind].hint
}

func (t *jobsTab) updateCreate(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	if t.creating {
		return t, nil
	}
	if msg.String() == "ctrl+b" {
		if t.createStep > createStepName {
			t.createStep--
		}
		t.createError = ""
		t.createScroll = 0
		return t, t.focusCreateStep()
	}
	if t.createStep == createStepConfirm {
		switch msg.String() {
		case "down", "j":
			t.createScroll++
			return t, nil
		case "up", "k":
			t.createScroll = maxInt(0, t.createScroll-1)
			return t, nil
		}
	}
	t.createError = ""

	// Esc closes the draft without discarding its contents.
	if key.Matches(msg, keys.Back) {
		t.mode = jobsModeList
		return t, nil
	}

	switch t.createStep {
	case createStepName:
		if msg.String() == "enter" {
			if strings.TrimSpace(t.createName.Value()) == "" {
				t.createError = "Name cannot be empty"
				return t, nil
			}
			t.createStep = createStepKind
			t.createName.Blur()
			return t, nil
		}
		var cmd tea.Cmd
		t.createName, cmd = t.createName.Update(msg)
		return t, cmd

	case createStepKind:
		switch {
		case msg.String() == "enter":
			t.createStep = createStepSpec
			t.updateSpecPlaceholder()
			return t, t.createSpec.Focus()
		case key.Matches(msg, keys.Down) || msg.String() == "j":
			if t.createKind < len(scheduleKinds)-1 {
				t.createKind++
			}
		case key.Matches(msg, keys.Up) || msg.String() == "k":
			if t.createKind > 0 {
				t.createKind--
			}
		}
		return t, nil

	case createStepSpec:
		if msg.String() == "enter" {
			if strings.TrimSpace(t.createSpec.Value()) == "" {
				t.createError = "Schedule cannot be empty"
				return t, nil
			}
			if err := t.validateSchedule(); err != nil {
				t.createError = err.Error()
				return t, nil
			}
			t.createStep = createStepPrompt
			t.createSpec.Blur()
			return t, t.createPrompt.Focus()
		}
		var cmd tea.Cmd
		t.createSpec, cmd = t.createSpec.Update(msg)
		return t, cmd

	case createStepPrompt:
		if msg.String() == "ctrl+j" {
			t.createPrompt.InsertString("\n")
			return t, nil
		}
		if msg.String() == "enter" {
			if strings.TrimSpace(t.createPrompt.Value()) == "" {
				t.createError = "Prompt cannot be empty"
				return t, nil
			}
			t.createStep = createStepConfirm
			t.createScroll = 0
			t.createPrompt.Blur()
			return t, nil
		}
		var cmd tea.Cmd
		t.createPrompt, cmd = t.createPrompt.Update(msg)
		return t, cmd

	case createStepConfirm:
		switch msg.String() {
		case "y", "enter":
			if err := t.validateSchedule(); err != nil {
				t.createStep = createStepSpec
				t.createError = err.Error()
				return t, t.focusCreateStep()
			}
			name := strings.TrimSpace(t.createName.Value())
			kind := scheduleKinds[t.createKind].value
			spec := strings.TrimSpace(t.createSpec.Value())
			prompt := strings.TrimSpace(t.createPrompt.Value())
			t.creating = true
			return t, func() tea.Msg {
				ctx := context.Background()
				_, err := svc.CreateJob(ctx, name, kind, spec, prompt)
				return jobActionMsg{action: "Created " + name, created: true, err: err}
			}
		case "n":
			t.mode = jobsModeList
			return t, nil
		}
	}
	return t, nil
}

// ── delete confirmation ─────────────────────────────────────────────

func (t *jobsTab) updateDelete(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back) || msg.String() == "n":
		t.mode = jobsModeList
	case msg.String() == "y":
		if len(t.jobs) > 0 {
			job := t.jobs[t.cursor]
			t.mode = jobsModeList
			return t, func() tea.Msg {
				ctx := context.Background()
				err := svc.DeleteJob(ctx, job.ID)
				return jobActionMsg{action: "Deleted " + job.Name, err: err}
			}
		}
	}
	return t, nil
}

// ── view ────────────────────────────────────────────────────────────

func (t *jobsTab) View(width, height int) string {
	switch t.mode {
	case jobsModeDetail:
		return t.viewDetail(width, height)
	case jobsModeCreate:
		return t.viewCreate(width, height)
	case jobsModeDelete:
		return t.viewDelete(width, height)
	default:
		return t.viewList(width, height)
	}
}

func (t *jobsTab) viewList(width, height int) string {
	if !t.loaded {
		return styleMuted.Render("  Loading…")
	}

	var b strings.Builder
	col := width - 4

	b.WriteString(renderPageHeader("Jobs", "scheduled agent work and run history", width))

	if t.message != "" {
		b.WriteString("  ")
		b.WriteString(t.message)
		b.WriteString("\n\n")
	}

	if len(t.jobs) == 0 {
		b.WriteString(renderEmptyState("No scheduled jobs", "Create recurring or one-time agent work from here.", "n", "new job"))
		return b.String()
	}

	// Column headers
	nameCol := maxInt(col-65, 15)
	b.WriteString(renderTableHeader(width,
		padRight("", 3)+
			padRight("Name", nameCol)+"  "+
			padRight("Schedule", 22)+"  "+
			padRight("Status", 10)+"  "+
			padRight("Next Run", 16)+"  "+
			padRight("Last", 10)))

	// Windowed rendering
	headerLines := 4 // top padding + message + headers + rule
	if t.message != "" {
		headerLines += 2
	}
	listHeight := height - headerLines
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := listWindow(t.cursor, len(t.jobs), listHeight)

	if start > 0 {
		b.WriteString("  ")
		b.WriteString(styleSubtle.Render(fmt.Sprintf("  ↑ %d more", start)))
		b.WriteString("\n")
	}

	for i := start; i < end; i++ {
		job := t.jobs[i]
		b.WriteString("  ")
		b.WriteString(t.renderJobRow(job, col, nameCol, i == t.cursor))
		b.WriteString("\n")
	}

	if end < len(t.jobs) {
		b.WriteString("  ")
		b.WriteString(styleSubtle.Render(fmt.Sprintf("  ↓ %d more", len(t.jobs)-end)))
		b.WriteString("\n")
	}

	return b.String()
}

func (t *jobsTab) renderJobRow(job state.ScheduledJob, col, nameCol int, selected bool) string {
	icon := enabledIcon(job.Enabled)
	name := padRight(truncate(job.Name, nameCol), nameCol)
	sched := padRight(truncate(job.ScheduleKind+" "+job.ScheduleSpec, 22), 22)

	health := t.healthByJob[job.ID]
	status := styleMuted.Render(padRight("—", 10))
	if !job.Enabled {
		status = styleWarning.Render(padRight("paused", 10))
	} else if job.Blocked || health.Blocked {
		status = styleWarning.Render(padRight("blocked", 10))
	} else if health.Overdue {
		status = styleError.Render(padRight("overdue", 10))
	} else if health.StaleClaim {
		status = styleError.Render(padRight("stale", 10))
	} else {
		status = styleSuccess.Render(padRight("active", 10))
	}

	next := padRight(fmtTime(job.NextRunAt), 16)
	last := padRight(timeAgo(job.LastRunAt), 10)

	row := fmt.Sprintf("%s  %s  %s  %s  %s  %s", icon, name, sched, status, next, last)

	return renderSelectableRow(row, selected)
}

func (t *jobsTab) viewDetail(width, height int) string {
	if t.cursor >= len(t.jobs) {
		return ""
	}
	job := t.jobs[t.cursor]

	// Build all lines, then apply scroll window.
	var lines []string
	lines = append(lines, "")
	lines = append(lines, "  "+styleSectionTitle.Render(job.Name))
	lines = append(lines, "")
	lines = append(lines, "  "+renderKeyValue("ID", job.ID))
	lines = append(lines, "  "+renderKeyValue("Schedule", job.ScheduleKind+" "+job.ScheduleSpec))
	lines = append(lines, "  "+renderKeyValue("Status", func() string {
		if job.Enabled {
			return styleSuccess.Render("active")
		}
		return styleWarning.Render("paused")
	}()))
	lines = append(lines, "  "+renderKeyValue("Next Run", fmtTime(job.NextRunAt)))
	if health, ok := t.healthByJob[job.ID]; ok {
		lastStatus := health.LastStatus
		if lastStatus == "" {
			lastStatus = "—"
		}
		lines = append(lines, "  "+renderKeyValue("Last Run Status", lastStatus))
		lines = append(lines, "  "+renderKeyValue("Blocked", fmt.Sprintf("%t", health.Blocked)))
		lines = append(lines, "  "+renderKeyValue("Overdue", fmt.Sprintf("%t", health.Overdue)))
		lines = append(lines, "  "+renderKeyValue("Claim", schedulerClaimSummary(health)))
		lines = append(lines, "  "+renderKeyValue("Heartbeat", schedulerHeartbeatSummary(health)))
	}
	lines = append(lines, "  "+renderKeyValue("Prompt", truncate(job.Prompt, width-22)))
	lines = append(lines, "")
	lines = append(lines, "  "+styleSectionTitle.Render("Run History"))
	lines = append(lines, "")

	if len(t.detailRuns) == 0 {
		lines = append(lines, "  "+styleMuted.Render("No runs yet"))
	}
	for _, run := range t.detailRuns {
		icon := statusIcon(run.Status == "ok")
		line := fmt.Sprintf("  %s  %s  %s  %s",
			icon,
			styleMuted.Render(padRight(run.Status, 8)),
			styleMuted.Render(fmtTime(run.StartedAt)),
			styleMuted.Render(fmtDuration(run.FinishedAt.Sub(run.StartedAt))),
		)
		if run.Error != "" {
			line += "  " + styleError.Render(truncate(run.Error, width-60))
		}
		lines = append(lines, line)
	}

	header := renderPageHeader("Jobs", "scheduled agent work and run history", width)
	detailHeight := maxInt(height-4, 1)

	// Apply scroll window
	maxScroll := maxInt(len(lines)-detailHeight+2, 0)
	t.detailScroll = clamp(t.detailScroll, 0, maxScroll)

	var b strings.Builder
	end := minInt(t.detailScroll+detailHeight-1, len(lines))
	for i := t.detailScroll; i < end; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}
	if maxScroll > 0 {
		b.WriteString("  ")
		b.WriteString(scrollHints(t.detailScroll, end, len(lines)))
	}

	return header + b.String()
}

func schedulerClaimSummary(health state.SchedulerHealth) string {
	switch {
	case health.StaleClaim:
		return "stale"
	case health.Claimed:
		return "active"
	default:
		return "none"
	}
}

func schedulerHeartbeatSummary(health state.SchedulerHealth) string {
	if health.LastHeartbeatAt.IsZero() {
		return "none"
	}
	freshness := "stale"
	if health.HeartbeatFresh {
		freshness = "fresh"
	}
	return fmt.Sprintf("%s (%s)", fmtTime(health.LastHeartbeatAt), freshness)
}

// ── create wizard view ──────────────────────────────────────────────

func (t *jobsTab) focusCreateStep() tea.Cmd {
	t.createName.Blur()
	t.createSpec.Blur()
	t.createPrompt.Blur()
	switch t.createStep {
	case createStepName:
		return t.createName.Focus()
	case createStepSpec:
		return t.createSpec.Focus()
	case createStepPrompt:
		return t.createPrompt.Focus()
	}
	return nil
}

func (t *jobsTab) validateSchedule() error {
	t.preview = nil
	now := time.Now()
	for i := 0; i < 3; i++ {
		next, err := scheduler.NextRun(scheduleKinds[t.createKind].value, t.createSpec.Value(), now)
		if err != nil {
			return err
		}
		if next.IsZero() {
			if i == 0 {
				return fmt.Errorf("Choose a time in the future")
			}
			break
		}
		t.preview = append(t.preview, next)
		now = next
	}
	return nil
}

func (t *jobsTab) viewCreate(width, height int) string {
	col := maxInt(width-6, 20)
	t.createName.Width = col - 4
	t.createSpec.Width = col - 4
	t.createPrompt.SetWidth(col)
	t.createPrompt.SetHeight(maxInt(minInt(height-10, 6), 2))
	header := renderPageHeader("New Scheduled Job", fmt.Sprintf("Step %d of %d · %s", t.createStep+1, createStepCount, createStepLabels[t.createStep]), width)
	var body string
	switch t.createStep {
	case createStepName:
		body = "  A short, descriptive name\n\n" + t.createName.View()
	case createStepKind:
		body = "  When should this job run?\n\n"
		for i, kind := range scheduleKinds {
			body += "  " + renderSelectableRow(kind.label+"  "+kind.hint, i == t.createKind) + "\n"
		}
	case createStepSpec:
		body = "  " + scheduleKinds[t.createKind].label + "\n\n" + t.createSpec.View() + "\n\n  " + t.specContextHelp()
	case createStepPrompt:
		body = "  Describe the outcome, target, and constraints.\n\n" + t.createPrompt.View()
	case createStepConfirm:
		body = "  Name: " + t.createName.Value() + "\n  Schedule: " + scheduleKinds[t.createKind].label + " " + t.createSpec.Value() + "\n  Next executions (UTC):\n"
		for _, at := range t.preview {
			body += "    " + at.UTC().Format("Mon Jan 2, 15:04:05 MST") + "\n"
		}
		body += "\n  Prompt:\n  " + strings.ReplaceAll(t.createPrompt.Value(), "\n", "\n  ") + "\n\n  Enter creates this job. Ctrl+B revises it."
	}
	if t.creating {
		body = "  Saving job…"
	}
	// Errors and navigation stay above the review viewport, not below long input.
	if t.createError != "" {
		header += "  " + styleError.Render(strings.Join(wrapText(t.createError, col), "\n  ")) + "\n\n"
	}
	body = wrapDisplay(body, width-2)
	available := maxInt(height-strings.Count(header, "\n")-1, 1)
	lines := strings.Split(body, "\n")
	t.createScroll = clamp(t.createScroll, 0, maxInt(len(lines)-available, 0))
	if t.createStep != createStepConfirm {
		t.createScroll = 0
	}
	end := minInt(t.createScroll+available, len(lines))
	result := header + strings.Join(lines[t.createScroll:end], "\n")
	if len(lines) > available {
		result += "\n  " + scrollHints(t.createScroll, end, len(lines))
	}
	return result
}

func (t *jobsTab) specContextHelp() string {
	switch scheduleKinds[t.createKind].value {
	case "every":
		return styleMuted.Render("Go duration: 30s, 5m, 1h, 24h, 168h (weekly)")
	case "cron":
		lines := []string{
			styleMuted.Render("UTC: minute hour day month weekday"),
			styleMuted.Render("Examples:"),
			styleMuted.Render("  0 */6 * * *    Every 6 hours"),
			styleMuted.Render("  30 9 * * 1-5   Weekdays at 9:30"),
			styleMuted.Render("  0 0 1 * *      First of month"),
		}
		return strings.Join(lines, "\n  ")
	case "at":
		return styleMuted.Render("RFC3339 with timezone, e.g. 2030-05-01T09:00:00-07:00")
	}
	return ""
}

// ── delete confirmation view ────────────────────────────────────────

func (t *jobsTab) viewDelete(width, height int) string {
	if t.cursor >= len(t.jobs) {
		return ""
	}
	job := t.jobs[t.cursor]

	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(styleWarning.Render("Delete Job"))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(styleBase.Render("Are you sure you want to delete "))
	b.WriteString(styleBright.Render(job.Name))
	b.WriteString(styleBase.Render("?"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(styleMuted.Render("This action cannot be undone."))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(renderKeyHint("y", "delete"))
	b.WriteString(styleMuted.Render("  "))
	b.WriteString(renderKeyHint("n/esc", "cancel"))

	return b.String()
}
