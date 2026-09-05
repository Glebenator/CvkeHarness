package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

// Ticks are scoped to an active animation chain. Completion and reduced motion
// invalidate any queued tick, so idle screens never keep a repaint loop alive.
type activityFrameMsg struct{ epoch uint64 }

var activityFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m model) activityGlyph() string {
	if m.reduceMotion {
		return "…"
	}
	return activityFrames[m.motionFrame%len(activityFrames)]
}
func (m model) chatBusy() bool {
	chat, ok := m.tabs[tabChat].(*chatTab)
	return ok && (chat.starting || chat.running || chat.approvalInFlight) && (chat.pendingApproval == nil || chat.approvalInFlight)
}
func (m model) hasActivity() bool {
	if m.quitSaving || m.chatBusy() {
		return true
	}
	if job, ok := m.tabs[tabJobs].(*jobsTab); ok && job.creating {
		return true
	}
	if cfg, ok := m.tabs[tabConfig].(*configTab); ok && cfg.saving {
		return true
	}
	return false
}
func (m *model) syncActivity() tea.Cmd {
	if chat, ok := m.tabs[tabChat].(*chatTab); ok {
		chat.activityMarker = ""
		if m.chatBusy() {
			chat.activityMarker = m.activityGlyph()
		}
	}
	if job, ok := m.tabs[tabJobs].(*jobsTab); ok {
		job.activityMarker = m.activityGlyph()
	}
	if !m.hasActivity() || m.reduceMotion {
		if m.motionPending {
			m.motionEpoch++
		}
		m.motionPending = false
		m.motionFrame = 0
		return nil
	}
	if m.motionPending {
		return nil
	}
	m.motionPending = true
	m.motionEpoch++
	epoch := m.motionEpoch
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return activityFrameMsg{epoch: epoch} })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var next model
	var cmd tea.Cmd
	if frame, ok := msg.(activityFrameMsg); ok {
		if frame.epoch != m.motionEpoch || !m.motionPending {
			return m, nil
		}
		m.motionPending = false
		m.motionFrame = (m.motionFrame + 1) % len(activityFrames)
		next = m
	} else {
		updated, effect := m.update(msg)
		next = updated.(model)
		cmd = effect
	}
	motion := next.syncActivity()
	// Retain a single command's type and semantics for callers and tests.
	if motion == nil {
		return next, cmd
	}
	if cmd == nil {
		return next, motion
	}
	return next, tea.Batch(cmd, motion)
}
