package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type tallTab struct{}

func (t tallTab) Init(*Service) tea.Cmd { return nil }

func (t tallTab) Update(tea.Msg, *Service, int, int) (tabModel, tea.Cmd) { return t, nil }

func (t tallTab) View(int, int) string {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString(fmt.Sprintf("content line %02d\n", i))
	}
	return b.String()
}

func (t tallTab) Consuming() bool { return false }

func (t tallTab) StatusHints() []string { return nil }

func TestModelViewClampsTallContentAndKeepsTabBar(t *testing.T) {
	t.Parallel()

	m := model{
		width:     80,
		height:    10,
		activeTab: tabJobs,
		tabs: [tabCount]tabModel{
			tallTab{},
			tallTab{},
			tallTab{},
			tallTab{},
			tallTab{},
		},
	}

	view := m.View()
	if got := strings.Count(view, "\n") + 1; got > m.height {
		t.Fatalf("expected view to fit terminal height %d, got %d lines:\n%s", m.height, got, view)
	}
	for _, want := range []string{"1·Overview", "2·Jobs", "CvkeHarness"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected clamped view to contain %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "content line 49") {
		t.Fatalf("expected overflowing content to be clipped, got:\n%s", view)
	}
}
