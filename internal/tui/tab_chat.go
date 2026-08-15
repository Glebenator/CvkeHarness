package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/internal/chatcmd"
	"github.com/coolcake/cvkeharness/internal/secrets"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

type chatDataMsg struct {
	sessions []state.ChatSessionSummary
	err      error
}

type chatDetailMsg struct {
	detail state.ChatSessionDetail
	err    error
}

type chatSessionReadyMsg struct {
	session LiveChatSession
	err     error
}

type chatTurnDoneMsg struct {
	prompt string
	result agent.ChatTurnResult
	err    error
}

type chatExportDoneMsg struct {
	path string
	err  error
}

type chatRuntimeEventMsg struct{ event tools.Event }
type chatRuntimeEventWaitStoppedMsg struct{}

type liveChatMessage struct {
	role    string
	content string
	at      time.Time
	turn    int
}

type liveToolCall struct {
	id       string
	name     string
	command  string
	output   string
	status   string
	err      string
	duration time.Duration
	expanded bool
	turn     int
}

type channelEventObserver struct{ ch chan tools.Event }

func (o channelEventObserver) Observe(event tools.Event) {
	select {
	case o.ch <- event:
	default:
		// Runtime events are useful UI detail, not an execution boundary. Never
		// block a tool because the renderer is briefly behind.
	}
}

type chatTab struct {
	sessions []state.ChatSessionSummary
	cursor   int
	history  bool
	expanded bool
	detail   state.ChatSessionDetail
	loaded   bool
	scroll   int
	message  string

	composer        textarea.Model
	viewport        viewport.Model
	composerFocused bool
	session         LiveChatSession
	starting        bool
	running         bool
	stopping        bool
	pendingPrompt   string
	pendingCommand  chatcmd.Action
	cancelTurn      context.CancelFunc
	eventCh         chan tools.Event
	eventWaitStop   chan struct{}
	messages        []liveChatMessage
	toolCalls       []liveToolCall
	toolCursor      int
	toolLineStarts  []int
	toolLineEnds    []int
	activeTurn      int
	status          string
	statusDetail    string
	target          string
	verification    string
	lastError       string
	controlsReady   bool
	configuredModel string
	safety          string
	markdownWidth   int
	markdownCache   map[string]string
	memorySources   []tools.MemorySource
	commandOpen     bool
	commandMatches  []chatcmd.Command
	commandCursor   int
	commandRows     int
}

func newChatTab() tabModel {
	composer := textarea.New()
	composer.Placeholder = "Ask CvkeHarness"
	composer.CharLimit = 16 * 1024
	composer.SetHeight(3)
	composer.ShowLineNumbers = false
	composer.Prompt = ""
	composer.FocusedStyle.CursorLine = lipgloss.NewStyle()
	composer.BlurredStyle.CursorLine = lipgloss.NewStyle()
	composer.Blur()

	vp := viewport.New(76, 12)
	return &chatTab{
		composer:        composer,
		viewport:        vp,
		composerFocused: false,
		eventCh:         make(chan tools.Event, 128),
		status:          "READY",
		verification:    "NOT RUN",
		controlsReady:   true,
		commandRows:     commandMenuLimit,
	}
}

// Activate lands in navigation mode so moving across the tab bar with left or
// right never drops the operator into an input trap. Enter focuses the composer.
func (t *chatTab) Activate() {
	t.composerFocused = false
	t.composer.Blur()
	t.closeCommandMenu()
}

func (t *chatTab) HorizontalTabNavigation() bool {
	return !t.history && !t.composerFocused
}

func (t *chatTab) Init(svc *Service) tea.Cmd {
	if svc != nil && svc.Config() != nil {
		cfg := svc.Config()
		t.configuredModel = strings.Trim(strings.TrimSpace(cfg.Provider)+"/"+strings.TrimSpace(cfg.PrimaryModel()), "/")
		if effective, err := cfg.EffectiveSecurity(); err == nil {
			t.safety = effective.Summary()
		} else {
			t.safety = "invalid"
		}
	}
	return func() tea.Msg { return loadChatData(svc) }
}

func (t *chatTab) Consuming() bool {
	return !t.history && (t.composerFocused || t.running || t.starting)
}

func (t *chatTab) StatusHints() []string {
	if t.history {
		if t.expanded {
			return []string{
				renderKeyHint("esc", "sessions"),
				renderKeyHint("↑↓", "scroll"),
				renderKeyHint("ctrl+h", "live chat"),
			}
		}
		return []string{
			renderKeyHint("ctrl+h", "live chat"),
			renderKeyHint("↑↓", "move"),
			renderKeyHint("enter", "open"),
		}
	}
	if t.running {
		hints := []string{
			renderKeyHint("esc", "interrupt"),
		}
		if len(t.toolCalls) > 0 {
			hints = append(hints,
				renderKeyHint("space", "tool detail"),
				renderKeyHint("↑↓", "tools + scroll"),
			)
		} else {
			hints = append(hints, renderKeyHint("↑↓", "scroll"))
		}
		return hints
	}
	if !t.composerFocused {
		hints := []string{
			renderKeyHint("enter", "compose"),
			renderKeyHint("↑↓", "scroll"),
		}
		if len(t.toolCalls) > 0 {
			hints[1] = renderKeyHint("↑↓", "tools + scroll")
			hints = append(hints, renderKeyHint("space", "tool detail"))
		}
		return append(hints, renderKeyHint("ctrl+h", "history"))
	}
	if t.commandOpen {
		return []string{
			renderKeyHint("↑↓", "commands"),
			renderKeyHint("enter", "complete or run"),
			renderKeyHint("esc", "close"),
		}
	}
	return []string{
		renderKeyHint("enter", "send"),
		renderKeyHint("ctrl+j", "newline"),
		renderKeyHint("ctrl+h", "history"),
		renderKeyHint("esc", "leave composer"),
	}
}

func (t *chatTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	t.resize(width, height)
	switch msg := msg.(type) {
	case chatDataMsg:
		t.sessions = msg.sessions
		t.loaded = true
		if msg.err != nil {
			t.message = "History unavailable: " + msg.err.Error()
		}
		if t.cursor >= len(t.sessions) && len(t.sessions) > 0 {
			t.cursor = len(t.sessions) - 1
		}

	case chatDetailMsg:
		t.detail = msg.detail
		t.scroll = 0
		if msg.err != nil {
			t.message = "Transcript unavailable: " + msg.err.Error()
		}

	case chatSessionReadyMsg:
		t.starting = false
		if msg.err != nil {
			t.status = "UNAVAILABLE"
			t.lastError = classifyChatStartError(msg.err)
			t.pendingPrompt = ""
			t.pendingCommand = chatcmd.None
			return t, nil
		}
		t.session = msg.session
		t.status = "READY"
		t.statusDetail = "in-process session started"
		if t.pendingPrompt != "" {
			prompt := t.pendingPrompt
			t.pendingPrompt = ""
			return t.beginTurn(prompt)
		}
		if t.pendingCommand != chatcmd.None {
			action := t.pendingCommand
			t.pendingCommand = chatcmd.None
			return t.runLocalCommand(action, svc)
		}

	case chatExportDoneMsg:
		if msg.err != nil {
			t.appendConsoleMessage("Export unavailable: " + msg.err.Error())
		} else {
			t.appendConsoleMessage("Export complete: " + msg.path + "\nPrivate file (0600). Review operational context before sharing.")
		}
		t.refreshViewport()
		t.viewport.GotoBottom()
		return t, nil

	case chatRuntimeEventMsg:
		t.applyRuntimeEvent(msg.event)
		t.refreshViewport()
		if t.running {
			return t, waitChatEventCmd(t.eventCh, t.eventWaitStop)
		}

	case chatTurnDoneMsg:
		if t.eventWaitStop != nil {
			close(t.eventWaitStop)
			t.eventWaitStop = nil
		}
		t.applyPendingRuntimeEvents()
		t.running = false
		t.cancelTurn = nil
		t.applyTurnResult(msg.result, msg.err)
		t.stopping = false
		t.refreshViewport()
		t.viewport.GotoBottom()
		return t, func() tea.Msg { return loadChatData(svc) }

	case chatRuntimeEventWaitStoppedMsg:
		return t, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+h" {
			t.history = !t.history
			t.expanded = false
			if !t.history {
				t.composerFocused = true
				t.composer.Focus()
			}
			return t, nil
		}
		if t.history {
			if t.expanded {
				return t.updateDetail(msg)
			}
			return t.updateHistory(msg, svc)
		}
		return t.updateLive(msg, svc)

	case tea.MouseMsg:
		return t.updateMouse(msg)
	}
	return t, nil
}

