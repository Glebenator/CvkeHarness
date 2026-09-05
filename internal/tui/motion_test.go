package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

func TestActivityTicksOnlyWhileWorkIsActive(t *testing.T) {
	m := recoveryModel()
	if cmd := m.syncActivity(); cmd != nil || m.motionPending {
		t.Fatal("idle screen schedules animation")
	}
	chat := m.tabs[tabChat].(*chatTab)
	chat.starting = true
	if cmd := m.syncActivity(); cmd == nil || !m.motionPending {
		t.Fatal("connection did not start animation")
	}
	epoch, glyph := m.motionEpoch, m.activityGlyph()
	next, cmd := m.Update(activityFrameMsg{epoch: epoch})
	m = next.(model)
	if cmd == nil || glyph == m.activityGlyph() {
		t.Fatal("activity did not advance")
	}
	oldEpoch := m.motionEpoch
	chat.starting = false
	m.syncActivity()
	next, cmd = m.Update(activityFrameMsg{epoch: oldEpoch})
	m = next.(model)
	if cmd != nil || m.motionPending || chat.activityMarker != "" {
		t.Fatal("completed work kept animating")
	}
}
func TestApprovalWaitAndReducedMotionDoNotAnimate(t *testing.T) {
	m := recoveryModel()
	chat := m.tabs[tabChat].(*chatTab)
	chat.running = true
	chat.pendingApproval = &pendingChatApproval{}
	if cmd := m.syncActivity(); cmd != nil {
		t.Fatal("approval wait looked like active execution")
	}
	chat.pendingApproval = nil
	m.reduceMotion = true
	if cmd := m.syncActivity(); cmd != nil || chat.activityMarker != "…" {
		t.Fatal("reduced motion did not use static activity")
	}
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	m = navigationText(m, "Enable motion")
	m = navigationKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.reduceMotion || !m.motionPending {
		t.Fatal("motion action did not resume activity")
	}
}
