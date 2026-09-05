package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type navigationAction struct {
	title, detail, action string
	tab                   int
}
type navigationInputMsg struct {
	epoch uint64
	msg   tea.Msg
}

func navigationInputCmd(cmd tea.Cmd, epoch uint64) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return navigationInputMsg{epoch: epoch, msg: cmd()} }
}

type navigationSwitcher struct {
	open   bool
	input  textinput.Model
	cursor int
}

func (m *model) openNavigation() tea.Cmd {
	input := textinput.New()
	input.Placeholder = "Find a workspace or action…"
	input.CharLimit = 100
	m.navigation = navigationSwitcher{open: true, input: input}
	m.navigationEpoch++
	return navigationInputCmd(m.navigation.input.Focus(), m.navigationEpoch)
}

func (m model) navigationActions() []navigationAction {
	actions := []navigationAction{
		{"Overview", "Attention, upcoming jobs, and recent results", "tab", tabOverview},
		{"Jobs", "Scheduled work and job drafts", "tab", tabJobs},
		{"Runs", "Search and revisit task outcomes", "tab", tabRuns},
		{"Chat", "Conversation, tools, and approvals", "tab", tabChat},
		{"Settings", "Provider and runtime configuration", "tab", tabConfig},
		{"Create a job", "Define a schedule and task", "job", tabJobs},
		{"Search run history", "Find a task, answer, error, or command", "search", tabRuns},
		{"Keyboard help", "All navigation and workspace shortcuts", "help", 0},
		{"Reduce motion", "Use static activity indicators for this session", "motion", 0},
	}
	if jobs, ok := m.tabs[tabJobs].(*jobsTab); ok && jobs.draftReady {
		actions[5].title = "Continue job draft"
		actions[5].detail = "Return to your unfinished job"
	}
	if m.reduceMotion {
		actions[8].title = "Enable motion"
		actions[8].detail = "Animate activity indicators for this session"
	}
	query := strings.ToLower(strings.TrimSpace(m.navigation.input.Value()))
	var out []navigationAction
	for _, action := range actions {
		aliases := map[string]string{"job": "new job create job", "search": "find runs", "help": "shortcuts", "motion": "animations"}
		if strings.Contains(strings.ToLower(action.title+" "+action.detail+" "+aliases[action.action]), query) {
			out = append(out, action)
		}
	}
	if query != "" {
		rank := func(action navigationAction) int {
			title := strings.ToLower(action.title)
			if title == query {
				return 0
			}
			if strings.HasPrefix(title, query) {
				return 1
			}
			if strings.Contains(title, query) {
				return 2
			}
			return 3
		}
		sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	}
	return out
}

func (m model) updateNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	actions := m.navigationActions()
	switch msg.String() {
	case "esc", "ctrl+o":
		m.navigation.open = false
		return m, nil
	case "down", "tab":
		if len(actions) > 0 {
			m.navigation.cursor = (m.navigation.cursor + 1) % len(actions)
		}
		return m, nil
	case "up", "shift+tab":
		if len(actions) > 0 {
			m.navigation.cursor = (m.navigation.cursor + len(actions) - 1) % len(actions)
		}
		return m, nil
	case "enter":
		if len(actions) == 0 {
			return m, nil
		}
		action := actions[clamp(m.navigation.cursor, 0, len(actions)-1)]
		m.navigation.open = false
		if action.action == "motion" {
			m.reduceMotion = !m.reduceMotion
			return m, nil
		}
		if action.action == "help" {
			m.showHelp = true
			m.helpScroll = 0
			return m, nil
		}
		m.showHelp = false
		switch action.action {
		case "job":
			tab := m.tabs[tabJobs].(*jobsTab)
			if tab.creating {
				return m, m.switchTab(tabJobs)
			}
			cmd := m.switchTab(tabJobs)
			_, focus := tab.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}, m.svc)
			return m, tea.Batch(cmd, focus)
		case "search":
			tab := m.tabs[tabRuns].(*runsTab)
			tab.expanded = false
			tab.selected = nil
			tab.searching = true
			tab.search.SetValue(tab.query)
			cmd := m.switchTab(tabRuns)
			return m, tea.Batch(cmd, tab.search.Focus())
		}
		return m, m.switchTab(action.tab)
	}
	var cmd tea.Cmd
	m.navigation.input, cmd = m.navigation.input.Update(msg)
	m.navigation.cursor = 0
	return m, navigationInputCmd(cmd, m.navigationEpoch)
}

func (m model) renderNavigation() string {
	header := renderPageHeader("Open workspace", "Type to filter. Esc returns to where you were.", m.contentWidth())
	m.navigation.input.Width = maxInt(m.contentWidth()-10, 20)
	header += renderInputSurface(m.navigation.input.View(), m.contentWidth()) + "\n\n"
	actions := m.navigationActions()
	if len(actions) == 0 {
		return header + "  No matches. Try a workspace name or clear the search."
	}
	count := maxInt((m.contentHeight()-strings.Count(header, "\n")-1)/2, 1)
	start, end := listWindow(m.navigation.cursor, len(actions), count)
	for i := start; i < end; i++ {
		title := actions[i].title
		if actions[i].action == "tab" && actions[i].tab == m.activeTab {
			title += " · current"
		}
		header += renderListEntry(title, actions[i].detail, i == m.navigation.cursor, m.contentWidth())
	}
	return header + "  " + scrollHints(start, end, len(actions))
}

func (m *model) activateTab(idx int) tea.Cmd {
	m.activeTab = idx
	m.unread[idx] = false
	if activator, ok := m.tabs[idx].(tabActivator); ok {
		activator.Activate()
	}
	return m.tabs[idx].Init(m.svc)
}

func (m *model) navigateHistory(delta int) tea.Cmd {
	next := m.navigationIndex + delta
	if next < 0 || next >= len(m.navigationHistory) {
		return nil
	}
	m.navigationIndex = next
	return m.activateTab(m.navigationHistory[next])
}

func (m model) clickedTab(x int) int {
	if x < 0 {
		return -1
	}
	left := 0
	for i, part := range m.tabSegments() {
		right := left + lipgloss.Width(part)
		if x >= left && x < right {
			return i
		}
		left = right
	}
	return -1
}