func (t *chatTab) updateMouse(msg tea.MouseMsg) (tabModel, tea.Cmd) {
	direction := verticalMouseWheelDirection(msg)
	if direction == 0 {
		return t, nil
	}
	delta := maxInt(t.viewport.MouseWheelDelta, 1)
	if t.history {
		if t.expanded {
			t.scroll = maxInt(t.scroll+direction*delta, 0)
			return t, nil
		}
		if len(t.sessions) > 0 {
			t.cursor = clamp(t.cursor+direction*delta, 0, len(t.sessions)-1)
		}
		return t, nil
	}

	var cmd tea.Cmd
	t.viewport, cmd = t.viewport.Update(msg)
	return t, cmd
}

func verticalMouseWheelDirection(msg tea.MouseMsg) int {
	if msg.Action != tea.MouseActionPress {
		return 0
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return -1
	case tea.MouseButtonWheelDown:
		return 1
	default:
		return 0
	}
}

func (t *chatTab) updateLive(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if t.commandOpen {
			t.closeCommandMenu()
			return t, nil
		}
		if t.running && t.cancelTurn != nil {
			t.stopping = true
			t.status = "INTERRUPTING"
			t.statusDetail = "waiting for the active turn to stop safely"
			t.cancelTurn()
			return t, nil
		}
		t.composerFocused = false
		t.composer.Blur()
		return t, nil
	case "ctrl+t":
		if len(t.toolCalls) > 0 {
			t.toolCursor = minInt(maxInt(t.toolCursor, 0), len(t.toolCalls)-1)
			t.toolCalls[t.toolCursor].expanded = !t.toolCalls[t.toolCursor].expanded
		}
		t.refreshViewport()
		t.ensureSelectedToolVisible(true)
		return t, nil
	case " ":
		if t.composerFocused {
			break
		}
		if len(t.toolCalls) > 0 {
			t.toolCursor = minInt(maxInt(t.toolCursor, 0), len(t.toolCalls)-1)
			t.toolCalls[t.toolCursor].expanded = !t.toolCalls[t.toolCursor].expanded
		}
		t.refreshViewport()
		t.ensureSelectedToolVisible(true)
		return t, nil
	case "pgup":
		t.viewport.HalfViewUp()
		return t, nil
	case "pgdown":
		t.viewport.HalfViewDown()
		return t, nil
	case "up":
		if t.composerFocused && t.commandOpen && len(t.commandMatches) > 0 {
			t.commandCursor = (t.commandCursor - 1 + len(t.commandMatches)) % len(t.commandMatches)
			return t, nil
		}
		if !t.composerFocused {
			if t.moveToolSelection(-1) {
				t.refreshViewport()
				t.focusSelectedTool(-1)
			} else {
				t.viewport.LineUp(1)
			}
			return t, nil
		}
	case "down":
		if t.composerFocused && t.commandOpen && len(t.commandMatches) > 0 {
			t.commandCursor = (t.commandCursor + 1) % len(t.commandMatches)
			return t, nil
		}
		if !t.composerFocused {
			if t.moveToolSelection(1) {
				t.refreshViewport()
				t.focusSelectedTool(1)
			} else {
				t.viewport.LineDown(1)
			}
			return t, nil
		}
	case "ctrl+j":
		t.composer.SetValue(t.composer.Value() + "\n")
		t.closeCommandMenu()
		return t, nil
	case "enter":
		if !t.composerFocused {
			t.composerFocused = true
			t.composer.Focus()
			return t, nil
		}
		if t.running || t.starting {
			return t, nil
		}
		prompt := strings.TrimSpace(t.composer.Value())
		if prompt == "" {
			return t, nil
		}
		if t.commandOpen && chatcmd.Parse(prompt, chatcmd.TUI) == chatcmd.None && len(t.commandMatches) > 0 {
			selected := t.commandMatches[minInt(maxInt(t.commandCursor, 0), len(t.commandMatches)-1)]
			t.composer.SetValue(selected.Name)
			t.composer.CursorEnd()
			t.closeCommandMenu()
			return t, nil
		}
		t.composer.Reset()
		t.closeCommandMenu()
		if action := chatcmd.Parse(prompt, chatcmd.TUI); action != chatcmd.None {
			return t.runLocalCommand(action, svc)
		}
		if chatcmd.IsUnknownSlash(prompt, chatcmd.TUI) {
			t.appendConsoleMessage("Unknown command: " + prompt + "\nType / to see commands. Prefix with // to send a literal leading slash.")
			t.refreshViewport()
			t.viewport.GotoBottom()
			return t, nil
		}
		prompt = chatcmd.PromptText(prompt)
		if t.session == nil {
			t.starting = true
			t.pendingPrompt = prompt
			t.status = "CONNECTING"
			t.statusDetail = "loading provider, memory, tools, and routing"
			return t, startLiveChatCmd(svc, channelEventObserver{ch: t.eventCh})
		}
		return t.beginTurn(prompt)
	}

	var cmd tea.Cmd
	t.composer, cmd = t.composer.Update(msg)
	t.updateCommandMenu()
	return t, cmd
}

func (t *chatTab) runLocalCommand(action chatcmd.Action, svc *Service) (tabModel, tea.Cmd) {
	switch action {
	case chatcmd.History:
		t.history = true
		return t, nil
	case chatcmd.New:
		return t.startFreshSession(svc)
	case chatcmd.Help:
		t.appendConsoleMessage(commandHelpText())
	case chatcmd.Memory:
		t.appendConsoleMessage(memoryCommandText(t.memorySources))
	case chatcmd.Tools:
		if t.session == nil {
			t.pendingCommand = chatcmd.Tools
			t.starting = true
			t.status = "CONNECTING"
			t.statusDetail = "loading the configured tool registry"
			return t, startLiveChatCmd(svc, channelEventObserver{ch: t.eventCh})
		}
		t.appendConsoleMessage(toolsCommandText(t.session.Tools(), t.safety))
	case chatcmd.Export:
		if t.session == nil || t.session.ID() <= 0 || t.activeTurn == 0 {
			t.appendConsoleMessage("Export unavailable: current chat has no completed turns to export.")
			break
		}
		return t, exportChatSessionCmd(svc, t.session.ID())
	}
	t.refreshViewport()
	t.viewport.GotoBottom()
	return t, nil
}

