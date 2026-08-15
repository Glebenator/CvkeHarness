package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/cli"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/router"
	"github.com/coolcake/cvkeharness/state"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var explainRouting bool
var streamShell bool
var streamMode string

type runOutcome struct {
	result agent.RunResult
	err    error
}

var runCmd = &cobra.Command{
	Use:   "run [task]",
	Short: "Run one bounded agent task and exit",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		task := args[0]

		cfg, err := config.LoadConfig()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		console := streamConsole()
		if console != nil {
			log.InitWithWriter(cfg.LogLevel, "text", console)
		} else {
			log.Init(cfg.LogLevel, "text")
		}
		ctx := context.Background()
		logger := log.FromContext(ctx)
		logger.Debug("CvkeHarness starting up", "default_model", cfg.PrimaryModel())

		p, err := resolveProvider(cfg, "")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		store := state.Open(cfg.StateDBPath)
		if store.Err() != nil {
			fmt.Printf("Warning: state DB unavailable, continuing with file-only memory fallback (%v)\n", store.Err())
		}
		defer store.Close()

		mem := memory.NewManager(cfg.MemoryDir, store)
		if err := mem.EnsureFiles(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if err := mem.Reindex(ctx); err != nil {
			logger.Warn("failed to reindex memory metadata", "error", err)
		}
		promptDumper := promptdump.NewWithRetentionDays(cfg.DebugPromptDumps, cfg.PromptDumpDir, cfg.PromptDumpRetentionDays)
		telemetryWriter := telemetryWriterFromConfig(cfg, store)
		registry, err := defaultRegistryFromConfig(cfg, store, mem, p, promptDumper, false)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		routingCfg := routingConfigFromConfig(cfg, store)
		r := router.New(routingCfg, store, func(ctx context.Context, selection core.RoutingSelection) (bool, error) {
			if selection.Recommendation == nil {
				return false, nil
			}
			return promptModelApproval(*selection.Recommendation, selection.RecommendationReason)
		})

		a := agent.New(agent.Options{
			Provider:           p,
			ProviderName:       cfg.Provider,
			ProviderResolver:   providerResolver{cfg: cfg},
			ToolRegistry:       registry,
			EventObserver:      console,
			DefaultModel:       cfg.PrimaryModel(),
			MaxIterations:      cfg.MaxIterations,
			MaxTokens:          cfg.MaxTokens,
			RoutingConfig:      routingCfg,
			Router:             r,
			MemoryRetriever:    mem,
			MemoryCurator:      mem,
			RunRecorder:        store,
			BlockedWorkStore:   store,
			PromptDumper:       promptDumper,
			TelemetryWriter:    telemetryWriter,
			SafetyMode:         cfg.SafetyMode,
			SafetyModel:        cfg.SafetyModel,
			ClassifierProvider: p,
		})

		initialTarget, targetErr := mem.ResolveTarget(ctx, memory.TargetResolutionInput{Task: task})
		if targetErr != nil {
			logger.Warn("failed to resolve initial run target", "error", targetErr)
		}
		ui := runSurfaceForOutput(os.Stdout)
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signals)

		ui.PrintRunHeader(cli.RunHeader{
			Task:   task,
			Target: runTargetLabel(initialTarget),
			Model:  core.NewModelRef(cfg.Provider, cfg.PrimaryModel()).String(),
		})

		runCtx, cancelRun := context.WithCancel(ctx)
		defer cancelRun()

		outcomeCh := make(chan runOutcome, 1)
		go func() {
			result, runErr := a.Run(runCtx, task)
			outcomeCh <- runOutcome{result: result, err: runErr}
		}()

		var result agent.RunResult
		interrupted := false
		exitReason := ""
		select {
		case sig := <-signals:
			interrupted = true
			exitReason = signalExitReason(sig)
			cancelRun()
			fmt.Println()
			ui.PrintInfo("Interrupt", []string{"Stopping the current run and waiting for cleanup..."})
			select {
			case outcome := <-outcomeCh:
				result = outcome.result
				err = outcome.err
			case sig := <-signals:
				exitReason = signalExitReason(sig)
				err = context.Canceled
			}
		case outcome := <-outcomeCh:
			result = outcome.result
			err = outcome.err
		}
		if interrupted && exitReason == "" {
			exitReason = "interrupt"
		}

		if explainRouting {
			printRoutingExplanation(result.Routing)
		}
		summary := summarizeRunResult(result, exitReason)
		if err != nil {
			if interrupted || errors.Is(err, context.Canceled) {
				ui.PrintRunSummary(summary)
			} else if result.Run.TaskState == state.TaskStateBlockedWaitingUser {
				ui.PrintApprovalRequired(blockedRunNotice(result))
				ui.PrintRunSummary(summary)
			} else {
				ui.PrintInfo("Failure", []string{err.Error()})
				ui.PrintRunSummary(summary)
			}
			if result.Output != "" {
				ui.PrintAnswer(result.Output, true)
			}
			ui.PrintRunReceipt(summary)
			os.Exit(1)
		}

		ui.PrintRunSummary(summary)
		ui.PrintAnswer(result.Output, false)
		ui.PrintRunReceipt(summary)
	},
}

