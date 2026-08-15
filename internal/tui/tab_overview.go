package tui

import (
	"fmt"
	"strings"

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
}

type overviewTab struct {
	cfg      *config.Config
	runs     []state.RunSummary
	jobs     []state.ScheduledJob
	sessions []state.ChatSessionSummary
	allRuns  []state.RunSummary
	loaded   bool
}

func newOverviewTab() tabModel {
	return &overviewTab{}
}

func (t *overviewTab) Init(svc *Service) tea.Cmd {
	return func() tea.Msg { return loadOverviewData(svc) }
}

func (t *overviewTab) Consuming() bool { return false }

func (t *overviewTab) StatusHints() []string { return nil }

func (t *overviewTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case overviewDataMsg:
		t.cfg = msg.cfg
		t.runs = msg.runs
		t.jobs = msg.jobs
		t.sessions = msg.sessions
		t.allRuns = msg.allRuns
		t.loaded = true
	}
	return t, nil
}

func (t *overviewTab) View(width, height int) string {
	if !t.loaded {
		return styleMuted.Render("  Loading…")
	}

	var b strings.Builder
	col := width - 4

	b.WriteString(renderPageHeader("Overview", "configuration, activity, and next work", width))
	b.WriteString(t.configBlock(col))
	b.WriteString("\n\n")
	b.WriteString(t.statsBlock(col))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(horizontalRule(col))
	b.WriteString("\n\n")

	// ── Recent runs ─────────────────────────────────────────────
	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render("Recent Runs"))
	b.WriteString("\n\n")
	if len(t.runs) == 0 {
		b.WriteString(renderEmptyState("No runs recorded yet", "Run an agent task to populate this feed.", "", ""))
	}
	for _, run := range t.runs {
		b.WriteString("  ")
		b.WriteString(t.renderRunLine(run, col))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// ── Upcoming jobs ───────────────────────────────────────────
	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render("Scheduled Jobs"))
	b.WriteString("\n\n")
	if len(t.jobs) == 0 {
		b.WriteString(renderEmptyState("No jobs configured", "Create a job from the Jobs tab when you want recurring work.", "", ""))
	}
	shown := minInt(len(t.jobs), 5)
	for _, job := range t.jobs[:shown] {
		b.WriteString("  ")
		b.WriteString(t.renderJobLine(job, col))
		b.WriteString("\n")
	}

	return b.String()
}

func (t *overviewTab) configBlock(col int) string {
	if t.cfg == nil {
		return "  " + styleMuted.Render("Configuration not available")
	}
	cfg := t.cfg
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Provider", cfg.Provider))
	b.WriteString("    ")
	b.WriteString(renderKeyValue("Model", cfg.PrimaryModel()))
	b.WriteString("\n  ")
	security := "invalid"
	if effective, err := cfg.EffectiveSecurity(); err == nil {
		security = effective.Summary()
	}
	b.WriteString(renderKeyValue("Security", security))
	b.WriteString("    ")
	routing := "disabled"
	if cfg.RoutingEnabled {
		routing = styleSuccess.Render("enabled") + styleMuted.Render(" ("+cfg.RoutingMode+")")
	}
	b.WriteString(renderKeyValue("Routing", routing))
	return b.String()
}

func (t *overviewTab) statsBlock(col int) string {
	totalRuns := len(t.allRuns)
	successes := 0
	for _, run := range t.allRuns {
		if run.Success {
			successes++
		}
	}
	chatCount := len(t.sessions)
	jobCount := len(t.jobs)

	parts := []string{
		renderKeyValue("Runs", fmt.Sprintf("%d", totalRuns)),
		renderKeyValue("Success Rate", successRate(totalRuns, successes)),
		renderKeyValue("Chat Sessions", fmt.Sprintf("%d", chatCount)),
		renderKeyValue("Jobs", fmt.Sprintf("%d", jobCount)),
	}

	var b strings.Builder
	for i, part := range parts {
		b.WriteString("  ")
		b.WriteString(part)
		if i < len(parts)-1 {
			b.WriteString(styleMuted.Render("   "))
		}
	}
	return b.String()
}

func (t *overviewTab) renderRunLine(run state.RunSummary, col int) string {
	icon := statusIcon(run.Success)
	task := truncate(run.Task, col-40)
	model := ""
	if len(run.Phases) > 0 {
		m := run.Phases[0].ActualModel
		if m == "" {
			m = run.Phases[0].RequestedModel
		}
		model = truncate(m, 30)
	}
	dur := ""
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		dur = fmtDuration(run.FinishedAt.Sub(run.StartedAt))
	}

	return fmt.Sprintf("%s  %s  %s  %s  %s",
		icon,
		styleBase.Render(padRight(task, maxInt(col-50, 20))),
		styleMuted.Render(padRight(model, 25)),
		styleMuted.Render(padRight(dur, 8)),
		styleMuted.Render(timeAgo(run.StartedAt)),
	)
}

func (t *overviewTab) renderJobLine(job state.ScheduledJob, col int) string {
	icon := enabledIcon(job.Enabled)
	name := truncate(job.Name, col-45)
	schedule := truncate(job.ScheduleKind+" "+job.ScheduleSpec, 20)
	status := ""
	if !job.Enabled {
		status = styleWarning.Render("paused")
	} else if !job.NextRunAt.IsZero() {
		status = styleMuted.Render("next " + fmtTime(job.NextRunAt))
	}

	return fmt.Sprintf("%s  %s  %s  %s",
		icon,
		styleBase.Render(padRight(name, maxInt(col-50, 15))),
		styleMuted.Render(padRight(schedule, 20)),
		status,
	)
}