func (t *chatTab) appendConsoleMessage(content string) {
	t.messages = append(t.messages, liveChatMessage{role: "system", content: strings.TrimSpace(content), at: time.Now()})
}

func (t *chatTab) updateCommandMenu() {
	value := strings.TrimSpace(t.composer.Value())
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, " \t\r\n") {
		t.closeCommandMenu()
		return
	}
	t.commandOpen = true
	t.commandMatches = chatcmd.Matches(value, chatcmd.TUI)
	if len(t.commandMatches) == 0 {
		t.commandCursor = 0
		return
	}
	t.commandCursor = clamp(t.commandCursor, 0, len(t.commandMatches)-1)
}

func (t *chatTab) closeCommandMenu() {
	t.commandOpen = false
	t.commandMatches = nil
	t.commandCursor = 0
}

func (t *chatTab) startFreshSession(svc *Service) (tabModel, tea.Cmd) {
	oldEvents := t.eventCh
	t.closeSession("new_chat")
	drainRuntimeEvents(oldEvents)

	// A fresh observer channel prevents delayed events from the retired runtime
	// from being rendered into the new conversation.
	t.eventCh = make(chan tools.Event, 128)
	t.messages = nil
	t.toolCalls = nil
	t.toolCursor = 0
	t.toolLineStarts = nil
	t.toolLineEnds = nil
	t.activeTurn = 0
	t.pendingPrompt = ""
	t.pendingCommand = chatcmd.None
	t.running = false
	t.stopping = false
	t.target = ""
	t.verification = "NOT RUN"
	t.lastError = ""
	t.memorySources = nil
	t.markdownWidth = 0
	t.markdownCache = nil
	t.closeCommandMenu()
	t.starting = true
	t.status = "CONNECTING"
	t.statusDetail = "starting a fresh in-process session"
	t.refreshViewport()
	t.viewport.GotoTop()
	return t, startLiveChatCmd(svc, channelEventObserver{ch: t.eventCh})
}

func (t *chatTab) beginTurn(prompt string) (tabModel, tea.Cmd) {
	drainRuntimeEvents(t.eventCh)
	t.closeCommandMenu()
	t.activeTurn++
	ctx, cancel := context.WithCancel(context.Background())
	t.cancelTurn = cancel
	t.eventWaitStop = make(chan struct{})
	t.running = true
	t.stopping = false
	t.status = "THINKING"
	t.statusDetail = "waiting for a complete provider response"
	t.lastError = ""
	t.verification = "PENDING"
	t.composerFocused = false
	t.composer.Blur()
	t.messages = append(t.messages, liveChatMessage{role: "user", content: prompt, at: time.Now(), turn: t.activeTurn})
	t.refreshViewport()
	t.viewport.GotoBottom()
	return t, tea.Batch(runChatTurnCmd(ctx, t.session, prompt), waitChatEventCmd(t.eventCh, t.eventWaitStop))
}

func (t *chatTab) updateHistory(msg tea.KeyMsg, svc *Service) (tabModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		t.history = false
		t.composerFocused = true
		t.composer.Focus()
	case "down", "j":
		if t.cursor < len(t.sessions)-1 {
			t.cursor++
		}
	case "up", "k":
		if t.cursor > 0 {
			t.cursor--
		}
	case "enter":
		if len(t.sessions) > 0 {
			t.expanded = true
			session := t.sessions[t.cursor]
			return t, func() tea.Msg {
				detail, err := svc.ChatSessionDetail(context.Background(), session.ID)
				return chatDetailMsg{detail: detail, err: err}
			}
		}
	}
	return t, nil
}

func (t *chatTab) updateDetail(msg tea.KeyMsg) (tabModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		t.expanded = false
	case "down", "j":
		t.scroll++
	case "up", "k":
		if t.scroll > 0 {
			t.scroll--
		}
	case "pgdown":
		t.scroll += 10
	case "pgup":
		t.scroll = maxInt(t.scroll-10, 0)
	case "home":
		t.scroll = 0
	case "end":
		t.scroll = 1 << 30
	}
	return t, nil
}

func (t *chatTab) View(width, height int) string {
	t.resize(width, height)
	if t.history {
		if !t.loaded {
			return renderPageHeader("Chat history", "loading saved sessions", width) +
				"  " + styleMuted.Render("LOADING  Reading the local state store…")
		}
		if t.expanded {
			return t.viewDetail(width, height)
		}
		return t.viewHistory(width, height)
	}
	return t.viewLive(width, height)
}

