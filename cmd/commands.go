package cmd

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

var commandsCmd = &cobra.Command{
	Use:   "commands",
	Short: "Inspect and manage approved shell commands",
}

var commandsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show static and learned approved shell commands",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}

		allowed := append([]string(nil), cfg.AllowedCommands...)
		sort.Strings(allowed)

		fmt.Println("Static allowlist:")
		if len(allowed) == 0 {
			fmt.Println("- (none)")
		}
		for _, item := range allowed {
			fmt.Printf("- %s\n", item)
		}

		store := state.Open(cfg.StateDBPath)
		defer store.Close()
		if !store.Available() {
			fmt.Printf("\nLearned approvals unavailable: %v\n", store.Err())
			return nil
		}

		approvals, err := store.ListCommandApprovals(context.Background())
		if err != nil {
			return err
		}

		fmt.Println("\nLearned approvals:")
		if len(approvals) == 0 {
			fmt.Println("- (none yet)")
			return nil
		}
		for _, item := range approvals {
			fmt.Printf("- %s source=%s status=%s approved_at=%s\n",
				item.Command, item.Source, item.Status, item.ApprovedAt.Format(time.RFC3339))
			if item.Rationale != "" {
				fmt.Printf("  %s\n", item.Rationale)
			}
		}
		return nil
	},
}

var commandsApproveCmd = &cobra.Command{
	Use:   "approve [command]",
	Short: "Approve one or more shell command segments for future runs",
	Args:  cobra.ExactArgs(1),
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

		parsed, err := tools.ParseShellCommand(args[0])
		if err != nil {
			return err
		}
		if len(parsed.Segments) == 0 {
			return fmt.Errorf("command cannot be empty")
		}

		now := time.Now().UTC()
		for _, segment := range parsed.Segments {
			if err := store.SaveCommandApproval(context.Background(), state.CommandApproval{
				Command:    segment.Normalized,
				Status:     state.ApprovalStatusApproved,
				Source:     "cli",
				Rationale:  "user approved via commands approve",
				ApprovedAt: now,
			}); err != nil {
				return err
			}
			if _, err := store.ResolveBlockedShellCommand(context.Background(), segment.Normalized); err != nil {
				return err
			}
		}

		if len(parsed.Segments) == 1 {
			fmt.Printf("Approved %s\n", parsed.Segments[0].Normalized)
			return nil
		}

		fmt.Printf("Approved %d command segments from: %s\n", len(parsed.Segments), args[0])
		return nil
	},
}

func init() {
	commandsCmd.AddCommand(commandsListCmd)
	commandsCmd.AddCommand(commandsApproveCmd)
	rootCmd.AddCommand(commandsCmd)
}
