package termui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	hideCursor = "\033[?25l"
	showCursor = "\033[?25h"

	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"

	fgWhite = FGWhite
	fgMuted = FGMuted
	fgCyan  = FGAccent
	fgGreen = FGGreen
	fgCoral = FGRed

	bgSelected = BGSelected
)

var ErrInterrupted = errors.New("interactive prompt interrupted")

type keyKind int

const (
	keyUnknown keyKind = iota
	keyLeft
	keyRight
	keyUp
	keyDown
	keyEnter
	keyEsc
	keyCtrlC
)

// Choice is one selectable item in a terminal prompt.
type Choice struct {
	Label       string
	Description string
}

// SelectOptions configures a keyboard-driven choice prompt.
type SelectOptions struct {
	Title        string
	Details      []string
	Choices      []Choice
	InitialIndex int
	In           io.Reader
	Out          io.Writer
}

// Select renders a prompt and returns the selected choice index.
func Select(opts SelectOptions) (int, error) {
	if len(opts.Choices) == 0 {
		return 0, errors.New("select prompt requires at least one choice")
	}
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.InitialIndex < 0 || opts.InitialIndex >= len(opts.Choices) {
		opts.InitialIndex = 0
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "Choose an action"
	}
	NotifyInputRequested(opts.Out, title, "Selection is waiting in the terminal.")

	if file, ok := opts.In.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return selectRaw(file, opts.Out, opts)
	}

	return selectFallback(opts.In, opts.Out, opts)
}

func selectFallback(in io.Reader, out io.Writer, opts SelectOptions) (int, error) {
	if err := renderStaticPrompt(out, opts); err != nil {
		return 0, err
	}
	if _, err := fmt.Fprintf(out, "Select an option [default %d]: ", opts.InitialIndex+1); err != nil {
		return 0, err
	}

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}

	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return opts.InitialIndex, nil
	}
	if n, convErr := strconv.Atoi(line); convErr == nil {
		if n >= 1 && n <= len(opts.Choices) {
			return n - 1, nil
		}
	}

	for i, choice := range opts.Choices {
		label := strings.ToLower(strings.TrimSpace(choice.Label))
		if line == label {
			return i, nil
		}
	}

	switch line {
	case "y", "yes":
		for i, choice := range opts.Choices {
			label := strings.ToLower(choice.Label)
			if strings.Contains(label, "approve") || strings.Contains(label, "apply") || strings.Contains(label, "allow") {
				return i, nil
			}
		}
	case "n", "no":
		for i, choice := range opts.Choices {
			label := strings.ToLower(choice.Label)
			if strings.Contains(label, "reject") || strings.Contains(label, "deny") || strings.Contains(label, "stay") || strings.Contains(label, "cancel") {
				return i, nil
			}
		}
	}

	return opts.InitialIndex, nil
}

func selectRaw(in *os.File, out io.Writer, opts SelectOptions) (int, error) {
	selected := opts.InitialIndex
	fd := int(in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return selectFallback(in, out, opts)
	}
	defer term.Restore(fd, oldState)

	if _, err := fmt.Fprint(out, hideCursor); err != nil {
		return 0, err
	}
	defer fmt.Fprint(out, showCursor)

	boxWidth := clamp(terminalWidth(fd)-8, 56, 104)
	if err := renderInteractiveHeader(out, opts, boxWidth); err != nil {
		return 0, err
	}
	lineCount, err := renderInteractiveChoices(out, opts, selected, boxWidth)
	if err != nil {
		return 0, err
	}

	for {
		ev, readErr := nextKey(in)
		if readErr != nil {
			return 0, readErr
		}
		switch ev {
		case keyLeft, keyUp:
			if selected > 0 {
				selected--
			}
		case keyRight, keyDown:
			if selected < len(opts.Choices)-1 {
				selected++
			}
		case keyEnter:
			if _, err := fmt.Fprintln(out); err != nil {
				return 0, err
			}
			return selected, nil
		case keyEsc, keyCtrlC:
			return 0, ErrInterrupted
		default:
			continue
		}

		if _, err := fmt.Fprintf(out, "\033[%dA", lineCount); err != nil {
			return 0, err
		}
		lineCount, err = renderInteractiveChoices(out, opts, selected, boxWidth)
		if err != nil {
			return 0, err
		}
	}
}

