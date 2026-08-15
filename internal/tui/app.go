package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	tabOverview = 0
	tabJobs     = 1
	tabRuns     = 2
	tabChat     = 3
	tabConfig   = 4
	tabCount    = 5
)

var tabNames = [tabCount]string{
	"Overview",
	"Jobs",
	"Runs",
	"Chat",
	"Settings",
}

// ── messages ────────────────────────────────────────────────────────

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ── tab interface ───────────────────────────────────────────────────

type tabModel interface {
	Init(svc *Service) tea.Cmd
	Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd)
	View(width, height int) string
	// Consuming returns true when the tab is in a mode that should capture
	// all key input (e.g. form input, confirmation dialog).
	Consuming() bool
	// StatusHints returns context-sensitive key hints for the status bar.
	StatusHints() []string
}

// tabActivator lets a tab choose a safe input mode when global navigation
// lands on it. Most tabs do not need activation-specific behavior.
type tabActivator interface {
	Activate()
}

// horizontalTabNavigator identifies consuming states where left and right are
// still available for global tab navigation.
type horizontalTabNavigator interface {
	HorizontalTabNavigation() bool
}

// ── root model ──────────────────────────────────────────────────────

type model struct {
	svc        *Service
	binaryName string
	width      int
	height     int
	activeTab  int
	tabs       [tabCount]tabModel
	showHelp   bool
}

// Run starts the Bubble Tea TUI.
func Run(svc *Service, binaryName string) error {
	if svc != nil {
		svc.SetBinaryName(binaryName)
	}
	m := model{
		svc:        svc,
		binaryName: binaryName,
		width:      80,
		height:     24,
		tabs: [tabCount]tabModel{
			newOverviewTab(),
			newJobsTab(),
			newRunsTab(),
			newChatTab(),
			newConfigTab(),
		},
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if final, ok := finalModel.(model); ok {
		if chat, ok := final.tabs[tabChat].(*chatTab); ok {
			chat.closeSession("tui_exit")
		}
	}
	return err
}

func (m model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, tabCount+1)
	for _, tab := range m.tabs {
		if cmd := tab.Init(m.svc); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	cmds = append(cmds, tickCmd())
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		// Dismiss help overlay on any key before applying navigation.
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}

		// Tab and shift+tab are the universal escape from an input-focused tab.
		// Text editors retain left/right for cursor movement while focused.
		switch {
		case key.Matches(msg, keys.Tab):
			return m, m.switchTab((m.activeTab + 1) % tabCount)
		case key.Matches(msg, keys.ShiftTab):
			return m, m.switchTab((m.activeTab - 1 + tabCount) % tabCount)
		}

		// A consuming tab may explicitly return horizontal arrows to global
		// navigation, for example while Chat is in its non-editing mode.
		if m.tabs[m.activeTab].Consuming() {
			if nav, ok := m.tabs[m.activeTab].(horizontalTabNavigator); ok && nav.HorizontalTabNavigation() {
				switch {
				case key.Matches(msg, keys.Right):
					return m, m.switchTab((m.activeTab + 1) % tabCount)
				case key.Matches(msg, keys.Left):
					return m, m.switchTab((m.activeTab - 1 + tabCount) % tabCount)
				}
			}
			tab, cmd := m.tabs[m.activeTab].Update(msg, m.svc, m.contentWidth(), m.contentHeight())
			m.tabs[m.activeTab] = tab
			return m, cmd
		}

		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Right):
			return m, m.switchTab((m.activeTab + 1) % tabCount)
		case key.Matches(msg, keys.Left):
			return m, m.switchTab((m.activeTab - 1 + tabCount) % tabCount)
		case key.Matches(msg, keys.Tab1):
			return m, m.switchTab(tabOverview)
		case key.Matches(msg, keys.Tab2):
			return m, m.switchTab(tabJobs)
		case key.Matches(msg, keys.Tab3):
			return m, m.switchTab(tabRuns)
		case key.Matches(msg, keys.Tab4):
			return m, m.switchTab(tabChat)
		case key.Matches(msg, keys.Tab5):
			return m, m.switchTab(tabConfig)
		case key.Matches(msg, keys.Help):
			m.showHelp = !m.showHelp
			return m, nil
		}

	case tickMsg:
		// Refresh the active tab's data on tick.
		cmd := m.tabs[m.activeTab].Init(m.svc)
		return m, tea.Batch(cmd, tickCmd())
	}

	// Forward to the active tab.
	tab, cmd := m.tabs[m.activeTab].Update(msg, m.svc, m.contentWidth(), m.contentHeight())
	m.tabs[m.activeTab] = tab
	return m, cmd
}

// switchTab changes the active tab and triggers a data refresh.
func (m *model) switchTab(idx int) tea.Cmd {
	m.activeTab = idx
	if activator, ok := m.tabs[idx].(tabActivator); ok {
		activator.Activate()
	}
	return m.tabs[idx].Init(m.svc)
}

func (m model) View() string {
	var b strings.Builder

	// Tab bar
	b.WriteString(m.renderTabBar())
	b.WriteString("\n")
	b.WriteString(horizontalRule(m.width))
	b.WriteString("\n")

	// Help overlay replaces content when active.
	if m.showHelp {
		b.WriteString(clampLines(m.renderHelp(), m.contentHeight()))
	} else {
		// Content area
		content := m.tabs[m.activeTab].View(m.contentWidth(), m.contentHeight())
		b.WriteString(clampLines(content, m.contentHeight()))
	}

	// Pad to fill the screen
	rendered := b.String()
	contentLines := strings.Count(rendered, "\n")
	for contentLines < m.height-2 {
		b.WriteString("\n")
		contentLines++
	}

	// Status bar at the very bottom
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	return b.String()
}

