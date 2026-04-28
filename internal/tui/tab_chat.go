package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/state"
)

type chatDataMsg struct {
	sessions []state.ChatSessionSummary
}

type chatDetailMsg struct {
	detail state.ChatSessionDetail
}

type chatTab struct {
	sessions []state.ChatSessionSummary
	cursor   int
	expanded bool
	detail   state.ChatSessionDetail
	loaded   bool
	scroll   int
	message  string
}

func newChatTab() tabModel {
	return &chatTab{}
}

func (t *chatTab) Init(svc *Service) tea.Cmd {
	return func() tea.Msg { return loadChatData(svc) }
}

func (t *chatTab) Consuming() bool { return false }

func (t *chatTab) StatusHints() []string {
	if t.expanded {
		return []string{
			renderKeyHint("esc", "back"),
			renderKeyHint("↑↓", "scroll"),
			renderKeyHint("pgup/pgdn", "page"),
			renderKeyHint("home/end", "jump"),
		}
	}
	if len(t.sessions) > 0 {
		return []string{
			renderKeyHint("n", "new chat"),
			renderKeyHint("↑↓", "move"),
			renderKeyHint("enter", "detail"),
			positionIndicator(t.cursor, len(t.sessions)),
		}
	}
	return []string{renderKeyHint("n", "new chat")}
}

func (t *chatTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case chatDataMsg:
		t.sessions = msg.sessions
		t.loaded = true
		if t.cursor >= len(t.sessions) && len(t.sessions) > 0 {
			t.cursor = len(t.sessions) - 1
		}

	case chatDetailMsg:
		t.detail = msg.detail
		t.scroll = 0

	case tea.KeyMsg:
		if t.expanded {
			return t.updateDetail(msg)
		}
		return t.updateList(msg, svc)
	}
	return t, nil
}

func (t *chatTab) updateList(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Down):
		if t.cursor < len(t.sessions)-1 {
			t.cursor++
		}
	case key.Matches(msg, keys.Up):
		if t.cursor > 0 {
			t.cursor--
		}
	case key.Matches(msg, keys.Enter):
		if len(t.sessions) > 0 {
			t.expanded = true
			session := t.sessions[t.cursor]
			return t, func() tea.Msg {
				ctx := context.Background()
				detail, _ := svc.ChatSessionDetail(ctx, session.ID)
				return chatDetailMsg{detail: detail}
			}
		}
	case key.Matches(msg, keys.NewChat):
		t.message = "Chat session closed"
		cmd := exec.Command(svc.BinaryName(), "chat")
		return t, tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return chatDataMsg{sessions: t.sessions}
			}
			return loadChatData(svc)
		})
	}
	return t, nil
}

func (t *chatTab) updateDetail(msg tea.KeyMsg) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		t.expanded = false
	case key.Matches(msg, keys.Down):
		t.scroll++
	case key.Matches(msg, keys.Up):
		if t.scroll > 0 {
			t.scroll--
		}
	case msg.String() == "pgdown":
		t.scroll += 10
	case msg.String() == "pgup":
		t.scroll = maxInt(t.scroll-10, 0)
	case msg.String() == "home":
		t.scroll = 0
	case msg.String() == "end":
		t.scroll = 1 << 30
	}
	return t, nil
}

func (t *chatTab) View(width, height int) string {
	if !t.loaded {
		return styleMuted.Render("  Loading…")
	}

	if t.expanded {
		return t.viewDetail(width, height)
	}
	return t.viewList(width, height)
}

func (t *chatTab) viewList(width, height int) string {
	var b strings.Builder
	col := width - 4

	b.WriteString(renderPageHeader("Chat", "history and live entry point", width))

	if t.message != "" {
		b.WriteString("  ")
		b.WriteString(styleSuccess.Render(t.message))
		b.WriteString("\n\n")
	}

	if len(t.sessions) == 0 {
		b.WriteString(renderEmptyState("No chat sessions recorded yet", "Start a conversation when you want an interactive agent loop.", "n", "new chat"))
		return b.String()
	}

	// Column headers
	b.WriteString(renderTableHeader(width,
		padRight("", 3)+
			padRight("Date", 14)+"  "+
			padRight("Model", 30)+"  "+
			padRight("Turns", 7)+"  "+
			padRight("Duration", 10)+"  "+
			padRight("Exit", 15)))

	// Windowed rendering
	listHeight := height - 5
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := listWindow(t.cursor, len(t.sessions), listHeight)

	if start > 0 {
		b.WriteString("  ")
		b.WriteString(styleSubtle.Render(fmt.Sprintf("  ↑ %d more", start)))
		b.WriteString("\n")
	}

	for i := start; i < end; i++ {
		session := t.sessions[i]
		selected := i == t.cursor
		b.WriteString("  ")
		b.WriteString(t.renderSessionRow(session, col, selected))
		b.WriteString("\n")
	}

	if end < len(t.sessions) {
		b.WriteString("  ")
		b.WriteString(styleSubtle.Render(fmt.Sprintf("  ↓ %d more", len(t.sessions)-end)))
		b.WriteString("\n")
	}

	return b.String()
}