func renderStaticPrompt(out io.Writer, opts SelectOptions) error {
	if strings.TrimSpace(opts.Title) != "" {
		if _, err := fmt.Fprintf(out, "\n%s%s%s\n", ansiBold, opts.Title, ansiReset); err != nil {
			return err
		}
	}
	for _, line := range opts.Details {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	for i, choice := range opts.Choices {
		if _, err := fmt.Fprintf(out, "  %d. %s", i+1, choice.Label); err != nil {
			return err
		}
		if strings.TrimSpace(choice.Description) != "" {
			if _, err := fmt.Fprintf(out, "  (%s)", choice.Description); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	return nil
}

func renderInteractiveHeader(out io.Writer, opts SelectOptions, boxWidth int) error {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "Choose an action"
	}

	if _, err := fmt.Fprintln(out, clearLine("")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, clearLine(promptHeader(title, boxWidth, fgCyan))); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, clearLine(colorize(FGSubtle, "  "+strings.Repeat("─", boxWidth-2)))); err != nil {
		return err
	}

	for i, detail := range opts.Details {
		if i > 0 {
			if _, err := fmt.Fprintln(out, clearLine(boxLine(boxWidth, ""))); err != nil {
				return err
			}
		}

		label, value, found := strings.Cut(detail, ":")
		if !found {
			for _, wrapped := range wrapText(strings.TrimSpace(detail), boxWidth-4) {
				if _, err := fmt.Fprintln(out, clearLine(boxLine(boxWidth, wrapped))); err != nil {
					return err
				}
			}
			continue
		}

		label = strings.TrimSpace(label)
		value = strings.TrimSpace(value)
		if label != "" {
			if _, err := fmt.Fprintln(out, clearLine(boxLine(boxWidth, label))); err != nil {
				return err
			}
		}
		for _, wrapped := range wrapText(value, boxWidth-6) {
			if _, err := fmt.Fprintln(out, clearLine(boxLine(boxWidth, "  "+wrapped))); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintln(out, clearLine("")); err != nil {
		return err
	}
	return nil
}

func renderInteractiveChoices(out io.Writer, opts SelectOptions, selected, boxWidth int) (int, error) {
	lineCount := 0

	if _, err := fmt.Fprintln(out, clearLine(renderChoiceTabs(opts.Choices, selected, boxWidth))); err != nil {
		return 0, err
	}
	lineCount++

	selectedChoice := opts.Choices[selected]
	tone := choiceTone(selectedChoice)

	if _, err := fmt.Fprintln(out, clearLine(promptHeader(selectedChoice.Label, boxWidth, tone))); err != nil {
		return 0, err
	}
	lineCount++

	bodyLines := wrapText(strings.TrimSpace(selectedChoice.Description), boxWidth-4)
	if len(bodyLines) == 0 {
		bodyLines = []string{"No additional details."}
	}
	bodyHeight := maxChoiceBodyLines(opts.Choices, boxWidth-4)
	for i := 0; i < bodyHeight; i++ {
		line := ""
		if i < len(bodyLines) {
			line = bodyLines[i]
		}
		if _, err := fmt.Fprintln(out, clearLine(boxLine(boxWidth, line))); err != nil {
			return 0, err
		}
		lineCount++
	}

	confirmLine := colorize(ansiDim+fgMuted, "Press Enter to confirm this action.")
	if _, err := fmt.Fprintln(out, clearLine(boxLine(boxWidth, confirmLine))); err != nil {
		return 0, err
	}
	lineCount++

	hint := colorize(ansiDim+fgMuted, "Use ↑↓ or ←→ to choose, Enter to confirm, Esc/Ctrl+C to cancel.")
	if _, err := fmt.Fprintln(out, clearLine(hint)); err != nil {
		return 0, err
	}
	lineCount++

	return lineCount, nil
}

func promptHeader(title string, width int, tone string) string {
	title = truncateRunes(strings.TrimSpace(title), width-5)
	head := "  " + colorize(tone+ansiBold, title)
	if pad := width - visibleRuneLen(title) - 4; pad > 0 {
		head += " " + colorize(fgMuted, strings.Repeat("─", pad))
	}
	return head
}

func boxLine(width int, content string) string {
	return "  " + colorize(fgMuted, "│") + " " + padRight(content, width-4)
}

func renderChoiceTabs(choices []Choice, selected, width int) string {
	parts := make([]string, 0, len(choices))
	for i, choice := range choices {
		label := truncateRunes(choice.Label, 24)
		tab := " " + label + " "
		if i == selected {
			tab = colorize(bgSelected+fgWhite+ansiBold, "▸"+tab)
		} else {
			tab = colorize(fgMuted, " "+tab)
		}
		parts = append(parts, tab)
	}

	line := strings.Join(parts, colorize(fgMuted, "  "))
	if visibleRuneLen(line) <= width {
		return line
	}
	return colorize(fgMuted, fmt.Sprintf("Option %d of %d", selected+1, len(choices)))
}

func maxChoiceBodyLines(choices []Choice, width int) int {
	maxLines := 1
	for _, choice := range choices {
		lines := wrapText(strings.TrimSpace(choice.Description), width)
		if len(lines) == 0 {
			continue
		}
		if len(lines) > maxLines {
			maxLines = len(lines)
		}
	}
	return maxLines
}

func choiceTone(choice Choice) string {
	label := strings.ToLower(choice.Label)
	switch {
	case strings.Contains(label, "reject"), strings.Contains(label, "deny"), strings.Contains(label, "stay"), strings.Contains(label, "cancel"):
		return fgCoral
	case strings.Contains(label, "approve"), strings.Contains(label, "apply"), strings.Contains(label, "allow"):
		return fgGreen
	default:
		return fgCyan
	}
}

func clearLine(text string) string {
	return "\033[2K\r" + text
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

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func colorize(code, text string) string {
	return code + text + ansiReset
}

func nextKey(in *os.File) (keyKind, error) {
	buf := make([]byte, 4)
	n, err := in.Read(buf)
	if err != nil {
		return keyUnknown, err
	}
	if n == 0 {
		return keyUnknown, nil
	}

	switch {
	case n == 1 && (buf[0] == 3 || buf[0] == 4):
		return keyCtrlC, nil
	case n == 1 && buf[0] == 27:
		return keyEsc, nil
	case n == 1 && (buf[0] == 13 || buf[0] == 10):
		return keyEnter, nil
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'C':
		return keyRight, nil
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'D':
		return keyLeft, nil
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A':
		return keyUp, nil
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B':
		return keyDown, nil
	default:
		return keyUnknown, nil
	}
}
