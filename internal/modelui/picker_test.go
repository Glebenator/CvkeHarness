package modelui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/modelcatalog"
)

func pickerConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Connections = map[string]config.Connection{"laptop": {Name: "Laptop", Provider: "lmstudio", BaseURL: "http://laptop.test/v1"}, "server": {Name: "Server", Provider: "lmstudio", BaseURL: "http://server.test/v1"}}
	cfg.Models.Primary = config.ModelBinding{Connection: "laptop", Model: "vendor/current"}
	cfg.Models.SafetyJudge = config.ModelBinding{Inherit: config.RolePrimary}
	return cfg
}
func runeKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func fakeCatalog(_ context.Context, c config.Connection) modelcatalog.ModelResult {
	return modelcatalog.ModelResult{Live: true, Source: c.BaseURL, Items: []modelcatalog.ModelOption{{ID: "native/alpha", Description: "Alpha"}, {ID: "native/beta", Description: "Beta"}}}
}

func TestPickerSearchCustomAndCancellationPreserveCaller(t *testing.T) {
	t.Parallel()
	cfg := pickerConfig()
	before := cfg.Clone()
	p := NewPicker(cfg, config.RolePrimary)
	p.loader = fakeCatalog
	p.Update(p.load()())
	p.Update(runeKey("vendor/new-native"))
	if p.search.Value() != "vendor/new-native" {
		t.Fatal("n/p keystrokes failed to reach search")
	}
	p.cursor = len(p.rows()) - 1
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.mode != "custom" || p.custom.Value() != "vendor/new-native" {
		t.Fatal("custom ID did not retain search query")
	}
	result := p.Update(tea.KeyMsg{Type: tea.KeyEnter})().(PickerResultMsg)
	if result.Binding.Model != "vendor/new-native" || result.Binding.Connection != "laptop" || result.Cancelled {
		t.Fatalf("bad custom selection: %+v", result)
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Fatal("picker mutated caller's configuration")
	}
	p = NewPicker(cfg, config.RolePrimary)
	result = p.Update(tea.KeyMsg{Type: tea.KeyEsc})().(PickerResultMsg)
	if !result.Cancelled || result.Binding != cfg.Models.Primary {
		t.Fatal("cancel did not preserve original binding")
	}
}

func TestPickerIgnoresStaleConnectionAndReopenedResponses(t *testing.T) {
	t.Parallel()
	p := NewPicker(pickerConfig(), config.RolePrimary)
	p.loader = fakeCatalog
	old := p.load()()
	p.connection = "server"
	fresh := p.load()()
	p.Update(old)
	if !p.loading || len(p.result.Items) != 0 {
		t.Fatal("old connection response replaced current catalog")
	}
	p.Update(fresh)
	if p.loading || p.result.Source != "http://server.test/v1" {
		t.Fatal("fresh connection response missing")
	}
	reopened := NewPicker(pickerConfig(), config.RolePrimary)
	reopened.Update(fresh)
	if len(reopened.result.Items) != 0 {
		t.Fatal("closed picker response contaminated reopened picker")
	}
	if !IsMessage(fresh) || !IsMessage(PickerResultMsg{}) || !IsMessage(ConnectionResultMsg{}) {
		t.Fatal("parent cannot route child messages")
	}
}

func TestPickerSameComponentSupportsEveryRoleAndExplicitInheritance(t *testing.T) {
	t.Parallel()
	for _, role := range config.ModelRoleList() {
		p := NewPicker(pickerConfig(), role)
		p.loader = fakeCatalog
		p.Update(p.load()())
		if !strings.Contains(ansi.Strip(p.View(80, 24)), "Choose "+RoleLabel(role)+" model") {
			t.Fatalf("wrong contextual title for %s", role)
		}
		if role != config.RolePrimary {
			p.cursor = 0
			result := p.Update(tea.KeyMsg{Type: tea.KeyEnter})().(PickerResultMsg)
			if result.Binding.Inherit != config.RolePrimary {
				t.Fatalf("role %s lost explicit inheritance", role)
			}
		}
	}
}

func TestPickerUsesSelectedConnectionWithoutModelAndKeepsCurrentMissingID(t *testing.T) {
	t.Parallel()
	cfg := pickerConfig()
	cfg.Models.Primary = config.ModelBinding{Connection: "server"}
	p := NewPicker(cfg, config.RolePrimary)
	if p.connection != "server" {
		t.Fatal("incomplete role binding lost explicit connection")
	}
	p = NewPicker(pickerConfig(), config.RolePrimary)
	p.loader = fakeCatalog
	p.Update(p.load()())
	found := false
	for _, row := range p.rows() {
		found = found || (row.model == "vendor/current" && strings.Contains(row.description, "not listed"))
	}
	if !found {
		t.Fatal("configured custom model disappeared from picker")
	}
}

func TestPickerConnectionChooserAndNarrowRendering(t *testing.T) {
	t.Parallel()
	p := NewPicker(pickerConfig(), config.RolePrimary)
	p.loader = fakeCatalog
	p.Update(p.load()())
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	p.Update(runeKey("Server"))
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.connection != "server" || p.mode != "" || !p.loading {
		t.Fatal("connection chooser did not reload the selected catalog")
	}
	for _, width := range []int{20, 40, 80, 120} {
		for _, height := range []int{4, 12, 24} {
			view := p.View(width, height)
			lines := strings.Split(view, "\n")
			if len(lines) > height {
				t.Fatal("picker exceeds height")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Fatal("picker exceeds width")
				}
			}
		}
	}
}

func TestPickerInitialSelectionAndFooterRemainUsable(t *testing.T) {
	t.Parallel()
	cfg := pickerConfig()
	cfg.Models.Primary.Model = "native/beta"
	p := NewPicker(cfg, config.RolePrimary)
	p.loader = fakeCatalog
	p.Update(p.load()())
	if p.rows()[p.cursor].model != "native/beta" {
		t.Fatal("initial catalog did not select current model")
	}
	for _, width := range []int{40, 76, 80} {
		view := ansi.Strip(p.View(width, 22))
		for _, key := range []string{"Ctrl+K", "Ctrl+R", "Esc"} {
			if !strings.Contains(view, key) {
				t.Fatalf("%d-column picker lost %s hint: %s", width, key, view)
			}
		}
	}
	p = NewPicker(cfg, config.RolePrimary)
	p.loader = fakeCatalog
	response := p.load()()
	p.Update(runeKey("alpha"))
	p.Update(response)
	if p.cursor != 0 || p.rows()[0].model != "native/alpha" {
		t.Fatal("late catalog stole manually searched selection")
	}
}

func TestPickerDirtyIgnoresSearchButProtectsCustomChoice(t *testing.T) {
	t.Parallel()
	p := NewPicker(pickerConfig(), config.RoleSafetyJudge)
	p.Update(runeKey("vendor/new-model"))
	if p.Dirty() {
		t.Fatal("filtering the catalog should not count as a pending assignment")
	}
	p.cursor = len(p.rows()) - 1
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.Dirty() {
		t.Fatal("custom model draft was not protected")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.Dirty() {
		t.Fatal("returning to the search should discard custom model draft state")
	}
}
