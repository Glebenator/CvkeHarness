package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (t *chatTab) approvalDialogOpen() bool {
	return t.pendingApproval != nil && !t.pendingApproval.dismissed && !t.history && !t.activity.focused
}

func (t *chatTab) updateApprovalDialog(msg tea.KeyMsg) (tabModel, tea.Cmd) {
	a := t.pendingApproval
	if t.approvalInFlight {
		return t, nil
	}
	switch msg.String() {
	case "esc":
		a.dismissed = true
	case "d":
		a.details = !a.details
		a.offset = 0
	case "up", "pgup":
		a.offset = maxInt(0, a.offset-1)
	case "down", "pgdown":
		a.offset++
	case "home":
		a.offset = 0
	case "end":
		a.offset = 1 << 30
	case "a":
		if strings.TrimSpace(a.summary) == "" {
			return t, nil
		}
		t.approvalInFlight = true
		t.approvalWorkID = a.workID
		t.status = "APPROVING"
		t.lastError = ""
		return t, approveBlockedWorkCmd(t.session, a.workID)
	}
	return t, nil
}

func (t *chatTab) approvalDialogView(width, height int) string {
	a := t.pendingApproval
	inner := maxInt(10, minInt(width-10, 98))
	// Hard wrap the command without abbreviating it or collapsing whitespace.
	command := strings.ReplaceAll(a.summary, "\t", "    ")
	command = strings.ReplaceAll(command, "\r", "\\r")
	command = strings.ReplaceAll(command, "\x1b", "\\x1b")
	rows := []string{styleAccent.Render("COMMAND TO APPROVE · secrets remain masked")}
	commandStyle := styleBright.Bold(true).Background(colorHighlight).Width(inner).Padding(0, 1)
	for _, line := range strings.Split(ansi.Hardwrap(command, inner-2, true), "\n") {
		rows = append(rows, commandStyle.Render(line))
	}
	if a.advice != nil {
		rows = append(rows, "")
		for _, line := range a.advice.Lines() {
			for _, row := range activityPhysicalLines(line, inner) {
				rows = append(rows, styleBase.Render(row))
			}
		}
	}
	if a.details {
		rows = append(rows, "", "POLICY DETAILS")
		rows = append(rows, activityPhysicalLines(activitySafeText(a.reason), inner)...)
		for _, effect := range a.effects {
			rows = append(rows, activityPhysicalLines(activitySafeText(effect.Setting+": "+effect.Detail+" "+effect.Target), inner)...)
		}
	}
	available := maxInt(height-12, 1)
	a.offset = clamp(a.offset, 0, maxInt(len(rows)-available, 0))
	end := minInt(a.offset+available, len(rows))
	body := strings.Join(rows[a.offset:end], "\n")
	title := "APPROVAL REQUIRED"
	context := "Target: " + firstNonEmptyText(t.target, "not resolved") + " · " + firstNonEmptyText(t.environment, "unknown environment")
	footer := "a approve once + continue"
	if strings.TrimSpace(a.summary) == "" {
		footer = "Command unavailable: approval disabled"
	}
	if t.approvalInFlight {
		footer = "Recording approval…"
	}
	content := styleWarning.Bold(true).Render(title) + "\n" +
		wrapDisplay(context, inner) + "\n\n" +
		body + "\n\n" +
		styleMuted.Render(fmt.Sprintf("Lines %d–%d of %d · ↑↓ scroll · d details", a.offset+1, end, len(rows))) + "\n" +
		styleMuted.Render("One use · exact action · expires in 15 minutes") + "\n" +
		styleAccent.Bold(true).Render(footer) + "\n" +
		styleMuted.Render("Esc Back without approving · Ctrl+X interrupt")
	if t.lastError != "" {
		content += "\n" + wrapDisplay(t.lastError, inner)
	}
	box := lipgloss.NewStyle().Width(inner+2).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
