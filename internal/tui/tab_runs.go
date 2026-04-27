package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/state"
)

type runsDataMsg struct {
	runs []state.RunSummary
}

type runsTab struct {
	runs     []state.RunSummary
	cursor   int
	expanded bool // Whether we're viewing run detail
	loaded   bool
	scroll   int // scroll offset for the detail view
}

func newRunsTab() tabModel {
	return &runsTab{}
}

func (t *runsTab) Init(svc *Service) tea.Cmd {
	return func() tea.Msg { return loadRunsData(svc) }
}

func (t *runsTab) Consuming() bool { return false }

func (t *runsTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case runsDataMsg:
		t.runs = msg.runs
		t.loaded = true
		if t.cursor >= len(t.runs) && len(t.runs) > 0 {
			t.cursor = len(t.runs) - 1
		}

	case tea.KeyMsg:
		if t.expanded {
			return t.updateDetail(msg)
		}
		return t.updateList(msg, svc)
	}
	return t, nil
}

func (t *runsTab) updateList(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Down):
		if t.cursor < len(t.runs)-1 {
			t.cursor++
		}
	case key.Matches(msg, keys.Up):
		if t.cursor > 0 {
			t.cursor--
		}
	case key.Matches(msg, keys.Enter):
		if len(t.runs) > 0 {
			t.expanded = true
			t.scroll = 0
		}
	}
	return t, nil
}

func (t *runsTab) updateDetail(msg tea.KeyMsg) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		t.expanded = false
	case key.Matches(msg, keys.Down):
		t.scroll++
	case key.Matches(msg, keys.Up):
		if t.scroll > 0 {
			t.scroll--
		}
	}
	return t, nil
}

func (t *runsTab) View(width, height int) string {
	if !t.loaded {
		return styleMuted.Render("  Loading…")
	}

	if t.expanded {
		return t.viewDetail(width, height)
	}
	return t.viewList(width, height)
}

func (t *runsTab) viewList(width, height int) string {
	var b strings.Builder
	col := width - 4

	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyHint("enter", "expand"))
	b.WriteString(styleMuted.Render("  "))
	b.WriteString(renderKeyHint("↑/↓", "navigate"))
	b.WriteString("\n\n")

	if len(t.runs) == 0 {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("No agent runs recorded yet"))
		b.WriteString("\n")
		return b.String()
	}

	// Column headers
	b.WriteString("  ")
	nameCol := maxInt(col-70, 15)
	b.WriteString(styleMuted.Render(
		padRight("", 3) +
			padRight("Task", nameCol) + "  " +
			padRight("Model", 25) + "  " +
			padRight("Duration", 10) + "  " +
			padRight("Tokens", 8) + "  " +
			padRight("Tools", 7) + "  " +
			padRight("When", 10)))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(horizontalRule(col))
	b.WriteString("\n")

	visible := minInt(len(t.runs), height-6)
	for i := 0; i < visible; i++ {
		run := t.runs[i]
		selected := i == t.cursor
		b.WriteString("  ")
		b.WriteString(t.renderRunRow(run, col, nameCol, selected))
		b.WriteString("\n")
	}

	if len(t.runs) > visible {
		b.WriteString("\n  ")
		b.WriteString(styleMuted.Render(fmt.Sprintf("… and %d more", len(t.runs)-visible)))
	}

	return b.String()
}

func (t *runsTab) renderRunRow(run state.RunSummary, col, nameCol int, selected bool) string {
	icon := statusIcon(run.Success)
	task := padRight(truncate(run.Task, nameCol), nameCol)

	model := "—"
	if len(run.Phases) > 0 {
		m := run.Phases[0].ActualModel
		if m == "" {
			m = run.Phases[0].RequestedModel
		}
		model = truncate(m, 25)
	}
	model = padRight(model, 25)

	dur := "—"
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		dur = fmtDuration(run.FinishedAt.Sub(run.StartedAt))
	}
	dur = padRight(dur, 10)

	totalTokens := 0
	for _, phase := range run.Phases {
		totalTokens += phase.TotalTokens
	}
	tokens := padRight(formatTokens(totalTokens), 8)
	toolCount := padRight(fmt.Sprintf("%d", len(run.Tools)), 7)
	when := padRight(timeAgo(run.StartedAt), 10)

	row := fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s", icon, task, model, dur, tokens, toolCount, when)
	if selected {
		return styleSelectedRow.Render(row)
	}
	return row
}