func (m model) renderTabBar() string {
	var parts []string
	for i, name := range tabNames {
		num := fmt.Sprintf("%d", i+1)
		label := " " + num + "·" + name + " "
		if i == m.activeTab {
			parts = append(parts, styleActiveTab.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	return bar
}

func clampLines(s string, maxLines int) string {
	if maxLines <= 0 || s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n")
}

func (m model) renderStatusBar() string {
	left := styleStatusBar.Render("CvkeHarness")

	// Context-sensitive hints from the active tab. Add only what fits so the
	// dashboard remains horizontally safe at 80 columns.
	consuming := m.tabs[m.activeTab].Consuming()
	quitKey := "q"
	if consuming {
		quitKey = "ctrl+c"
	}
	hints := []string{renderKeyHint(quitKey, "quit")}
	navigationHint := renderKeyHint("←→", "switch")
	if consuming {
		navigationHint = renderKeyHint("tab", "switch")
		if nav, ok := m.tabs[m.activeTab].(horizontalTabNavigator); ok && nav.HorizontalTabNavigation() {
			navigationHint = renderKeyHint("←→", "switch")
		}
	}
	// Keep the universal escape visible before optional tab-local actions.
	candidates := []string{navigationHint}
	if tabHints := m.tabs[m.activeTab].StatusHints(); len(tabHints) > 0 {
		candidates = append(candidates, tabHints...)
	}
	if !consuming {
		candidates = append(candidates, renderKeyHint("?", "help"))
	}

	for _, candidate := range candidates {
		next := append(append([]string(nil), hints...), candidate)
		right := strings.Join(next, "  ")
		if lipgloss.Width(left)+1+lipgloss.Width(right) > m.width {
			continue
		}
		hints = next
	}
	right := strings.Join(hints, "  ")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m model) renderHelp() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render("Keyboard Shortcuts"))
	b.WriteString("\n\n")

	groups := []struct {
		title string
		items [][2]string
	}{
		{
			"Navigation",
			[][2]string{
				{"tab / shift+tab", "Cycle tabs from any input mode"},
				{"←/→", "Cycle tabs outside text editing"},
				{"1-5", "Jump to tab directly"},
				{"↑/k  ↓/j", "Move cursor in lists"},
				{"enter", "Expand / select item"},
				{"esc", "Go back / collapse detail"},
			},
		},
		{
			"Jobs Tab",
			[][2]string{
				{"n", "Create a new scheduled job"},
				{"r", "Trigger a job run now"},
				{"p", "Pause or resume a job"},
				{"x", "Delete a job"},
			},
		},
		{
			"Chat Tab",
			[][2]string{
				{"enter", "Focus the composer or send a message"},
				{"esc", "Leave the composer or interrupt active work"},
				{"ctrl+h", "Toggle live chat and saved conversations"},
				{"↑/↓", "Select tool calls and scroll at list boundaries"},
				{"space or ctrl+t", "Expand or collapse the selected tool"},
			},
		},
		{
			"Settings Tab",
			[][2]string{
				{"enter", "Edit or toggle the selected setting"},
				{"s", "Save configuration"},
				{"r", "Reset unsaved edits"},
			},
		},
		{
			"General",
			[][2]string{
				{"?", "Toggle this help"},
				{"q / ctrl+c", "Quit the dashboard"},
			},
		},
	}

	for _, group := range groups {
		b.WriteString("  ")
		b.WriteString(styleBright.Render(group.title))
		b.WriteString("\n")
		for _, item := range group.items {
			b.WriteString("    ")
			b.WriteString(styleKeyHelpKey.Render(padRight(item[0], 20)))
			b.WriteString(styleBase.Render(item[1]))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("  ")
	b.WriteString(styleMuted.Render("Press any key to dismiss"))
	return b.String()
}

func (m model) contentWidth() int {
	w := m.width
	if w < 40 {
		w = 40
	}
	return w
}

func (m model) contentHeight() int {
	// Tab bar (1) + rule (1) + status bar (2) = 4
	h := m.height - 4
	if h < 5 {
		h = 5
	}
	return h
}

// ── data loading helpers ────────────────────────────────────────────

func loadOverviewData(svc *Service) tea.Msg {
	ctx := context.Background()
	runs, _ := svc.RecentRuns(ctx, 5)
	jobs, _ := svc.ScheduledJobs(ctx)
	sessions, _ := svc.RecentChatSessions(ctx, 5)
	allRuns, _ := svc.RecentRuns(ctx, 100)
	return overviewDataMsg{cfg: svc.Config(), runs: runs, jobs: jobs, sessions: sessions, allRuns: allRuns}
}

func loadJobsData(svc *Service) tea.Msg {
	ctx := context.Background()
	jobs, _ := svc.ScheduledJobs(ctx)
	health, _ := svc.SchedulerHealth(ctx)
	return jobsDataMsg{jobs: jobs, health: health}
}

func loadRunsData(svc *Service) tea.Msg {
	ctx := context.Background()
	runs, _ := svc.RecentRuns(ctx, 50)
	return runsDataMsg{runs: runs}
}
