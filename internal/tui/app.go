package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/coolcake/cvkeharness/state"
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

// InitialView identifies the console workspace shown at startup.
type InitialView string

const (
	InitialViewOverview InitialView = "overview"
	InitialViewJobs     InitialView = "jobs"
	InitialViewRuns     InitialView = "runs"
	InitialViewChat     InitialView = "chat"
	InitialViewSettings InitialView = "settings"
)

// ParseInitialView validates a user-facing console view name.
func ParseInitialView(value string) (InitialView, error) {
	view := InitialView(strings.ToLower(strings.TrimSpace(value)))
	switch view {
	case InitialViewOverview, InitialViewJobs, InitialViewRuns, InitialViewChat, InitialViewSettings:
		return view, nil
	default:
		return "", fmt.Errorf("unknown console view %q; choose overview, jobs, runs, chat, or settings", value)
	}
}

func (v InitialView) tabIndex() int {
	switch v {
	case InitialViewJobs:
		return tabJobs
	case InitialViewRuns:
		return tabRuns
	case InitialViewChat:
		return tabChat
	case InitialViewSettings:
		return tabConfig
	default:
		return tabOverview
	}
}

// ── messages ────────────────────────────────────────────────────────

type tickMsg time.Time

type navigateMsg struct {
	tab   int
	run   *state.RunSummary
	jobID string
}
type quitSavedMsg struct{ err error }

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
	svc         *Service
	binaryName  string
	width       int
	height      int
	activeTab   int
	tabs        [tabCount]tabModel
	showHelp    bool
	helpScroll  int
	confirmQuit bool
	quitSaving  bool
	quitError   string
	unread      [tabCount]bool
}

