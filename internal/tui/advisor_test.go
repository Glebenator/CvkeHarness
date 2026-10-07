package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/glebenator/cvkeharness/agent"
	"github.com/glebenator/cvkeharness/state"
	"github.com/glebenator/cvkeharness/tools"
)

func TestAdvisorDialogShowsAdviceAndKeepsEntireCommandReachable(t *testing.T) {
	advice := &tools.CommandAdvice{Model: "chosen-model", Recommendation: "reject", Explanation: "Overwrites a file.", Steps: []string{"Run the script."}, Risks: []string{"Old content is lost."}, Uncertainty: "Contents unknown.", Reason: "Check the target."}
	tab := newChatTab().(*chatTab)
	tab.applyRuntimeEvent(tools.Event{Type: tools.EventApprovalRequired, ToolName: "shell_execute", ToolCallID: "tool", BlockedWorkID: "work", Command: strings.Repeat("echo first\n", 40) + "echo LAST_COMMAND", ApprovalAdvice: advice})
	for _, size := range [][2]int{{80, 24}, {100, 32}, {120, 30}} {
		tab.pendingApproval.offset = 0
		view := tab.View(size[0], size[1])
		if !strings.Contains(view, "COMMAND TO APPROVE") || !strings.Contains(view, "echo first") || strings.Contains(view, "chosen-model") {
			t.Fatalf("command must appear before long advice: %s", view)
		}
		if lipgloss.Height(view) > size[1] {
			t.Fatalf("height overflow at %v", size)
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("width overflow at %v", size)
			}
		}
		for !strings.Contains(view, "LAST_COMMAND") {
			tab.updateApprovalDialog(tea.KeyMsg{Type: tea.KeyDown})
			next := tab.View(size[0], size[1])
			if next == view {
				t.Fatal("command tail unreachable")
			}
			view = next
		}
		tab.updateApprovalDialog(tea.KeyMsg{Type: tea.KeyEnd})
		if !strings.Contains(tab.View(size[0], size[1]), "Uncertainty: Contents unknown.") {
			t.Fatal("advice below the command is unreachable")
		}
	}
	_, cmd := tab.updateApprovalDialog(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || tab.approvalInFlight {
		t.Fatal("closing advice authorized execution")
	}
	tab.applyTurnResult(agent.ChatTurnResult{TaskState: state.TaskStateBlockedWaitingUser, BlockedWorkID: "work", ApprovalSummary: "echo same", ApprovalAdvice: advice}, nil)
	if tab.pendingApproval.advice != advice {
		t.Fatal("turn result lost advice")
	}
}

func TestAdvisorDialogCanInterruptWithoutApproving(t *testing.T) {
	tab := newChatTab().(*chatTab)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tab.running, tab.cancelTurn = true, cancel
	tab.pendingApproval = &pendingChatApproval{workID: "pending", summary: "echo test", advice: &tools.CommandAdvice{Unavailable: true}}
	_, cmd := tab.updateLive(tea.KeyMsg{Type: tea.KeyCtrlX}, nil)
	if cmd != nil || ctx.Err() != context.Canceled || tab.approvalInFlight {
		t.Fatal("approval dialog swallowed interruption or approved work")
	}
}
