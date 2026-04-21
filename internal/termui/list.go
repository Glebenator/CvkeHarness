package termui

import (
	"bufio"
	"fmt"
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
	listKeyBackspace
)

// SelectList renders an arrow-key navigable vertical list.
func SelectList(items []ListItem, initial int, canGoBack bool) (int, error) {
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

	lineCount := len(items) + 2

	render := func() {
		for i, item := range items {
			fmt.Print("\033[2K\r")
			if i == selected {
				fmt.Printf("  %s%s ▶  %-42s%s  %s%s%s\n",
					BGSelected, FGAccent+ANSIBold, item.Label, ANSIReset,
					BGSelected+FGGray, item.Description, ANSIReset)
			} else {
				fmt.Printf("     %s%-42s%s  %s%s%s\n",
					FGMuted, item.Label, ANSIReset,
					ANSIDim+FGMuted, item.Description, ANSIReset)
			}
		}
		fmt.Print("\033[2K\r\n")
		fmt.Print("\033[2K\r")
		fmt.Printf("  %s%s%s\n", FGGray, buildListHint(canGoBack), ANSIReset)
	}

	render()
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
		render()
	}
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
	parts := []string{"↑↓ navigate", "Return select", "^C quit"}
	if canGoBack {
		parts = append([]string{"← Backspace: back"}, parts...)
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
