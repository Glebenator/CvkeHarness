package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Inspect approvals, shortlist candidates, and model stats",
}

var modelsShortlistCmd = &cobra.Command{
	Use:   "shortlist",
	Short: "Show approved models and learned routing candidates",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		store := state.Open(cfg.StateDBPath)
		defer store.Close()

		fmt.Println("Approved:")
		if len(cfg.ApprovedModels) == 0 {
			fmt.Println("- (none)")
		}
		for _, item := range cfg.ApprovedModels {
			fmt.Printf("- %s\n", item)
		}

		if !store.Available() {
			fmt.Printf("\nRouting shortlist unavailable: %v\n", store.Err())
			return nil
		}

		candidates, err := store.ListRoutingCandidates(context.Background())
		if err != nil {
			return err
		}
		fmt.Println("\nLearned shortlist:")
		if len(candidates) == 0 {
			fmt.Println("- (none yet)")
			return nil
		}
		for _, item := range candidates {
			fmt.Printf("- %s/%s phase=%s task=%s score=%.2f confidence=%.2f status=%s\n",
				item.Provider, item.Model, item.Phase, item.TaskClass, item.Score, item.Confidence, item.Status)
			if item.Reason != "" {
				fmt.Printf("  %s\n", item.Reason)
			}
		}
		return nil
	},
}

var modelsApproveCmd = &cobra.Command{
	Use:   "approve [provider/model]",
	Short: "Approve a model for future routing",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		ref := core.ParseModelRef(args[0], cfg.Provider)
		if ref.IsZero() {
			return fmt.Errorf("invalid model reference %q", args[0])
		}

		normalized := ref.String()
		already := false
		for _, item := range cfg.ApprovedModels {
			if item == normalized {
				already = true
				break
			}
		}
		if !already {
			cfg.ApprovedModels = append(cfg.ApprovedModels, normalized)
		}
		if err := cfg.Save(); err != nil {
			return err
		}

		store := state.Open(cfg.StateDBPath)
		defer store.Close()
		if store.Available() {
			if err := store.SaveModelApproval(context.Background(), state.ModelApproval{
				Provider:  ref.Provider,
				Model:     ref.Model,
				Status:    state.ApprovalStatusApproved,
				Source:    "cli",
				Rationale: "user approved via models approve",
			}); err != nil {
				return err
			}
		}

		fmt.Printf("Approved %s\n", normalized)
		return nil
	},
}

var modelsStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show normalized per-model performance stats",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		store := state.Open(cfg.StateDBPath)
		defer store.Close()
		if !store.Available() {
			return fmt.Errorf("state database unavailable: %w", store.Err())
		}

		stats, err := store.ListAllModelStats(context.Background())
		if err != nil {
			return err
		}
		if len(stats) == 0 {
			fmt.Println("No model stats recorded yet")
			return nil
		}
		for _, item := range stats {
			successRate := 0.0
			if item.Runs > 0 {
				successRate = float64(item.Successes) / float64(item.Runs) * 100
			}
			fmt.Printf("- %s/%s phase=%s task=%s toolset=%s runs=%d success=%.0f%% denials=%d avg_latency=%.0fms last_seen=%s\n",
				item.Provider, item.Model, item.Phase, item.TaskClass, item.Toolset, item.Runs, successRate, item.PolicyDenials, item.AvgLatencyMs, item.LastSeenAt.Format(time.RFC3339))
		}
		return nil
	},
}

func init() {
	modelsCmd.AddCommand(modelsShortlistCmd)
	modelsCmd.AddCommand(modelsApproveCmd)
	modelsCmd.AddCommand(modelsStatsCmd)
	rootCmd.AddCommand(modelsCmd)
}
