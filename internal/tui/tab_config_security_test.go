package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/tools"
)

func TestConfigSecurityEditorCreatesAndResetsIndividualOverride(t *testing.T) {
	t.Parallel()
	svc := NewService(config.DefaultConfig(), nil, nil, nil, nil)
	tab := newConfigTab().(*configTab)
	tab.Init(svc)
	for index, field := range tab.fields {
		if field.Kind == configFieldSecurity {
			tab.cursor = index
			break
		}
	}
	tab.beginEdit()
	if !tab.securityOpen {
		t.Fatal("security editor did not open")
	}

	catalog := securitypolicy.Catalog()
	for index, setting := range catalog {
		if setting.ID == securitypolicy.SettingFileDelete {
			tab.securityCursor = index + 1
			break
		}
	}
	before, _ := tab.cfg.EffectiveSecurity()
	tab.cycleSecurityValue(1)
	after, _ := tab.cfg.EffectiveSecurity()
	if after.Value(securitypolicy.SettingFileDelete) == before.Value(securitypolicy.SettingFileDelete) {
		t.Fatal("delete policy did not change")
	}
	if after.Origins[securitypolicy.SettingFileDelete] != "override" {
		t.Fatalf("delete policy origin = %q", after.Origins[securitypolicy.SettingFileDelete])
	}
	tab.updateSecurity(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}, svc)
	reset, _ := tab.cfg.EffectiveSecurity()
	if reset.Origins[securitypolicy.SettingFileDelete] != "profile" {
		t.Fatalf("delete policy did not reset: %#v", reset.Origins)
	}
}

func TestServiceStartsNewChatWithLatestConfigSnapshot(t *testing.T) {
	t.Parallel()
	initial := config.DefaultConfig()
	svc := NewService(initial, nil, nil, nil, nil)
	latest := initial.Clone()
	if err := latest.Security.ApplyProfile(securitypolicy.ProfileYOLO); err != nil {
		t.Fatal(err)
	}
	latest.Normalize()
	svc.cfg = latest
	var observed *config.Config
	svc.SetChatStarter(func(_ context.Context, cfg *config.Config, _ tools.EventObserver) (LiveChatSession, error) {
		observed = cfg
		return &fakeLiveChatSession{}, nil
	})
	if _, err := svc.StartChat(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if observed == nil || observed.Security.Profile != securitypolicy.ProfileYOLO {
		t.Fatalf("new chat received stale config: %#v", observed)
	}
	_ = observed.Security.ApplyProfile(securitypolicy.ProfileExtraStrict)
	if svc.cfg.Security.Profile != securitypolicy.ProfileYOLO {
		t.Fatal("chat snapshot mutated service configuration")
	}
}

func TestConfigSecurityProfileRequiresConfirmationAndYOLOCopy(t *testing.T) {
	t.Parallel()
	tab := newConfigTab().(*configTab)
	tab.cfg = config.DefaultConfig()
	tab.cfg.Normalize()
	tab.securityOpen = true
	tab.securityCursor = 0
	tab.pendingProfile = securitypolicy.ProfileYOLO
	view := tab.viewSecurity(100, 30)
	if !strings.Contains(view, "YOLO") || !strings.Contains(view, "does not bypass OS") {
		t.Fatalf("missing YOLO consequence copy:\n%s", view)
	}
	tab.updateSecurity(tea.KeyMsg{Type: tea.KeyEnter}, NewService(tab.cfg, nil, nil, nil, nil))
	if tab.cfg.Security.Profile != securitypolicy.ProfileYOLO {
		t.Fatalf("confirmed profile = %q", tab.cfg.Security.Profile)
	}
}

func TestConfigSecurityEditorDoesNotOverflowRepresentativeWidths(t *testing.T) {
	t.Parallel()
	for _, width := range []int{80, 100, 120} {
		tab := newConfigTab().(*configTab)
		tab.cfg = config.DefaultConfig()
		tab.cfg.Normalize()
		tab.securityOpen = true
		tab.pendingProfile = securitypolicy.ProfileYOLO
		tab.message = profileConfirmation(tab.pendingProfile, 12)
		view := tab.viewSecurity(width, 30)
		for lineNumber, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d overflow at line %d: got %d\n%s", width, lineNumber+1, got, line)
			}
		}
	}
}

func TestConfigInitPreservesUnsavedSecurityEditsAcrossRefresh(t *testing.T) {
	t.Parallel()
	svc := NewService(config.DefaultConfig(), nil, nil, nil, nil)
	tab := newConfigTab().(*configTab)
	tab.Init(svc)
	tab.securityOpen = true
	tab.dirty = true
	if err := tab.cfg.Security.ApplyProfile(securitypolicy.ProfileYOLO); err != nil {
		t.Fatal(err)
	}
	tab.Init(svc) // mirrors the dashboard's periodic tick refresh
	if tab.cfg.Security.Profile != securitypolicy.ProfileYOLO || !tab.securityOpen || !tab.dirty {
		t.Fatalf("refresh discarded in-progress security edit: %#v", tab)
	}
}
