package tui

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestTruncatePreservesColorBoundaryAndTerminalCellWidth(t *testing.T) {
	styled := "\x1b[1;38;2;238;232;223;48;2;60;51;42mSelected 界界 field\x1b[0m"
	for _, width := range []int{1, 10, 16, 30} {
		got := truncate(styled, width)
		if ansi.StringWidth(got) > width {
			t.Fatalf("overflow at %d: %q", width, got)
		}
		if !strings.HasSuffix(got, "\x1b[0m") {
			t.Fatalf("style leaks into next row at %d: %q", width, got)
		}
	}
	if got := ansi.StringWidth(padRight("界", 5)); got != 5 {
		t.Fatalf("wide glyph padding occupied %d cells", got)
	}
}
