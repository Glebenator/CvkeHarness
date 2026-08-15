package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseInitialViewAndTabSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		view  InitialView
		tab   int
	}{
		{input: "overview", view: InitialViewOverview, tab: tabOverview},
		{input: " Jobs ", view: InitialViewJobs, tab: tabJobs},
		{input: "runs", view: InitialViewRuns, tab: tabRuns},
		{input: "CHAT", view: InitialViewChat, tab: tabChat},
		{input: "settings", view: InitialViewSettings, tab: tabConfig},
	}
	for _, test := range tests {
		view, err := ParseInitialView(test.input)
		if err != nil {
			t.Fatalf("ParseInitialView(%q) returned error: %v", test.input, err)
		}
		if view != test.view || view.tabIndex() != test.tab {
			t.Fatalf("ParseInitialView(%q) = %q/tab %d, want %q/tab %d", test.input, view, view.tabIndex(), test.view, test.tab)
		}
	}
	if _, err := ParseInitialView("terminal"); err == nil {
		t.Fatal("expected an unknown initial view to fail")
	}
}

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

func TestHorizontalNavigationCrossesChatWithoutFocusingComposer(t *testing.T) {
	t.Parallel()

	chat := newChatTab().(*chatTab)
	m := model{
		width:     100,
		height:    30,
		activeTab: tabConfig,
		tabs: [tabCount]tabModel{
			tallTab{},
			tallTab{},
			tallTab{},
			chat,
			tallTab{},
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)
	if m.activeTab != tabChat {
		t.Fatalf("expected left from Settings to land on Chat, got tab %d", m.activeTab)
	}
	if chat.composerFocused || chat.Consuming() {
		t.Fatal("expected Chat to activate in navigation mode")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)
	if m.activeTab != tabRuns {
		t.Fatalf("expected a second left to continue through Chat to Runs, got tab %d", m.activeTab)
	}
}

func TestTabLeavesFocusedChatComposer(t *testing.T) {
	t.Parallel()

	chat := newChatTab().(*chatTab)
	chat.composerFocused = true
	chat.composer.Focus()
	m := model{
		width:     100,
		height:    30,
		activeTab: tabChat,
		tabs: [tabCount]tabModel{
			tallTab{},
			tallTab{},
			tallTab{},
			chat,
			tallTab{},
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)
	if m.activeTab != tabConfig {
		t.Fatalf("expected tab to leave the focused composer, got tab %d", m.activeTab)
	}
}

func TestModelForwardsMouseWheelToFocusedChatTranscript(t *testing.T) {
	t.Parallel()

	chat := newChatTab().(*chatTab)
	for i := 0; i < 30; i++ {
		chat.messages = append(chat.messages, liveChatMessage{
			role:    "system",
			content: fmt.Sprintf("transcript line %02d", i),
		})
	}
	chat.composerFocused = true
	chat.composer.Focus()
	m := model{
		width:     80,
		height:    20,
		activeTab: tabChat,
	}
	m.tabs[tabChat] = chat
	chat.resize(m.contentWidth(), m.contentHeight())
	chat.viewport.GotoBottom()
	bottom := chat.viewport.YOffset

	updated, _ := m.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
		Type:   tea.MouseWheelUp,
	})
	m = updated.(model)
	chat = m.tabs[tabChat].(*chatTab)
	if chat.viewport.YOffset >= bottom {
		t.Fatalf("expected app to forward wheel event above offset %d, got %d", bottom, chat.viewport.YOffset)
	}
	if !chat.composerFocused {
		t.Fatal("expected app-level wheel scrolling to retain composer focus")
	}
}

func TestFocusedInputStatusBarPrioritizesRealEscapeHint(t *testing.T) {
	t.Parallel()

	chat := newChatTab().(*chatTab)
	chat.composerFocused = true
	chat.composer.Focus()
	m := model{
		width:     80,
		activeTab: tabChat,
	}
	m.tabs[tabChat] = chat

	status := m.renderStatusBar()
	if !strings.Contains(status, "tab") || !strings.Contains(status, "switch") {
		t.Fatalf("expected focused input footer to expose tab switching, got %q", status)
	}
	if strings.Contains(status, "?") {
		t.Fatalf("footer must not advertise help while ? is captured as input, got %q", status)
	}
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
