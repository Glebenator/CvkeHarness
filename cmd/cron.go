package cmd

import (
	"context"
	"fmt"

	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
	"github.com/spf13/cobra"
)

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Manage the current user's system crontab",
}

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List current-user crontab entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, _, err := systemcron.New(nil).List(context.Background())
		if err != nil {
			return err
		}
		for _, entry := range entries {
			status := "enabled"
			if entry.Disabled {
				status = "disabled"
			}
			id := entry.ID
			if id == "" {
				id = entry.Hash
			}
			fmt.Printf("- %s line=%d %s managed=%v schedule=%q command=%q\n", id, entry.Line+1, status, entry.Managed, entry.Schedule, entry.Command)
		}
		return nil
	},
}

var cronShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show raw current-user crontab",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, content, err := systemcron.New(nil).List(context.Background())
		if err != nil {
			return err
		}
		fmt.Print(content)
		return nil
	},
}

var cronAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a current-user crontab entry",
	RunE: func(cmd *cobra.Command, args []string) error {
		schedule, _ := cmd.Flags().GetString("schedule")
		command, _ := cmd.Flags().GetString("command")
		name, _ := cmd.Flags().GetString("name")
		client := systemcron.New(nil)
		mutation, err := client.Add(context.Background(), schedule, command, name)
		if err != nil {
			return err
		}
		return applyCronMutation(context.Background(), client, mutation)
	},
}

var cronUpdateCmd = &cobra.Command{
	Use:   "update [target]",
	Short: "Update a current-user crontab entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		schedule, _ := cmd.Flags().GetString("schedule")
		command, _ := cmd.Flags().GetString("command")
		client := systemcron.New(nil)
		mutation, err := client.Update(context.Background(), args[0], schedule, command)
		if err != nil {
			return err
		}
		return applyCronMutation(context.Background(), client, mutation)
	},
}

var cronDryRunCmd = &cobra.Command{
	Use:   "dry-run",
	Short: "Preview a current-user crontab change",
	RunE: func(cmd *cobra.Command, args []string) error {
		action, _ := cmd.Flags().GetString("action")
		target, _ := cmd.Flags().GetString("target")
		schedule, _ := cmd.Flags().GetString("schedule")
		command, _ := cmd.Flags().GetString("command")
		name, _ := cmd.Flags().GetString("name")
		client := systemcron.New(nil)
		var mutation systemcron.Mutation
		var err error
		switch action {
		case "add":
			mutation, err = client.Add(context.Background(), schedule, command, name)
		case "update":
			mutation, err = client.Update(context.Background(), target, schedule, command)
		case "remove":
			mutation, err = client.Remove(context.Background(), target)
		case "enable":
			mutation, err = client.SetEnabled(context.Background(), target, true)
		case "disable":
			mutation, err = client.SetEnabled(context.Background(), target, false)
		default:
			return fmt.Errorf("unsupported dry-run action %q", action)
		}
		if err != nil {
			return err
		}
		fmt.Print(systemcron.Diff(mutation.OldContent, mutation.NewContent))
		return nil
	},
}

var cronRemoveCmd = &cobra.Command{
	Use:   "remove [target]",
	Short: "Remove a current-user crontab entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := systemcron.New(nil)
		mutation, err := client.Remove(context.Background(), args[0])
		if err != nil {
			return err
		}
		return applyCronMutation(context.Background(), client, mutation)
	},
}

var cronEnableCmd = &cobra.Command{
	Use:   "enable [target]",
	Short: "Enable a current-user crontab entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := systemcron.New(nil)
		mutation, err := client.SetEnabled(context.Background(), args[0], true)
		if err != nil {
			return err
		}
		return applyCronMutation(context.Background(), client, mutation)
	},
}

var cronDisableCmd = &cobra.Command{
	Use:   "disable [target]",
	Short: "Disable a current-user crontab entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := systemcron.New(nil)
		mutation, err := client.SetEnabled(context.Background(), args[0], false)
		if err != nil {
			return err
		}
		return applyCronMutation(context.Background(), client, mutation)
	},
}

func applyCronMutation(ctx context.Context, client *systemcron.Client, mutation systemcron.Mutation) error {
	fmt.Print(systemcron.Diff(mutation.OldContent, mutation.NewContent))
	if !confirmCLI("Apply this crontab change?") {
		recordCronAudit(ctx, mutation, false, fmt.Errorf("user cancelled"))
		return fmt.Errorf("cancelled")
	}
	err := client.Apply(ctx, mutation)
	recordCronAudit(ctx, mutation, err == nil, err)
	return err
}

func recordCronAudit(ctx context.Context, mutation systemcron.Mutation, success bool, err error) {
	store, closeFn, openErr := openState()
	if openErr != nil {
		return
	}
	defer closeFn()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = store.RecordSystemCronAudit(ctx, state.SystemCronAudit{
		Action:       mutation.Action,
		Target:       mutation.Target,
		OldSnippet:   mutation.OldContent,
		NewSnippet:   mutation.NewContent,
		Success:      success,
		ErrorMessage: msg,
	})
}

func init() {
	cronAddCmd.Flags().String("schedule", "", "five-field cron schedule")
	cronAddCmd.Flags().String("command", "", "command to place in crontab")
	cronAddCmd.Flags().String("name", "", "human label")
	cronUpdateCmd.Flags().String("schedule", "", "five-field cron schedule")
	cronUpdateCmd.Flags().String("command", "", "command to place in crontab")
	cronDryRunCmd.Flags().String("action", "add", "action to preview: add, update, remove, enable, disable")
	cronDryRunCmd.Flags().String("target", "", "target id, line, or hash")
	cronDryRunCmd.Flags().String("schedule", "", "five-field cron schedule")
	cronDryRunCmd.Flags().String("command", "", "command to place in crontab")
	cronDryRunCmd.Flags().String("name", "", "human label")

	cronCmd.AddCommand(cronListCmd, cronShowCmd, cronAddCmd, cronUpdateCmd, cronRemoveCmd, cronEnableCmd, cronDisableCmd, cronDryRunCmd)
	rootCmd.AddCommand(cronCmd)
}
