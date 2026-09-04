package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

func renderPageHeader(title, subtitle string, width int) string {
	// Keep headings legible while reserving a consistent title and subtitle of a small terminal.
	return "\n  " + styleTitle.Render(title) + "\n  " +
		styleMuted.Render(truncate(subtitle, maxInt(width-4, 1))) + "\n\n"
}

// renderListEntry reserves a consistent selection lane and separates the task
// from its metadata. The pointer also identifies selection without color.
func renderListEntry(title, metadata string, selected bool, width int) string {
	width = maxInt(width-4, 1)
	line := truncate(title, maxInt(width-3, 1))
	marker := "  "
	if selected {
		marker = "▸ "
	}
	var row string
	if selected {
		row = styleSelectedRow.Width(width).Render(marker + line)
	} else {
		row = styleBase.Render(marker + line)
	}
	stateLabel, detail, split := strings.Cut(metadata, " · ")
	stateStyle := styleMuted
	switch strings.ToUpper(stateLabel) {
	case "FAILED", "UNSATISFIED", "STALE CLAIM":
		stateStyle = styleError
	case "APPROVAL REQUIRED", "BLOCKED", "BLOCKED_WAITING_USER", "PAUSED", "OVERDUE JOB":
		stateStyle = styleWarning
	case "COMPLETED", "SUCCEEDED", "ACTIVE":
		stateStyle = styleSuccess
	}
	secondary := stateStyle.Render(stateLabel)
	if split {
		secondary += styleMuted.Render(" · " + detail)
	}
	return "  " + row + "\n    " + truncate(secondary, maxInt(width-2, 1)) + "\n"
}

func renderGroupLabel(label string, count int) string {
	return "  " + styleMuted.Bold(true).Render(strings.ToUpper(label)) +
		styleMuted.Render(fmt.Sprintf("  %d", count)) + "\n"
}

func renderEmptyState(title, detail, keyName, action string) string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(styleBright.Render(title))
	b.WriteString("\n")
	if detail != "" {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render(detail))
		b.WriteString("\n")
	}
	if keyName != "" && action != "" {
		b.WriteString("\n  ")
		b.WriteString(renderKeyHint(keyName, action))
		b.WriteString("\n")
	}
	return b.String()
}

func renderTableHeader(width int, header string) string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(styleMuted.Render(truncate(header, maxInt(width-4, 20))))
	b.WriteString("\n  ")
	b.WriteString(horizontalRule(maxInt(width-4, 20)))
	b.WriteString("\n")
	return b.String()
}

func renderSelectableRow(content string, selected bool) string {
	if selected {
		return styleSectionTitle.Render("▸ ") + styleSelectedRow.Render(content)
	}
	return "  " + content
}

func renderStatusBadge(label string, active bool) string {
	if active {
		return styleSuccess.Render(label)
	}
	return styleWarning.Render(label)
}

// wrapDisplay wraps already-rendered UI text without truncating its contents.
func wrapDisplay(value string, width int) string {
	return ansi.Hardwrap(ansi.Wrap(value, maxInt(width, 1), ""), maxInt(width, 1), true)
}

func renderInputSurface(content string, width int) string {
	box := lipgloss.NewStyle().Width(maxInt(width-6, 1)).
		Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).
		Padding(0, 1).Render(content)
	return "  " + strings.ReplaceAll(box, "\n", "\n  ")
}