func (t *chatTab) renderSessionRow(session state.ChatSessionSummary, col int, selected bool) string {
	icon := styleMuted.Render("●")
	if !session.FinishedAt.IsZero() {
		icon = styleSuccess.Render("●")
	}
	if session.ExitReason == "interrupt" || session.ExitReason == "terminated" {
		icon = styleWarning.Render("●")
	}

	date := padRight(fmtTime(session.StartedAt), 14)
	model := padRight(truncate(session.PinnedModel, 30), 30)
	turns := padRight(fmt.Sprintf("%d", session.TurnCount), 7)

	dur := "—"
	if !session.StartedAt.IsZero() && !session.FinishedAt.IsZero() {
		dur = fmtDuration(session.FinishedAt.Sub(session.StartedAt))
	}
	dur = padRight(dur, 10)

	exit := padRight(truncate(session.ExitReason, 15), 15)

	row := fmt.Sprintf("%s  %s  %s  %s  %s  %s", icon, date, model, turns, dur, exit)
	return renderSelectableRow(row, selected)
}

func (t *chatTab) viewDetail(width, height int) string {
	session := t.detail.Session
	col := maxInt(width-4, 20)
	contentWidth := maxInt(width-8, 20)
	messagesByTurn := chatMessagesByTurn(t.detail.Messages)

	// Build all lines then apply scroll window.
	var lines []string

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s", styleSectionTitle.Render(fmt.Sprintf("Chat Session #%d", session.ID))))
	lines = append(lines, "")
	lines = append(lines, "  "+renderKeyValue("Model", session.PinnedModel))
	lines = append(lines, "  "+renderKeyValue("Provider", session.Provider))
	lines = append(lines, "  "+renderKeyValue("Started", fmtTime(session.StartedAt)))
	if !session.StartedAt.IsZero() && !session.FinishedAt.IsZero() {
		lines = append(lines, "  "+renderKeyValue("Duration", fmtDuration(session.FinishedAt.Sub(session.StartedAt))))
	}
	lines = append(lines, "  "+renderKeyValue("Turns", fmt.Sprintf("%d", session.TurnCount)))
	lines = append(lines, "  "+renderKeyValue("Exit", session.ExitReason))

	if len(t.detail.Turns) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+horizontalRule(col))
		lines = append(lines, "")

		for _, turn := range t.detail.Turns {
			header := fmt.Sprintf("  %s %s  %s  %s tokens",
				styleMuted.Render(fmt.Sprintf("#%d", turn.TurnIndex+1)),
				statusIcon(turn.Success),
				styleMuted.Render(fmtDurationMs(turn.LatencyMs)),
				styleMuted.Render(formatTokens(turn.TotalTokens)))
			lines = append(lines, header)

			meta := chatTurnMeta(turn)
			if meta != "" {
				lines = append(lines, "  "+styleMuted.Render(meta))
			}

			appendWrappedBlock(&lines, "  ", "You:", turn.UserInput, contentWidth, styleSuccess, styleBase)

			if output := strings.TrimSpace(turn.FinalOutput); output != "" {
				appendWrappedBlock(&lines, "  ", "AI:", output, contentWidth, styleSectionTitle, styleMuted)
			}

			if turn.ErrorMessage != "" {
				appendWrappedBlock(&lines, "  ", "Error:", turn.ErrorMessage, contentWidth, styleError, styleError)
			}

			appendVerificationLines(&lines, turn, contentWidth)
			appendToolOutcomeLines(&lines, t.detail.ToolsByTurnID[turn.ID], contentWidth)
			appendTranscriptToolLines(&lines, messagesByTurn[turn.ID], contentWidth)

			if turn.FinalOutput == "" && turn.ErrorMessage == "" && turn.VerificationStatus == "" && len(t.detail.ToolsByTurnID[turn.ID]) == 0 && len(chatToolMessages(messagesByTurn[turn.ID])) == 0 {
				lines = append(lines, "  "+styleSubtle.Render("No assistant output, verification, or tool detail was persisted for this turn."))
			}
			lines = append(lines, "")
		}
	}

	// Apply scroll window
	maxScroll := maxInt(len(lines)-height+2, 0)
	t.scroll = clamp(t.scroll, 0, maxScroll)

	var b strings.Builder
	end := minInt(t.scroll+height-1, len(lines))
	for i := t.scroll; i < end; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}

	if maxScroll > 0 {
		b.WriteString("  ")
		b.WriteString(scrollHints(t.scroll, end, len(lines)))
	}

	return b.String()
}

