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
	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/router"
	"github.com/coolcake/cvkeharness/scheduler"
	"github.com/coolcake/cvkeharness/state"
	"github.com/spf13/cobra"
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Manage CvkeHarness scheduled jobs",
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List scheduled jobs",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		jobs, err := store.ListScheduledJobs(context.Background(), true)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			status := "disabled"
			if job.Enabled {
				status = "enabled"
			}
			next := "(none)"
			if !job.NextRunAt.IsZero() {
				next = job.NextRunAt.Format(time.RFC3339)
			}
			fmt.Printf("- %s %s %s %s next=%s name=%q\n", job.ID, status, job.ScheduleKind, job.ScheduleSpec, next, job.Name)
		}
		return nil
	},
}

var jobsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a scheduled agent job",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		kind, _ := cmd.Flags().GetString("kind")
		spec, _ := cmd.Flags().GetString("spec")
		prompt, _ := cmd.Flags().GetString("prompt")
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		job, err := scheduler.New(store).Create(context.Background(), name, kind, spec, prompt)
		if err != nil {
			return err
		}
		fmt.Printf("Created %s next=%s\n", job.ID, job.NextRunAt.Format(time.RFC3339))
		return nil
	},
}

var jobsRunCmd = &cobra.Command{
	Use:   "run [job-id]",
	Short: "Run a scheduled job immediately",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		runner, err := newScheduledAgentRunner(ctx, store)
		if err != nil {
			return err
		}
		run, err := scheduler.New(store).RunNow(ctx, runner, args[0], true)
		if err != nil {
			return err
		}
		fmt.Printf("Run %d status=%s\n%s\n", run.ID, run.Status, run.Output)
		return nil
	},
}

var jobsRunLoopCmd = &cobra.Command{
	Use:   "run-loop",
	Short: "Run due jobs continuously",
	RunE: func(cmd *cobra.Command, args []string) error {
		interval, _ := cmd.Flags().GetDuration("interval")
		once, _ := cmd.Flags().GetBool("once")
		return runJobsLoop(context.Background(), interval, once)
	},
}

var jobsUpdateCmd = &cobra.Command{
	Use:   "update [job-id]",
	Short: "Update a scheduled agent job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		kind, _ := cmd.Flags().GetString("kind")
		spec, _ := cmd.Flags().GetString("spec")
		prompt, _ := cmd.Flags().GetString("prompt")
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		job, err := scheduler.New(store).Update(context.Background(), args[0], name, kind, spec, prompt)
		if err != nil {
			return err
		}
		fmt.Printf("Updated %s next=%s\n", job.ID, job.NextRunAt.Format(time.RFC3339))
		return nil
	},
}

var jobsPauseCmd = &cobra.Command{
	Use:   "pause [job-id]",
	Short: "Pause a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		_, err = scheduler.New(store).SetEnabled(context.Background(), args[0], false)
		return err
	},
}

var jobsResumeCmd = &cobra.Command{
	Use:   "resume [job-id]",
	Short: "Resume a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		_, err = scheduler.New(store).SetEnabled(context.Background(), args[0], true)
		return err
	},
}

var jobsRemoveCmd = &cobra.Command{
	Use:   "remove [job-id]",
	Short: "Remove a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		return store.DeleteScheduledJob(context.Background(), args[0])
	},
}

var jobsRunsCmd = &cobra.Command{
	Use:   "runs [job-id]",
	Short: "Show scheduled job run history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		runs, err := store.ListScheduledJobRuns(context.Background(), args[0], 20)
		if err != nil {
			return err
		}
		for _, run := range runs {
			fmt.Printf("- %d status=%s started=%s error=%q\n", run.ID, run.Status, run.StartedAt.Format(time.RFC3339), run.Error)
		}
		return nil
	},
}

var jobsHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Show scheduler health derived from telemetry",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := openState()
		if err != nil {
			return err
		}
		defer closeFn()
		health, err := store.ListSchedulerHealth(context.Background())
		if err != nil {
			return err
		}
		for _, item := range health {
			fmt.Printf("- %s status=%s blocked=%t overdue=%t claimed=%t stale_claim=%t heartbeat_fresh=%t last_heartbeat=%s last_finished=%s\n",
				item.JobID,
				emptyAs(item.LastStatus, "(none)"),
				item.Blocked,
				item.Overdue,
				item.Claimed,
				item.StaleClaim,
				item.HeartbeatFresh,
				formatOptionalTime(item.LastHeartbeatAt),
				formatOptionalTime(item.LastFinishedAt),
			)
		}
		return nil
	},
}

type scheduledAgentRunner struct {
	agent *agent.Agent
}

func (r scheduledAgentRunner) RunScheduledJob(ctx context.Context, job state.ScheduledJob) (string, int64, error) {
	result, err := r.agent.Run(agent.WithScheduledJobID(ctx, job.ID), job.Prompt)
	return result.Output, result.Run.ID, err
}

