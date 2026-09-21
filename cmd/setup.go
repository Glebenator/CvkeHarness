package cmd

import (
	"github.com/coolcake/cvkeharness/internal/setuptui"
	dashboard "github.com/coolcake/cvkeharness/internal/tui"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Guided onboarding using the shared connection and model editors",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, args []string) error { return setuptui.Run() },
}

// Settings has one implementation, whether opened from the command line or
// the console tab. Onboarding reuses its model and connection components.
var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Open the console Settings workspace",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runConsole(dashboard.InitialViewSettings)
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(settingsCmd)
}