func chatTurnMeta(turn state.ChatTurn) string {
	var parts []string
	if turn.TaskClass != "" {
		parts = append(parts, fmt.Sprintf("task=%s", turn.TaskClass))
	}
	if turn.RequestedModel != "" {
		model := turn.RequestedModel
		if turn.ActualModel != "" && turn.ActualModel != turn.RequestedModel {
			model += " -> " + turn.ActualModel
		}
		parts = append(parts, "model="+model)
	}
	if turn.PromptTokens > 0 || turn.CompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("prompt=%s completion=%s", formatTokens(turn.PromptTokens), formatTokens(turn.CompletionTokens)))
	}
	return strings.Join(parts, "  ")
}

func appendVerificationLines(lines *[]string, turn state.ChatTurn, width int) {
	if turn.VerificationStatus == "" && turn.VerificationReason == "" && turn.VerificationMissingActions == "" && !turn.VerificationRepairTriggered {
		return
	}
	status := turn.VerificationStatus
	if status == "" {
		status = "unknown"
	}
	if turn.VerificationRepairTriggered {
		status += " repair-triggered"
	}
	*lines = append(*lines, "  "+styleMuted.Render("Verification: ")+styleBase.Render(status))
	appendWrappedBlock(lines, "    ", "Reason:", turn.VerificationReason, width-2, styleMuted, styleBase)
	appendWrappedBlock(lines, "    ", "Missing:", turn.VerificationMissingActions, width-2, styleWarning, styleBase)
}

func appendToolOutcomeLines(lines *[]string, tools []state.ToolOutcome, width int) {
	if len(tools) == 0 {
		return
	}
	*lines = append(*lines, "  "+styleMuted.Render(fmt.Sprintf("Tools: %d persisted outcome(s)", len(tools))))
	for i, tool := range tools {
		status := "ok"
		style := styleSuccess
		if tool.PolicyDenied {
			status = "denied"
			style = styleWarning
		} else if !tool.Success {
			status = "failed"
			style = styleError
		}
		title := fmt.Sprintf("%d. %s %s  %s", i+1, tool.ToolName, style.Render(status), fmtDurationMs(tool.DurationMs))
		*lines = append(*lines, "    "+title)
		appendWrappedBlock(lines, "      ", "Command:", firstNonEmptyText(tool.Command, tool.Arguments), width-6, styleMuted, styleBase)
		appendWrappedBlock(lines, "      ", "Args:", tool.Arguments, width-6, styleMuted, styleBase)
		appendWrappedBlock(lines, "      ", "Error:", tool.ErrorMessage, width-6, styleError, styleError)
		if tool.DenialClass != "" {
			*lines = append(*lines, "      "+styleWarning.Render("Denial: ")+styleBase.Render(tool.DenialClass))
		}
	}
}

func appendTranscriptToolLines(lines *[]string, messages []state.ChatMessage, width int) {
	toolMessages := chatToolMessages(messages)
	if len(toolMessages) == 0 {
		return
	}
	*lines = append(*lines, "  "+styleMuted.Render("Transcript tool evidence:"))
	for _, message := range toolMessages {
		if message.ToolName != "" {
			appendWrappedBlock(lines, "    ", "Tool call:", message.ToolName+" "+message.ToolArguments, width-4, styleMuted, styleBase)
		}
		if message.ToolCallsJSON != "" {
			appendWrappedBlock(lines, "    ", "Tool calls:", message.ToolCallsJSON, width-4, styleMuted, styleBase)
		}
		if message.Role == "tool" && message.Content != "" {
			appendWrappedBlock(lines, "    ", "Tool result:", message.Content, width-4, styleMuted, styleBase)
		}
	}
}

func chatMessagesByTurn(messages []state.ChatMessage) map[int64][]state.ChatMessage {
	out := make(map[int64][]state.ChatMessage)
	for _, message := range messages {
		out[message.TurnID] = append(out[message.TurnID], message)
	}
	return out
}

func chatToolMessages(messages []state.ChatMessage) []state.ChatMessage {
	var out []state.ChatMessage
	for _, message := range messages {
		if message.Role == "tool" || message.ToolName != "" || message.ToolArguments != "" || message.ToolCallsJSON != "" {
			out = append(out, message)
		}
	}
	return out
}

func appendWrappedBlock(lines *[]string, indent, label, text string, width int, labelStyle, bodyStyle lipgloss.Style) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	labelWidth := len(label) + 1
	bodyWidth := maxInt(width-labelWidth, 12)
	first := true
	for _, raw := range strings.Split(text, "\n") {
		wrapped := wrapText(raw, bodyWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, line := range wrapped {
			if first {
				*lines = append(*lines, indent+labelStyle.Render(label+" ")+bodyStyle.Render(line))
				first = false
				continue
			}
			*lines = append(*lines, indent+strings.Repeat(" ", labelWidth)+bodyStyle.Render(line))
		}
	}
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
