package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	runSurfaceDefaultWidth = 80
	runSurfaceMinWidth     = 40
	runSurfaceMaxWidth     = 120
	runLabelWidth          = 10
)

// RunHeader captures the stable identity shown before one bounded run starts.
type RunHeader struct {
	Task   string
	Target string
	Model  string
}

// ApprovalNotice captures the safe, operator-facing explanation for blocked work.
type ApprovalNotice struct {
	Action  string
	Reason  string
	Scope   string
	Effect  string
	Approve string
}

// RunSummary captures the close-out stats for one bounded agent run.
type RunSummary struct {
	Duration           time.Duration
	ExitReason         string
	ExitCode           int
	Target             string
	ModelsUsed         []string
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	CachedTokens       int
	CachedTokensKnown  bool
	ToolCalls          int
	SuccessfulTools    int
	FailedTools        int
	BlockedTools       int
	VerificationStatus string
}

// RunSurfaceOptions controls terminal-only styling without changing plain output.
type RunSurfaceOptions struct {
	Mode  string
	Width int
}

// RunSurface renders the stable, line-oriented output for a bounded run.
type RunSurface struct {
	out   io.Writer
	mode  string
	width int
}

func NewRunSurface(out io.Writer) *RunSurface {
	return NewRunSurfaceWithOptions(out, RunSurfaceOptions{})
}

func NewRunSurfaceWithOptions(out io.Writer, opts RunSurfaceOptions) *RunSurface {
	if out == nil {
		out = os.Stdout
	}
	mode := streamModePlain
	if opts.Mode == streamModeRich {
		mode = streamModeRich
	}
	width := opts.Width
	if width == 0 {
		width = runSurfaceDefaultWidth
	}
	if width < runSurfaceMinWidth {
		width = runSurfaceMinWidth
	}
	if width > runSurfaceMaxWidth {
		width = runSurfaceMaxWidth
	}
	return &RunSurface{out: out, mode: mode, width: width}
}

func (s *RunSurface) PrintRunHeader(header RunHeader) {
	if !s.ready() {
		return
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, s.style("1;38;5;252", "$ cvkeharness run"))
	s.writeLabeled("Task", header.Task, "muted")
	s.writeLabeled("Target", s.semanticSeparators(header.Target), "muted")
	s.writeLabeled("Model", header.Model, "muted")
	fmt.Fprintln(s.out)
}

func (s *RunSurface) PrintApprovalRequired(notice ApprovalNotice) {
	if !s.ready() {
		return
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, s.rule("38;5;179"))
	fmt.Fprintln(s.out, s.style("1;38;5;179", "APPROVAL REQUIRED"))
	fmt.Fprintln(s.out)
	s.writeLabeled("Action", notice.Action, "approval")
	s.writeLabeled("Reason", notice.Reason, "approval")
	s.writeLabeled("Scope", s.semanticSeparators(notice.Scope), "approval")
	s.writeLabeled("Effect", notice.Effect, "approval")
	if strings.TrimSpace(notice.Approve) != "" {
		s.writeLabeled("Approve", notice.Approve, "approval")
	}
	fmt.Fprintln(s.out, s.rule("38;5;179"))
}

func (s *RunSurface) PrintInfo(title string, lines []string) {
	if !s.ready() {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "STATUS"
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, s.style("1;38;5;179", strings.ToUpper(title)))
	for _, line := range lines {
		s.writeWrapped("", "", strings.TrimSpace(line), "")
	}
}

func (s *RunSurface) PrintRunSummary(summary RunSummary) {
	if !s.ready() {
		return
	}
	fmt.Fprintln(s.out)
	status, note, color := runCloseoutStatus(summary.ExitReason)
	statusPrefix := fmt.Sprintf("%-10s", status)
	detail := fmt.Sprintf("exit %d%s%s", summary.ExitCode, s.separator(), formatDuration(summary.Duration))
	if note != "" {
		detail = fmt.Sprintf("exit %d%s%s%s%s", summary.ExitCode, s.separator(), note, s.separator(), formatDuration(summary.Duration))
	}
	s.writeWrapped(s.style(color, statusPrefix), strings.Repeat(" ", len([]rune(statusPrefix))), detail, "")
}

func (s *RunSurface) PrintRunReceipt(summary RunSummary) {
	if !s.ready() {
		return
	}
	lines := runSummaryLines(summary, s.separator(), s.width)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(s.out)
	for _, line := range lines {
		s.writeWrapped("", "", line, "muted")
	}
}

func (s *RunSurface) PrintAnswer(output string, partial bool) {
	output = sanitizeRunOutput(output)
	if !s.ready() || strings.TrimSpace(output) == "" {
		return
	}
	fmt.Fprintln(s.out)
	label := "ANSWER"
	if partial {
		label = "PARTIAL"
	}
	if s.mode == streamModeRich {
		s.writeMarkdownAnswer(label, output)
		return
	}
	s.writeLabeled(label, strings.TrimSpace(output), "strong")
}

func (s *RunSurface) writeMarkdownAnswer(label, output string) {
	contentWidth := s.width - runLabelWidth
	if contentWidth < 12 {
		contentWidth = 12
	}
	rendered := renderRunMarkdown(output, contentWidth)
	if strings.TrimSpace(rendered) == "" {
		return
	}

	prefixPlain := fmt.Sprintf("%-*s", runLabelWidth, strings.TrimSpace(label))
	prefix := s.style("1;38;5;252", prefixPlain)
	continuation := strings.Repeat(" ", runLabelWidth)
	for i, line := range strings.Split(rendered, "\n") {
		if line == "" {
			fmt.Fprintln(s.out)
			continue
		}
		linePrefix := continuation
		if i == 0 {
			linePrefix = prefix
		}
		fmt.Fprintln(s.out, linePrefix+line)
	}
}

