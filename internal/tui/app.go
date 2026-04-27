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
	_, err := p.Run()
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
		// If the active tab is consuming input (e.g. a form), forward
		// everything except ctrl+c which always quits.
		if m.tabs[m.activeTab].Consuming() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			tab, cmd := m.tabs[m.activeTab].Update(msg, m.svc, m.contentWidth(), m.contentHeight())
			m.tabs[m.activeTab] = tab
			return m, cmd
		}

		// Dismiss help overlay on any key.
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}

		switch {
		case msg.String() == "ctrl+c":
			return m, tea.Quit
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Tab), key.Matches(msg, keys.Right):
			return m, m.switchTab((m.activeTab + 1) % tabCount)
		case key.Matches(msg, keys.ShiftTab), key.Matches(msg, keys.Left):
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
		b.WriteString(m.renderHelp())
	} else {
		// Content area
		content := m.tabs[m.activeTab].View(m.contentWidth(), m.contentHeight())
		b.WriteString(content)
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

func (m model) renderStatusBar() string {
	left := styleStatusBar.Render("CvkeHarness")

	// Context-sensitive hints from the active tab.
	var hints []string
	if tabHints := m.tabs[m.activeTab].StatusHints(); len(tabHints) > 0 {
		hints = append(hints, tabHints...)
		hints = append(hints, styleMuted.Render("│"))
	}
	hints = append(hints,
		renderKeyHint("←→", "switch"),
		renderKeyHint("?", "help"),
		renderKeyHint("q", "quit"),
	)
	right := strings.Join(hints, styleMuted.Render(" "))

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
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
				{"←/→ or tab/shift+tab", "Cycle through tabs"},
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
				{"n", "Start a live chat session"},
				{"enter", "Open the selected transcript"},
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
	return jobsDataMsg{jobs: jobs}
}

func loadRunsData(svc *Service) tea.Msg {
	ctx := context.Background()
	runs, _ := svc.RecentRuns(ctx, 50)
	return runsDataMsg{runs: runs}
}

func loadChatData(svc *Service) tea.Msg {
	ctx := context.Background()
	sessions, _ := svc.RecentChatSessions(ctx, 50)
	return chatDataMsg{sessions: sessions}
}
