package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
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

	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render("Chat"))
	b.WriteString("  ")
	b.WriteString(styleMuted.Render("history and live entry point"))
	b.WriteString("\n\n")

	if t.message != "" {
		b.WriteString("  ")
		b.WriteString(styleSuccess.Render(t.message))
		b.WriteString("\n\n")
	}

	if len(t.sessions) == 0 {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("No chat sessions recorded yet"))
		b.WriteString("\n\n  ")
		b.WriteString(styleAccent.Render("Press n to start a chat session."))
		b.WriteString("\n")
		return b.String()
	}

	// Column headers
	b.WriteString("  ")
	b.WriteString(styleMuted.Render(
		padRight("", 3) +
			padRight("Date", 14) + "  " +
			padRight("Model", 30) + "  " +
			padRight("Turns", 7) + "  " +
			padRight("Duration", 10) + "  " +
			padRight("Exit", 15)))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(horizontalRule(col))
	b.WriteString("\n")

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
	if selected {
		return styleAccent.Render("▸ ") + styleSelectedRow.Render(row)
	}
	return "  " + row
}

func (t *chatTab) viewDetail(width, height int) string {
	session := t.detail.Session

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
		lines = append(lines, "  "+horizontalRule(width-4))
		lines = append(lines, "")

		for _, turn := range t.detail.Turns {
			header := fmt.Sprintf("  %s %s  %s  %s tokens",
				styleMuted.Render(fmt.Sprintf("#%d", turn.TurnIndex+1)),
				statusIcon(turn.Success),
				styleMuted.Render(fmtDurationMs(turn.LatencyMs)),
				styleMuted.Render(formatTokens(turn.TotalTokens)))
			lines = append(lines, header)

			lines = append(lines, "  "+styleSuccess.Render("You: ")+styleBase.Render(truncate(turn.UserInput, width-12)))

			if output := strings.TrimSpace(turn.FinalOutput); output != "" {
				outLines := strings.Split(output, "\n")
				maxShow := minInt(len(outLines), 4)
				for j, line := range outLines[:maxShow] {
					prefix := "    "
					if j == 0 {
						prefix = "  " + styleSectionTitle.Render("AI: ")
					}
					lines = append(lines, prefix+styleMuted.Render(truncate(line, width-12)))
				}
				if len(outLines) > maxShow {
					lines = append(lines, fmt.Sprintf("      %s", styleSubtle.Render(fmt.Sprintf("… %d more lines", len(outLines)-maxShow))))
				}
			}

			if turn.ErrorMessage != "" {
				lines = append(lines, "  "+styleError.Render("Error: "+truncate(turn.ErrorMessage, width-14)))
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
