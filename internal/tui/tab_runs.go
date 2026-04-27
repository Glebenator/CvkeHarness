package tui

import (
	"encoding/json"
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
	expanded bool
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

func (t *runsTab) StatusHints() []string {
	if t.expanded {
		return []string{
			renderKeyHint("esc", "back"),
			renderKeyHint("↑↓", "scroll"),
		}
	}
	if len(t.runs) > 0 {
		return []string{
			renderKeyHint("↑↓", "move"),
			renderKeyHint("enter", "detail"),
			positionIndicator(t.cursor, len(t.runs)),
		}
	}
	return nil
}

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
			return t.updateDetail(msg, height)
		}
		return t.updateList(msg)
	}
	return t, nil
}

func (t *runsTab) updateList(msg tea.KeyMsg) (tabModel, tea.Cmd) {
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

func (t *runsTab) updateDetail(msg tea.KeyMsg, height int) (tabModel, tea.Cmd) {
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

	if len(t.runs) == 0 {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("No agent runs recorded yet"))
		b.WriteString("\n")
		return b.String()
	}

	// Column headers
	nameCol := maxInt(col-70, 15)
	b.WriteString("  ")
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

	// Windowed rendering: cursor stays visible
	listHeight := height - 5
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := listWindow(t.cursor, len(t.runs), listHeight)

	// Scroll-up indicator
	if start > 0 {
		b.WriteString("  ")
		b.WriteString(styleSubtle.Render(fmt.Sprintf("  ↑ %d more", start)))
		b.WriteString("\n")
	}

	for i := start; i < end; i++ {
		run := t.runs[i]
		selected := i == t.cursor
		b.WriteString("  ")
		b.WriteString(t.renderRunRow(run, col, nameCol, selected))
		b.WriteString("\n")
	}

	// Scroll-down indicator
	if end < len(t.runs) {
		b.WriteString("  ")
		b.WriteString(styleSubtle.Render(fmt.Sprintf("  ↓ %d more", len(t.runs)-end)))
		b.WriteString("\n")
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
		return styleAccent.Render("▸ ") + styleSelectedRow.Render(row)
	}
	return "  " + row
}

