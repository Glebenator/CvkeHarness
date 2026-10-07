package modelui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/glebenator/cvkeharness/config"
)

func TestInputCommandsPreserveBatchExecutionAndParentRouting(t *testing.T) {
	t.Parallel()
	cmd := scopedInputCmd(tea.Batch(
		func() tea.Msg { return runeKey("first") },
		func() tea.Msg { return runeKey("second") },
	), 10, 3)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("input wrapper swallowed a nested command batch")
	}
	for _, child := range batch {
		msg := child()
		if !IsMessage(msg) {
			t.Fatal("parent cannot route asynchronous input completion")
		}
		input := msg.(inputMsg)
		if input.id != 10 || input.epoch != 3 {
			t.Fatal("input completion lost its child and field generation")
		}
	}
	if msg := scopedInputCmd(func() tea.Msg { return tea.BatchMsg{} }, 10, 3)(); msg != nil {
		t.Fatal("empty input batch should not produce a message")
	}
}

func TestPickerRejectsInputFromPreviousModeAndReopenedChild(t *testing.T) {
	t.Parallel()
	p := NewPicker(pickerConfig(), config.RolePrimary)
	delayed := p.inputCmd(func() tea.Msg { return runeKey("old query") })
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	p.Update(delayed())
	if p.connectionSearch.Value() != "" {
		t.Fatal("late model search input entered the connection chooser")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	p.Update(delayed())
	if p.search.Value() != "" {
		t.Fatal("returning to the same mode accepted its previous generation")
	}
	current := p.inputCmd(func() tea.Msg { return runeKey("new query") })()
	reopened := NewPicker(pickerConfig(), config.RolePrimary)
	reopened.Update(current)
	if reopened.search.Value() != "" {
		t.Fatal("closed picker completion entered a newly opened picker")
	}
	p.Update(current)
	if p.search.Value() != "new query" {
		t.Fatal("current picker did not receive its asynchronous input completion")
	}
}

func TestConnectionEditorRejectsInputFromPreviousFieldAndChild(t *testing.T) {
	t.Parallel()
	e := NewConnectionEditor("local", config.Connection{Name: "Local", Provider: "lmstudio"}, nil)
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	delayed := e.inputCmd(func() tea.Msg { return runeKey("old name") })
	e.Update(tea.KeyMsg{Type: tea.KeyEsc})
	e.cursor = 2 // Endpoint
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	e.Update(delayed())
	if e.input.Value() != "" {
		t.Fatal("late name input entered the endpoint field")
	}
	current := e.inputCmd(func() tea.Msg { return runeKey("https://local.test/v1") })()
	reopened := NewConnectionEditor("local", e.original, nil)
	reopened.cursor = 2
	reopened.Update(tea.KeyMsg{Type: tea.KeyEnter})
	reopened.Update(current)
	if reopened.input.Value() != "" {
		t.Fatal("closed editor completion entered its replacement")
	}
	e.Update(current)
	if e.input.Value() != "https://local.test/v1" {
		t.Fatal("current field did not receive its asynchronous input completion")
	}
}
