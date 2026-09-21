package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/internal/setupflow"
)

const customModelID = "[ custom model ]"

type configModelsMsg struct {
	epoch  uint64
	result setupflow.ModelResult
}
type configModelPicker struct {
	search  textinput.Model
	result  setupflow.ModelResult
	items   []setupflow.ModelOption
	cursor  int
	loading bool
}

func (t *configTab) openModelPicker() tea.Cmd {
	search := textinput.New()
	search.Placeholder = "Type to filter models"
	search.CharLimit = 128
	search.Focus()
	t.modelPicker = &configModelPicker{search: search}
	t.editIdx = t.cursor
	t.message, t.saveErr = "", ""
	return t.loadModels()
}

func (t *configTab) loadModels() tea.Cmd {
	t.modelEpoch++
	epoch := t.modelEpoch
	cfg := cloneTUIConfig(t.cfg)
	t.modelPicker.loading = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return configModelsMsg{epoch: epoch, result: setupflow.FetchModels(ctx, cfg)}
	}
}

func (t *configTab) acceptModels(result setupflow.ModelResult) {
	p := t.modelPicker
	p.loading, p.result = false, result
	p.items = nil
	current := t.fields[t.editIdx].Get(t.cfg)
	found := false
	for _, item := range result.Items {
		if item.ID == customModelID {
			continue
		}
		p.items = append(p.items, item)
		found = found || item.ID == current
	}
	if !found && current != "" {
		p.items = append([]setupflow.ModelOption{{ID: current, Description: "Configured model; not listed in this catalog"}}, p.items...)
	}
	p.cursor = 0
	for i, item := range t.filteredModels() {
		if item.ID == current {
			p.cursor = i
			break
		}
	}
}

func (t *configTab) filteredModels() []setupflow.ModelOption {
	p := t.modelPicker
	query := strings.ToLower(strings.TrimSpace(p.search.Value()))
	var items []setupflow.ModelOption
	for _, item := range p.items {
		if strings.Contains(strings.ToLower(item.ID+" "+item.Description), query) {
			items = append(items, item)
		}
	}
	return append(items, setupflow.ModelOption{ID: customModelID, Description: "Enter an exact model ID manually"})
}

func (t *configTab) updateModelPicker(msg tea.KeyMsg) (tabModel, tea.Cmd) {
	p := t.modelPicker
	items := t.filteredModels()
	switch msg.String() {
	case "esc":
		t.modelPicker = nil
		return t, nil
	case "ctrl+r":
		return t, t.loadModels()
	case "up":
		p.cursor = maxInt(0, p.cursor-1)
		return t, nil
	case "down":
		p.cursor = minInt(len(items)-1, p.cursor+1)
		return t, nil
	case "enter":
		item := items[p.cursor]
		t.modelPicker = nil
		if item.ID == customModelID {
			t.beginEdit()
			return t, nil
		}
		field := t.fields[t.editIdx]
		if field.Get(t.cfg) != item.ID {
			field.Set(t.cfg, item.ID)
			t.dirty = true
		}
		t.message = field.Label + " set to " + item.ID + "; press s to save"
		return t, nil
	}
	before := p.search.Value()
	var cmd tea.Cmd
	p.search, cmd = p.search.Update(msg)
	if before != p.search.Value() {
		p.cursor = 0
	}
	return t, cmd
}

func (t *configTab) viewModelPicker(width, height int) string {
	p := t.modelPicker
	col := maxInt(1, width-4)
	var lines []string
	add := func(s string) { lines = append(lines, "  "+truncate(s, col)) }
	add(styleBright.Render("Default Model · " + t.cfg.Provider))
	add(styleMuted.Render("Choose a model, then press s in Settings to save."))
	source := "Loading model catalog…"
	if !p.loading {
		source = "Catalog: " + p.result.Source
		if !p.result.Live {
			source += " · cached / fallback"
		}
		if !p.result.Timestamp.IsZero() {
			source += " · " + p.result.Timestamp.Local().Format("Jan 2 15:04")
		}
	}
	add(styleMuted.Render(source))
	if !p.loading && !p.result.Live && p.result.Message != "" {
		add(styleWarning.Render(p.result.Message))
	}
	p.search.Width = maxInt(1, col-3)
	add(p.search.View())
	items := t.filteredModels()
	slots := maxInt(1, height-len(lines)-4)
	start, end := listWindow(p.cursor, len(items), slots)
	current := t.fields[t.editIdx].Get(t.cfg)
	for i := start; i < end; i++ {
		label := items[i].ID
		if label == current {
			label += "  ✓ current"
		}
		if width >= 80 {
			label = padRight(truncate(label, 34), 34) + "  " + items[i].Description
		}
		add(renderSelectableRow(truncate(label, maxInt(1, col-2)), i == p.cursor))
	}
	add(styleMuted.Render(fmt.Sprintf("%d models · %d/%d", len(items)-1, p.cursor+1, len(items))))
	description := items[p.cursor].Description
	for _, line := range wrapText(description, col) {
		if len(lines) >= height {
			break
		}
		add(styleMuted.Render(line))
	}
	return strings.Join(lines, "\n")
}
