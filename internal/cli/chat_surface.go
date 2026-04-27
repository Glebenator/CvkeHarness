package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
	"golang.org/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SessionSummary captures the close-out stats for an interactive chat session.
type SessionSummary struct {
	Duration          time.Duration
	TurnCount         int
	ExitReason        string
	ModelsUsed        []string
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	CachedTokens      int
	CachedTokensKnown bool
	ToolCalls         int
	SuccessfulTools   int
	FailedTools       int
}

// ChatSurface renders a richer interactive chat experience in the terminal.
type ChatSurface struct {
	out   io.Writer
	width int
	rich  bool

	mu          sync.Mutex
	shells      map[string]*shellRenderState
	logPending  string
	statusRun   bool
	statusLabel string
	statusInfo  string
	statusSince time.Time
	statusFrame int
	statusStop  chan struct{}
}

// NewChatSurface creates a terminal chat renderer.
func NewChatSurface(out io.Writer) *ChatSurface {
	if out == nil {
		out = os.Stdout
	}

	surface := &ChatSurface{
		out:    out,
		width:  96,
		shells: make(map[string]*shellRenderState),
	}

	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		surface.width = clampWidth(terminalWidth(int(file.Fd()))-6, 64, 110)
		if strings.TrimSpace(os.Getenv("NO_COLOR")) == "" && !strings.EqualFold(os.Getenv("TERM"), "dumb") {
			surface.rich = true
		}
	}

	return surface
}

// Write renders structured logs in a subdued lane below the chat transcript.
func (c *ChatSurface) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	text := c.logPending + normalizeChunk(string(p))
	lines := strings.Split(text, "\n")
	c.logPending = lines[len(lines)-1]

	for _, line := range lines[:len(lines)-1] {
		line = simplifyLogLine(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		c.writeLinesLocked(c.renderNoteLines("log", termui.FGMuted, []string{line}))
	}

	return len(p), nil
}

// Observe updates the live thinking lane as tools begin and finish work.
func (c *ChatSurface) Observe(event tools.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch event.Type {
	case tools.EventMemoryInjected:
		if c.statusRun {
			c.statusLabel = "Injecting memory"
			c.statusInfo = truncateRunes(strings.TrimSpace(event.Output), c.width-28)
		}
		if strings.TrimSpace(event.Output) != "" {
			c.writeLinesLocked(c.renderNoteLines("memory", termui.FGMuted, []string{event.Output}))
		}
	case tools.EventToolCallStarted:
		if event.ToolName == "shell_execute" {
			if c.statusRun {
				c.statusLabel = "Running shell command"
				c.statusInfo = ""
			}
			return
		}
		if c.statusRun {
			c.statusLabel = "Using " + humanizeToolName(event.ToolName)
			c.statusInfo = ""
		}
	case tools.EventToolCallFinished:
		if c.statusRun {
			c.statusLabel = "Thinking through results"
			c.statusInfo = ""
		}
	case tools.EventShellCommandStarted:
		if c.statusRun {
			c.statusLabel = "Running shell command"
			c.statusInfo = truncateRunes(strings.TrimSpace(event.Command), c.width-28)
		}
		c.writeLinesLocked(c.renderShellHeaderLines(event.Command))
	case tools.EventShellApproval:
		if c.statusRun {
			c.statusLabel = "Waiting for shell approval"
			c.statusInfo = strings.ReplaceAll(strings.TrimSpace(event.ApprovalMode), "_", " ")
		}
		if event.ApprovalMode != "" && event.ApprovalMode != "allowlist" {
			c.writeLinesLocked([]string{c.renderShellMetaLine("approval", strings.ReplaceAll(strings.TrimSpace(event.ApprovalMode), "_", " "))})
		}
	case tools.EventShellOutput:
		if strings.TrimSpace(event.Output) != "" {
			if c.statusRun {
				c.statusLabel = "Reading command output"
			}
		}
		state := c.shellState(event)
		c.writeShellOutputLocked(state, event.Output)
	case tools.EventShellCommandFinished:
		state := c.shellState(event)
		c.flushShellOutputLocked(state)
		if event.Success {
			if c.statusRun {
				c.statusLabel = "Integrating tool results"
				c.statusInfo = fmt.Sprintf("exit %s in %s", formatExitCode(event), formatDuration(event.Duration))
			}
			c.writeLinesLocked([]string{c.renderShellStatusLine("done", fmt.Sprintf("exit %s in %s", formatExitCode(event), formatDuration(event.Duration)))})
			return
		}
		if c.statusRun {
			c.statusLabel = "Tool reported an error"
			c.statusInfo = compactError("shell", summarizeShellError(event.ErrorMessage))
		}
		c.writeLinesLocked([]string{c.renderShellStatusLine("fail", compactError(fmt.Sprintf("exit %s in %s", formatExitCode(event), formatDuration(event.Duration)), summarizeShellError(event.ErrorMessage)))})
	}
}

