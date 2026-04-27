package tui

import (
	"context"
	"fmt"
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
}

func newChatTab() tabModel {
	return &chatTab{}
}

func (t *chatTab) Init(svc *Service) tea.Cmd {
	return func() tea.Msg { return loadChatData(svc) }
}

func (t *chatTab) Consuming() bool { return false }

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
	b.WriteString(renderKeyHint("enter", "view turns"))
	b.WriteString(styleMuted.Render("  "))
	b.WriteString(renderKeyHint("↑/↓", "navigate"))
	b.WriteString("\n\n")

	if len(t.sessions) == 0 {
		b.WriteString("  ")
		b.WriteString(styleMuted.Render("No chat sessions recorded yet"))
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

	visible := minInt(len(t.sessions), height-6)
	for i := 0; i < visible; i++ {
		session := t.sessions[i]
		selected := i == t.cursor
		b.WriteString("  ")
		b.WriteString(t.renderSessionRow(session, col, selected))
		b.WriteString("\n")
	}

	return b.String()
}

func (t *chatTab) renderSessionRow(session state.ChatSessionSummary, col int, selected bool) string {
	active := !session.FinishedAt.IsZero()
	icon := styleMuted.Render("●")
	if active {
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
		return styleSelectedRow.Render(row)
	}
	return row
}

func (t *chatTab) viewDetail(width, height int) string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyHint("esc", "back to list"))
	b.WriteString(styleMuted.Render("  "))
	b.WriteString(renderKeyHint("↑/↓", "scroll"))
	b.WriteString("\n\n")

	session := t.detail.Session
	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render(fmt.Sprintf("Chat Session #%d", session.ID)))
	b.WriteString("\n\n")

	b.WriteString("  ")
	b.WriteString(renderKeyValue("Model", session.PinnedModel))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Provider", session.Provider))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Started", fmtTime(session.StartedAt)))
	b.WriteString("\n")
	if !session.StartedAt.IsZero() && !session.FinishedAt.IsZero() {
		b.WriteString("  ")
		b.WriteString(renderKeyValue("Duration", fmtDuration(session.FinishedAt.Sub(session.StartedAt))))
		b.WriteString("\n")
	}
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Turns", fmt.Sprintf("%d", session.TurnCount)))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(renderKeyValue("Exit", session.ExitReason))
	b.WriteString("\n")

	// Turn-by-turn transcript
	if len(t.detail.Turns) > 0 {
		b.WriteString("\n  ")
		b.WriteString(horizontalRule(width - 4))
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(styleSectionTitle.Render("Turns"))
		b.WriteString("\n\n")

		// Apply scroll offset
		start := clamp(t.scroll, 0, maxInt(len(t.detail.Turns)-1, 0))
		end := minInt(start+height-15, len(t.detail.Turns))

		for i := start; i < end; i++ {
			turn := t.detail.Turns[i]
			b.WriteString("  ")
			b.WriteString(styleMuted.Render(fmt.Sprintf("#%d ", turn.TurnIndex+1)))
			b.WriteString(statusIcon(turn.Success))
			b.WriteString("  ")
			b.WriteString(styleMuted.Render(fmtDurationMs(turn.LatencyMs)))
			b.WriteString("  ")
			b.WriteString(styleMuted.Render(formatTokens(turn.TotalTokens) + " tokens"))
			b.WriteString("\n")

			// User input
			b.WriteString("  ")
			b.WriteString(styleSuccess.Render("You: "))
			b.WriteString(styleBase.Render(truncate(turn.UserInput, width-12)))
			b.WriteString("\n")

			// Assistant output
			if output := strings.TrimSpace(turn.FinalOutput); output != "" {
				lines := strings.Split(output, "\n")
				maxLines := minInt(len(lines), 3)
				for j, line := range lines[:maxLines] {
					b.WriteString("  ")
					if j == 0 {
						b.WriteString(styleSectionTitle.Render("AI: "))
					} else {
						b.WriteString("    ")
					}
					b.WriteString(styleMuted.Render(truncate(line, width-12)))
					b.WriteString("\n")
				}
				if len(lines) > maxLines {
					b.WriteString("      ")
					b.WriteString(styleSubtle.Render(fmt.Sprintf("… %d more lines", len(lines)-maxLines)))
					b.WriteString("\n")
				}
			}

			if turn.ErrorMessage != "" {
				b.WriteString("  ")
				b.WriteString(styleError.Render("Error: " + truncate(turn.ErrorMessage, width-14)))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}