func blockedRunNotice(result agent.RunResult) cli.ApprovalNotice {
	notice := cli.ApprovalNotice{
		Action: "Protected action",
		Reason: "The current safety policy requires explicit operator approval.",
		Scope:  approvalScope(result),
		Effect: "Approve once + retry. No action has run.",
	}
	if approval := result.BlockedApproval; approval != nil {
		if strings.TrimSpace(approval.Action) != "" {
			notice.Action = approval.Action
		}
		if strings.TrimSpace(approval.Reason) != "" {
			notice.Reason = approval.Reason
		}
	}
	if result.BlockedWorkID != "" {
		notice.Approve = fmt.Sprintf("cvkeharness commands approve-work %s", result.BlockedWorkID)
	}
	return notice
}

func init() {
	runCmd.Flags().BoolVar(&explainRouting, "explain-routing", false, "show why each phase/model choice was made")
	runCmd.Flags().BoolVar(&streamShell, "stream-shell", true, "stream shell tool output to stderr while commands run")
	runCmd.Flags().StringVar(&streamMode, "stream-mode", "auto", "shell transcript rendering mode: auto, plain, or rich")
	rootCmd.AddCommand(runCmd)
}

func promptModelApproval(ref core.ModelRef, reason string) (bool, error) {
	if !shouldPromptModelApproval(
		term.IsTerminal(int(os.Stdin.Fd())),
		term.IsTerminal(int(os.Stdout.Fd())),
	) {
		return false, nil
	}
	return promptModelApprovalWithIO(os.Stdin, os.Stdout, ref, reason)
}

func shouldPromptModelApproval(stdinTTY, stdoutTTY bool) bool {
	return stdinTTY && stdoutTTY
}

func promptModelApprovalWithIO(in io.Reader, out io.Writer, ref core.ModelRef, reason string) (bool, error) {
	details := []string{
		fmt.Sprintf("Recommended model: %s", ref.String()),
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		details = append(details, "Reason: "+trimmed)
	}

	idx, err := termui.Select(termui.SelectOptions{
		Title:   "Routing recommendation",
		Details: details,
		Choices: []termui.Choice{
			{Label: "Stay on approved model", Description: "Reject this recommendation for now"},
			{Label: "Approve model for this run", Description: "Use the recommended model once"},
		},
		InitialIndex: 0,
		In:           in,
		Out:          out,
	})
	if err != nil {
		return false, err
	}
	return idx == 1, nil
}

func printRoutingExplanation(selections []core.RoutingSelection) {
	if len(selections) == 0 {
		return
	}

	fmt.Println("\nRouting:")
	for _, selection := range selections {
		modelID := selection.Requested.String()
		if modelID == "" {
			modelID = "(none)"
		}
		fmt.Printf("- %s: %s", selection.Phase, modelID)
		if selection.Confidence > 0 {
			fmt.Printf(" (confidence %.2f)", selection.Confidence)
		}
		fmt.Println()
		if selection.Reason != "" {
			fmt.Printf("  %s\n", selection.Reason)
		}
		if selection.Recommendation != nil && selection.RecommendationReason != "" {
			fmt.Printf("  Recommendation: %s\n", selection.RecommendationReason)
		}
	}
}

