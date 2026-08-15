package setuptui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/internal/setupflow"
)

var (
	colorBase       = lipgloss.Color("#a8a29e")
	colorMuted      = lipgloss.Color("#78716c")
	colorSubtle     = lipgloss.Color("#57534e")
	colorAccent     = lipgloss.Color("#d4a574")
	colorSuccess    = lipgloss.Color("#87a987")
	colorWarning    = lipgloss.Color("#c4a35a")
	colorError      = lipgloss.Color("#c47a5a")
	colorHighlight  = lipgloss.Color("#3a3533")
	colorBrightText = lipgloss.Color("#d6d3d1")

	styleTitle         = lipgloss.NewStyle().Foreground(colorBrightText).Bold(true)
	styleStep          = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleBase          = lipgloss.NewStyle().Foreground(colorBase)
	styleAccent        = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleMuted         = lipgloss.NewStyle().Foreground(colorMuted)
	styleSubtle        = lipgloss.NewStyle().Foreground(colorSubtle)
	styleSuccess       = lipgloss.NewStyle().Foreground(colorSuccess)
	styleWarning       = lipgloss.NewStyle().Foreground(colorWarning)
	styleError         = lipgloss.NewStyle().Foreground(colorError)
	styleRowLabel      = lipgloss.NewStyle().Foreground(colorBrightText)
	styleRowDesc       = lipgloss.NewStyle().Foreground(colorMuted)
	styleSelectedLabel = lipgloss.NewStyle().Foreground(colorBrightText).Bold(true)
	styleSelectedDesc  = lipgloss.NewStyle().Foreground(colorBase)
	styleSelect        = lipgloss.NewStyle().Background(colorHighlight)
	styleKey           = lipgloss.NewStyle().Foreground(colorAccent)
)

type setupStage int

const (
	stageConnect setupStage = iota
	stageSafety
	stageCapabilities
	stageReady
)

var stageOrder = []setupStage{stageConnect, stageSafety, stageCapabilities, stageReady}

var stageLabels = map[setupStage]string{
	stageConnect:      "Connect",
	stageSafety:       "Safety",
	stageCapabilities: "Capabilities",
	stageReady:        "Ready",
}

var stepLabels = map[step]string{
	stepWelcome:          "Welcome",
	stepProvider:         "Provider",
	stepCredentials:      "Credentials",
	stepModel:            "Model",
	stepSafety:           "Safety",
	stepSecurityControls: "Security Controls",
	stepScan:             "System Scan",
	stepDependencies:     "Dependencies",
	stepDaemon:           "Scheduler Daemon",
	stepCapabilities:     "Capabilities",
	stepWebSearch:        "Web Search",
	stepRecommendations:  "Guided Review",
	stepSoul:             "Guidance",
	stepNotes:            "Machine Notes",
	stepReview:           "Review",
}

type row struct {
	label string
	desc  string
}

