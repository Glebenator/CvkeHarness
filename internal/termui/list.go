package termui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// GoBack is returned when the user navigates backward from a list prompt.
const GoBack = -1

// ListItem is one selectable row in a vertical menu.
type ListItem struct {
	Label       string
	Description string
}

type listKeyKind int

const (
	listKeyUnknown listKeyKind = iota
	listKeyUp
	listKeyDown
	listKeyEnter
	listKeyCtrlC
	listKeyEsc
	listKeyBackspace
)

const (
	listDefaultWidth        = 100
	listDefaultHeight       = 24
	listLabelWidth          = 42
	listMinVisibleRows      = 4
	listReservedScreenRows  = 13
	listNonListContentRows  = 2
	listItemPrefixWidth     = 5
	listDescriptionGapWidth = 2
)

// SelectList renders an arrow-key navigable vertical list.
func SelectList(items []ListItem, initial int, canGoBack bool) (int, error) {
	if len(items) == 0 {
		return 0, errors.New("select list requires at least one item")
	}

	selected := initial
	if selected < 0 || selected >= len(items) {
		selected = 0
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return numberedFallback(items, selected, canGoBack)
	}
	defer term.Restore(fd, oldState)

	fmt.Print(HideCursor)
	defer fmt.Print(ShowCursor)

	width, height := listTerminalSize(fd)
	visibleRows := listVisibleRows(len(items), height)
	scrollTop := listScrollTop(selected, 0, len(items), visibleRows)

	render := func() int {
		scrollTop = listScrollTop(selected, scrollTop, len(items), visibleRows)
		return renderListViewport(os.Stdout, items, selected, scrollTop, visibleRows, width, canGoBack)
	}

	lineCount := render()
	NotifyInputRequested(os.Stdout, "Choose an option", "Selection is waiting in the terminal.")

	for {
		ev := nextListKey()
		switch ev {
		case listKeyUp:
			if selected > 0 {
				selected--
			}
		case listKeyDown:
			if selected < len(items)-1 {
				selected++
			}
		case listKeyEnter:
			return selected, nil
		case listKeyEsc:
			if canGoBack {
				return GoBack, nil
			}
			return 0, ErrInterrupted
		case listKeyBackspace:
			if canGoBack {
				return GoBack, nil
			}
		case listKeyCtrlC:
			return 0, ErrInterrupted
		default:
			continue
		}
		fmt.Printf("\033[%dA", lineCount)
		lineCount = render()
	}
}

func renderListViewport(out io.Writer, items []ListItem, selected, scrollTop, visibleRows, width int, canGoBack bool) int {
	lineCount := 0
	end := scrollTop + visibleRows
	if end > len(items) {
		end = len(items)
	}
	for i := scrollTop; i < end; i++ {
		fmt.Fprintln(out, clearLine(formatListItem(items[i], i == selected, width)))
		lineCount++
	}
	fmt.Fprintln(out, clearLine(""))
	lineCount++
	fmt.Fprintln(out, clearLine(formatListHint(canGoBack, selected, len(items), visibleRows, width)))
	lineCount++
	return lineCount
}

func formatListItem(item ListItem, selected bool, width int) string {
	if width <= 0 {
		width = listDefaultWidth
	}
	if width <= listItemPrefixWidth {
		text := item.Label
		style := FGMuted
		if selected {
			text = "▶ " + text
			style = BGSelected + FGAccent + ANSIBold
		}
		return style + truncateRunes(text, width) + ANSIReset
	}
	labelWidth, descWidth := listColumnWidths(width)
	label := padRight(item.Label, labelWidth)

	var b strings.Builder
	if selected {
		b.WriteString("  ")
		b.WriteString(BGSelected)
		b.WriteString(FGWhite)
		b.WriteString(ANSIBold)
		b.WriteString("▸  ")
		b.WriteString(label)
		if descWidth > 0 {
			b.WriteString(ANSIReset)
			b.WriteString(BGSelected)
			b.WriteString(FGGray)
			b.WriteString(strings.Repeat(" ", listDescriptionGapWidth))
			b.WriteString(padRight(item.Description, descWidth))
		}
		b.WriteString(ANSIReset)
		return b.String()
	}

	b.WriteString(strings.Repeat(" ", listItemPrefixWidth))
	b.WriteString(FGGray)
	b.WriteString(label)
	b.WriteString(ANSIReset)
	if descWidth > 0 {
		b.WriteString(strings.Repeat(" ", listDescriptionGapWidth))
		b.WriteString(ANSIDim)
		b.WriteString(FGMuted)
		b.WriteString(padRight(item.Description, descWidth))
		b.WriteString(ANSIReset)
	}
	return b.String()
}