func (t *chatTab) viewLive(width, height int) string {
	contextLine := t.contextLine(width)
	header := renderPageHeader("Chat", contextLine, width)

	conversation := t.viewport.View()
	composerWidth := maxInt(width-6, 20)
	if split, mainWidth, paneWidth := liveChatColumns(width); split {
		conversation = lipgloss.JoinHorizontal(
			lipgloss.Top,
			lipgloss.NewStyle().Width(mainWidth).Render(t.viewport.View()),
			" ",
			t.contextPane(paneWidth),
		)
		composerWidth = mainWidth - 2
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString(conversation)
	b.WriteString("\n")
	if selection := t.renderToolSelection(composerWidth); selection != "" {
		b.WriteString(selection)
		b.WriteString("\n")
	}
	if commands := t.renderCommandMenu(composerWidth); commands != "" {
		b.WriteString(commands)
		b.WriteString("\n")
	}
	b.WriteString(t.renderComposer(composerWidth))
	return b.String()
}

const commandMenuLimit = 4

func (t *chatTab) renderCommandMenu(width int) string {
	if !t.commandOpen {
		return ""
	}
	width = maxInt(width, 20)
	var lines []string
	lines = append(lines, "  "+styleSectionTitle.Render("COMMANDS")+"  "+styleMuted.Render("↑↓ select  Enter complete or run  Esc close"))
	if len(t.commandMatches) == 0 {
		lines = append(lines, "  "+styleMuted.Render(truncate("No match. Use // to send a literal leading slash.", width-4)))
		return strings.Join(lines, "\n")
	}
	if t.commandRows <= 0 {
		return strings.Join(lines, "\n")
	}
	start, end := listWindow(t.commandCursor, len(t.commandMatches), minInt(t.commandRows, len(t.commandMatches)))
	for i := start; i < end; i++ {
		command := t.commandMatches[i]
		label := fmt.Sprintf("%-12s %s", chatcmd.Label(command), command.Description)
		lines = append(lines, "  "+renderSelectableRow(truncate(label, width-4), i == t.commandCursor))
	}
	return strings.Join(lines, "\n")
}

func (t *chatTab) commandMenuLines() int {
	if !t.commandOpen {
		return 0
	}
	if len(t.commandMatches) == 0 {
		return 2
	}
	return 1 + minInt(maxInt(t.commandRows, 0), len(t.commandMatches))
}

func (t *chatTab) viewHistory(width, height int) string {
	var b strings.Builder
	b.WriteString(renderPageHeader("Chat history", "saved locally, Ctrl+H returns to live chat", width))
	if t.message != "" {
		b.WriteString("  ")
		b.WriteString(styleWarning.Render("NOTICE  " + t.message))
		b.WriteString("\n\n")
	}
	if len(t.sessions) == 0 {
		b.WriteString(renderEmptyState("No saved conversations", "Return to live chat and send a message to create one.", "ctrl+h", "live chat"))
		return b.String()
	}

	listHeight := maxInt(height-5, 3)
	start, end := listWindow(t.cursor, len(t.sessions), listHeight)
	for i := start; i < end; i++ {
		session := t.sessions[i]
		status := "OPEN"
		if !session.FinishedAt.IsZero() {
			status = strings.ToUpper(firstNonEmptyText(session.ExitReason, "finished"))
		}
		line := fmt.Sprintf("%-14s  %-22s  %3d turns  %s",
			fmtTime(session.StartedAt),
			truncate(session.PinnedModel, 22),
			session.TurnCount,
			truncate(status, 15),
		)
		b.WriteString("  ")
		b.WriteString(renderSelectableRow(truncate(line, maxInt(width-6, 20)), i == t.cursor))
		b.WriteString("\n")
	}
	return b.String()
}

func (t *chatTab) renderComposer(width int) string {
	width = maxInt(width, 20)
	t.composer.SetWidth(maxInt(width-4, 16))
	label := styleSectionTitle.Render("MESSAGE")
	if t.running {
		label = styleMuted.Render("MESSAGE  locked while the agent is working")
	} else if !t.composerFocused {
		label = styleMuted.Render("MESSAGE  press Enter to compose")
	}
	body := t.composer.View()
	if t.lastError != "" {
		body += "\n" + styleError.Render("ERROR  "+truncate(t.lastError, maxInt(width-4, 16)))
	}
	box := lipgloss.NewStyle().
		Width(maxInt(width-2, 18)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorSubtle).
		Padding(0, 1).
		Render(body)
	return "  " + label + "\n  " + strings.ReplaceAll(box, "\n", "\n  ")
}

func (t *chatTab) renderToolSelection(width int) string {
	if len(t.toolCalls) == 0 {
		return ""
	}
	index := minInt(maxInt(t.toolCursor, 0), len(t.toolCalls)-1)
	tool := t.toolCalls[index]
	action := "Space: open"
	if tool.expanded {
		action = "Space: close"
	}
	text := fmt.Sprintf(
		"TOOL %d/%d  %s  |  %s  |  ↑↓ select",
		index+1,
		len(t.toolCalls),
		firstNonEmptyText(tool.name, "tool"),
		action,
	)
	return "  " + styleSelectedRow.Render(truncate(text, maxInt(width-4, 16)))
}

func (t *chatTab) contextLine(width int) string {
	cfgModel := firstNonEmptyText(t.configuredModel, "not configured")
	safety := firstNonEmptyText(t.safety, "unknown")
	if t.session != nil {
		sel := t.session.Selection()
		cfgModel = firstNonEmptyText(sel.Requested.String(), sel.Requested.Model)
	}
	target := firstNonEmptyText(t.target, "runtime host")
	parts := []string{
		"target: " + target,
		"model: " + cfgModel,
		"safety: " + safety,
	}
	line := strings.Join(parts, "  |  ")
	return truncate(line, maxInt(width-12, 20))
}

func (t *chatTab) contextPane(width int) string {
	var lines []string
	lines = append(lines, styleMuted.Render("CONTEXT"))
	lines = append(lines, styleBright.Render(statusIconText(t.status)))
	if t.statusDetail != "" {
		lines = append(lines, styleMuted.Render(strings.Join(wrapText(t.statusDetail, width-2), "\n")))
	}
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("TARGET"))
	lines = append(lines, styleBase.Render(firstNonEmptyText(t.target, "runtime host")))
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("VERIFICATION"))
	lines = append(lines, renderNamedStatus(t.verification))
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("TOOLS"))
	if len(t.toolCalls) == 0 {
		lines = append(lines, styleMuted.Render("No calls this session"))
	} else {
		for _, tool := range t.toolCalls[maxInt(0, len(t.toolCalls)-5):] {
			lines = append(lines, truncate(tool.name+"  "+tool.status, width-2))
		}
	}
	return lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorSubtle).
		PaddingLeft(2).
		Render(strings.Join(lines, "\n"))
}

func (t *chatTab) refreshViewport() {
	var lines []string
	t.toolLineStarts = make([]int, len(t.toolCalls))
	t.toolLineEnds = make([]int, len(t.toolCalls))
	for i := range t.toolLineStarts {
		t.toolLineStarts[i] = -1
		t.toolLineEnds[i] = -1
	}
	if len(t.messages) == 0 {
		lines = append(lines,
			styleBright.Render("  Ready for a task"),
			styleMuted.Render("  Describe the outcome you want. CvkeHarness will keep target, tools,"),
			styleMuted.Render("  approvals, and verification visible while it works."),
			"",
			"  "+renderKeyHint("/help", "commands"),
		)
	}
	renderedTools := make([]bool, len(t.toolCalls))
	for _, message := range t.messages {
		if message.role == "assistant" {
			t.appendToolsForTurn(&lines, message.turn, renderedTools)
			t.appendAssistantResponse(&lines, message.content)
			continue
		}
		label := "CVKEHARNESS"
		labelStyle := styleSectionTitle
		switch message.role {
		case "user":
			label = "YOU"
			labelStyle = styleSuccess
		case "system":
			label = "CONSOLE"
			labelStyle = styleMuted
		case "error":
			label = "ERROR"
			labelStyle = styleError
		}
		lines = append(lines, "  "+labelStyle.Render(label))
		for _, raw := range strings.Split(message.content, "\n") {
			for _, line := range wrapText(raw, maxInt(t.viewport.Width-4, 18)) {
				lines = append(lines, "  "+styleBase.Render(line))
			}
		}
		lines = append(lines, "")
	}
	for i := range t.toolCalls {
		if !renderedTools[i] {
			t.appendToolRow(&lines, i)
		}
	}
	if t.running {
		lines = append(lines, "", "  "+renderNamedStatus(t.status)+"  "+styleMuted.Render(t.statusDetail))
	}
	t.viewport.SetContent(strings.Join(lines, "\n"))
}

func (t *chatTab) appendToolsForTurn(lines *[]string, turn int, rendered []bool) {
	for i, tool := range t.toolCalls {
		if !rendered[i] && tool.turn == turn {
			t.appendToolRow(lines, i)
			rendered[i] = true
		}
	}
}

