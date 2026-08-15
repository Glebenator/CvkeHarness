package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Quit      key.Binding
	Tab       key.Binding
	ShiftTab  key.Binding
	Left      key.Binding
	Right     key.Binding
	Tab1      key.Binding
	Tab2      key.Binding
	Tab3      key.Binding
	Tab4      key.Binding
	Tab5      key.Binding
	Up        key.Binding
	Down      key.Binding
	Enter     key.Binding
	Back      key.Binding
	RunJob    key.Binding
	PauseJob  key.Binding
	NewJob    key.Binding
	DeleteJob key.Binding
	Help      key.Binding
}

var keys = keyMap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next tab"),
	),
	ShiftTab: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "prev tab"),
	),
	Left: key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←", "prev tab"),
	),
	Right: key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("→", "next tab"),
	),
	Tab1: key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "overview")),
	Tab2: key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "jobs")),
	Tab3: key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "runs")),
	Tab4: key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "chat")),
	Tab5: key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "settings")),
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	RunJob: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "run job"),
	),
	PauseJob: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "pause/resume"),
	),
	NewJob: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new job"),
	),
	DeleteJob: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "delete job"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
}
