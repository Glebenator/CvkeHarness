// Package modelui provides reusable, draft-only model and connection editors.
package modelui

import (
	"strings"
	"sync/atomic"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/coolcake/cvkeharness/config"
)

var nextID atomic.Uint64
var (
	bodyStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#c6c0b9", Light: "#49413b"})
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#a69c92", Light: "#71665c"})
	titleStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#eee8df", Light: "#302923"}).Bold(true)
	focusStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#d6ad7b", Light: "#845321"}).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#dea08a", Light: "#a0442f"})
)

func IsMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case catalogMsg, PickerResultMsg, ConnectionResultMsg, inputMsg:
		return true
	}
	return false
}

// Input effects must return to the editor that requested them, even if its
// parent workspace is hidden when a clipboard or cursor command completes.
type inputMsg struct {
	id, epoch uint64
	msg       tea.Msg
}

func scopedInputCmd(cmd tea.Cmd, id, epoch uint64) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			commands := make([]tea.Cmd, 0, len(batch))
			for _, child := range batch {
				commands = append(commands, scopedInputCmd(child, id, epoch))
			}
			if next := tea.Batch(commands...); next != nil {
				return next()
			}
			return nil
		}
		if msg == nil {
			return nil
		}
		return inputMsg{id: id, epoch: epoch, msg: msg}
	}
}

func RoleLabel(role config.ModelRole) string {
	switch role {
	case config.RolePrimary:
		return "Primary"
	case config.RoleSafetyJudge:
		return "Safety judge"
	case config.RoleClassifier:
		return "Classifier"
	case config.RoleVerifier:
		return "Verifier"
	case config.RolePlanning:
		return "Planning"
	case config.RoleExecution:
		return "Execution"
	case config.RoleCuration:
		return "Curation"
	}
	return string(role)
}

// ConnectionUsage includes incomplete role bindings so repair flows cannot
// remove or repurpose the connection a role is still configured to use.
func ConnectionUsage(cfg *config.Config, id string) []string {
	if cfg == nil || id == "" {
		return nil
	}
	var labels []string
	for _, role := range config.ModelRoleList() {
		seen := map[config.ModelRole]bool{}
		for current := role; !seen[current]; {
			seen[current] = true
			binding := cfg.RoleBinding(current)
			if binding.Connection == id {
				labels = append(labels, RoleLabel(role))
				break
			}
			if binding.Inherit == "" {
				break
			}
			current = binding.Inherit
		}
	}
	return labels
}

func clean(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(value))
}
func oneLine(value string) string { return strings.Join(strings.Fields(clean(value)), " ") }
func clipped(value string, width int) string {
	return ansi.Truncate(oneLine(value), max(width, 1), "…")
}

func wrapped(value string, width int) []string {
	return strings.Split(ansi.Hardwrap(ansi.Wrap(value, max(width, 1), ""), max(width, 1), true), "\n")
}
func selectedRow(label string, selected bool, width int) string {
	if selected {
		return focusStyle.Render("› " + clipped(label, width-2))
	}
	return bodyStyle.Render("  " + clipped(label, width-2))
}
func window(cursor, total, slots int) (int, int) {
	slots = max(slots, 1)
	start := max(0, cursor-slots+1)
	end := min(total, start+slots)
	return start, end
}
func fit(lines []string, width, height int) string {
	width = max(1, width)
	height = max(1, height)
	var physical []string
	for _, line := range lines {
		physical = append(physical, strings.Split(ansi.Hardwrap(line, width, true), "\n")...)
	}
	if len(physical) > height {
		physical = physical[:height]
	}
	return strings.Join(physical, "\n")
}