func (t *chatTab) appendToolRow(lines *[]string, index int) {
	tool := t.toolCalls[index]
	if index >= 0 && index < len(t.toolLineStarts) {
		t.toolLineStarts[index] = len(*lines)
	}
	selector := "  "
	if index == t.toolCursor {
		selector = styleAccent.Render(fmt.Sprintf("▸ %d/%d", index+1, len(t.toolCalls))) + " "
	}
	disclosure := "▸"
	if tool.expanded {
		disclosure = "▾"
	}
	lead := "  " + selector + disclosure + " " + renderNamedStatus(tool.status) + "  " + styleBright.Render(firstNonEmptyText(tool.name, "tool"))
	if tool.command != "" {
		remaining := maxInt(t.viewport.Width-lipgloss.Width(lead)-3, 8)
		lead += styleMuted.Render("  " + truncate(firstLine(tool.command), remaining))
	}
	*lines = append(*lines, lead)
	if tool.expanded {
		appendLiveToolDetail(lines, tool, maxInt(t.viewport.Width-8, 18))
	}
	if index >= 0 && index < len(t.toolLineEnds) {
		t.toolLineEnds[index] = maxInt(len(*lines)-1, t.toolLineStarts[index])
	}
}

func (t *chatTab) ensureSelectedToolVisible(revealDetails bool) {
	if t.toolCursor < 0 || t.toolCursor >= len(t.toolLineStarts) {
		return
	}
	start := t.toolLineStarts[t.toolCursor]
	end := start
	if revealDetails && t.toolCursor < len(t.toolLineEnds) {
		end = minInt(t.toolLineEnds[t.toolCursor], start+4)
	}
	if start < 0 {
		return
	}
	if start < t.viewport.YOffset {
		t.viewport.SetYOffset(start)
		return
	}
	visibleBottom := t.viewport.YOffset + maxInt(t.viewport.Height-1, 0)
	if end > visibleBottom {
		t.viewport.SetYOffset(end - maxInt(t.viewport.Height-1, 0))
	}
}

func (t *chatTab) moveToolSelection(direction int) bool {
	if len(t.toolCalls) == 0 || direction == 0 {
		return false
	}
	t.toolCursor = minInt(maxInt(t.toolCursor, 0), len(t.toolCalls)-1)
	selectedLine := -1
	if t.toolCursor < len(t.toolLineStarts) {
		selectedLine = t.toolLineStarts[t.toolCursor]
	}
	top := t.viewport.YOffset
	bottom := top + maxInt(t.viewport.Height-1, 0)

	// Manual transcript scrolling can leave the cursor far outside the current
	// view. Recover from the visible region in the requested direction instead
	// of advancing the stale cursor and jumping the viewport backwards.
	if direction > 0 && selectedLine < top {
		for i, line := range t.toolLineStarts {
			if line >= top {
				t.toolCursor = i
				return true
			}
		}
		return false
	}
	if direction < 0 && selectedLine > bottom {
		for i := len(t.toolLineStarts) - 1; i >= 0; i-- {
			if t.toolLineStarts[i] <= bottom {
				t.toolCursor = i
				return true
			}
		}
		return false
	}

	next := t.toolCursor + direction
	if next < 0 || next >= len(t.toolCalls) {
		return false
	}
	t.toolCursor = next
	return true
}

// focusSelectedTool frames the selected row and its disclosure with balanced
// transcript context whenever possible, without moving opposite to the key the
// operator pressed.
func (t *chatTab) focusSelectedTool(direction int) {
	if t.toolCursor < 0 || t.toolCursor >= len(t.toolLineStarts) {
		return
	}
	start := t.toolLineStarts[t.toolCursor]
	if start < 0 {
		return
	}
	end := start
	if t.toolCursor < len(t.toolLineEnds) {
		end = maxInt(t.toolLineEnds[t.toolCursor], start)
	}
	height := maxInt(t.viewport.Height, 1)
	blockHeight := end - start + 1
	contextAbove := 1
	if blockHeight < height {
		contextAbove = (height - blockHeight) / 2
	}
	target := maxInt(start-contextAbove, 0)
	if direction > 0 {
		target = maxInt(target, t.viewport.YOffset)
	} else if direction < 0 {
		target = minInt(target, t.viewport.YOffset)
	}
	t.viewport.SetYOffset(target)
}

func (t *chatTab) appendAssistantResponse(lines *[]string, content string) {
	width := maxInt(t.viewport.Width-8, 18)
	body := t.renderedMarkdown(content, width)
	box := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(0, 1).
		Render(body)
	*lines = append(*lines, "", "  "+styleSectionTitle.Render("RESPONSE"), "  "+strings.ReplaceAll(box, "\n", "\n  "), "")
}

func (t *chatTab) renderedMarkdown(content string, width int) string {
	width = maxInt(width, 12)
	if t.markdownWidth != width || t.markdownCache == nil {
		t.markdownWidth = width
		t.markdownCache = make(map[string]string)
	}
	if rendered, ok := t.markdownCache[content]; ok {
		return rendered
	}
	rendered := renderMarkdown(content, width)
	t.markdownCache[content] = rendered
	return rendered
}

func firstLine(text string) string {
	if before, _, ok := strings.Cut(strings.TrimSpace(text), "\n"); ok {
		return before
	}
	return strings.TrimSpace(text)
}

func (t *chatTab) applyRuntimeEvent(event tools.Event) {
	if event.Type == tools.EventMemoryInjected {
		t.memorySources = append([]tools.MemorySource(nil), event.MemorySources...)
		return
	}
	if !isToolRuntimeEvent(event.Type) {
		return
	}
	id := firstNonEmptyText(event.ToolCallID, event.ToolName)
	if id == "" {
		return
	}
	idx := -1
	for i := range t.toolCalls {
		if t.toolCalls[i].id == id && t.toolCalls[i].turn == t.activeTurn {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.toolCalls = append(t.toolCalls, liveToolCall{id: id, name: event.ToolName, status: "RUNNING", turn: t.activeTurn})
		idx = len(t.toolCalls) - 1
		t.toolCursor = idx
	}
	item := &t.toolCalls[idx]
	item.name = firstNonEmptyText(event.ToolName, item.name)
	item.command = firstNonEmptyText(event.Command, item.command)
	switch event.Type {
	case tools.EventToolCallStarted, tools.EventShellCommandStarted:
		item.status = "RUNNING"
		t.status = "TOOL RUNNING"
		t.statusDetail = firstNonEmptyText(item.name, item.command)
	case tools.EventShellApproval:
		item.status = "APPROVAL CHECK"
		t.status = "APPROVAL CHECK"
		t.statusDetail = strings.ReplaceAll(event.ApprovalMode, "_", " ")
	case tools.EventShellOutput:
		item.output = appendBoundedToolOutput(item.output, event.Output)
	case tools.EventToolCallFinished, tools.EventShellCommandFinished:
		item.duration = event.Duration
		item.err = event.ErrorMessage
		if event.Output != "" {
			item.output = boundedToolOutput(event.Output)
		}
		if event.Success {
			item.status = "SUCCEEDED"
		} else {
			item.status = "FAILED"
			item.expanded = true
		}
		t.status = "THINKING"
		t.statusDetail = "tool finished; waiting for the assistant"
	}
}

func (t *chatTab) applyTurnResult(result agent.ChatTurnResult, err error) {
	if result.Target.TargetID != "" {
		t.target = result.Target.TargetID
	} else if result.Target.RuntimeHostID != "" {
		t.target = result.Target.RuntimeHostID
	}

	t.reconcileToolOutcomes(result.Tools)
	t.reconcileToolOutputs(result.Observed)

	switch result.TaskState {
	case state.TaskStateBlockedWaitingUser:
		t.status = "APPROVAL REQUIRED"
		if result.BlockedWorkID != "" {
			t.statusDetail = fmt.Sprintf("%s — run: cvkeharness commands approve-work %s", secrets.Mask(result.ApprovalSummary), result.BlockedWorkID)
		} else {
			t.statusDetail = "work is persisted and remains blocked until explicitly approved"
		}
	case state.TaskStateCompleted:
		t.status = "READY"
		t.statusDetail = "turn completed"
	default:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.status = "FAILED"
		} else if errors.Is(err, context.Canceled) || t.stopping {
			t.status = "INTERRUPTED"
			t.statusDetail = "the active turn was canceled; the session remains available"
		} else {
			t.status = "READY"
		}
	}

	if result.Verification.Status != "" {
		t.verification = strings.ToUpper(result.Verification.Status)
	} else if result.TaskState == state.TaskStateCompleted {
		t.verification = "NOT REPORTED"
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		t.lastError = err.Error()
		t.messages = append(t.messages, liveChatMessage{role: "error", content: err.Error(), at: time.Now(), turn: t.activeTurn})
	}
	if result.CurationError != nil {
		t.messages = append(t.messages, liveChatMessage{
			role:    "system",
			content: "Memory curation warning: " + result.CurationError.Error(),
			at:      time.Now(),
			turn:    t.activeTurn,
		})
	}
	if strings.TrimSpace(result.Output) != "" {
		t.messages = append(t.messages, liveChatMessage{role: "assistant", content: result.Output, at: time.Now(), turn: t.activeTurn})
	}
}

