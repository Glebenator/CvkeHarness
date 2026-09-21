package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/coolcake/cvkeharness/internal/setupflow"
)

func pickerFixture() (*configTab, *Service) {
	m := recoveryModel()
	t := m.tabs[tabConfig].(*configTab)
	t.cursor = 1
	t.cfg.DefaultModel = "configured-model"
	t.openModelPicker()
	t.acceptModels(setupflow.ModelResult{Source: "codex-cache", Live: true, Items: []setupflow.ModelOption{
		{ID: "model-a", Description: "Fast coding"}, {ID: "model-b", Description: "Deep reasoning"},
	}})
	return t, m.svc
}

func TestModelPickerSearchChooseAndCancel(t *testing.T) {
	tab, svc := pickerFixture()
	tab.Init(svc)
	if tab.modelPicker == nil || tab.cfg.DefaultModel != "configured-model" {
		t.Fatal("refresh discarded picker")
	}
	tab.updateModelPicker(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("deep")})
	if got := tab.filteredModels(); len(got) != 2 || got[0].ID != "model-b" {
		t.Fatalf("search: %+v", got)
	}
	tab.updateModelPicker(tea.KeyMsg{Type: tea.KeyEnter})
	if tab.cfg.DefaultModel != "model-b" || !tab.dirty || tab.modelPicker != nil {
		t.Fatal("selection not staged")
	}
	if svc.Config().DefaultModel == "model-b" {
		t.Fatal("selection saved prematurely")
	}
	tab.openModelPicker()
	tab.updateModelPicker(tea.KeyMsg{Type: tea.KeyEsc})
	if tab.cfg.DefaultModel != "model-b" {
		t.Fatal("cancel changed model")
	}
}

func TestModelPickerCustomAndLateResults(t *testing.T) {
	tab, svc := pickerFixture()
	old := tab.modelEpoch
	tab.updateModelPicker(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("unlisted")})
	tab.updateModelPicker(tea.KeyMsg{Type: tea.KeyEnter})
	if !tab.editing || tab.input.Value() != "configured-model" {
		t.Fatal("custom editor not opened")
	}
	tab.input.SetValue("custom-id")
	tab.updateEditor(tea.KeyMsg{Type: tea.KeyEnter})
	tab.openModelPicker()
	tab.Update(configModelsMsg{epoch: old, result: setupflow.ModelResult{Source: "old"}}, svc, 80, 20)
	if !tab.modelPicker.loading {
		t.Fatal("accepted old catalog")
	}
	if tab.cfg.DefaultModel != "custom-id" {
		t.Fatal("custom ID lost")
	}
}

func TestModelPickerViewport(t *testing.T) {
	tab, _ := pickerFixture()
	var items []setupflow.ModelOption
	for i := 0; i < 50; i++ {
		items = append(items, setupflow.ModelOption{ID: fmt.Sprintf("model-%02d", i), Description: strings.Repeat("Useful description ", 20)})
	}
	tab.acceptModels(setupflow.ModelResult{Items: items, Source: "codex-cache"})
	for _, size := range [][2]int{{80, 17}, {40, 12}, {120, 30}} {
		tab.modelPicker.cursor = 45
		view := tab.View(size[0], size[1])
		if len(strings.Split(view, "\n")) > size[1] {
			t.Fatalf("height overflow at %v", size)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("width overflow at %v", size)
			}
		}
		if !strings.Contains(view, "model-44") {
			t.Fatal("selection not visible")
		}
	}
}
