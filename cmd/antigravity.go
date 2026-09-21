package cmd

import (
	"fmt"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/spf13/cobra"
)

func init() {
	command := &cobra.Command{Use: "antigravity", Short: "Manage personal Antigravity authentication"}
	command.AddCommand(&cobra.Command{Use: "login", SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs, Short: "Sign in to Google for the unofficial Antigravity provider", RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "Unofficial personal integration: Google currently prohibits third-party subscription access and may suspend Antigravity/Gemini CLI access.")
		return provider.LoginAntigravity(cmd.Context(), cmd.OutOrStdout())
	}})
	command.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, Short: "Check the local Antigravity login", RunE: func(cmd *cobra.Command, args []string) error {
		_, err := provider.LoadAntigravityAuth(provider.AntigravityAuthPath())
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Antigravity credentials found locally (not a live access check).")
		return nil
	}})
	rootCmd.AddCommand(command)
}
