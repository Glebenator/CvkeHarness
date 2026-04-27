package cmd

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the CvkeHarness scheduler daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		interval, _ := cmd.Flags().GetDuration("interval")
		once, _ := cmd.Flags().GetBool("once")
		return runJobsLoop(context.Background(), interval, once)
	},
}

func init() {
	daemonCmd.Flags().Duration("interval", 30*time.Second, "poll interval")
	daemonCmd.Flags().Bool("once", false, "run due jobs once and exit")
	rootCmd.AddCommand(daemonCmd)
}