func isToolRuntimeEvent(eventType tools.EventType) bool {
	switch eventType {
	case tools.EventToolCallStarted,
		tools.EventToolCallFinished,
		tools.EventShellCommandStarted,
		tools.EventShellApproval,
		tools.EventShellOutput,
		tools.EventShellCommandFinished:
		return true
	default:
		return false
	}
}

func (t *chatTab) reconcileToolOutcomes(outcomes []state.ToolOutcome) {
	claimed := make(map[int]bool, len(outcomes))
	for _, outcome := range outcomes {
		command := firstNonEmptyText(outcome.Command, outcome.Arguments)
		idx := -1
		for i := len(t.toolCalls) - 1; i >= 0; i-- {
			item := t.toolCalls[i]
			if claimed[i] || item.turn != t.activeTurn || item.name != outcome.ToolName {
				continue
			}
			if command == "" || item.command == command {
				idx = i
				break
			}
		}
		if idx < 0 {
			for i := len(t.toolCalls) - 1; i >= 0; i-- {
				item := t.toolCalls[i]
				if claimed[i] || item.turn != t.activeTurn || item.name != outcome.ToolName {
					continue
				}
				if item.status == "RUNNING" || item.status == "APPROVAL CHECK" || item.command == "" {
					idx = i
					break
				}
			}
		}

		status := "SUCCEEDED"
		if outcome.PolicyDenied {
			status = "DENIED"
		} else if !outcome.Success {
			status = "FAILED"
		}
		if idx < 0 {
			t.toolCalls = append(t.toolCalls, liveToolCall{
				id:      outcome.ToolName + command,
				name:    outcome.ToolName,
				command: command,
				turn:    t.activeTurn,
			})
			idx = len(t.toolCalls) - 1
			t.toolCursor = idx
		}

		item := &t.toolCalls[idx]
		if item.command == "" {
			item.command = command
		}
		item.status = status
		item.err = outcome.ErrorMessage
		item.duration = time.Duration(outcome.DurationMs) * time.Millisecond
		item.expanded = status != "SUCCEEDED"
		claimed[idx] = true
	}
}

func (t *chatTab) reconcileToolOutputs(observed []memory.ObservedToolCall) {
	claimed := make(map[int]bool, len(observed))
	for _, call := range observed {
		for i := range t.toolCalls {
			item := &t.toolCalls[i]
			if claimed[i] || item.turn != t.activeTurn || item.name != call.ToolName {
				continue
			}
			if call.Command != "" && item.command != "" && item.command != call.Command {
				continue
			}
			item.output = boundedToolOutput(call.Result)
			claimed[i] = true
			break
		}
	}
}

func drainRuntimeEvents(ch <-chan tools.Event) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func (t *chatTab) applyPendingRuntimeEvents() {
	for {
		select {
		case event, ok := <-t.eventCh:
			if !ok {
				return
			}
			t.applyRuntimeEvent(event)
		default:
			return
		}
	}
}

func commandHelpText() string {
	var lines []string
	lines = append(lines, "Commands")
	for _, command := range chatcmd.Available(chatcmd.TUI) {
		lines = append(lines, fmt.Sprintf("%-15s %s", chatcmd.Label(command), command.Description))
	}
	lines = append(lines, "", "Type / to autocomplete. Prefix with // to send a literal leading slash.")
	return strings.Join(lines, "\n")
}