// RenderBanner draws the session header.
func (c *ChatSurface) RenderBanner(selection core.RoutingSelection) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.rich {
		fmt.Fprint(c.out, termui.ClearScreen)
		lines := []string{
			termui.FGWhite + termui.ANSIBold + "CvkeHarness" + termui.ANSIReset + "  " + termui.FGMuted + "interactive chat workspace" + termui.ANSIReset,
			renderStatusBadges(
				renderBadge("model", selection.Requested.String(), termui.FGAccent),
				renderBadge("commands", "/help  /clear  /exit", termui.FGGreen),
			),
		}
		if reason := strings.TrimSpace(selection.Reason); reason != "" {
			lines = append(lines, termui.FGMuted+"routing"+termui.ANSIReset+" "+truncateRunes(reason, c.width-14))
		}
		c.writeLinesLocked(c.renderPanelLines("Session", termui.FGAccent, lines))
		return
	}

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "CvkeHarness chat")
	fmt.Fprintf(c.out, "Pinned model: %s\n", selection.Requested.String())
	fmt.Fprintln(c.out, "Commands: /help, /clear, /exit")
	if reason := strings.TrimSpace(selection.Reason); reason != "" {
		fmt.Fprintf(c.out, "Reason: %s\n", reason)
	}
}

// PrintHelp renders the available slash commands.
func (c *ChatSurface) PrintHelp() {
	c.printNote("Commands", termui.FGGreen, []string{
		"/help  Show the available chat commands",
		"/clear Start a fresh in-process chat session",
		"/exit  End chat",
	})
}

// PrintInfo renders a compact informational note.
func (c *ChatSurface) PrintInfo(title string, lines []string) {
	c.printNote(title, termui.FGAccent, lines)
}

// PrintError renders a compact error note.
func (c *ChatSurface) PrintError(title string, lines []string) {
	c.printNote(title, termui.FGRed, lines)
}

// PrintUser renders a user-authored chat turn.
func (c *ChatSurface) PrintUser(text string) {
	c.printMessage("You", termui.FGGreen, text, nil)
}

// PrintAssistant renders the assistant response and turn metadata.
func (c *ChatSurface) PrintAssistant(text string, phase state.PhaseRecord, toolCount int) {
	meta := assistantMetaLines(phase, toolCount, c.width-6)
	c.printMessage("Assistant", termui.FGAccent, text, meta)
}

// PrintSessionSummary renders a compact end-of-session summary.
func (c *ChatSurface) PrintSessionSummary(summary SessionSummary) {
	c.printNote("Session Summary", termui.FGAccent, summaryLines(summary))
}

// PrintRunSummary renders a compact end-of-run summary.
func (c *ChatSurface) PrintRunSummary(summary SessionSummary) {
	c.printNote("Run Summary", termui.FGAccent, summaryLines(summary))
}

// StartThinking starts the live status spinner for an assistant turn.
func (c *ChatSurface) StartThinking() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.statusLabel = "Thinking"
	c.statusInfo = "waiting for the model"
	c.statusSince = time.Now()

	if !c.rich {
		return
	}

	if c.statusRun {
		c.renderStatusLocked()
		return
	}

	c.statusRun = true
	c.statusFrame = 0
	c.statusStop = make(chan struct{})
	c.renderStatusLocked()

	stop := c.statusStop
	go c.runSpinner(stop)
}

