package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestApprovalDialogScrollAndDismiss(t *testing.T) {
	tab := newChatTab().(*chatTab)
	tab.pendingApproval = &pendingChatApproval{workID: "exact", summary: strings.Repeat("echo first\n", 80) + "echo FINAL_COMMAND"}
	for _, width := range []int{80, 100, 120} {
		view := tab.View(width, 24)
		if strings.Contains(view, "FINAL_COMMAND") {
			t.Fatal("expected command to require scrolling")
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("overflow: %q", line)
			}
		}
		if lipgloss.Height(view) > 24 {
			t.Fatalf("height overflow: %d", lipgloss.Height(view))
		}
		tab.updateApprovalDialog(tea.KeyMsg{Type: tea.KeyEnd})
		if !strings.Contains(tab.View(width, 24), "FINAL_COMMAND") {
			t.Fatal("command tail unreachable")
		}
		tab.pendingApproval.offset = 0
	}
	_, cmd := tab.updateApprovalDialog(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || tab.approvalInFlight || tab.approvalDialogOpen() {
		t.Fatal("dismissal must not authorize execution")
	}
	tab.composerFocused = false
	_, cmd = tab.updateLive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, nil)
	if cmd != nil || !tab.approvalDialogOpen() {
		t.Fatal("reopen must only show review")
	}
}

func TestPassiveSidebarKeepsEvidenceInInspector(t *testing.T) {
	tab := newChatTab().(*chatTab)
	tab.activeTurn = 1
	tab.toolCalls = []liveToolCall{{turn: 1, command: "echo VERBOSE_COMMAND", status: "SUCCEEDED"}}
	view := tab.View(160, 30)
	if !strings.Contains(view, "TASK PROGRESS") || strings.Contains(view, "VERBOSE_COMMAND") {
		t.Fatalf("sidebar not compact: %s", view)
	}
}
