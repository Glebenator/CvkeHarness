package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
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

var modelsFavoritesCmd = &cobra.Command{
	Use:   "favorites",
	Short: "Show saved favorite models",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}

		fmt.Println("Favorites:")
		if len(cfg.FavoriteModels) == 0 {
			fmt.Println("- (none)")
			return nil
		}
		for _, item := range cfg.FavoriteModels {
			fmt.Printf("- %s\n", item)
		}
		return nil
	},
}

var modelsFavoriteCmd = &cobra.Command{
	Use:   "favorite [provider/model]",
	Short: "Save a favorite model without changing routing approvals",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		normalized, err := normalizeModelArg(cfg, args[0])
		if err != nil {
			return err
		}
		if containsString(cfg.FavoriteModels, normalized) {
			fmt.Printf("Already in favorites: %s\n", normalized)
			return nil
		}
		cfg.FavoriteModels = append(cfg.FavoriteModels, normalized)
		sort.Strings(cfg.FavoriteModels)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Favorited %s\n", normalized)
		return nil
	},
}

var modelsUnfavoriteCmd = &cobra.Command{
	Use:   "unfavorite [provider/model]",
	Short: "Remove a model from favorites",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		normalized, err := normalizeModelArg(cfg, args[0])
		if err != nil {
			return err
		}

		next := make([]string, 0, len(cfg.FavoriteModels))
		removed := false
		for _, item := range cfg.FavoriteModels {
			if item == normalized {
				removed = true
				continue
			}
			next = append(next, item)
		}
		if !removed {
			fmt.Printf("Not in favorites: %s\n", normalized)
			return nil
		}
		cfg.FavoriteModels = next
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Removed favorite %s\n", normalized)
		return nil
	},
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

		fmt.Println("Favorites:")
		if len(cfg.FavoriteModels) == 0 {
			fmt.Println("- (none)")
		}
		for _, item := range cfg.FavoriteModels {
			fmt.Printf("- %s\n", item)
		}

		fmt.Println("\nApproved:")
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

var modelsRecentLimit int

var modelsRecentCmd = &cobra.Command{
	Use:   "recent",
	Short: "Show recently used models across runs and chat",
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

		items, err := store.ListRecentModelUsage(context.Background(), modelsRecentLimit)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No recent model usage recorded yet")
			return nil
		}

		favorites := stringSet(cfg.FavoriteModels)
		for _, item := range items {
			ref := core.NewModelRef(item.Provider, item.RequestedModel).String()
			marker := ""
			if favorites[ref] {
				marker = " favorite"
			}
			successRate := 0.0
			if item.Uses > 0 {
				successRate = float64(item.Successes) / float64(item.Uses) * 100
			}
			line := fmt.Sprintf("- %s/%s%s uses=%d success=%.0f%% last_used=%s",
				item.Provider,
				item.RequestedModel,
				marker,
				item.Uses,
				successRate,
				item.LastUsedAt.Format(time.RFC3339),
			)
			if strings.TrimSpace(item.ActualModel) != "" && item.ActualModel != item.RequestedModel {
				line += fmt.Sprintf(" resolved_as=%s/%s", item.Provider, item.ActualModel)
			}
			fmt.Println(line)
		}
		return nil
	},
}

var modelsAliasesCmd = &cobra.Command{
	Use:   "aliases",
	Short: "Show requested models that resolved to different actual models",
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

		items, err := store.ListModelAliases(context.Background())
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No model aliases recorded yet")
			return nil
		}

		for _, item := range items {
			fmt.Printf("- %s/%s -> %s/%s seen=%d first_seen=%s last_seen=%s\n",
				item.Provider,
				item.RequestedModel,
				item.Provider,
				item.ActualModel,
				item.SeenCount,
				item.FirstSeenAt.Format(time.RFC3339),
				item.LastSeenAt.Format(time.RFC3339),
			)
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
		normalized, err := normalizeModelArg(cfg, args[0])
		if err != nil {
			return err
		}
		ref := core.ParseModelRef(normalized, cfg.Provider)

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
	modelsRecentCmd.Flags().IntVar(&modelsRecentLimit, "limit", 10, "maximum number of recent model entries to show")
	modelsCmd.AddCommand(modelsFavoritesCmd)
	modelsCmd.AddCommand(modelsFavoriteCmd)
	modelsCmd.AddCommand(modelsUnfavoriteCmd)
	modelsCmd.AddCommand(modelsShortlistCmd)
	modelsCmd.AddCommand(modelsRecentCmd)
	modelsCmd.AddCommand(modelsAliasesCmd)
	modelsCmd.AddCommand(modelsApproveCmd)
	modelsCmd.AddCommand(modelsStatsCmd)
	rootCmd.AddCommand(modelsCmd)
}

func normalizeModelArg(cfg *config.Config, raw string) (string, error) {
	ref := core.ParseModelRef(raw, cfg.Provider)
	if ref.IsZero() {
		return "", fmt.Errorf("invalid model reference %q", raw)
	}
	return ref.String(), nil
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func stringSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out[item] = true
	}
	return out
}
