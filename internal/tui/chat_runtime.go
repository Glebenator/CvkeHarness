package tui

import (
	"context"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/tools"
)

// LiveChatSession is the in-process chat boundary consumed by the Bubble Tea
// UI. The cmd package supplies the concrete runtime so the TUI reuses the same
// provider, routing, memory, tools, persistence, and telemetry construction as
// the CLI without importing the main package.
type LiveChatSession interface {
	ID() int64
	Selection() core.RoutingSelection
	Tools() []agent.ChatTool
	Turn(ctx context.Context, prompt string) (agent.ChatTurnResult, error)
	Close(ctx context.Context, exitReason string)
}

// StartChatFunc creates one live chat session with structured tool events.
type StartChatFunc func(ctx context.Context, observer tools.EventObserver) (LiveChatSession, error)
