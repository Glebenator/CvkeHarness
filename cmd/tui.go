package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/log"
	dashboard "github.com/coolcake/cvkeharness/internal/tui"
	"github.com/coolcake/cvkeharness/scheduler"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Open the interactive CvkeHarness operations dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		setupMode := false
		cfg, err := config.LoadConfig()
		if err != nil {
			cfg = config.DefaultConfig()
			cfg.Normalize()
			setupMode = true
		}
		log.Init(cfg.LogLevel, "text")

		store := state.Open(cfg.StateDBPath)
		if !store.Available() {
			return fmt.Errorf("state database unavailable: %w", store.Err())
		}
		defer store.Close()

		sched := scheduler.New(store)

		runJobNow := func(ctx context.Context, id string) (state.ScheduledJobRun, error) {
			runner, err := newScheduledAgentRunner(ctx, store)
			if err != nil {
				return state.ScheduledJobRun{}, err
			}
			return sched.RunNow(ctx, runner, id, true)
		}

		service := dashboard.NewService(cfg, store, systemcron.New(nil), sched, runJobNow)
		service.SetChatStarter(func(ctx context.Context, cfg *config.Config, observer tools.EventObserver) (dashboard.LiveChatSession, error) {
			switch cfg.Provider {
			case "openrouter", "openai":
				if cfg.GetAPIKey(cfg.Provider) == "" {
					return nil, fmt.Errorf("missing %s API key; open Settings or run setup to add credentials", cfg.Provider)
				}
			}
			a, err := newChatAgent(ctx, cfg, store, observer, true, func(context.Context, core.RoutingSelection) (bool, error) {
				// The console never prompts on stdin behind Bubble Tea. Unapproved
				// routing recommendations safely fall back to the configured model.
				return false, nil
			})
			if err != nil {
				return nil, err
			}
			conversation, sessionID, err := startChatSession(ctx, a, store, cfg)
			if err != nil {
				return nil, err
			}
			return &dashboardChatSession{
				conversation: conversation,
				cfg:          cfg,
				store:        store,
				sessionID:    sessionID,
				stats:        newChatSessionStats(conversation.Selection()),
			}, nil
		})
		if setupMode {
			service.MarkSetupMode()
		}
		return dashboard.Run(service, os.Args[0])
	},
}

type dashboardChatSession struct {
	conversation *agent.ChatConversation
	cfg          *config.Config
	store        *state.Store
	sessionID    int64
	stats        *chatSessionStats
	closed       bool
}

func (s *dashboardChatSession) ID() int64 { return s.sessionID }

func (s *dashboardChatSession) Selection() core.RoutingSelection {
	return s.conversation.Selection()
}

func (s *dashboardChatSession) Tools() []agent.ChatTool { return s.conversation.Tools() }

func (s *dashboardChatSession) Turn(ctx context.Context, prompt string) (agent.ChatTurnResult, error) {
	result, err := s.conversation.Turn(ctx, prompt)
	current := &chatSessionState{
		session:   s.conversation,
		sessionID: s.sessionID,
		stats:     s.stats,
	}
	recordChatTurn(ctx, s.store, current, prompt, result)
	s.sessionID = current.sessionID
	return result, err
}

func (s *dashboardChatSession) ApproveBlockedWork(ctx context.Context, workID string) (state.SecurityActionGrant, error) {
	if s.closed || s.conversation == nil {
		return state.SecurityActionGrant{}, fmt.Errorf("chat session is closed")
	}
	if s.cfg == nil {
		return state.SecurityActionGrant{}, fmt.Errorf("chat approval configuration is unavailable")
	}
	policy, err := s.cfg.EffectiveSecurity()
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	grant, err := tools.ApproveBlockedWork(ctx, s.store, policy, workID, 15*time.Minute, "tui-blocked-work")
	if err != nil {
		return state.SecurityActionGrant{}, err
	}
	if err := s.conversation.ResumeApproval(workID); err != nil {
		return state.SecurityActionGrant{}, err
	}
	return grant, nil
}

func (s *dashboardChatSession) Close(ctx context.Context, exitReason string) {
	if s.closed {
		return
	}
	finishChatSession(ctx, s.store, s.sessionID, exitReason)
	s.closed = true
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
