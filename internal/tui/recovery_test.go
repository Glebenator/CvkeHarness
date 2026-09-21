package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/state"
)

func TestOverviewSSHConfirmationRemainsExplicit(t *testing.T) {
	tab := newOverviewTab().(*overviewTab)
	manifest, _ := json.Marshal(recovery.Manifest{SSH: &recovery.SSHPlan{Config: recovery.SSHService{Name: "managed", ConfirmationSeconds: 30}, OldPort: 22, NewPort: 2223}})
	model, cmd := tab.Update(overviewDataMsg{recovery: []state.RecoveryOperation{{ID: "ssh-operation", Status: recovery.AwaitingConfirmation, Manifest: manifest}}}, nil, 100, 50)
	if cmd != nil {
		t.Fatal("SSH visibility triggered an action")
	}
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter}, nil, 100, 50)
	if cmd != nil {
		t.Fatal("SSH inspection confirmed or restored an operation")
	}
	view := model.View(100, 50)
	for _, want := range []string{"Guarded SSH change", "22 -> 2223", "new authenticated connection", "recovery ssh confirm"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing SSH context %q: %s", want, view)
		}
	}
}

func TestOverviewFleetInspectionNeverDispatches(t *testing.T) {
	tab := newOverviewTab().(*overviewTab)
	manifest, _ := json.Marshal(recovery.FleetPlan{Impact: recovery.FleetImpact{Hosts: 2, Files: 2}, Limits: recovery.DefaultFleetLimits()})
	model, cmd := tab.Update(overviewDataMsg{batches: []state.RecoveryBatch{{ID: "batch", Status: recovery.Unknown, Manifest: manifest, Progress: json.RawMessage(`[]`)}}}, nil, 110, 50)
	if cmd != nil {
		t.Fatal("loading batch dispatched a command")
	}
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter}, nil, 110, 50)
	if cmd != nil {
		t.Fatal("opening batch dispatched a command")
	}
	view := model.View(110, 50)
	for _, want := range []string{"Fleet recovery batch", "2 hosts", "cannot be applied again", "recovery fleet reconcile", "never dispatches"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q: %s", want, view)
		}
	}
}

func TestOverviewShowsRecoveryWithoutStartingMutation(t *testing.T) {
	tab := newOverviewTab().(*overviewTab)
	msg := overviewDataMsg{recovery: []state.RecoveryOperation{{ID: "test-operation", Status: "outcome_unknown", Target: "executor", UpdatedAt: time.Now(), Problem: "interrupted"}}}
	model, cmd := tab.Update(msg, nil, 80, 30)
	if cmd != nil {
		t.Fatal("loading recovery state triggered a command")
	}
	if !strings.Contains(model.View(80, 30), "outcome unknown") {
		t.Fatal("unknown outcome not visible")
	}
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter}, nil, 80, 30)
	if cmd != nil {
		t.Fatal("opening recovery details triggered a command")
	}
	view := model.View(80, 30)
	if !strings.Contains(view, "recovery inspect") || !strings.Contains(view, "may have changed some files") {
		t.Fatalf("missing recovery context: %s", view)
	}
}

func TestApprovalReceiptSurvivesFastCompletion(t *testing.T) {
	tab := newChatTab().(*chatTab)
	tab.approvalWorkID = "work-current"
	tab.status = "READY"
	tab.toolCalls = []liveToolCall{{name: "recovery_manage", status: "SUCCEEDED"}}
	updated, _ := tab.Update(chatApprovalDoneMsg{workID: "work-current", grant: state.SecurityActionGrant{ActionKind: "recovery_manage"}}, nil, 100, 30)
	tab = updated.(*chatTab)
	if tab.status != "READY" || !strings.Contains(tab.renderToolSelection(96), "APPROVED ONCE") {
		t.Fatal("fast tool completion hid or regressed approval receipt")
	}
	tab.approvalNotice = ""
	tab.Update(chatApprovalDoneMsg{workID: "work-old", grant: state.SecurityActionGrant{ActionKind: "shell_execute"}}, nil, 100, 30)
	if tab.approvalNotice != "" {
		t.Fatal("accepted stale approval completion")
	}
}
