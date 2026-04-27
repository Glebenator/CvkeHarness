package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/coolcake/cvkeharness/tools"
)

const (
	streamModePlain = "plain"
	streamModeRich  = "rich"
)

type shellRenderState struct {
	pending string
}

// TranscriptRenderer renders runtime events into a readable streaming transcript.
type TranscriptRenderer struct {
	out        io.Writer
	mode       string
	mu         sync.Mutex
	shells     map[string]*shellRenderState
	logPending string
}

// NewTranscriptRenderer creates a renderer for the requested mode.
func NewTranscriptRenderer(out io.Writer, mode string) *TranscriptRenderer {
	if mode != streamModeRich {
		mode = streamModePlain
	}
	return &TranscriptRenderer{
		out:    out,
		mode:   mode,
		shells: make(map[string]*shellRenderState),
	}
}

// Observe renders one runtime event.
func (r *TranscriptRenderer) Observe(event tools.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch event.Type {
	case tools.EventMemoryInjected:
		if strings.TrimSpace(event.Output) != "" {
			fmt.Fprintln(r.out, r.label("memory", event.Output))
		}
	case tools.EventToolCallStarted:
		if event.ToolName == "shell_execute" {
			return
		}
		fmt.Fprintln(r.out, r.label("tool", event.ToolName))
	case tools.EventToolCallFinished:
		if event.ToolName == "shell_execute" {
			return
		}
		if event.Success {
			fmt.Fprintln(r.out, r.label("done", fmt.Sprintf("%s in %s", event.ToolName, formatDuration(event.Duration))))
			return
		}
		fmt.Fprintln(r.out, r.label("fail", compactError(event.ToolName, event.ErrorMessage)))
	case tools.EventShellCommandStarted:
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, r.sectionLine("shell", "command"))
		fmt.Fprintf(r.out, "%s %s\n\n", r.commandPrefix(), event.Command)
	case tools.EventShellApproval:
		if event.ApprovalMode != "" && event.ApprovalMode != "allowlist" {
			fmt.Fprintln(r.out, r.metaLine("approval", strings.ReplaceAll(event.ApprovalMode, "_", " ")))
		}
	case tools.EventShellOutput:
		state := r.shellState(event)
		r.writeShellOutput(state, event.Output)
	case tools.EventShellCommandFinished:
		state := r.shellState(event)
		r.flushPending(state)
		status := "done"
		message := fmt.Sprintf("exit %s in %s", formatExitCode(event), formatDuration(event.Duration))
		if !event.Success {
			status = "fail"
			message = compactError(message, summarizeShellError(event.ErrorMessage))
		}
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, r.label(status, message))
	}
}

// Write renders structured harness log lines.
func (r *TranscriptRenderer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	text := r.logPending + normalizeChunk(string(p))
	lines := strings.Split(text, "\n")
	r.logPending = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Fprintf(r.out, "%s%s\n", r.logPrefix(), simplifyLogLine(line))
	}

	return len(p), nil
}

func (r *TranscriptRenderer) shellState(event tools.Event) *shellRenderState {
	key := event.ToolCallID
	if key == "" {
		key = event.ToolName
	}
	state, ok := r.shells[key]
	if !ok {
		state = &shellRenderState{}
		r.shells[key] = state
	}
	return state
}

func (r *TranscriptRenderer) writeShellOutput(state *shellRenderState, chunk string) {
	if chunk == "" {
		return
	}

	text := state.pending + normalizeChunk(chunk)
	lines := strings.Split(text, "\n")
	state.pending = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		fmt.Fprintf(r.out, "%s%s\n", r.outputPrefix(), line)
	}
}

func (r *TranscriptRenderer) flushPending(state *shellRenderState) {
	if state.pending == "" {
		return
	}
	fmt.Fprintf(r.out, "%s%s\n", r.outputPrefix(), state.pending)
	state.pending = ""
}

func (r *TranscriptRenderer) label(kind, message string) string {
	if r.mode == streamModeRich {
		switch kind {
		case "tool":
			return colorize("38;5;250", "[tool]") + " " + bold(message)
		case "done":
			return colorize("38;5;108", "[done]") + " " + message
		case "fail":
			return colorize("38;5;167", "[fail]") + " " + message
		}
	}
	return "[" + kind + "] " + message
}

func (r *TranscriptRenderer) sectionLine(kind, label string) string {
	switch kind {
	case "shell":
		if r.mode == streamModeRich {
			return colorize("38;5;250", "shell") + " " + colorize("38;5;240", label)
		}
		return "--- shell: " + label + " ---"
	default:
		return label
	}
}

func (r *TranscriptRenderer) metaLine(label, value string) string {
	if r.mode == streamModeRich {
		return colorize("38;5;240", "  "+label+":") + " " + value
	}
	return "  " + label + ": " + value
}

func (r *TranscriptRenderer) commandPrefix() string {
	if r.mode == streamModeRich {
		return colorize("1;252", "$")
	}
	return "$"
}

func (r *TranscriptRenderer) outputPrefix() string {
	if r.mode == streamModeRich {
		return colorize("38;5;240", "│ ")
	}
	return "| "
}

func (r *TranscriptRenderer) logPrefix() string {
	if r.mode == streamModeRich {
		return colorize("38;5;240", "· log ")
	}
	return "[log] "
}

func normalizeChunk(chunk string) string {
	chunk = strings.ReplaceAll(chunk, "\r\n", "\n")
	chunk = strings.ReplaceAll(chunk, "\r", "\n")
	return chunk
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func formatExitCode(event tools.Event) string {
	if !event.ExitCodeKnown {
		return "?"
	}
	return fmt.Sprintf("%d", event.ExitCode)
}

func compactError(prefix, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return prefix
	}
	return prefix + " · " + message
}

func summarizeShellError(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}

	if before, _, found := strings.Cut(message, ". Output:"); found {
		message = before
	}

	message = strings.TrimPrefix(message, "command exited with error: ")
	message = strings.TrimPrefix(message, "security violation: ")
	message = strings.TrimSpace(message)
	return message
}

func simplifyLogLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	if strings.HasPrefix(line, "level=") {
		return line
	}

	if idx := strings.Index(line, " level="); idx >= 0 {
		line = strings.TrimSpace(line[idx+1:])
	}
	return line
}

func colorize(code, text string) string {
	return "\033[" + code + "m" + text + "\033[0m"
}

func bold(text string) string {
	return colorize("1", text)
}
