package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/log"
	dashboard "github.com/coolcake/cvkeharness/internal/tui"
	"github.com/coolcake/cvkeharness/scheduler"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
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
		if setupMode {
			service.MarkSetupMode()
		}
		return dashboard.Run(service, os.Args[0])
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
