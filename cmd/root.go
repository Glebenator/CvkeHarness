package cmd

import (
	"fmt"
	"os"

	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cvkeharness",
	Short: "CvkeHarness is a lightweight LLM DevOps agent",
	Long: `A provider-agnostic Go harness that wires LLM reasoning to DevOps tooling
(Docker, healthchecks, shell) via a robust agentic loop.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Initialize default logger just in case, setup/run commands will re-init with config
		log.Init("info", "text")
	},
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