func (t *runsTab) viewDetail(width, height int) string {
	if t.cursor >= len(t.runs) {
		return ""
	}
	run := t.runs[t.cursor]

	// Build all the detail lines, then apply scroll window.
	var lines []string

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s  %s",
		styleSectionTitle.Render(fmt.Sprintf("Run #%d", run.ID)),
		statusIcon(run.Success)))
	lines = append(lines, "")
	lines = append(lines, "  "+renderKeyValue("Task", run.Task))
	if run.TaskClass != "" {
		lines = append(lines, "  "+renderKeyValue("Task Class", string(run.TaskClass)))
	}
	lines = append(lines, "  "+renderKeyValue("Provider", run.Provider))
	if !run.StartedAt.IsZero() {
		lines = append(lines, "  "+renderKeyValue("Started", fmtTime(run.StartedAt)))
	}
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		lines = append(lines, "  "+renderKeyValue("Duration", fmtDuration(run.FinishedAt.Sub(run.StartedAt))))
	}
	if run.RoutingEnabled {
		lines = append(lines, "  "+renderKeyValue("Routing", styleSuccess.Render("enabled")))
	}

	// Verification section
	if run.VerificationStatus != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+styleSectionTitle.Render("Verification"))
		lines = append(lines, "")
		verStyle := styleSuccess
		if run.VerificationStatus != "pass" && run.VerificationStatus != "ok" {
			verStyle = styleWarning
		}
		lines = append(lines, "  "+renderKeyValue("Status", verStyle.Render(run.VerificationStatus)))
		if run.VerificationReason != "" {
			// Wrap long reason text
			for _, line := range wrapText(run.VerificationReason, width-22) {
				lines = append(lines, "  "+renderKeyValue("Reason", styleMuted.Render(line)))
			}
		}
		if run.VerificationMissingActions != "" {
			lines = append(lines, "  "+renderKeyValue("Missing", styleWarning.Render(truncate(run.VerificationMissingActions, width-22))))
		}
		if run.VerificationRepairTriggered {
			lines = append(lines, "  "+renderKeyValue("Auto-Repair", styleWarning.Render("triggered")))
		}
	}

	if run.ErrorMessage != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+renderKeyValue("Error", styleError.Render(truncate(run.ErrorMessage, width-22))))
	}

	// Phases section with token breakdown
	if len(run.Phases) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+styleSectionTitle.Render("Phases"))
		lines = append(lines, "")
		for _, phase := range run.Phases {
			icon := statusIcon(phase.Success)
			model := phase.ActualModel
			if model == "" {
				model = phase.RequestedModel
			}
			lines = append(lines, fmt.Sprintf("  %s  %s  %s  %s  %s tokens",
				icon,
				styleMuted.Render(padRight(string(phase.Phase), 12)),
				styleBase.Render(padRight(truncate(model, 30), 30)),
				styleMuted.Render(fmtDurationMs(phase.LatencyMs)),
				styleMuted.Render(formatTokens(phase.TotalTokens)),
			))
			// Token breakdown sub-line
			var tokenParts []string
			if phase.PromptTokens > 0 {
				tokenParts = append(tokenParts, fmt.Sprintf("prompt %s", formatTokens(phase.PromptTokens)))
			}
			if phase.CompletionTokens > 0 {
				tokenParts = append(tokenParts, fmt.Sprintf("completion %s", formatTokens(phase.CompletionTokens)))
			}
			if phase.CachedTokensKnown && phase.CachedTokens > 0 {
				tokenParts = append(tokenParts, fmt.Sprintf("cached %s", formatTokens(phase.CachedTokens)))
			}
			if len(tokenParts) > 0 {
				lines = append(lines, "       "+styleSubtle.Render(strings.Join(tokenParts, "  ·  ")))
			}
			if phase.Confidence > 0 {
				lines = append(lines, "       "+styleSubtle.Render(fmt.Sprintf("confidence %.0f%%", phase.Confidence*100)))
			}
			if phase.Explanation != "" {
				lines = append(lines, "       "+styleSubtle.Render(truncate(phase.Explanation, width-12)))
			}
		}
	}

	// Tool outcomes with more detail
	if len(run.Tools) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+styleSectionTitle.Render("Tool Outcomes"))
		lines = append(lines, "")
		for _, tool := range run.Tools {
			icon := statusIcon(tool.Success)
			denied := ""
			if tool.PolicyDenied {
				denied = styleWarning.Render(" denied")
				if tool.DenialClass != "" {
					denied += styleMuted.Render(" (" + tool.DenialClass + ")")
				}
			}
			phaseBadge := ""
			if tool.Phase != "" {
				phaseBadge = styleSubtle.Render("[" + string(tool.Phase) + "] ")
			}
			lines = append(lines, fmt.Sprintf("  %s  %s%s  %s%s",
				icon,
				phaseBadge,
				styleBase.Render(padRight(tool.ToolName, 20)),
				styleMuted.Render(fmtDurationMs(tool.DurationMs)),
				denied,
			))
			if tool.ErrorMessage != "" {
				lines = append(lines, fmt.Sprintf("     %s", styleError.Render(truncate(tool.ErrorMessage, width-10))))
			}
			if tool.Command != "" {
				lines = append(lines, fmt.Sprintf("     %s %s", styleSubtle.Render("command"), styleBase.Render(truncate(tool.Command, width-18))))
			} else if args := formatToolArguments(tool.Arguments); args != "" {
				lines = append(lines, fmt.Sprintf("     %s %s", styleSubtle.Render("args"), styleBase.Render(truncate(args, width-15))))
			}
		}
	}

	// Final output — always show prominently
	if output := strings.TrimSpace(run.FinalOutput); output != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+horizontalRule(width-4))
		lines = append(lines, "")
		lines = append(lines, "  "+styleSectionTitle.Render("Agent Output"))
		lines = append(lines, "")
		for _, line := range strings.Split(output, "\n") {
			lines = append(lines, "  "+styleBase.Render(truncate(line, width-6)))
		}
	}

	// Apply scroll offset
	maxScroll := maxInt(len(lines)-height+2, 0)
	t.scroll = clamp(t.scroll, 0, maxScroll)

	var b strings.Builder
	end := minInt(t.scroll+height-1, len(lines))
	for i := t.scroll; i < end; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}

	// Scroll indicator
	if maxScroll > 0 {
		b.WriteString("  ")
		b.WriteString(scrollHints(t.scroll, end, len(lines)))
	}

	return b.String()
}

func formatToolArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	var compacted any
	if err := json.Unmarshal([]byte(arguments), &compacted); err != nil {
		return arguments
	}
	b, err := json.Marshal(compacted)
	if err != nil {
		return arguments
	}
	return string(b)
}
