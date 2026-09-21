package modelui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/coolcake/cvkeharness/config"
)

func TestConnectionEditorKeepsStableIDsAndNeverShowsKeyFragments(t *testing.T) {
	t.Parallel()
	connection := config.Connection{Name: "Hosted", Provider: "openai", APIKey: "secret-key-never-visible"}
	e := NewConnectionEditor("hosted", connection, nil)
	if strings.Contains(e.View(80, 24), "secret") {
		t.Fatal("connection list exposed key")
	}
	for i, field := range e.fields() {
		if field == "API key" {
			e.cursor = i
		}
	}
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if strings.Contains(e.View(80, 24), "secret") {
		t.Fatal("password editor exposed key")
	}
	e.Update(tea.KeyMsg{Type: tea.KeyEsc})
	e.draft.Name = "Renamed"
	e.cursor = len(e.fields()) - 1
	result := e.Update(tea.KeyMsg{Type: tea.KeyEnter})().(ConnectionResultMsg)
	if result.ID != "hosted" || result.OriginalID != "hosted" || result.Connection.Name != "Renamed" {
		t.Fatalf("rename broke saved identity: %+v", result)
	}
	if connection.Name != "Hosted" {
		t.Fatal("editor mutated caller's connection")
	}
}

func TestConnectionProviderChooserIsExplicitAndClearsOldCredentials(t *testing.T) {
	t.Parallel()
	e := NewConnectionEditor("hosted", config.Connection{Name: "Hosted", Provider: "openai", APIKey: "old-key", BaseURL: "https://old.test/v1"}, nil)
	e.cursor = 1
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.mode != "provider" || e.draft.Provider != "openai" {
		t.Fatal("opening provider field silently cycled provider")
	}
	e.Update(tea.KeyMsg{Type: tea.KeyDown})
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.draft.Provider != "lmstudio" || e.draft.APIKey != "" || e.draft.BaseURL != "" {
		t.Fatal("new provider retained previous provider's connection credentials")
	}
	result := e.Update(tea.KeyMsg{Type: tea.KeyEsc})().(ConnectionResultMsg)
	if !result.Cancelled {
		t.Fatal("outer Escape must cancel editor")
	}
}

func TestConnectionNamesProduceUniqueIDsAndRejectUnsafeURLs(t *testing.T) {
	t.Parallel()
	e := NewConnectionEditor("", config.Connection{Name: "Office Mac", Provider: "lmstudio", BaseURL: "http://office.test/v1"}, []string{"office-mac", "office-mac-2"})
	e.cursor = len(e.fields()) - 1
	result := e.Update(tea.KeyMsg{Type: tea.KeyEnter})().(ConnectionResultMsg)
	if result.ID != "office-mac-3" {
		t.Fatalf("connection overwrote an existing identity: %s", result.ID)
	}
	e.draft.BaseURL = "https://user:secret@example.test/v1"
	if cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || e.err == "" {
		t.Fatal("embedded URL credentials accepted")
	}
}

func TestConnectionEditorBoundsAndProviderSpecificFields(t *testing.T) {
	t.Parallel()
	e := NewConnectionEditor("", config.Connection{Name: "Codex account", Provider: "codex"}, nil)
	fields := strings.Join(e.fields(), ",")
	if !strings.Contains(fields, "Login file") || strings.Contains(fields, "API key") || strings.Contains(fields, "Endpoint") {
		t.Fatal("login provider renders irrelevant authentication fields")
	}
	for _, width := range []int{20, 40, 80} {
		for _, height := range []int{4, 12, 24} {
			view := e.View(width, height)
			lines := strings.Split(view, "\n")
			if len(lines) > height {
				t.Fatal("editor exceeds height")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Fatal("editor exceeds width")
				}
			}
		}
	}
}

func TestConnectionEditorUsedProviderIsLockedButEndpointRemainsEditable(t *testing.T) {
	t.Parallel()
	e := NewConnectionEditor("shared", config.Connection{Name: "Shared", Provider: "lmstudio"}, nil)
	e.SetUsage([]string{"Primary", "Safety judge"})
	e.cursor = 1
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.mode != "" || !strings.Contains(e.err, "add a new connection") || e.draft.Provider != "lmstudio" {
		t.Fatal("used connection provider was changeable")
	}
	view := ansi.Strip(e.View(76, 22))
	for _, text := range []string{"Primary", "Safety judge", "add a new connection", "Esc cancel"} {
		if !strings.Contains(view, text) {
			t.Fatalf("used connection view missing %q: %s", text, view)
		}
	}
	e.cursor = 2
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.mode != "input" || e.field != "Endpoint" {
		t.Fatal("used connection endpoint was unnecessarily locked")
	}
}

func TestConnectionEditorDirtyIncludesUnfinishedInputAndHonorsCancel(t *testing.T) {
	t.Parallel()
	e := NewConnectionEditor("local", config.Connection{Provider: "lmstudio"}, nil)
	if e.Dirty() {
		t.Fatal("opening an unnamed legacy connection created a pending edit")
	}
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	e.Update(runeKey(" changed"))
	if !e.Dirty() {
		t.Fatal("unfinished field text was not protected")
	}
	e.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if e.Dirty() {
		t.Fatal("cancelled field text remained dirty")
	}
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	e.Update(runeKey(" changed"))
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !e.Dirty() {
		t.Fatal("kept field draft was not protected before keeping the connection")
	}
}

func TestConnectionUsageProtectsIncompleteBindingsAndTerminatesCycles(t *testing.T) {
	t.Parallel()
	cfg := pickerConfig()
	cfg.EnsureModelBindings()
	cfg.Models.Primary.Model = ""
	usage := strings.Join(ConnectionUsage(cfg, "laptop"), ",")
	for _, label := range []string{"Primary", "Safety judge", "Classifier", "Verifier", "Execution"} {
		if !strings.Contains(usage, label) {
			t.Fatalf("incomplete binding lost dependent role %s: %s", label, usage)
		}
	}
	cfg.Models.Primary = config.ModelBinding{Inherit: config.RoleSafetyJudge}
	if usage := ConnectionUsage(cfg, "laptop"); len(usage) != 0 {
		t.Fatalf("cycle reported a connection it never reaches: %v", usage)
	}
}