func memoryCommandText(sources []tools.MemorySource) string {
	if len(sources) == 0 {
		return "Memory\nNo memory has been retrieved yet. Send a task first; /memory reports the latest model call."
	}
	lines := []string{"Memory used by the latest model call"}
	for _, source := range sources {
		label := strings.TrimSpace(source.Name)
		if source.Origin != "" {
			label += " (" + strings.TrimSpace(source.Origin) + ")"
		}
		line := fmt.Sprintf("- %s, %d chars", label, source.Chars)
		if preview := strings.TrimSpace(secrets.Mask(source.Preview)); preview != "" {
			line += ": " + preview
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func toolsCommandText(items []agent.ChatTool, safetyMode string) string {
	safetyMode = strings.TrimSpace(safetyMode)
	if safetyMode == "" {
		safetyMode = "unknown"
	}
	lines := []string{
		"Registered tools",
		"Availability is not authorization. Task filtering, target binding, policy, and approvals still apply.",
		"Security policy: " + strings.ReplaceAll(safetyMode, "_", " "),
	}
	if len(items) == 0 {
		return strings.Join(append(lines, "No tools are registered for this runtime."), "\n")
	}
	for _, item := range items {
		lines = append(lines, "- "+item.Name+": "+compactChatToolDescription(item.Description))
	}
	return strings.Join(lines, "\n")
}

func compactChatToolDescription(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	if value == "" {
		return "No description available"
	}
	if idx := strings.Index(value, ". "); idx >= 0 {
		value = value[:idx+1]
	}
	return truncate(value, 180)
}

func (t *chatTab) resize(width, height int) {
	if !t.controlsReady {
		fresh := newChatTab().(*chatTab)
		t.composer = fresh.composer
		t.viewport = fresh.viewport
		t.eventCh = fresh.eventCh
		t.controlsReady = true
	}
	contentWidth := maxInt(width-4, 20)
	if split, mainWidth, _ := liveChatColumns(width); split {
		contentWidth = maxInt(mainWidth-2, 20)
	}
	t.viewport.Width = contentWidth
	composerLines := 7
	headerLines := 4
	selectionLines := 0
	if len(t.toolCalls) > 0 {
		selectionLines = 1
	}
	minimumViewport := 5
	t.commandRows = commandMenuLimit
	if t.commandOpen {
		minimumViewport = 3
		availableRows := height - composerLines - headerLines - selectionLines - minimumViewport - 1
		t.commandRows = clamp(availableRows, 0, commandMenuLimit)
	}
	t.viewport.Height = maxInt(height-composerLines-headerLines-selectionLines-t.commandMenuLines(), minimumViewport)
	t.composer.SetWidth(maxInt(contentWidth-4, 16))
	t.refreshViewport()
}

func liveChatColumns(width int) (split bool, mainWidth, paneWidth int) {
	if width < 120 {
		return false, width, 0
	}
	paneWidth = 29
	mainWidth = maxInt(width-paneWidth-3, 50)
	return true, mainWidth, paneWidth
}

func (t *chatTab) closeSession(reason string) {
	if t.cancelTurn != nil {
		t.cancelTurn()
		t.cancelTurn = nil
	}
	if t.eventWaitStop != nil {
		close(t.eventWaitStop)
		t.eventWaitStop = nil
	}
	if t.session != nil {
		t.session.Close(context.Background(), reason)
		t.session = nil
	}
}

func startLiveChatCmd(svc *Service, observer tools.EventObserver) tea.Cmd {
	return func() tea.Msg {
		session, err := svc.StartChat(context.Background(), observer)
		return chatSessionReadyMsg{session: session, err: err}
	}
}

func exportChatSessionCmd(svc *Service, sessionID int64) tea.Cmd {
	return func() tea.Msg {
		if svc == nil {
			return chatExportDoneMsg{err: fmt.Errorf("chat export service is unavailable")}
		}
		path, err := svc.ExportChatSession(context.Background(), sessionID)
		return chatExportDoneMsg{path: path, err: err}
	}
}

func runChatTurnCmd(ctx context.Context, session LiveChatSession, prompt string) tea.Cmd {
	return func() tea.Msg {
		result, err := session.Turn(ctx, prompt)
		return chatTurnDoneMsg{prompt: prompt, result: result, err: err}
	}
}

func waitChatEventCmd(ch <-chan tools.Event, stop <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-ch:
			return chatRuntimeEventMsg{event: event}
		case <-stop:
			return chatRuntimeEventWaitStoppedMsg{}
		}
	}
}

func loadChatData(svc *Service) chatDataMsg {
	if svc == nil {
		return chatDataMsg{err: fmt.Errorf("dashboard service is unavailable")}
	}
	sessions, err := svc.RecentChatSessions(context.Background(), 50)
	return chatDataMsg{sessions: sessions, err: err}
}

func classifyChatStartError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "unsupported provider"):
		return "Provider unavailable: " + text
	case strings.Contains(lower, "api key"), strings.Contains(lower, "credential"), strings.Contains(lower, "auth"):
		return "Missing or invalid credentials: " + text
	case strings.Contains(lower, "connection"), strings.Contains(lower, "offline"):
		return "Provider appears offline: " + text
	default:
		return text
	}
}

func appendLiveToolDetail(lines *[]string, tool liveToolCall, width int) {
	width = maxInt(width, 18)
	contentWidth := maxInt(width-2, 14)
	var body []string
	if tool.command != "" {
		body = append(body, styleMuted.Render("COMMAND"))
		for _, raw := range strings.Split(sanitizeToolOutput(tool.command), "\n") {
			for _, line := range wrapRawLine(raw, contentWidth) {
				body = append(body, styleBright.Render(line))
			}
		}
	}
	if len(body) > 0 {
		body = append(body, "")
	}
	body = append(body, styleMuted.Render("RAW OUTPUT  stdout + stderr"))
	output := strings.TrimSuffix(sanitizeToolOutput(tool.output), "\n")
	if output == "" {
		placeholder := "No output emitted."
		if tool.status == "RUNNING" || tool.status == "APPROVAL CHECK" {
			placeholder = "Waiting for output…"
		}
		body = append(body, styleMuted.Render(placeholder))
	} else {
		for _, raw := range strings.Split(output, "\n") {
			for _, line := range wrapRawLine(raw, contentWidth) {
				body = append(body, styleBase.Render(line))
			}
		}
	}
	if tool.err != "" {
		body = append(body, "", styleError.Render("ERROR"))
		for _, line := range wrapRawLine(sanitizeToolOutput(tool.err), contentWidth) {
			body = append(body, styleError.Render(line))
		}
	}
	if tool.duration > 0 {
		body = append(body, "", styleMuted.Render("DURATION  "+tool.duration.Round(time.Millisecond).String()))
	}
	box := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorSubtle).
		Background(colorSurface).
		Padding(0, 1).
		Render(strings.Join(body, "\n"))
	*lines = append(*lines, "    "+strings.ReplaceAll(box, "\n", "\n    "))
}

const (
	maxLiveToolOutputRunes = 64 * 1024
	toolOutputTruncated    = "\n… (live output truncated)"
)

func appendBoundedToolOutput(current, addition string) string {
	if strings.HasSuffix(current, toolOutputTruncated) {
		return current
	}
	return boundedToolOutput(current + addition)
}

func boundedToolOutput(output string) string {
	runes := []rune(output)
	if len(runes) <= maxLiveToolOutputRunes {
		return output
	}
	return string(runes[:maxLiveToolOutputRunes]) + toolOutputTruncated
}

func sanitizeToolOutput(output string) string {
	output = ansi.Strip(output)
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	output = strings.ReplaceAll(output, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, output)
}

func wrapRawLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	runes := []rune(line)
	if len(runes) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	return append(lines, string(runes))
}

func renderNamedStatus(status string) string {
	status = strings.ToUpper(strings.TrimSpace(status))
	switch status {
	case "SUCCEEDED", "READY", "VERIFIED", "COMPLETE", "COMPLETED":
		return styleSuccess.Render("✓ " + status)
	case "FAILED", "DENIED", "UNAVAILABLE", "INTERRUPTED":
		return styleError.Render("! " + status)
	case "APPROVAL REQUIRED", "APPROVAL CHECK", "PENDING", "INTERRUPTING":
		return styleWarning.Render("! " + status)
	default:
		return styleAccent.Render("• " + firstNonEmptyText(status, "WORKING"))
	}
}

func statusIconText(status string) string {
	status = strings.ToUpper(strings.TrimSpace(status))
	switch status {
	case "READY", "SUCCEEDED", "VERIFIED":
		return "✓ " + status
	case "FAILED", "UNAVAILABLE", "INTERRUPTED":
		return "! " + status
	default:
		return "• " + firstNonEmptyText(status, "WORKING")
	}
}