// StopThinking clears the live status line.
func (c *ChatSurface) StopThinking() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.rich {
		return
	}

	if c.statusRun && c.statusStop != nil {
		close(c.statusStop)
		c.statusStop = nil
	}
	c.statusRun = false
	c.statusLabel = ""
	c.statusInfo = ""
	c.clearStatusLocked()
}

// Prompt returns the styled prompt prefix for the next user turn.
func (c *ChatSurface) Prompt() string {
	if !c.rich {
		return "\nYou> "
	}
	return "\n  " + termui.FGGreen + termui.ANSIBold + "You" + termui.ANSIReset + termui.FGMuted + " ▸ " + termui.ANSIReset
}

func (c *ChatSurface) runSpinner(stop chan struct{}) {
	ticker := time.NewTicker(110 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			if !c.statusRun || c.statusStop != stop {
				c.mu.Unlock()
				return
			}
			c.statusFrame = (c.statusFrame + 1) % len(spinnerFrames)
			c.renderStatusLocked()
			c.mu.Unlock()
		case <-stop:
			return
		}
	}
}

func (c *ChatSurface) printMessage(title, tone, body string, meta []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.rich {
		fmt.Fprintf(c.out, "\n%s\n", title)
		for _, line := range meta {
			fmt.Fprintln(c.out, stripANSI(line))
		}
		for _, line := range renderBodyLines(body, c.width-4) {
			fmt.Fprintln(c.out, stripANSI(line))
		}
		return
	}

	lines := append([]string{}, meta...)
	if len(lines) > 0 && strings.TrimSpace(body) != "" {
		lines = append(lines, "")
	}
	lines = append(lines, renderBodyLines(body, c.width-6)...)
	c.writeLinesLocked(c.renderPanelLines(title, tone, lines))
}

func (c *ChatSurface) printNote(title, tone string, lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeLinesLocked(c.renderNoteLines(title, tone, lines))
}

func (c *ChatSurface) renderPanelLines(title, tone string, lines []string) []string {
	if !c.rich {
		out := []string{title}
		out = append(out, lines...)
		return out
	}

	width := c.width
	label := truncateRunes(title, width-6)
	head := "  " + tone + termui.ANSIBold + label + termui.ANSIReset
	if pad := width - visibleRuneLen(label) - 5; pad > 0 {
		head += " " + termui.FGSubtle + strings.Repeat("─", pad) + termui.ANSIReset
	}

	out := []string{"", head}
	for _, line := range lines {
		if strings.TrimSpace(stripANSI(line)) == "" {
			out = append(out, "")
			continue
		}
		for _, wrapped := range wrapText(line, width-7) {
			out = append(out, "  "+termui.FGMuted+"│"+termui.ANSIReset+" "+wrapped)
		}
	}
	return out
}

func (c *ChatSurface) renderNoteLines(title, tone string, lines []string) []string {
	if !c.rich {
		out := []string{title + ":"}
		for _, line := range lines {
			out = append(out, "  "+line)
		}
		return out
	}

	width := c.width
	header := "  " + tone + termui.ANSIBold + title + termui.ANSIReset
	out := []string{header}
	for _, line := range lines {
		for _, wrapped := range wrapText(strings.TrimSpace(line), width-6) {
			out = append(out, "  "+termui.FGMuted+"│"+termui.ANSIReset+" "+wrapped)
		}
	}
	if len(lines) == 0 {
		out = append(out, "  "+termui.FGMuted+"│"+termui.ANSIReset)
	}
	return out
}

func (c *ChatSurface) writeLinesLocked(lines []string) {
	for _, line := range lines {
		if c.statusRun {
			c.clearStatusLocked()
		}
		fmt.Fprintln(c.out, line)
	}
	if c.statusRun {
		c.renderStatusLocked()
	}
}

func (c *ChatSurface) shellState(event tools.Event) *shellRenderState {
	key := event.ToolCallID
	if key == "" {
		key = event.ToolName
	}
	state, ok := c.shells[key]
	if !ok {
		state = &shellRenderState{}
		c.shells[key] = state
	}
	return state
}

