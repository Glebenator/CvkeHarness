package termui

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestListScrollTopKeepsSelectionVisible(t *testing.T) {
	tests := []struct {
		name        string
		selected    int
		currentTop  int
		itemCount   int
		visibleRows int
		want        int
	}{
		{name: "selected below viewport", selected: 7, currentTop: 0, itemCount: 10, visibleRows: 5, want: 3},
		{name: "selected above viewport", selected: 1, currentTop: 4, itemCount: 10, visibleRows: 5, want: 1},
		{name: "selected already visible", selected: 5, currentTop: 3, itemCount: 10, visibleRows: 5, want: 3},
		{name: "clamps current top", selected: 9, currentTop: 99, itemCount: 10, visibleRows: 5, want: 5},
		{name: "all rows fit", selected: 9, currentTop: 4, itemCount: 10, visibleRows: 10, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listScrollTop(tt.selected, tt.currentTop, tt.itemCount, tt.visibleRows)
			if got != tt.want {
				t.Fatalf("listScrollTop() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRenderListViewportTruncatesRowsToWidth(t *testing.T) {
	items := []ListItem{
		{Label: "gpt-5.4", Description: "Strong model for everyday coding with a very long description that must not wrap"},
		{Label: "gpt-5.3-codex", Description: "Coding-optimized model with enough detail to overflow narrow terminals"},
		{Label: "[ custom model ]", Description: "Enter your own model ID ->"},
	}

	var out bytes.Buffer
	lineCount := renderListViewport(&out, items, 1, 0, len(items), 48, true)
	if lineCount != len(items)+2 {
		t.Fatalf("renderListViewport() wrote %d lines, want %d", lineCount, len(items)+2)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for _, line := range lines {
		plain := strings.TrimPrefix(stripANSI(line), "\r")
		if got := utf8.RuneCountInString(plain); got > 48 {
			t.Fatalf("rendered line is %d runes, want <= 48: %q", got, plain)
		}
	}
}

func TestListVisibleRowsReservesRoomForWizardChrome(t *testing.T) {
	if got := listVisibleRows(30, 24); got != 11 {
		t.Fatalf("listVisibleRows(30, 24) = %d, want 11", got)
	}
	if got := listVisibleRows(3, 24); got != 3 {
		t.Fatalf("listVisibleRows(3, 24) = %d, want 3", got)
	}
}
