package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Activity is linked at the prompt that owns it, independently of whether a
// provider ever supplies a final answer. All hit targets use rendered rows.
func (t *chatTab) refreshViewport() {
	var lines []string
	t.chatTurnLinks = make(map[int]int)
	if t.contextExpanded {
		lines = append(lines, "  "+styleSectionTitle.Render("Session context"))
		appendWrappedBlock(&lines, "  ", "Model:", t.configuredModel, t.viewport.Width-4, styleMuted, styleBase)
		appendWrappedBlock(&lines, "  ", "Activity:", t.statusDetail, t.viewport.Width-4, styleMuted, styleBase)
		lines = append(lines, "  Ctrl+G returns to the conversation.", "")
	}
	if t.lastError != "" {
		appendWrappedBlock(&lines, "  ", "Unable to continue:", t.lastError, t.viewport.Width-4, styleError, styleBase)
		lines = append(lines, "  Your draft is preserved. Enter edits it; Enter again retries.", "  Leave the composer, then press s to open Settings.", "")
	}
	if len(t.messages) == 0 && t.lastError == "" {
		title := "What would you like to investigate?"
		if t.starting {
			title = "Connecting. Your message is waiting."
		}
		lines = append(lines,
			styleBright.Render("  "+title),
			styleMuted.Render("  Name the target, the outcome, and any constraints."), "",
			styleMuted.Render("  For example"),
			styleBase.Render("  Inspect staging API health. Report issues without changing it."), "",
			"  "+renderKeyHint("/", "Commands")+"    "+renderKeyHint("ctrl+h", "Past conversations"))
	}
	for _, message := range t.messages {
		if message.role == "assistant" {
			t.appendAssistantResponse(&lines, message.content)
			continue
		}
		label, labelStyle := "CVKEHARNESS", styleSectionTitle
		switch message.role {
		case "user":
			label, labelStyle = "YOU", styleBright
		case "system":
			label, labelStyle = "CONSOLE", styleMuted
		case "error":
			label, labelStyle = "ERROR", styleError
		}
		lines = append(lines, "  "+labelStyle.Render(label))
		for _, raw := range strings.Split(message.content, "\n") {
			for _, line := range wrapText(raw, maxInt(t.viewport.Width-4, 18)) {
				lines = append(lines, "  "+styleBase.Render(line))
			}
		}
		if message.role == "user" {
			t.chatTurnLinks[chatRenderedRows(lines)] = message.turn
			lines = append(lines, "  "+styleAccent.Render(truncate(t.turnActivityLabel(message.turn), maxInt(t.viewport.Width-4, 18))))
		}
		lines = append(lines, "")
	}
	if t.pendingApproval != nil {
		t.appendApprovalPrompt(&lines, t.pendingApproval, maxInt(t.viewport.Width-4, 18))
	}
	if t.running {
		lines = append(lines, "", "  "+renderNamedStatus(t.status))
		for _, line := range wrapText(t.statusDetail, maxInt(t.viewport.Width-4, 18)) {
			lines = append(lines, "  "+styleMuted.Render(line))
		}
	}
	t.viewport.SetContent(strings.Join(lines, "\n"))
	t.refreshActivity()
}

func chatRenderedRows(blocks []string) int {
	rows := 0
	for _, block := range blocks {
		rows += strings.Count(block, "\n") + 1
	}
	return rows
}

func (t *chatTab) turnActivityLabel(turn int) string {
	count, status := 0, ""
	for _, call := range t.toolCalls {
		if call.turn != turn {
			continue
		}
		count++
		// Actionable states must survive the compact conversation summary.
		if status == "" || chatToolStatusPriority(call.status) > chatToolStatusPriority(status) {
			status = call.status
		}
	}
	label := "View activity · no tool calls"
	if count == 1 {
		label = "View 1 tool call"
	} else if count > 1 {
		label = fmt.Sprintf("View %d tool calls", count)
	}
	if status != "" {
		label += " · " + status
	}
	if verification, ok := t.verifierActivity[turn]; ok {
		label += " · " + verificationActivityLabel(verification.VerificationActivity)
	}
	return label
}

func chatToolStatusPriority(status string) int {
	switch status {
	case "APPROVAL REQUIRED":
		return 5
	case "FAILED", "DENIED":
		return 4
	case "RUNNING":
		return 3
	case "APPROVAL CHECK":
		return 2
	default:
		return 1
	}
}

func (t *chatTab) conversationBar(width int) string {
	focus := "CONVERSATION"
	if !t.composerFocused && !t.activity.focused {
		focus = "▸ " + focus
	}
	state := "Latest"
	if !t.viewport.AtBottom() {
		state = "Reading history · Ctrl+End latest"
	}
	return "  " + styleMuted.Render(truncate(focus+" · "+state, maxInt(width-4, 16)))
}

func (t *chatTab) visibleChatTurn() int {
	if t.viewport.AtBottom() {
		return t.activeTurn
	}
	turn, nearest := t.activeTurn, -1
	center := t.viewport.YOffset + t.viewport.Height/2
	for row, candidate := range t.chatTurnLinks {
		if row <= center && row > nearest {
			turn, nearest = candidate, row
		}
	}
	return turn
}

func (t *chatTab) updateMouse(msg tea.MouseMsg) (tabModel, tea.Cmd) {
	// A narrow Activity view can close on mouse-down. Its matching release
	// still belongs to that click, not the conversation newly revealed below.
	if msg.Action == tea.MouseActionRelease && t.swallowMouseRelease {
		t.swallowMouseRelease = false
		t.activity.mousePressHandled = false
		return t, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		t.swallowMouseRelease = false
	}
	direction := verticalMouseWheelDirection(msg)
	if t.history {
		if direction != 0 {
			delta := maxInt(t.viewport.MouseWheelDelta, 1)
			if t.expanded {
				t.scroll = maxInt(t.scroll+direction*delta, 0)
			} else if len(t.sessions) > 0 {
				t.cursor = clamp(t.cursor+direction*delta, 0, len(t.sessions)-1)
			}
		}
		return t, nil
	}
	// Bubble Tea reports screen coordinates; the console tab bar and divider
	// take two rows before this tab's own header.
	y := msg.Y - 2 - strings.Count(t.liveHeader(t.viewWidth), "\n")
	if y < 0 || y >= t.viewHeight-strings.Count(t.liveHeader(t.viewWidth), "\n") {
		return t, nil
	}
	split, mainWidth, _ := liveChatColumns(t.viewWidth)
	if (!split && t.activity.focused) || (split && msg.X > mainWidth) {
		local := msg
		local.Y = y
		if split {
			local.X -= mainWidth + 1
		}
		t.handleActivityMouse(local)
		if !split && !t.activity.focused && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			t.swallowMouseRelease = true
		}
		return t, nil
	}
	click := msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease
	if click && y == 0 && !t.viewport.AtBottom() {
		t.viewport.GotoBottom()
		return t, nil
	}
	if y > 0 && y <= t.viewport.Height {
		if direction != 0 {
			t.viewport.SetYOffset(t.viewport.YOffset + direction*maxInt(t.viewport.MouseWheelDelta, 1))
		} else if click {
			if turn, ok := t.chatTurnLinks[t.viewport.YOffset+y-1]; ok {
				t.openActivity(turn)
			} else {
				t.activity.focused = false
				t.composerFocused = false
				t.composer.Blur()
			}
		}
		return t, nil
	}
	if click && y > t.viewport.Height+t.commandMenuLines() {
		t.activity.focused = false
		t.composerFocused = true
		t.composer.Focus()
	}
	return t, nil
}