func (s *RunSurface) ready() bool {
	return s != nil && s.out != nil
}

func (s *RunSurface) writeLabeled(label, value, role string) {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	prefixPlain := fmt.Sprintf("%-*s", runLabelWidth, label)
	prefix := prefixPlain
	switch role {
	case "approval":
		prefix = s.style("1;38;5;179", prefixPlain)
	case "strong":
		prefix = s.style("1;38;5;252", prefixPlain)
	default:
		prefix = s.style("38;5;244", prefixPlain)
	}
	s.writeWrapped(prefix, strings.Repeat(" ", runLabelWidth), value, "")
}

func (s *RunSurface) writeWrapped(prefix, continuation, value, role string) {
	available := s.width - len([]rune(continuation))
	if available < 12 {
		available = 12
	}
	lines := wrapRunText(value, available)
	if len(lines) == 0 {
		return
	}
	for i, line := range lines {
		linePrefix := continuation
		if i == 0 {
			linePrefix = prefix
		}
		if role == "muted" {
			line = s.style("38;5;244", line)
		}
		fmt.Fprintln(s.out, linePrefix+line)
	}
}

func (s *RunSurface) rule(color string) string {
	width := s.width
	if width > 72 {
		width = 72
	}
	rule := strings.Repeat("-", width)
	if s.mode == streamModeRich {
		rule = strings.Repeat("─", width)
	}
	return s.style(color, rule)
}

func (s *RunSurface) separator() string {
	if s.mode == streamModeRich {
		return "  ·  "
	}
	return " | "
}

func (s *RunSurface) semanticSeparators(value string) string {
	return strings.ReplaceAll(value, " | ", s.separator())
}

func (s *RunSurface) style(code, value string) string {
	if s.mode != streamModeRich {
		return value
	}
	return colorize(code, value)
}

func runCloseoutStatus(reason string) (status, note, color string) {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "approval required":
		return "BLOCKED", "waiting for operator", "1;38;5;167"
	case "interrupted":
		return "INTERRUPTED", "cleanup complete", "1;38;5;179"
	case "terminated":
		return "TERMINATED", "cleanup complete", "1;38;5;179"
	case "incomplete":
		return "INCOMPLETE", "verification stopped", "1;38;5;179"
	case "failed":
		return "FAILED", "run stopped", "1;38;5;167"
	default:
		return "COMPLETED", "", "1;38;5;108"
	}
}

func runSummaryLines(summary RunSummary, separator string, width int) []string {
	var lines []string
	var receipt []string
	if target := strings.TrimSpace(summary.Target); target != "" {
		lines = append(lines, "target "+strings.ReplaceAll(target, " | ", separator))
	}
	if len(summary.ModelsUsed) > 0 {
		receipt = append(receipt, "model "+strings.Join(summary.ModelsUsed, ", "))
	}
	if summary.ToolCalls > 0 {
		toolText := fmt.Sprintf("tools %d/%d done", summary.SuccessfulTools, summary.ToolCalls)
		if summary.BlockedTools > 0 {
			toolText += fmt.Sprintf(" (%d blocked)", summary.BlockedTools)
		}
		if summary.FailedTools > 0 {
			toolText += fmt.Sprintf(" (%d failed)", summary.FailedTools)
		}
		receipt = append(receipt, toolText)
	}
	if verification := strings.TrimSpace(summary.VerificationStatus); verification != "" {
		receipt = append(receipt, "verify "+verification)
	}
	if len(receipt) > 0 {
		lines = append(lines, packRunReceipt(receipt, separator, width)...)
	}
	if summary.TotalTokens > 0 || summary.PromptTokens > 0 || summary.CompletionTokens > 0 {
		tokens := []string{
			fmt.Sprintf("tokens %d prompt", summary.PromptTokens),
			fmt.Sprintf("%d completion", summary.CompletionTokens),
			fmt.Sprintf("%d total", summary.TotalTokens),
		}
		if summary.CachedTokensKnown {
			tokens = append(tokens, fmt.Sprintf("%d cached", summary.CachedTokens))
		}
		lines = append(lines, packRunReceipt(tokens, separator, width)...)
	} else if summary.CachedTokensKnown {
		lines = append(lines, fmt.Sprintf("tokens %d cached", summary.CachedTokens))
	}
	return lines
}

func packRunReceipt(parts []string, separator string, width int) []string {
	var lines []string
	var line string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		candidate := part
		if line != "" {
			candidate = line + separator + part
		}
		if line != "" && len([]rune(candidate)) > width {
			lines = append(lines, line)
			line = part
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func wrapRunText(value string, width int) []string {
	if width < 1 {
		return nil
	}
	var out []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(value, "\r", ""), "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			if len(out) > 0 {
				out = append(out, "")
			}
			continue
		}
		var line string
		for _, word := range strings.Fields(paragraph) {
			for len([]rune(word)) > width {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				runes := []rune(word)
				out = append(out, string(runes[:width]))
				word = string(runes[width:])
			}
			if line == "" {
				line = word
				continue
			}
			if len([]rune(line))+1+len([]rune(word)) <= width {
				line += " " + word
				continue
			}
			out = append(out, line)
			line = word
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