func (t *runsTab) viewDetail(width, height int) string {
	if t.cursor >= len(t.runs) {
		return ""
	}
	run := t.runs[t.cursor]

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyHint("esc", "back to list"))
	b.WriteString(styleMuted.Render("  "))
	b.WriteString(renderKeyHint("↑/↓", "scroll"))
	b.WriteString("\n\n")

	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render("Run #"))
	b.WriteString(styleSectionTitle.Render(fmt.Sprintf("%d", run.ID)))
	b.WriteString("  ")
	b.WriteString(statusIcon(run.Success))
	b.WriteString("\n\n")

	b.WriteString("  ")
	b.WriteString(renderKeyValue("Task", run.Task))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Provider", run.Provider))
	b.WriteString("\n")
	if !run.StartedAt.IsZero() {
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Started", fmtTime(run.StartedAt)))
		b.WriteString("\n")
	}
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Duration", fmtDuration(run.FinishedAt.Sub(run.StartedAt))))
		b.WriteString("\n")
	}
	if run.VerificationStatus != "" {
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Verification", run.VerificationStatus))
		b.WriteString("\n")
	}
	if run.ErrorMessage != "" {
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Error", styleError.Render(truncate(run.ErrorMessage, width-22))))
		b.WriteString("\n")
	}

	// Phases
	if len(run.Phases) > 0 {
		b.WriteString("\n  ")
		b.WriteString(styleSectionTitle.Render("Phases"))
		b.WriteString("\n\n")
		for _, phase := range run.Phases {
			icon := statusIcon(phase.Success)
			model := phase.ActualModel
			if model == "" {
				model = phase.RequestedModel
			}
			b.WriteString(fmt.Sprintf("  %s  %s  %s  %s  %s tokens\n",
				icon,
				styleMuted.Render(padRight(string(phase.Phase), 12)),
				styleBase.Render(padRight(truncate(model, 30), 30)),
				styleMuted.Render(fmtDurationMs(phase.LatencyMs)),
				styleMuted.Render(formatTokens(phase.TotalTokens)),
			))
		}
	}

	// Tools
	if len(run.Tools) > 0 {
		b.WriteString("\n  ")
		b.WriteString(styleSectionTitle.Render("Tool Outcomes"))
		b.WriteString("\n\n")
		for _, tool := range run.Tools {
			icon := statusIcon(tool.Success)
			denied := ""
			if tool.PolicyDenied {
				denied = styleWarning.Render(" denied")
			}
			b.WriteString(fmt.Sprintf("  %s  %s  %s%s\n",
				icon,
				styleBase.Render(padRight(tool.ToolName, 20)),
				styleMuted.Render(fmtDurationMs(tool.DurationMs)),
				denied,
			))
			if tool.ErrorMessage != "" {
				b.WriteString(fmt.Sprintf("     %s\n", styleError.Render(truncate(tool.ErrorMessage, width-10))))
			}
		}
	}

	// Output preview
	if output := strings.TrimSpace(run.FinalOutput); output != "" {
		b.WriteString("\n  ")
		b.WriteString(styleSectionTitle.Render("Output"))
		b.WriteString("\n\n")
		lines := strings.Split(output, "\n")
		maxLines := minInt(len(lines), height-20)
		if maxLines < 1 {
			maxLines = 3
		}
		for _, line := range lines[:maxLines] {
			b.WriteString("  ")
			b.WriteString(styleMuted.Render(truncate(line, width-6)))
			b.WriteString("\n")
		}
		if len(lines) > maxLines {
			b.WriteString("  ")
			b.WriteString(styleSubtle.Render(fmt.Sprintf("… %d more lines", len(lines)-maxLines)))
			b.WriteString("\n")
		}
	}

	return b.String()
}

func loadRunDetail(svc *Service, runID int64) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		runs, _ := svc.RecentRuns(ctx, 1)
		_ = runs
		return nil
	}
}
