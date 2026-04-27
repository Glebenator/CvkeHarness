package cmd

import (
	"context"
	"fmt"
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
	daemonInstallCmd.Flags().Bool("system", false, "install a system service instead of a user service")
	daemonInstallCmd.Flags().String("user", "", "user for --system service installs")
	daemonInstallCmd.Flags().Duration("interval", 30*time.Second, "daemon poll interval")
	daemonInstallCmd.Flags().Bool("enable-linger", false, "enable login linger for the current user service")
	for _, cmd := range []*cobra.Command{daemonStartCmd, daemonStopCmd, daemonRestartCmd, daemonStatusCmd, daemonUninstallCmd} {
		cmd.Flags().Bool("system", false, "manage the system service instead of the user service")
		cmd.Flags().String("user", "", "user for --system service management")
	}
	daemonCmd.AddCommand(daemonInstallCmd, daemonStartCmd, daemonStopCmd, daemonRestartCmd, daemonStatusCmd, daemonUninstallCmd)
	rootCmd.AddCommand(daemonCmd)
}

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the CvkeHarness systemd daemon service",
	RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := daemonServiceOptionsFromCommand(cmd)
		if err != nil {
			return err
		}
		opts.Interval, _ = cmd.Flags().GetDuration("interval")
		opts.EnableLinger, _ = cmd.Flags().GetBool("enable-linger")
		result, err := installDaemonService(context.Background(), opts)
		if err != nil {
			return err
		}
		fmt.Print(result)
		return nil
	},
}

var daemonStartCmd = daemonServiceActionCommand("start", "Start the CvkeHarness daemon service")
var daemonStopCmd = daemonServiceActionCommand("stop", "Stop the CvkeHarness daemon service")
var daemonRestartCmd = daemonServiceActionCommand("restart", "Restart the CvkeHarness daemon service")
var daemonStatusCmd = daemonServiceActionCommand("status", "Show the CvkeHarness daemon service status")
var daemonUninstallCmd = daemonServiceActionCommand("uninstall", "Uninstall the CvkeHarness daemon service")

func daemonServiceActionCommand(action, short string) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := daemonServiceOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			result, err := runDaemonServiceAction(context.Background(), action, opts)
			if err != nil {
				return err
			}
			fmt.Print(result)
			return nil
		},
	}
}

func daemonServiceOptionsFromCommand(cmd *cobra.Command) (daemonServiceOptions, error) {
	system, _ := cmd.Flags().GetBool("system")
	user, _ := cmd.Flags().GetString("user")
	return daemonServiceOptions{System: system, User: user}, nil
}