func (c *ChatSurface) writeShellOutputLocked(state *shellRenderState, chunk string) {
	if chunk == "" {
		return
	}

	text := state.pending + normalizeChunk(chunk)
	lines := strings.Split(text, "\n")
	state.pending = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		c.writeLinesLocked([]string{c.renderShellOutputLine(line)})
	}
}

func (c *ChatSurface) flushShellOutputLocked(state *shellRenderState) {
	if state.pending == "" {
		return
	}
	c.writeLinesLocked([]string{c.renderShellOutputLine(state.pending)})
	state.pending = ""
}

func (c *ChatSurface) renderShellHeaderLines(command string) []string {
	command = strings.TrimSpace(command)
	if !c.rich {
		return []string{
			"Shell:",
			"  $ " + command,
		}
	}
	return []string{
		"  " + termui.FGAccent + termui.ANSIBold + "Shell" + termui.ANSIReset,
		"  " + termui.FGMuted + "│" + termui.ANSIReset + " " + termui.FGAccent + termui.ANSIBold + "$" + termui.ANSIReset + " " + command,
	}
}

func (c *ChatSurface) renderShellMetaLine(label, value string) string {
	if !c.rich {
		return "  " + label + ": " + value
	}
	return "  " + termui.FGMuted + "│" + termui.ANSIReset + " " + termui.FGMuted + label + ":" + termui.ANSIReset + " " + value
}

func (c *ChatSurface) renderShellOutputLine(line string) string {
	if !c.rich {
		return "  | " + line
	}
	return "  " + termui.FGMuted + "│" + termui.ANSIReset + " " + line
}

func (c *ChatSurface) renderShellStatusLine(kind, message string) string {
	if !c.rich {
		return "  " + kind + ": " + message
	}
	tone := termui.FGGreen
	if kind == "fail" {
		tone = termui.FGRed
	}
	return "  " + termui.FGMuted + "│" + termui.ANSIReset + " " + tone + kind + ":" + termui.ANSIReset + " " + message
}

func (c *ChatSurface) renderStatusLocked() {
	if !c.rich || !c.statusRun {
		return
	}

	frame := spinnerFrames[c.statusFrame%len(spinnerFrames)]
	label := termui.FGAccent + termui.ANSIBold + frame + termui.ANSIReset + " " + termui.FGWhite + termui.ANSIBold + c.statusLabel + termui.ANSIReset
	elapsed := termui.FGMuted + formatDuration(time.Since(c.statusSince)) + termui.ANSIReset
	info := ""
	if strings.TrimSpace(c.statusInfo) != "" {
		info = "  " + termui.FGMuted + truncateRunes(c.statusInfo, c.width-28) + termui.ANSIReset
	}
	fmt.Fprintf(c.out, "\r\033[2K  %s  %s%s", label, elapsed, info)
}

func (c *ChatSurface) clearStatusLocked() {
	if !c.rich {
		return
	}
	fmt.Fprint(c.out, "\r\033[2K")
}

func renderStatusBadges(badges ...string) string {
	parts := make([]string, 0, len(badges))
	for _, badge := range badges {
		if strings.TrimSpace(stripANSI(badge)) == "" {
			continue
		}
		parts = append(parts, badge)
	}
	return strings.Join(parts, "  ")
}

func renderBadge(label, value, tone string) string {
	return termui.FGMuted + label + " " + termui.ANSIReset + tone + value + termui.ANSIReset
}

