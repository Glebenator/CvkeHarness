package cmd

import (
	"fmt"
	"os"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/setupflow"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cvkeharness",
	Short: "CvkeHarness is a local-first operations agent",
	Long: `Run one bounded operations task and exit, or open the interactive console
for ongoing chat, approvals, tool activity, verification, history, and jobs.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Initialize default logger just in case, setup/run commands will re-init with config
		log.Init("info", "text")
		return requireSetup(cmd)
	},
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

func requireSetup(cmd *cobra.Command) error {
	top := cmd
	for top.Parent() != nil && top.Parent() != cmd.Root() {
		top = top.Parent()
	}
	switch top.Name() {
	case "setup", "help", "completion", "__complete", "__completeNoDesc", "antigravity", "recovery":
		// Bootstrap, help, and model-independent recovery must stay available.
		return nil
	case "daemon":
		if cmd.Name() == "stop" || cmd.Name() == "status" || cmd.Name() == "uninstall" {
			return nil
		}
	}
	cfg, err := config.LoadConfig()
	if err == nil {
		err = setupflow.ValidateReady(cfg)
	}
	if err != nil {
		return fmt.Errorf("setup required: %w\nComplete onboarding with 'cvkeharness setup' before using %s", err, cmd.CommandPath())
	}
	return nil
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Add global flags here if needed in the future
}
