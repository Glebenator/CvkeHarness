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

var (
	commandApprovalTarget      string
	commandApprovalEnvironment string
	commandApprovalTTL         time.Duration
)

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

		fmt.Println("\nScoped approvals and explicit CLI policy exceptions:")
		if len(approvals) == 0 {
			fmt.Println("- (none yet)")
			return nil
		}
		for _, item := range approvals {
			sessionScope := item.SessionID
			if sessionScope == "" {
				sessionScope = "(global CLI exception)"
			}
			fmt.Printf("- %s target=%s environment=%s action=%s source=%s session=%s status=%s expires_at=%s\n",
				item.Command, item.TargetID, item.Environment, item.Action, item.Source,
				sessionScope, item.Status, item.ExpiresAt.Format(time.RFC3339))
			if item.Rationale != "" {
				fmt.Printf("  %s\n", item.Rationale)
			}
		}
		return nil
	},
}

var commandsApproveCmd = &cobra.Command{
	Use:   "approve [command]",
	Short: "Create an explicit target-scoped CLI policy exception",
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
		target, err := store.GetTarget(context.Background(), commandApprovalTarget)
		if err != nil {
			return fmt.Errorf("target %q is not available: %w", commandApprovalTarget, err)
		}
		now := time.Now().UTC()
		if target.Status != state.MemoryStatusActive ||
			target.Environment == state.EnvironmentUnknown ||
			target.Environment != commandApprovalEnvironment ||
			target.RemoteIdentity == "" ||
			target.ExpiresAt.IsZero() ||
			!target.ExpiresAt.After(now) {
			return fmt.Errorf("target must be active, unexpired, identity-bound, and match --environment before approval")
		}
		if commandApprovalTTL <= 0 || commandApprovalTTL > 24*time.Hour {
			return fmt.Errorf("--ttl must be greater than zero and no more than 24h")
		}

		for _, segment := range parsed.Segments {
			if !tools.ReusableShellSegment(segment) {
				return fmt.Errorf("command segment %q contains runtime interpolation and cannot be remembered", segment.Normalized)
			}
			if err := store.SaveCommandApproval(context.Background(), state.CommandApproval{
				TargetID:       target.ID,
				Environment:    target.Environment,
				RemoteIdentity: target.RemoteIdentity,
				Command:        segment.Normalized,
				Action:         tools.ShellSegmentAction(segment),
				Status:         state.ApprovalStatusApproved,
				Source:         "cli_policy",
				Rationale:      "explicit operator policy exception via commands approve",
				PolicyVersion:  state.CommandApprovalPolicyVersion,
				ApprovedAt:     now,
				ExpiresAt:      now.Add(commandApprovalTTL),
			}); err != nil {
				return err
			}
			if _, err := store.ResolveBlockedShellCommand(context.Background(), segment.Normalized); err != nil {
				return err
			}
		}

		if len(parsed.Segments) == 1 {
			fmt.Printf("Created explicit CLI policy exception for %s\n", parsed.Segments[0].Normalized)
			return nil
		}

		fmt.Printf("Created explicit CLI policy exceptions for %d command segments from: %s\n", len(parsed.Segments), args[0])
		return nil
	},
}

func init() {
	commandsApproveCmd.Flags().StringVar(&commandApprovalTarget, "target", "", "stable target id (required)")
	commandsApproveCmd.Flags().StringVar(&commandApprovalEnvironment, "environment", "", "exact target environment (required)")
	commandsApproveCmd.Flags().DurationVar(&commandApprovalTTL, "ttl", time.Hour, "approval lifetime, maximum 24h")
	_ = commandsApproveCmd.MarkFlagRequired("target")
	_ = commandsApproveCmd.MarkFlagRequired("environment")
	commandsCmd.AddCommand(commandsListCmd)
	commandsCmd.AddCommand(commandsApproveCmd)
	rootCmd.AddCommand(commandsCmd)
}
