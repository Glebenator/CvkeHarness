package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/state"
)

// ── messages ────────────────────────────────────────────────────────

type jobsDataMsg struct {
	jobs []state.ScheduledJob
}

type jobRunsDataMsg struct {
	jobID string
	runs  []state.ScheduledJobRun
}

type jobActionMsg struct {
	action string
	err    error
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
	createStepName     createStep = iota
	createStepKind                // schedule kind selection
	createStepSpec                // schedule spec input
	createStepPrompt              // agent prompt input
	createStepConfirm             // review and confirm
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
	jobs    []state.ScheduledJob
	cursor  int
	mode    jobsMode
	loaded  bool
	message string // Transient feedback message

	// Detail mode
	detailRuns  []state.ScheduledJobRun
	detailJobID string

	// Create wizard
	createStep    createStep
	createName    textinput.Model
	createKind    int // 0=every, 1=cron, 2=at
	createSpec    textinput.Model
	createPrompt  textinput.Model
	createError   string

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

func (t *jobsTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case jobsDataMsg:
		t.jobs = msg.jobs
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
		if msg.err != nil {
			t.message = styleError.Render("Error: " + msg.err.Error())
		} else {
			t.message = styleSuccess.Render(msg.action)
		}
		return t, func() tea.Msg { return loadJobsData(svc) }

	case tea.KeyMsg:
		switch t.mode {
		case jobsModeList:
			return t.updateList(msg, svc)
		case jobsModeDetail:
			return t.updateDetail(msg, svc)
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
	switch {
	case key.Matches(msg, keys.Down):
		if t.cursor < len(t.jobs)-1 {
			t.cursor++
		}
	case key.Matches(msg, keys.Up):
		if t.cursor > 0 {
			t.cursor--
		}
	case key.Matches(msg, keys.Enter):
		if len(t.jobs) > 0 {
			t.mode = jobsModeDetail
			job := t.jobs[t.cursor]
			return t, func() tea.Msg {
				ctx := context.Background()
				runs, _ := svc.ScheduledJobRuns(ctx, job.ID, 10)
				return jobRunsDataMsg{jobID: job.ID, runs: runs}
			}
		}
	case key.Matches(msg, keys.NewJob):
		t.mode = jobsModeCreate
		t.createStep = createStepName
		t.createError = ""
		t.initCreateInputs()
		return t, t.createName.Focus()
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

func (t *jobsTab) updateDetail(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	if key.Matches(msg, keys.Back) {
		t.mode = jobsModeList
		t.detailRuns = nil
		return t, nil
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
	t.createName.Prompt = "  "

	t.createKind = 0

	t.createSpec = textinput.New()
	t.createSpec.CharLimit = 100
	t.createSpec.Width = 50
	t.createSpec.PromptStyle = styleInputPrompt
	t.createSpec.TextStyle = styleInputActive
	t.createSpec.Prompt = "  "
	t.updateSpecPlaceholder()

	t.createPrompt = textinput.New()
	t.createPrompt.Placeholder = "Check if the API is responding and report status"
	t.createPrompt.CharLimit = 500
	t.createPrompt.Width = 60
	t.createPrompt.PromptStyle = styleInputPrompt
	t.createPrompt.TextStyle = styleInputActive
	t.createPrompt.Prompt = "  "
}

func (t *jobsTab) updateSpecPlaceholder() {
	t.createSpec.Placeholder = scheduleKinds[t.createKind].hint
}

func (t *jobsTab) updateCreate(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	t.createError = ""

	// Esc always cancels the wizard
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
			t.createStep = createStepPrompt
			t.createSpec.Blur()
			return t, t.createPrompt.Focus()
		}
		var cmd tea.Cmd
		t.createSpec, cmd = t.createSpec.Update(msg)
		return t, cmd

	case createStepPrompt:
		if msg.String() == "enter" {
			if strings.TrimSpace(t.createPrompt.Value()) == "" {
				t.createError = "Prompt cannot be empty"
				return t, nil
			}
			t.createStep = createStepConfirm
			t.createPrompt.Blur()
			return t, nil
		}
		var cmd tea.Cmd
		t.createPrompt, cmd = t.createPrompt.Update(msg)
		return t, cmd

	case createStepConfirm:
		switch msg.String() {
		case "y", "enter":
			name := strings.TrimSpace(t.createName.Value())
			kind := scheduleKinds[t.createKind].value
			spec := strings.TrimSpace(t.createSpec.Value())
			prompt := strings.TrimSpace(t.createPrompt.Value())
			t.mode = jobsModeList
			return t, func() tea.Msg {
				ctx := context.Background()
				_, err := svc.CreateJob(ctx, name, kind, spec, prompt)
				return jobActionMsg{action: "Created " + name, err: err}
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

	b.WriteString("\n")

	// Action hints
	b.WriteString("  ")
	hints := []string{
		renderKeyHint("n", "new"),
		renderKeyHint("r", "run now"),
		renderKeyHint("p", "pause/resume"),
		renderKeyHint("x", "delete"),
		renderKeyHint("enter", "details"),
	}
	b.WriteString(strings.Join(hints, styleMuted.Render("  ")))
	b.WriteString("\n\n")

	if t.message != "" {
		b.WriteString("  ")
		b.WriteString(t.message)
		b.WriteString("\n\n")
	}

	if len(t.jobs) == 0 {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("No scheduled jobs. Press "))
		b.WriteString(styleKeyHelpKey.Render("n"))
		b.WriteString(styleMuted.Render(" to create one."))
		b.WriteString("\n")
		return b.String()
	}

	// Column headers
	b.WriteString("  ")
	b.WriteString(styleMuted.Render(
		padRight("", 3) +
			padRight("Name", maxInt(col-65, 15)) + "  " +
			padRight("Schedule", 22) + "  " +
			padRight("Status", 10) + "  " +
			padRight("Next Run", 16) + "  " +
			padRight("Last", 10)))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(horizontalRule(col))
	b.WriteString("\n")

	for i, job := range t.jobs {
		b.WriteString("  ")
		line := t.renderJobRow(job, col, i == t.cursor)
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (t *jobsTab) renderJobRow(job state.ScheduledJob, col int, selected bool) string {
	icon := enabledIcon(job.Enabled)
	name := padRight(truncate(job.Name, maxInt(col-65, 15)), maxInt(col-65, 15))
	sched := padRight(truncate(job.ScheduleKind+" "+job.ScheduleSpec, 22), 22)

	status := styleMuted.Render(padRight("—", 10))
	if !job.Enabled {
		status = styleWarning.Render(padRight("paused", 10))
	} else {
		status = styleSuccess.Render(padRight("active", 10))
	}

	next := padRight(fmtTime(job.NextRunAt), 16)
	last := padRight(timeAgo(job.LastRunAt), 10)

	row := fmt.Sprintf("%s  %s  %s  %s  %s  %s", icon, name, sched, status, next, last)

	if selected {
		// Apply subtle highlight to the entire row
		return styleSelectedRow.Render(row)
	}
	return row
}

func (t *jobsTab) viewDetail(width, height int) string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyHint("esc", "back to list"))
	b.WriteString("\n\n")

	if t.cursor >= len(t.jobs) {
		return b.String()
	}
	job := t.jobs[t.cursor]

	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render(job.Name))
	b.WriteString("\n\n")

	b.WriteString("  ")
	b.WriteString(renderKeyValue("ID", job.ID))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Schedule", job.ScheduleKind+" "+job.ScheduleSpec))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Status", func() string {
		if job.Enabled {
			return styleSuccess.Render("active")
		}
		return styleWarning.Render("paused")
	}()))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Next Run", fmtTime(job.NextRunAt)))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Prompt", truncate(job.Prompt, width-22)))
	b.WriteString("\n\n")

	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render("Run History"))
	b.WriteString("\n\n")

	if len(t.detailRuns) == 0 {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("No runs yet"))
		b.WriteString("\n")
	}
	for _, run := range t.detailRuns {
		b.WriteString("  ")
		icon := statusIcon(run.Status == "ok")
		b.WriteString(fmt.Sprintf("%s  %s  %s  %s",
			icon,
			styleMuted.Render(padRight(run.Status, 8)),
			styleMuted.Render(fmtTime(run.StartedAt)),
			styleMuted.Render(fmtDuration(run.FinishedAt.Sub(run.StartedAt))),
		))
		if run.Error != "" {
			b.WriteString("  ")
			b.WriteString(styleError.Render(truncate(run.Error, width-60)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// ── create wizard view ──────────────────────────────────────────────

func (t *jobsTab) viewCreate(width, height int) string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(styleTitle.Render("New Scheduled Job"))
	b.WriteString("\n\n")

	// Progress indicator
	b.WriteString("  ")
	for i := createStep(0); i < createStepCount; i++ {
		if i == t.createStep {
			b.WriteString(styleAccent.Render("● "))
			b.WriteString(styleBright.Render(createStepLabels[i]))
		} else if i < t.createStep {
			b.WriteString(styleSuccess.Render("● "))
			b.WriteString(styleMuted.Render(createStepLabels[i]))
		} else {
			b.WriteString(styleSubtle.Render("○ "))
			b.WriteString(styleSubtle.Render(createStepLabels[i]))
		}
		if i < createStepCount-1 {
			b.WriteString(styleMuted.Render("  →  "))
		}
	}
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(horizontalRule(width - 4))
	b.WriteString("\n\n")

	switch t.createStep {
	case createStepName:
		b.WriteString("  ")
		b.WriteString(styleInputLabel.Render("Job Name"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("A short, descriptive name for this job"))
		b.WriteString("\n\n")
		b.WriteString(t.createName.View())
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(renderKeyHint("enter", "next"))
		b.WriteString(styleMuted.Render("  "))
		b.WriteString(renderKeyHint("esc", "cancel"))

	case createStepKind:
		b.WriteString("  ")
		b.WriteString(styleInputLabel.Render("Schedule Type"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("How should this job be scheduled?"))
		b.WriteString("\n\n")
		for i, kind := range scheduleKinds {
			b.WriteString("  ")
			if i == t.createKind {
				b.WriteString(styleAccent.Render("▸ "))
				b.WriteString(styleBright.Render(kind.label))
				b.WriteString("  ")
				b.WriteString(styleMuted.Render(kind.hint))
			} else {
				b.WriteString(styleMuted.Render("  "))
				b.WriteString(styleBase.Render(kind.label))
				b.WriteString("  ")
				b.WriteString(styleSubtle.Render(kind.hint))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(renderKeyHint("↑/↓", "select"))
		b.WriteString(styleMuted.Render("  "))
		b.WriteString(renderKeyHint("enter", "next"))
		b.WriteString(styleMuted.Render("  "))
		b.WriteString(renderKeyHint("esc", "cancel"))

	case createStepSpec:
		kindInfo := scheduleKinds[t.createKind]
		b.WriteString("  ")
		b.WriteString(styleInputLabel.Render("Schedule "))
		b.WriteString(styleMuted.Render("(" + kindInfo.label + ")"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("Format: " + kindInfo.hint))
		b.WriteString("\n\n")
		b.WriteString(t.createSpec.View())
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(t.specContextHelp())
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(renderKeyHint("enter", "next"))
		b.WriteString(styleMuted.Render("  "))
		b.WriteString(renderKeyHint("esc", "cancel"))

	case createStepPrompt:
		b.WriteString("  ")
		b.WriteString(styleInputLabel.Render("Agent Prompt"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("What should the agent do when this job runs?"))
		b.WriteString("\n\n")
		b.WriteString(t.createPrompt.View())
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(renderKeyHint("enter", "next"))
		b.WriteString(styleMuted.Render("  "))
		b.WriteString(renderKeyHint("esc", "cancel"))

	case createStepConfirm:
		b.WriteString("  ")
		b.WriteString(styleInputLabel.Render("Review"))
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Name", t.createName.Value()))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Type", scheduleKinds[t.createKind].label))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Schedule", t.createSpec.Value()))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Prompt", truncate(t.createPrompt.Value(), width-22)))
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(styleBright.Render("Create this job?"))
		b.WriteString("  ")
		b.WriteString(renderKeyHint("y/enter", "confirm"))
		b.WriteString(styleMuted.Render("  "))
		b.WriteString(renderKeyHint("n/esc", "cancel"))
	}

	if t.createError != "" {
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(styleError.Render("⚠ " + t.createError))
	}

	return b.String()
}

func (t *jobsTab) specContextHelp() string {
	switch scheduleKinds[t.createKind].value {
	case "every":
		return styleMuted.Render("Go duration: 30s, 5m, 1h, 24h, 168h (weekly)")
	case "cron":
		lines := []string{
			styleMuted.Render("Five fields: minute hour day month weekday"),
			styleMuted.Render("Examples:"),
			styleMuted.Render("  0 */6 * * *    Every 6 hours"),
			styleMuted.Render("  30 9 * * 1-5   Weekdays at 9:30"),
			styleMuted.Render("  0 0 1 * *      First of month"),
		}
		return strings.Join(lines, "\n  ")
	case "at":
		return styleMuted.Render("RFC3339 timestamp: 2026-05-01T09:00:00Z")
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

var styleAccent = styleSectionTitle // Reuse the amber accent for consistency
