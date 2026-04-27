package tui

import (
	"context"
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
	"Config",
}

// ── messages ────────────────────────────────────────────────────────

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type dataLoadedMsg struct {
	tab  int
	data interface{}
}

// ── tab interface ───────────────────────────────────────────────────

type tabModel interface {
	Init(svc *Service) tea.Cmd
	Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd)
	View(width, height int) string
	// Consuming returns true when the tab is in a mode that should capture
	// all key input (e.g. form input, confirmation dialog).
	Consuming() bool
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
		// If the active tab is consuming input (e.g. a form), forward everything.
		if m.tabs[m.activeTab].Consuming() {
			tab, cmd := m.tabs[m.activeTab].Update(msg, m.svc, m.contentWidth(), m.contentHeight())
			m.tabs[m.activeTab] = tab
			return m, cmd
		}

		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Tab):
			m.activeTab = (m.activeTab + 1) % tabCount
			return m, nil
		case key.Matches(msg, keys.ShiftTab):
			m.activeTab = (m.activeTab - 1 + tabCount) % tabCount
			return m, nil
		case key.Matches(msg, keys.Tab1):
			m.activeTab = tabOverview
			return m, nil
		case key.Matches(msg, keys.Tab2):
			m.activeTab = tabJobs
			return m, nil
		case key.Matches(msg, keys.Tab3):
			m.activeTab = tabRuns
			return m, nil
		case key.Matches(msg, keys.Tab4):
			m.activeTab = tabChat
			return m, nil
		case key.Matches(msg, keys.Tab5):
			m.activeTab = tabConfig
			return m, nil
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

func (m model) View() string {
	var b strings.Builder

	// Tab bar
	b.WriteString(m.renderTabBar())
	b.WriteString("\n")
	b.WriteString(horizontalRule(m.width))
	b.WriteString("\n")

	// Content area
	content := m.tabs[m.activeTab].View(m.contentWidth(), m.contentHeight())
	b.WriteString(content)

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
		label := strings.Builder{}
		label.WriteString(" ")
		label.WriteString(name)
		label.WriteString(" ")
		if i == m.activeTab {
			parts = append(parts, styleActiveTab.Render(label.String()))
		} else {
			parts = append(parts, styleTab.Render(label.String()))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	return bar
}

func (m model) renderStatusBar() string {
	left := styleStatusBar.Render("CvkeHarness")

	hints := []string{
		renderKeyHint("tab", "switch"),
		renderKeyHint("?", "help"),
		renderKeyHint("q", "quit"),
	}
	right := strings.Join(hints, styleMuted.Render(" · "))

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
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
	runs, _ := svc.RecentRuns(ctx, 20)
	return runsDataMsg{runs: runs}
}

func loadChatData(svc *Service) tea.Msg {
	ctx := context.Background()
	sessions, _ := svc.RecentChatSessions(ctx, 20)
	return chatDataMsg{sessions: sessions}
}
