package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/router"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

var explainRouting bool

var runCmd = &cobra.Command{
	Use:   "run [task]",
	Short: "Execute a DevOps task via the LLM agent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		task := args[0]

		cfg, err := config.LoadConfig()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		log.Init(cfg.LogLevel, "text")
		ctx := context.Background()
		logger := log.FromContext(ctx)
		logger.Info("CvkeHarness starting up", "default_model", cfg.PrimaryModel())

		p, err := providerFromConfig(cfg)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		registry := tools.NewDefaultRegistry(cfg.AllowedCommands, p, cfg.SafetyModel, cfg.PrimaryModel())
		store := state.Open(cfg.StateDBPath)
		if store.Err() != nil {
			fmt.Printf("Warning: state DB unavailable, continuing with file-only memory fallback (%v)\n", store.Err())
		}
		defer store.Close()

		mem := memory.NewManager(cfg.MemoryDir, store, cfg.MemoryMaxSnippets)
		if err := mem.EnsureFiles(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if err := mem.Reindex(ctx); err != nil {
			logger.Warn("failed to reindex memory metadata", "error", err)
		}

		routingCfg := routingConfigFromConfig(cfg, store)
		r := router.New(routingCfg, store, func(ctx context.Context, selection core.RoutingSelection) (bool, error) {
			if selection.Recommendation == nil {
				return false, nil
			}
			return promptModelApproval(*selection.Recommendation, selection.RecommendationReason)
		})

		a := agent.New(agent.Options{
			Provider:         p,
			ProviderName:     cfg.Provider,
			ProviderResolver: providerResolver{cfg: cfg},
			ToolRegistry:     registry,
			DefaultModel:     cfg.PrimaryModel(),
			MaxIterations:    cfg.MaxIterations,
			MaxTokens:        cfg.MaxTokens,
			RoutingConfig:    routingCfg,
			Router:           r,
			MemoryRetriever:  mem,
			MemoryCurator:    mem,
			RunRecorder:      store,
		})

		fmt.Printf("\nExecuting task: %s\n", task)
		fmt.Println("----------------------------------------")

		result, err := a.Run(ctx, task)
		if explainRouting {
			printRoutingExplanation(result.Routing)
		}
		if err != nil {
			fmt.Printf("\nAgent failed: %v\n", err)
			if result.Output != "" {
				fmt.Println("\nPartial Result:")
				fmt.Println(result.Output)
			}
			os.Exit(1)
		}

		fmt.Println("\nResult:")
		fmt.Println(result.Output)
	},
}

func init() {
	runCmd.Flags().BoolVar(&explainRouting, "explain-routing", false, "show why each phase/model choice was made")
	rootCmd.AddCommand(runCmd)
}

func promptModelApproval(ref core.ModelRef, reason string) (bool, error) {
	fmt.Printf("\nRouting recommendation: use %s\n", ref.String())
	if strings.TrimSpace(reason) != "" {
		fmt.Printf("Reason: %s\n", reason)
	}
	fmt.Print("Approve for this run? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
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