func newScheduledAgentRunner(ctx context.Context, store *state.Store) (scheduledAgentRunner, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return scheduledAgentRunner{}, err
	}
	log.Init(cfg.LogLevel, "text")
	p, err := resolveProvider(cfg, "")
	if err != nil {
		return scheduledAgentRunner{}, err
	}
	mem := memory.NewManager(cfg.MemoryDir, store)
	if err := mem.EnsureFiles(); err != nil {
		return scheduledAgentRunner{}, err
	}
	if err := mem.Reindex(ctx); err != nil {
		log.FromContext(ctx).Warn("failed to reindex memory metadata", "error", err)
	}
	promptDumper := promptdump.NewWithRetentionDays(cfg.DebugPromptDumps, cfg.PromptDumpDir, cfg.PromptDumpRetentionDays)
	telemetryWriter := telemetryWriterFromConfig(cfg, store)
	registry, err := defaultRegistryFromConfig(cfg, store, mem, p, promptDumper, true)
	if err != nil {
		return scheduledAgentRunner{}, err
	}
	routingCfg := routingConfigFromConfig(cfg, store)
	rt := router.New(routingCfg, store, func(ctx context.Context, selection core.RoutingSelection) (bool, error) {
		return false, nil
	})
	return scheduledAgentRunner{agent: agent.New(agent.Options{
		Provider:           p,
		ProviderName:       cfg.Provider,
		ProviderResolver:   providerResolver{cfg: cfg},
		ToolRegistry:       registry,
		DefaultModel:       cfg.PrimaryModel(),
		MaxIterations:      cfg.MaxIterations,
		MaxTokens:          cfg.MaxTokens,
		RoutingConfig:      routingCfg,
		Router:             rt,
		MemoryRetriever:    mem,
		MemoryCurator:      mem,
		RunRecorder:        store,
		BlockedWorkStore:   store,
		PromptDumper:       promptDumper,
		TelemetryWriter:    telemetryWriter,
		SafetyMode:         cfg.SafetyMode,
		SafetyModel:        cfg.SafetyModel,
		ClassifierProvider: p,
	})}, nil
}

func runJobsLoop(ctx context.Context, interval time.Duration, once bool) error {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	store, closeFn, err := openState()
	if err != nil {
		return err
	}
	defer closeFn()
	runner, err := newScheduledAgentRunner(ctx, store)
	if err != nil {
		return err
	}
	svc := scheduler.New(store)
	cfg, cfgErr := config.LoadConfig()
	if cfgErr == nil {
		svc.SetTelemetryWriter(telemetryWriterFromConfig(cfg, store))
	}
	for {
		runs, err := svc.RunDue(ctx, runner)
		if err != nil {
			return err
		}
		for _, run := range runs {
			fmt.Printf("job=%s run=%d status=%s\n", run.JobID, run.ID, run.Status)
		}
		if once {
			return nil
		}
		time.Sleep(interval)
	}
}

func openState() (*state.Store, func(), error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	store := state.Open(cfg.StateDBPath)
	if !store.Available() {
		return nil, func() {}, fmt.Errorf("state database unavailable: %w", store.Err())
	}
	return store, func() { _ = store.Close() }, nil
}

func confirmCLI(prompt string) bool {
	idx, err := termui.Select(termui.SelectOptions{
		Title: prompt,
		Details: []string{
			"Review the diff above before applying the change.",
		},
		Choices: []termui.Choice{
			{Label: "Cancel", Description: "Leave the current crontab unchanged"},
			{Label: "Apply change", Description: "Write the reviewed crontab update"},
		},
		InitialIndex: 0,
		In:           os.Stdin,
		Out:          os.Stdout,
	})
	if err != nil {
		return false
	}
	return idx == 1
}

func emptyAs(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return "(none)"
	}
	return value.Format(time.RFC3339)
}

func init() {
	jobsAddCmd.Flags().String("name", "", "job name")
	jobsAddCmd.Flags().String("kind", "every", "schedule kind: at, every, cron")
	jobsAddCmd.Flags().String("spec", "", "schedule spec")
	jobsAddCmd.Flags().String("prompt", "", "agent prompt")
	jobsUpdateCmd.Flags().String("name", "", "job name")
	jobsUpdateCmd.Flags().String("kind", "", "schedule kind: at, every, cron")
	jobsUpdateCmd.Flags().String("spec", "", "schedule spec")
	jobsUpdateCmd.Flags().String("prompt", "", "agent prompt")
	jobsRunLoopCmd.Flags().Duration("interval", 30*time.Second, "poll interval")
	jobsRunLoopCmd.Flags().Bool("once", false, "run due jobs once and exit")

	jobsCmd.AddCommand(jobsListCmd, jobsAddCmd, jobsUpdateCmd, jobsRunCmd, jobsRunLoopCmd, jobsPauseCmd, jobsResumeCmd, jobsRemoveCmd, jobsRunsCmd, jobsHealthCmd)
	rootCmd.AddCommand(jobsCmd)
}