func formatListHint(canGoBack bool, selected, itemCount, visibleRows, width int) string {
	hint := buildListHint(canGoBack)
	if itemCount > visibleRows {
		hint = fmt.Sprintf("%s   %d/%d", hint, selected+1, itemCount)
	}
	contentWidth := width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	return "  " + FGMuted + truncateRunes(hint, contentWidth) + ANSIReset
}

func listColumnWidths(width int) (int, int) {
	if width <= 0 {
		width = listDefaultWidth
	}
	available := width - listItemPrefixWidth
	if available <= 0 {
		return 0, 0
	}
	if available <= listLabelWidth+listDescriptionGapWidth {
		return available, 0
	}
	descWidth := available - listLabelWidth - listDescriptionGapWidth
	return listLabelWidth, descWidth
}

func listVisibleRows(itemCount, terminalHeight int) int {
	if itemCount <= 0 {
		return 0
	}
	if terminalHeight <= 0 {
		terminalHeight = listDefaultHeight
	}
	available := terminalHeight - listReservedScreenRows
	if available < listMinVisibleRows {
		available = terminalHeight - listNonListContentRows
	}
	if available < 1 {
		available = 1
	}
	if available > itemCount {
		return itemCount
	}
	return available
}

func listScrollTop(selected, currentTop, itemCount, visibleRows int) int {
	if visibleRows <= 0 || itemCount <= visibleRows {
		return 0
	}
	maxTop := itemCount - visibleRows
	if currentTop < 0 {
		currentTop = 0
	}
	if currentTop > maxTop {
		currentTop = maxTop
	}
	if selected < currentTop {
		return selected
	}
	if selected >= currentTop+visibleRows {
		return selected - visibleRows + 1
	}
	return currentTop
}

func listTerminalSize(fd int) (int, int) {
	width, height, err := term.GetSize(fd)
	if err != nil {
		return listDefaultWidth, listDefaultHeight
	}
	if width <= 0 {
		width = listDefaultWidth
	}
	if height <= 0 {
		height = listDefaultHeight
	}
	return width, height
}

func numberedFallback(items []ListItem, initial int, canGoBack bool) (int, error) {
	for i, item := range items {
		mark := "   "
		if i == initial {
			mark = FGAccent + ANSIBold + " ▶ " + ANSIReset
		}
		fmt.Printf("  %s%s%s%d%s  %-42s  %s%s%s\n",
			mark, ANSIBold, FGGray, i+1, ANSIReset,
			FGWhite+item.Label+ANSIReset,
			ANSIDim+FGMuted, item.Description, ANSIReset)
	}

	hint := fmt.Sprintf("\n  %sEnter number [1-%d] (↵ default %d)", FGGray, len(items), initial+1)
	if canGoBack {
		hint += "  ·  0 to go back"
	}
	fmt.Printf("%s:%s ", hint, ANSIReset)
	fmt.Print(FGWhite + ANSIBold)
	NotifyInputRequested(os.Stdout, "Choose an option", "Selection is waiting in the terminal.")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	fmt.Print(ANSIReset)
	line = strings.TrimSpace(line)
	if line == "" {
		return initial, nil
	}
	if line == "0" && canGoBack {
		return GoBack, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(items) {
		return initial, nil
	}
	return n - 1, nil
}

func buildListHint(canGoBack bool) string {
	parts := []string{"↑↓ move", "Enter select", "Ctrl+C quit"}
	if canGoBack {
		parts = append([]string{"Esc back"}, parts...)
	}
	return strings.Join(parts, "   ")
}

func nextListKey() listKeyKind {
	buf := make([]byte, 4)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return listKeyUnknown
	}
	switch {
	case n == 1 && (buf[0] == 3 || buf[0] == 4):
		return listKeyCtrlC
	case n == 1 && buf[0] == 27:
		return listKeyEsc
	case n == 1 && (buf[0] == 13 || buf[0] == 10):
		return listKeyEnter
	case n == 1 && (buf[0] == 127 || buf[0] == 8):
		return listKeyBackspace
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A':
		return listKeyUp
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B':
		return listKeyDown
	default:
		return listKeyUnknown
	}
}