func (t *chatTab) viewDetail(width, height int) string {
	session := t.detail.Session
	col := maxInt(width-4, 20)
	contentWidth := maxInt(width-8, 20)
	messagesByTurn := chatMessagesByTurn(t.detail.Messages)
	var lines []string
	lines = append(lines, "")
	lines = append(lines, "  "+styleSectionTitle.Render(fmt.Sprintf("Chat Session #%d", session.ID)))
	lines = append(lines, "")
	lines = append(lines, "  "+renderKeyValue("Model", session.PinnedModel))
	lines = append(lines, "  "+renderKeyValue("Provider", session.Provider))
	lines = append(lines, "  "+renderKeyValue("Started", fmtTime(session.StartedAt)))
	if !session.StartedAt.IsZero() && !session.FinishedAt.IsZero() {
		lines = append(lines, "  "+renderKeyValue("Duration", fmtDuration(session.FinishedAt.Sub(session.StartedAt))))
	}
	lines = append(lines, "  "+renderKeyValue("Turns", fmt.Sprintf("%d", session.TurnCount)))
	lines = append(lines, "  "+renderKeyValue("Exit", session.ExitReason))
	if len(t.detail.Turns) == 0 {
		lines = append(lines, "", "  "+styleMuted.Render("No persisted turns in this session."))
	}
	for _, turn := range t.detail.Turns {
		lines = append(lines, "", "  "+horizontalRule(col))
		header := fmt.Sprintf("#%d  %s  %s  %s tokens",
			turn.TurnIndex+1,
			statusIcon(turn.Success),
			fmtDurationMs(turn.LatencyMs),
			formatTokens(turn.TotalTokens),
		)
		lines = append(lines, "  "+header)
		if meta := chatTurnMeta(turn); meta != "" {
			lines = append(lines, "  "+styleMuted.Render(meta))
		}
		appendWrappedBlock(&lines, "  ", "You:", turn.UserInput, contentWidth, styleSuccess, styleBase)
		t.appendPersistedAssistantMarkdown(&lines, turn.FinalOutput, contentWidth)
		appendWrappedBlock(&lines, "  ", "Error:", turn.ErrorMessage, contentWidth, styleError, styleError)
		appendVerificationLines(&lines, turn, contentWidth)
		appendToolOutcomeLines(&lines, t.detail.ToolsByTurnID[turn.ID], contentWidth)
		appendTranscriptToolLines(&lines, messagesByTurn[turn.ID], contentWidth)
		if turn.FinalOutput == "" && turn.ErrorMessage == "" && turn.VerificationStatus == "" && len(t.detail.ToolsByTurnID[turn.ID]) == 0 && len(chatToolMessages(messagesByTurn[turn.ID])) == 0 {
			lines = append(lines, "  "+styleSubtle.Render("No assistant output, verification, or tool detail was persisted for this turn."))
		}
	}
	maxScroll := maxInt(len(lines)-height+2, 0)
	t.scroll = clamp(t.scroll, 0, maxScroll)
	end := minInt(t.scroll+height-1, len(lines))
	return strings.Join(lines[t.scroll:end], "\n")
}

func (t *chatTab) appendPersistedAssistantMarkdown(lines *[]string, content string, width int) {
	if strings.TrimSpace(content) == "" {
		return
	}
	*lines = append(*lines, "  "+styleSectionTitle.Render("AI:"))
	rendered := t.renderedMarkdown(content, maxInt(width-4, 12))
	for _, line := range strings.Split(rendered, "\n") {
		*lines = append(*lines, "    "+line)
	}
}

func chatTurnMeta(turn state.ChatTurn) string {
	var parts []string
	if turn.TaskClass != "" {
		parts = append(parts, fmt.Sprintf("task=%s", turn.TaskClass))
	}
	if turn.RequestedModel != "" {
		model := turn.RequestedModel
		if turn.ActualModel != "" && turn.ActualModel != turn.RequestedModel {
			model += " -> " + turn.ActualModel
		}
		parts = append(parts, "model="+model)
	}
	if turn.PromptTokens > 0 || turn.CompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("prompt=%s completion=%s", formatTokens(turn.PromptTokens), formatTokens(turn.CompletionTokens)))
	}
	return strings.Join(parts, "  ")
}

func appendVerificationLines(lines *[]string, turn state.ChatTurn, width int) {
	if turn.VerificationStatus == "" && turn.VerificationReason == "" && turn.VerificationMissingActions == "" && !turn.VerificationRepairTriggered {
		return
	}
	status := firstNonEmptyText(turn.VerificationStatus, "unknown")
	if turn.VerificationRepairTriggered {
		status += " repair-triggered"
	}
	*lines = append(*lines, "  "+styleMuted.Render("Verification: ")+styleBase.Render(status))
	appendWrappedBlock(lines, "    ", "Reason:", turn.VerificationReason, width-2, styleMuted, styleBase)
	appendWrappedBlock(lines, "    ", "Missing:", turn.VerificationMissingActions, width-2, styleWarning, styleBase)
}

func appendToolOutcomeLines(lines *[]string, outcomes []state.ToolOutcome, width int) {
	if len(outcomes) == 0 {
		return
	}
	*lines = append(*lines, "  "+styleMuted.Render(fmt.Sprintf("Tools: %d persisted outcome(s)", len(outcomes))))
	for i, outcome := range outcomes {
		status := "ok"
		statusStyle := styleSuccess
		if outcome.PolicyDenied {
			status, statusStyle = "denied", styleWarning
		} else if !outcome.Success {
			status, statusStyle = "failed", styleError
		}
		*lines = append(*lines, fmt.Sprintf("    %d. %s %s  %s", i+1, outcome.ToolName, statusStyle.Render(status), fmtDurationMs(outcome.DurationMs)))
		appendWrappedBlock(lines, "      ", "Command:", firstNonEmptyText(outcome.Command, outcome.Arguments), width-6, styleMuted, styleBase)
		appendWrappedBlock(lines, "      ", "Args:", outcome.Arguments, width-6, styleMuted, styleBase)
		appendWrappedBlock(lines, "      ", "Error:", outcome.ErrorMessage, width-6, styleError, styleError)
		if outcome.DenialClass != "" {
			*lines = append(*lines, "      "+styleWarning.Render("Denial: ")+styleBase.Render(outcome.DenialClass))
		}
	}
}

func appendTranscriptToolLines(lines *[]string, messages []state.ChatMessage, width int) {
	toolMessages := chatToolMessages(messages)
	if len(toolMessages) == 0 {
		return
	}
	*lines = append(*lines, "  "+styleMuted.Render("Transcript tool evidence:"))
	for _, message := range toolMessages {
		appendWrappedBlock(lines, "    ", "Tool call:", message.ToolName+" "+message.ToolArguments, width-4, styleMuted, styleBase)
		appendWrappedBlock(lines, "    ", "Tool calls:", message.ToolCallsJSON, width-4, styleMuted, styleBase)
		appendWrappedBlock(lines, "    ", "Tool result:", message.Content, width-4, styleMuted, styleBase)
	}
}

func chatMessagesByTurn(messages []state.ChatMessage) map[int64][]state.ChatMessage {
	out := make(map[int64][]state.ChatMessage)
	for _, message := range messages {
		out[message.TurnID] = append(out[message.TurnID], message)
	}
	return out
}

func chatToolMessages(messages []state.ChatMessage) []state.ChatMessage {
	var out []state.ChatMessage
	for _, message := range messages {
		if message.Role == "tool" || message.ToolName != "" || message.ToolArguments != "" || message.ToolCallsJSON != "" {
			out = append(out, message)
		}
	}
	return out
}

func appendWrappedBlock(lines *[]string, indent, label, text string, width int, labelStyle, bodyStyle lipgloss.Style) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	labelWidth := len(label) + 1
	bodyWidth := maxInt(width-labelWidth, 12)
	first := true
	for _, raw := range strings.Split(text, "\n") {
		for _, line := range wrapText(raw, bodyWidth) {
			if first {
				*lines = append(*lines, indent+labelStyle.Render(label+" ")+bodyStyle.Render(line))
				first = false
			} else {
				*lines = append(*lines, indent+strings.Repeat(" ", labelWidth)+bodyStyle.Render(line))
			}
		}
	}
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
