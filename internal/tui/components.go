package tui

import (
	"strings"
)

func renderPageHeader(title, subtitle string, width int) string {
	var b strings.Builder
	b.WriteString("\n  ")
	b.WriteString(styleSectionTitle.Render(title))
	if subtitle != "" {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render(subtitle))
	}
	b.WriteString("\n  ")
	b.WriteString(horizontalRule(maxInt(width-4, 20)))
	b.WriteString("\n\n")
	return b.String()
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