func renderBodyLines(text string, width int) []string {
	text = normalizeChunk(text)
	if strings.TrimSpace(text) == "" {
		return []string{termui.FGMuted + "No content." + termui.ANSIReset}
	}

	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		trimmedRight := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(trimmedRight) == "" {
			lines = append(lines, "")
			continue
		}

		trimmed := strings.TrimSpace(trimmedRight)
		if strings.HasPrefix(trimmed, "```") {
			lines = append(lines, trimmed)
			continue
		}

		wrapped := wrapText(trimmed, width)
		if len(wrapped) == 0 {
			lines = append(lines, trimmed)
			continue
		}
		lines = append(lines, wrapped...)
	}

	for len(lines) > 0 && strings.TrimSpace(stripANSI(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(stripANSI(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{termui.FGMuted + "No content." + termui.ANSIReset}
	}
	return lines
}

func assistantMetaLines(phase state.PhaseRecord, toolCount, width int) []string {
	model := strings.TrimSpace(phase.ActualModel)
	if model == "" {
		model = strings.TrimSpace(phase.RequestedModel)
	}

	parts := make([]string, 0, 4)
	if model != "" {
		parts = append(parts, "model "+model)
	}
	if phase.LatencyMs > 0 {
		parts = append(parts, formatDuration(time.Duration(phase.LatencyMs)*time.Millisecond))
	}
	if phase.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", phase.TotalTokens))
	}
	if toolCount > 0 {
		suffix := "tools"
		if toolCount == 1 {
			suffix = "tool"
		}
		parts = append(parts, fmt.Sprintf("%d %s", toolCount, suffix))
	}
	if len(parts) == 0 {
		return nil
	}

	line := strings.Join(parts, "  •  ")
	line = truncateRunes(line, width)
	return []string{termui.FGMuted + line + termui.ANSIReset}
}

func humanizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "a tool"
	}
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	parts := strings.Fields(name)
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func summaryLines(summary SessionSummary) []string {
	lines := []string{
		fmt.Sprintf("Duration: %s", formatDuration(summary.Duration)),
		fmt.Sprintf("Turns: %d", summary.TurnCount),
	}

	if reason := strings.TrimSpace(summary.ExitReason); reason != "" {
		lines = append(lines, "Exit: "+reason)
	}

	if len(summary.ModelsUsed) > 0 {
		lines = append(lines, "Models: "+strings.Join(summary.ModelsUsed, ", "))
	}

	if summary.TotalTokens > 0 || summary.PromptTokens > 0 || summary.CompletionTokens > 0 {
		lines = append(lines, fmt.Sprintf(
			"Tokens: %d prompt, %d completion, %d total",
			summary.PromptTokens,
			summary.CompletionTokens,
			summary.TotalTokens,
		))
	}

	if summary.CachedTokensKnown {
		lines = append(lines, fmt.Sprintf("Cached tokens: %d", summary.CachedTokens))
	}

	if summary.ToolCalls > 0 {
		lines = append(lines, fmt.Sprintf(
			"Tool calls: %d total, %d succeeded, %d failed",
			summary.ToolCalls,
			summary.SuccessfulTools,
			summary.FailedTools,
		))
	}

	if summary.TurnCount == 0 {
		lines = append(lines, "No turns were completed in this session.")
	}

	return lines
}

func wrapText(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" || width <= 0 {
		return nil
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	lines := make([]string, 0, len(words))
	current := ""
	for _, word := range words {
		for visibleRuneLen(word) > width {
			chunk := truncatePlain(word, width)
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			lines = append(lines, chunk)
			word = strings.TrimPrefix(word, chunk)
		}

		if current == "" {
			current = word
			continue
		}
		if visibleRuneLen(current)+1+visibleRuneLen(word) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func truncatePlain(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return string(runes[:width])
}

func truncateRunes(text string, width int) string {
	plain := stripANSI(text)
	if width <= 0 || visibleRuneLen(plain) <= width {
		return plain
	}
	if width == 1 {
		return truncatePlain(plain, 1)
	}
	runes := []rune(plain)
	return string(runes[:width-1]) + "…"
}

func padRight(text string, width int) string {
	text = truncateRunes(text, width)
	if pad := width - visibleRuneLen(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return text
}

func visibleRuneLen(text string) int {
	return utf8.RuneCountInString(stripANSI(text))
}

func stripANSI(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == 27 && i+1 < len(text) && text[i+1] == '[' {
			i += 2
			for i < len(text) && ((text[i] >= '0' && text[i] <= '9') || text[i] == ';') {
				i++
			}
			continue
		}
		out.WriteByte(text[i])
	}
	return out.String()
}

func terminalWidth(fd int) int {
	width, _, err := term.GetSize(fd)
	if err != nil || width <= 0 {
		return 100
	}
	return width
}

func clampWidth(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