func summarizeRunResult(result agent.RunResult, exitReason string) cli.RunSummary {
	run := result.Run
	modelCounts := make(map[string]int)
	summary := cli.RunSummary{
		ExitReason:         summarizeRunExit(run.TaskState, exitReason),
		VerificationStatus: strings.TrimSpace(result.Verification.Status),
	}
	if strings.TrimSpace(result.Target.PrimaryName) != "" || strings.TrimSpace(result.Target.TargetID) != "" {
		summary.Target = runTargetLabel(result.Target)
	}
	if summary.ExitReason != "Completed" {
		summary.ExitCode = 1
	}
	if summary.VerificationStatus == "" && run.TaskState == state.TaskStateBlockedWaitingUser {
		summary.VerificationStatus = "stopped safely"
	}

	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() && !run.FinishedAt.Before(run.StartedAt) {
		summary.Duration = run.FinishedAt.Sub(run.StartedAt)
	}

	for _, phase := range run.Phases {
		summary.PromptTokens += phase.PromptTokens
		summary.CompletionTokens += phase.CompletionTokens
		summary.TotalTokens += phase.TotalTokens
		if phase.CachedTokensKnown {
			summary.CachedTokensKnown = true
			summary.CachedTokens += phase.CachedTokens
		}
		if label := phaseModelLabel(phase); label != "" {
			modelCounts[label]++
		}
	}

	summary.ModelsUsed = summarizeModelCounts(modelCounts)
	summary.ToolCalls = len(run.Tools)
	for _, tool := range run.Tools {
		if tool.Success {
			summary.SuccessfulTools++
			continue
		}
		if tool.DenialClass == "approval_required" {
			summary.BlockedTools++
			continue
		}
		summary.FailedTools++
	}

	return summary
}

func streamConsole() *cli.TranscriptRenderer {
	if !streamShell {
		return nil
	}

	mode, ok := resolveStreamMode(streamMode)
	if !ok {
		fmt.Fprintf(os.Stderr, "Warning: unsupported stream mode %q, falling back to auto\n", streamMode)
		mode, ok = resolveStreamMode("auto")
	}
	if !ok {
		return nil
	}

	return cli.NewTranscriptRenderer(os.Stderr, mode)
}

func runSurfaceForOutput(out *os.File) *cli.RunSurface {
	mode := "plain"
	width := 80
	if out != nil && term.IsTerminal(int(out.Fd())) {
		if terminalWidth, _, err := term.GetSize(int(out.Fd())); err == nil && terminalWidth > 0 {
			width = terminalWidth
		}
		if strings.TrimSpace(os.Getenv("NO_COLOR")) == "" && !strings.EqualFold(os.Getenv("TERM"), "dumb") {
			mode = "rich"
		}
	}
	return cli.NewRunSurfaceWithOptions(out, cli.RunSurfaceOptions{Mode: mode, Width: width})
}

func runTargetLabel(target memory.TargetResolution) string {
	name := strings.TrimSpace(target.PrimaryName)
	if name == "" {
		name = strings.TrimSpace(target.TargetID)
	}
	if name == "" {
		return "unresolved"
	}
	switch target.TargetKind {
	case memory.TargetKindSSH:
		return name + " | remote via ssh"
	case memory.TargetKindLocalContainer:
		return name + " | local container"
	case memory.TargetKindRuntime:
		return name + " | runtime host"
	default:
		kind := strings.ReplaceAll(strings.TrimSpace(target.TargetKind), "_", " ")
		if kind == "" {
			return name
		}
		return name + " | " + kind
	}
}

func approvalScope(result agent.RunResult) string {
	approval := result.BlockedApproval
	var parts []string
	if approval != nil && strings.TrimSpace(approval.Host) != "" {
		parts = append(parts, approval.Host)
	} else if target := strings.TrimSpace(result.Target.PrimaryName); target != "" {
		parts = append(parts, target)
	} else if target := strings.TrimSpace(result.Target.TargetID); target != "" {
		parts = append(parts, target)
	}
	actionKind := "action"
	if approval != nil && strings.TrimSpace(approval.ActionKind) != "" {
		actionKind = strings.ReplaceAll(strings.TrimSpace(approval.ActionKind), "_", " ")
	}
	if actionKind == "shell execute" {
		actionKind = "command"
	}
	parts = append(parts, "exact "+actionKind)
	if approval != nil && strings.TrimSpace(approval.Principal) != "" {
		parts = append(parts, "principal "+approval.Principal)
	}
	if approval != nil && strings.TrimSpace(approval.WorkingDirectory) != "" {
		parts = append(parts, "cwd "+approval.WorkingDirectory)
	}
	parts = append(parts, "approve once")
	return strings.Join(parts, " | ")
}

func resolveStreamMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "plain":
		return "plain", true
	case "rich":
		return "rich", true
	case "", "auto":
		if !term.IsTerminal(int(os.Stderr.Fd())) {
			return "", false
		}
		if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
			return "plain", true
		}
		return "rich", true
	default:
		return "", false
	}
}
