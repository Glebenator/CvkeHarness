package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/safety"
	"github.com/spf13/cobra"
)

var redteamOutputDir string
var redteamPrompt string

var redteamCmd = &cobra.Command{
	Use:   "redteam",
	Short: "Run a live model red-team eval against shadow tools",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}

		p, err := resolveProvider(cfg, "")
		if err != nil {
			return err
		}

		prompt := redteamPrompt
		if prompt == "" {
			prompt = safety.DefaultRedTeamPrompt()
		}

		harness := safety.NewRedTeamHarness(cfg.AllowedCommands)
		report, err := harness.Evaluate(context.Background(), p, cfg.PrimaryModel(), cfg.MaxIterations, cfg.MaxTokens, prompt)
		if report == nil && err != nil {
			return err
		}

		report.GeneratedAt = time.Now().UTC()
		report.Commit = gitCommit()
		report.Provider = cfg.Provider
		report.Model = cfg.PrimaryModel()

		if err := safety.WriteRedTeamReport(redteamOutputDir, *report); err != nil {
			return err
		}

		fmt.Printf("Live red-team report written to %s\n", redteamOutputDir)
		fmt.Printf("Attempts %d | dangerous allowed %d | dangerous denied %d | tools used %d\n",
			report.Metrics.TotalAttempts,
			report.Metrics.DangerousAllowed,
			report.Metrics.DangerousDenied,
			report.Metrics.UniqueToolsUsed,
		)
		if err != nil {
			fmt.Printf("Run ended early: %v\n", err)
		}

		return nil
	},
}

func init() {
	redteamCmd.Flags().StringVar(&redteamOutputDir, "output-dir", "docs", "directory to write generated red-team report files into")
	redteamCmd.Flags().StringVar(&redteamPrompt, "prompt", "", "override the default live red-team prompt")
	rootCmd.AddCommand(redteamCmd)
}
