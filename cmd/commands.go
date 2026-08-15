package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/secrets"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

var commandsCmd = &cobra.Command{
	Use:   "commands",
	Short: "Inspect and manage scoped action approvals",
}

var commandsApproveWorkCmd = &cobra.Command{
	Use:   "approve-work [blocked-work-id]",
	Short: "Approve one exact blocked shell or tool action once",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		policy, err := cfg.EffectiveSecurity()
		if err != nil {
			return err
		}
		store := state.Open(cfg.StateDBPath)
		defer store.Close()
		if !store.Available() {
			return fmt.Errorf("state database unavailable: %w", store.Err())
		}
		work, err := store.GetBlockedWork(context.Background(), args[0])
		if err != nil {
			return err
		}
		if work.PendingApprovalType != "security_action" || work.PendingApprovalPayload == "" {
			return fmt.Errorf("blocked work %s is not waiting on a scoped security action", work.ID)
		}
		var expected state.SecurityActionGrant
		if err := json.Unmarshal([]byte(work.PendingApprovalPayload), &expected); err != nil || expected.Digest == "" {
			return fmt.Errorf("blocked work %s does not contain a scoped approval envelope", work.ID)
		}
		var continuation struct {
			ToolCall provider.ToolCall `json:"tool_call"`
		}
		if err := json.Unmarshal([]byte(work.ContinuationData), &continuation); err != nil {
			return fmt.Errorf("read blocked action: %w", err)
		}
		call := continuation.ToolCall
		var actionPayload string
		if call.Function.Name == "shell_execute" {
			var shellArgs tools.ShellArgs
			if err := json.Unmarshal([]byte(call.Function.Arguments), &shellArgs); err != nil {
				return fmt.Errorf("read blocked shell action: %w", err)
			}
			actionPayload = shellArgs.Command
		} else {
			actionPayload = call.Function.Arguments
		}
		grant, err := tools.NewBlockedWorkSecurityGrant(call.Function.Name, actionPayload, policy, expected, 15*time.Minute, "cli-blocked-work")
		if err != nil {
			return err
		}
		if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
			return err
		}
		if _, err := store.ResolveBlockedSecurityGrant(context.Background(), grant.Digest); err != nil {
			return err
		}
		fmt.Printf("Approved blocked work %s once (expires in 15 minutes): %s\n", work.ID, grant.MaskedSummary)
		return nil
	},
}

var commandsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show static commands, scoped grants, and quarantined legacy approvals",
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

		grants, err := store.ListSecurityActionGrants(context.Background())
		if err != nil {
			return err
		}

		fmt.Println("\nScoped one-time grants:")
		if len(grants) == 0 {
			fmt.Println("- (none yet)")
		} else {
			for _, item := range grants {
				status := "available"
				if item.RemainingUses == 0 {
					status = "used"
				} else if !item.ExpiresAt.After(time.Now()) {
					status = "expired"
				}
				fmt.Printf("- %s source=%s status=%s expires_at=%s policy=%s\n",
					item.MaskedSummary, item.Source, status, item.ExpiresAt.Format(time.RFC3339), shortHash(item.PolicyHash))
			}
		}

		approvals, err := store.ListCommandApprovals(context.Background())
		if err != nil {
			return err
		}
		fmt.Println("\nLegacy approvals (quarantined; not used by security profiles):")
		if len(approvals) == 0 {
			fmt.Println("- (none)")
		}
		for _, item := range approvals {
			fmt.Printf("- %s source=%s status=%s approved_at=%s\n",
				secrets.Mask(item.Command), item.Source, item.Status, item.ApprovedAt.Format(time.RFC3339))
			if item.Rationale != "" {
				fmt.Printf("  %s\n", item.Rationale)
			}
		}
		return nil
	},
}

var commandsApproveCmd = &cobra.Command{
	Use:   "approve [command]",
	Short: "Approve one exact shell action once for the next 15 minutes",
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

		policy, err := cfg.EffectiveSecurity()
		if err != nil {
			return err
		}
		grant, err := tools.NewShellSecurityGrant(args[0], policy, 15*time.Minute, "cli")
		if err != nil {
			return err
		}
		if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
			return err
		}
		if _, err := store.ResolveBlockedSecurityGrant(context.Background(), grant.Digest); err != nil {
			return err
		}
		fmt.Printf("Approved once (15 minutes, exact action and current policy): %s\n", grant.MaskedSummary)
		return nil
	},
}

func shortHash(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func init() {
	commandsCmd.AddCommand(commandsListCmd)
	commandsCmd.AddCommand(commandsApproveCmd)
	commandsCmd.AddCommand(commandsApproveWorkCmd)
	rootCmd.AddCommand(commandsCmd)
}