// Run starts the Bubble Tea operations console.
func Run(svc *Service, binaryName string, initialView InitialView) error {
	if svc != nil {
		svc.SetBinaryName(binaryName)
	}
	m := model{
		svc:        svc,
		binaryName: binaryName,
		width:      80,
		height:     24,
		activeTab:  initialView.tabIndex(),
		tabs: [tabCount]tabModel{
			newOverviewTab(),
			newJobsTab(),
			newRunsTab(),
			newChatTab(),
			newConfigTab(),
		},
	}
	if activator, ok := m.tabs[m.activeTab].(tabActivator); ok {
		activator.Activate()
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
	case navigateMsg:
		if msg.run != nil {
			t := m.tabs[tabRuns].(*runsTab)
			t.selected = msg.run
			t.expanded = true
			t.loaded = true
			t.scroll = 0
		}
		if msg.jobID != "" {
			t := m.tabs[tabJobs].(*jobsTab)
			t.focusJobID = msg.jobID
		}
		return m, m.switchTab(msg.tab)
	case quitSavedMsg:
		m.quitSaving = false
		if msg.err != nil {
			m.quitError = msg.err.Error()
			return m, nil
		}
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		if m.confirmQuit {
			if m.quitSaving {
				return m, nil
			}
			switch msg.String() {
			case "esc", "c":
				m.confirmQuit = false
			case "d":
				return m, tea.Quit
			case "s":
				cfg := cloneTUIConfig(m.tabs[tabConfig].(*configTab).cfg)
				m.quitSaving = true
				return m, func() tea.Msg { return quitSavedMsg{err: m.svc.SaveConfig(cfg)} }
			}
			return m, nil
		}
		if m.showHelp {
			switch msg.String() {
			case "esc", "?":
				m.showHelp = false
			case "down", "j":
				m.helpScroll++
			case "up", "k":
				m.helpScroll--
			case "pgdown":
				m.helpScroll += m.contentHeight() - 1
			case "pgup":
				m.helpScroll -= m.contentHeight() - 1
			case "home":
				m.helpScroll = 0
			case "end":
				m.helpScroll = 1 << 20
			}
			m.helpScroll = clamp(m.helpScroll, 0, maxInt(strings.Count(m.renderHelp(), "\n")+1-m.contentHeight(), 0))
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
			if cfg, ok := m.tabs[tabConfig].(*configTab); ok && cfg.dirty {
				m.confirmQuit = true
				m.quitError = ""
				return m, nil
			}
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

	// Async results belong to their workspace, even while another tab has focus.
	owner := m.activeTab
	switch msg.(type) {
	case overviewDataMsg:
		owner = tabOverview
	case jobsDataMsg, jobRunsDataMsg, jobActionMsg:
		owner = tabJobs
	case runsDataMsg, runExportMsg:
		owner = tabRuns
	case configSavedMsg:
		owner = tabConfig
	case chatDataMsg, chatDetailMsg, chatSessionReadyMsg, chatTurnDoneMsg,
		chatExportDoneMsg, chatApprovalDoneMsg, chatRuntimeEventMsg, chatRuntimeEventWaitStoppedMsg:
		owner = tabChat
	}
	if m.tabs[owner] == nil {
		return m, nil
	}
	tab, cmd := m.tabs[owner].Update(msg, m.svc, m.contentWidth(), m.contentHeight())
	m.tabs[owner] = tab
	if owner != m.activeTab {
		switch msg.(type) {
		case chatTurnDoneMsg, chatSessionReadyMsg, jobActionMsg, configSavedMsg:
			m.unread[owner] = true
		}
	}
	return m, cmd
}

// switchTab changes the active tab and triggers a data refresh.
func (m *model) switchTab(idx int) tea.Cmd {
	m.activeTab = idx
	m.unread[idx] = false
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
	if m.confirmQuit {
		content := "\n  Unsaved settings\n\n  Save settings before leaving?\n\n  s Save and quit    d Discard    Esc Continue editing"
		if m.quitSaving {
			content += "\n\n  Saving…"
		}
		if m.quitError != "" {
			content += "\n\n  " + strings.Join(wrapText(m.quitError, m.contentWidth()-4), "\n  ")
		}
		b.WriteString(clampLines(content, m.contentHeight()))
	} else if m.showHelp {
		lines := strings.Split(m.renderHelp(), "\n")
		start := clamp(m.helpScroll, 0, maxInt(len(lines)-m.contentHeight(), 0))
		b.WriteString(clampLines(strings.Join(lines[start:], "\n"), m.contentHeight()))
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
		badge := ""
		if m.unread[i] {
			badge = " +"
		}
		if i == tabConfig {
			if cfg, ok := m.tabs[i].(*configTab); ok && cfg.dirty {
				badge = " *"
			}
		}
		if i == tabChat {
			if chat, ok := m.tabs[i].(*chatTab); ok {
				if chat.pendingApproval != nil {
					badge = " !"
				} else if chat.running || chat.starting {
					badge = " …"
				}
			}
		}
		label := num + "·" + name + badge
		// Compact padding preserves badges at 80 columns.
		if m.width >= 100 {
			label = " " + label + " "
		}
		if i == m.activeTab {
			parts = append(parts, styleActiveTab.Padding(0, 1).Render(label))
		} else {
			parts = append(parts, styleTab.Padding(0, 1).Render(label))
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
	if m.confirmQuit {
		return left + "  " + renderKeyHint("esc", "continue editing")
	}
	if m.showHelp {
		return left + "  " + renderKeyHint("↑↓ / PgUp PgDn", "scroll") + "  " + renderKeyHint("esc", "close")
	}

	// Context-sensitive hints from the active tab. Add only what fits so the
	// dashboard remains horizontally safe at 80 columns.
	consuming := m.tabs[m.activeTab].Consuming()
	quitKey := "q"
	if consuming {
		quitKey = "ctrl+c"
	}
	hints := []string{}
	navigationHint := renderKeyHint("←→", "switch")
	if consuming {
		navigationHint = renderKeyHint("tab", "switch")
		if nav, ok := m.tabs[m.activeTab].(horizontalTabNavigator); ok && nav.HorizontalTabNavigation() {
			navigationHint = renderKeyHint("←→", "switch")
		}
	}
	// Prioritize navigation and local completion/recovery actions over quit.
	candidates := []string{navigationHint}
	tabHints := m.tabs[m.activeTab].StatusHints()
	if consuming {
		candidates = append(candidates, tabHints...)
	} else {
		primary := minInt(len(tabHints), 2)
		candidates = append(candidates, tabHints[:primary]...)
		candidates = append(candidates, renderKeyHint("?", "help"))
		candidates = append(candidates, tabHints[primary:]...)
	}
	candidates = append(candidates, renderKeyHint(quitKey, "quit"))

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
				{"n", "Create or reopen a job draft"},
				{"ctrl+b", "Previous step in job wizard"},
				{"r", "Trigger a job run now"},
				{"p", "Pause or resume a job"},
				{"x", "Delete a job"},
			},
		},
		{
			"Chat Tab",
			[][2]string{
				{"enter", "Focus the composer or send a message"},
				{"/", "Open chat command suggestions"},
				{"/new", "Start a fresh chat (/clear remains an alias)"},
				{"ctrl+g", "Show full session context"},
				{"esc", "Leave the composer or interrupt active work"},
				{"ctrl+h", "Toggle live chat and saved conversations"},
				{"↑/↓", "Select tool calls and scroll at list boundaries"},
				{"space or ctrl+t", "Expand or collapse the selected tool"},
			},
		},
		{
			"Runs Tab",
			[][2]string{
				{"/", "Search task, answer, errors and commands"},
				{"f / t", "Filter status / cycle last recorded target"},
				{"[ / ]", "Previous page / next page"},
				{"d / e", "Toggle diagnostics / export selected run"},
				{"PgUp / PgDn", "Scroll run detail"},
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
				{"q / ctrl+c", "Quit the dashboard; Ctrl+C forces exit"},
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
	b.WriteString(styleMuted.Render("↑↓ / PgUp PgDn scroll · Esc closes help"))
	return wrapDisplay(b.String(), m.contentWidth()-2)
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
	runs, e1 := svc.RecentRuns(ctx, 10)
	jobs, e2 := svc.ScheduledJobs(ctx)
	health, e3 := svc.SchedulerHealth(ctx)
	totals, e4 := svc.ActivityTotals(ctx)
	blocked, e5 := svc.BlockedWork(ctx)
	return overviewDataMsg{cfg: svc.Config(), runs: runs, jobs: jobs, health: health, totals: totals, blocked: blocked, setup: svc.SetupMode(), at: time.Now(), err: errors.Join(e1, e2, e3, e4, e5)}
}

func loadJobsData(svc *Service) tea.Msg {
	ctx := context.Background()
	jobs, e1 := svc.ScheduledJobs(ctx)
	health, e2 := svc.SchedulerHealth(ctx)
	return jobsDataMsg{jobs: jobs, health: health, err: errors.Join(e1, e2)}
}

func loadRunsData(svc *Service) tea.Msg {
	runs, err := svc.SearchRuns(context.Background(), 26, 0, "", "all")
	return runsDataMsg{runs: runs, err: err, status: "all"}
}