func (m setupModel) frame(title, body string) string {
	var b strings.Builder
	width := m.width - 4
	if width < 24 {
		width = 24
	}

	b.WriteString("\n  ")
	b.WriteString(styleTitle.Render("CvkeHarness"))
	b.WriteString(styleMuted.Render("  GUIDED SETUP  "))
	b.WriteString(styleStep.Render(stageLabels[m.stage()]))
	b.WriteString(styleMuted.Render(fmt.Sprintf("  %d/%d", m.stageNumber(), len(stageOrder))))
	b.WriteString("\n  ")
	b.WriteString(styleSubtle.Render(strings.Repeat("─", min(width, 110))))
	b.WriteString("\n\n")

	content := "  " + styleTitle.Render(title) + "\n\n" + body
	if m.width >= 120 && m.step != stepDone {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, m.progressRail(), content))
	} else {
		b.WriteString(content)
	}

	if m.errMessage != "" {
		b.WriteString("\n")
		for _, line := range wrap(m.errMessage, max(20, width-4)) {
			b.WriteString("  ")
			b.WriteString(styleError.Render("! " + line))
			b.WriteString("\n")
		}
	}
	if m.message != "" {
		b.WriteString("\n")
		for _, line := range wrap(m.message, max(20, width-4)) {
			b.WriteString("  ")
			b.WriteString(styleAccent.Render("• " + line))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m setupModel) progressRail() string {
	var b strings.Builder
	for _, s := range stageOrder {
		label := stageLabels[s]
		switch {
		case s < m.stage():
			b.WriteString(styleSuccess.Render("  ● "))
			b.WriteString(styleMuted.Render(label))
		case s == m.stage():
			b.WriteString(styleAccent.Render("  ▸ "))
			b.WriteString(styleStep.Render(label))
		default:
			b.WriteString(styleSubtle.Render("  ○ "))
			b.WriteString(styleSubtle.Render(label))
		}
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(22).Render(b.String())
}

func (m setupModel) footer() string {
	if m.inputMode != inputNone {
		return "  " + keyHint("enter", "submit") + "  " + keyHint("esc", "cancel") + "  " + keyHint("ctrl+c", "quit")
	}
	hints := []string{
		keyHint("enter", "select"),
		keyHint("n", "continue"),
		keyHint("esc", "back"),
		keyHint("↑↓", "move"),
	}
	if m.step == stepSafety || m.step == stepScan || m.step == stepCapabilities {
		hints = append(hints, keyHint("a", "advanced"))
	}
	if m.step == stepSecurityControls {
		hints = append(hints, keyHint("←→", "change"), keyHint("r", "reset"))
	}
	if m.width >= 72 {
		hints = append(hints, keyHint("q", "quit"))
	}
	return "  " + strings.Join(hints, "  ")
}

func (m setupModel) stage() setupStage {
	switch {
	case m.step <= stepModel:
		return stageConnect
	case m.step <= stepDaemon:
		return stageSafety
	case m.step <= stepNotes:
		return stageCapabilities
	default:
		return stageReady
	}
}

func (m setupModel) stageNumber() int {
	return int(m.stage()) + 1
}

func (m setupModel) stepTitle() string {
	if title := stepLabels[m.step]; title != "" {
		return title
	}
	return "Setup"
}

func (m setupModel) renderList(rows []row) string {
	var b strings.Builder
	contentWidth := max(20, m.width-8)
	if m.width >= 120 {
		contentWidth = max(20, m.width-30)
	}
	stacked := contentWidth < 88
	for i, row := range rows {
		pointer := "  "
		labelStyle := styleRowLabel
		descStyle := styleRowDesc
		if i == m.cursor {
			pointer = styleAccent.Render("▸ ")
			labelStyle = styleSelectedLabel
			descStyle = styleSelectedDesc
		}
		if stacked {
			line := pointer + labelStyle.Render(truncate(row.label, contentWidth-4))
			if i == m.cursor {
				line = styleSelect.Render(line)
			}
			b.WriteString("  " + line + "\n")
			for _, desc := range wrap(row.desc, max(16, contentWidth-6)) {
				b.WriteString("      " + descStyle.Render(desc) + "\n")
			}
		} else {
			labelWidth := min(24, max(14, contentWidth/3))
			label := truncate(row.label, labelWidth)
			gap := strings.Repeat(" ", max(1, labelWidth-len([]rune(label))))
			desc := truncate(row.desc, max(16, contentWidth-labelWidth-5))
			line := pointer + labelStyle.Render(label) + gap + " " + descStyle.Render(desc)
			if i == m.cursor {
				line = styleSelect.Render(line)
			}
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

func (m setupModel) renderInputField(label string) string {
	width := 72
	if m.width > 0 {
		width = min(72, max(18, m.width-8))
		if m.width >= 120 {
			width = min(72, max(18, m.width-30))
		}
	}
	field := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(colorHighlight).
		Foreground(colorBrightText).
		Render(m.input.View())
	return "  " + styleAccent.Render(label) + "\n  " + field + "\n"
}

func modelRows(items []setupflow.ModelOption) []row {
	rows := make([]row, 0, len(items))
	for _, item := range items {
		rows = append(rows, row{item.ID, item.Description})
	}
	return rows
}

func (m setupModel) paragraph(lines ...string) string {
	var b strings.Builder
	maxWidth := 72
	if m.width > 0 {
		maxWidth = max(20, min(72, m.width-8))
	}
	if m.width >= 120 {
		maxWidth = 72
	}
	for _, line := range lines {
		for _, wrapped := range wrap(line, maxWidth) {
			b.WriteString("  ")
			b.WriteString(styleBase.Render(wrapped))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func line(text string) string { return "  " + styleBase.Render(text) + "\n" }

func keyHint(key, desc string) string {
	return styleKey.Render(key) + " " + styleMuted.Render(desc)
}

func foundText(tool setupflow.ToolStatus) string {
	if !tool.Found {
		return styleWarning.Render("not found")
	}
	if tool.Version != "" {
		return styleSuccess.Render(tool.Path) + styleMuted.Render(" · "+tool.Version)
	}
	return styleSuccess.Render(tool.Path)
}

func boolText(value bool) string {
	if value {
		return styleSuccess.Render("yes")
	}
	return styleWarning.Render("no")
}

func bytesText(value uint64) string {
	if value == 0 {
		return styleMuted.Render("unknown")
	}
	const gib = 1024 * 1024 * 1024
	const mib = 1024 * 1024
	if value >= gib {
		return styleSuccess.Render(fmt.Sprintf("%.1f GiB", float64(value)/gib))
	}
	return styleSuccess.Render(fmt.Sprintf("%.0f MiB", float64(value)/mib))
}

func wrap(s string, maxWidth int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len([]rune(current))+1+len([]rune(word)) > maxWidth {
			lines = append(lines, current)
			current = word
		} else {
			current += " " + word
		}
	}
	lines = append(lines, current)
	return lines
}

func truncate(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return string(runes[:maxWidth])
	}
	return string(runes[:maxWidth-1]) + "…"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
