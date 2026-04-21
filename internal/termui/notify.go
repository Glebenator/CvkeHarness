package termui

import (
	"io"
	"os"
	"strings"
	"unicode"

	"golang.org/x/term"
)

type notificationProtocol int

const (
	notificationUnsupported notificationProtocol = iota
	notificationOSC9
	notificationOSC777
)

// NotifyInputRequested sends a best-effort terminal notification when the
// current terminal advertises support for desktop notifications.
func NotifyInputRequested(out io.Writer, title, body string) {
	if out == nil {
		out = os.Stdout
	}

	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return
	}

	sequence := notificationSequence(title, body, os.Getenv)
	if sequence == "" {
		return
	}

	_, _ = io.WriteString(out, sequence)
}

func notificationSequence(title, body string, getenv func(string) string) string {
	protocol := detectNotificationProtocol(getenv)
	if protocol == notificationUnsupported {
		return ""
	}

	title = sanitizeNotificationText(title)
	body = sanitizeNotificationText(body)
	if title == "" {
		title = "Input requested"
	}
	if body == "" {
		body = "Response needed in the terminal."
	}

	var sequence string
	switch protocol {
	case notificationOSC9:
		message := title
		if body != "" {
			message += ": " + body
		}
		sequence = osc("9;" + message)
	case notificationOSC777:
		sequence = osc("777;notify;" + title + ";" + body)
	default:
		return ""
	}

	if strings.TrimSpace(getenv("TMUX")) != "" {
		sequence = wrapTmuxOSC(sequence)
	}
	return sequence
}

func detectNotificationProtocol(getenv func(string) string) notificationProtocol {
	termProgram := strings.TrimSpace(getenv("TERM_PROGRAM"))

	switch {
	case termProgram == "iTerm.app", termProgram == "WezTerm", strings.TrimSpace(getenv("LC_TERMINAL")) == "iTerm2":
		return notificationOSC9
	case strings.TrimSpace(getenv("VTE_VERSION")) != "",
		strings.TrimSpace(getenv("GNOME_TERMINAL_SCREEN")) != "",
		strings.TrimSpace(getenv("TILIX_ID")) != "",
		strings.TrimSpace(getenv("TERMINATOR_UUID")) != "":
		return notificationOSC777
	default:
		return notificationUnsupported
	}
}

func sanitizeNotificationText(text string) string {
	text = strings.Map(func(r rune) rune {
		switch {
		case r == ';':
			return ','
		case unicode.IsControl(r):
			if r == ' ' || r == '\t' {
				return ' '
			}
			return ' '
		default:
			return r
		}
	}, text)

	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

func osc(payload string) string {
	return "\033]" + payload + "\033\\"
}

func wrapTmuxOSC(sequence string) string {
	return "\033Ptmux;" + strings.ReplaceAll(sequence, "\033", "\033\033") + "\033\\"
}
