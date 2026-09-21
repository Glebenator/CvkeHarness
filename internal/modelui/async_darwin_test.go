package modelui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/config"
)

// Exercise the actual private textinput paste message without reading or
// changing the operator's clipboard. The platform clipboard command is a stub.
func TestClipboardCompletionIsRoutableAndBoundToItsField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pbpaste"), []byte("#!/bin/sh\nprintf '%s' 'synthetic-paste'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	p := NewPicker(pickerConfig(), config.RolePrimary)
	p.cursor = len(p.rows()) - 1
	cmd := p.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	if cmd == nil {
		t.Fatal("Ctrl+V returned no clipboard command")
	}
	completion := cmd()
	if !IsMessage(completion) {
		t.Fatal("Settings cannot route the clipboard completion to its child")
	}
	p.Update(completion)
	if p.search.Value() != "synthetic-paste" || p.cursor != 0 || !p.touched {
		t.Fatal("paste did not update the search and reset filtered selection")
	}

	e := NewConnectionEditor("local", config.Connection{Name: "Local", Provider: "lmstudio"}, nil)
	e.cursor = 3 // API key
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	completion = e.Update(tea.KeyMsg{Type: tea.KeyCtrlV})()
	if !IsMessage(completion) {
		t.Fatal("Settings cannot route credential clipboard completion")
	}
	e.Update(completion)
	if e.input.Value() != "synthetic-paste" || !e.Dirty() {
		t.Fatal("credential clipboard completion was lost")
	}
	if strings.Contains(e.View(80, 20), "synthetic-paste") {
		t.Fatal("pasted credential was rendered in plaintext")
	}
	e.Update(tea.KeyMsg{Type: tea.KeyEsc})
	e.cursor = 0
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	e.Update(completion)
	if e.input.Value() != "Local" {
		t.Fatal("old credential paste crossed into a later Name field")
	}
}
