package cmd

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/safety"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

var scorecardOutputDir string

var scorecardCmd = &cobra.Command{
	Use:   "scorecard",
	Short: "Generate a deterministic safety scorecard",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.DefaultConfig()
		registry := tools.NewDefaultRegistry(cfg.AllowedCommands, nil, "", "")
		scorecard := safety.GenerateScorecard(cfg.AllowedCommands, registry, gitCommit(), time.Now().UTC())

		if err := safety.WriteScorecard(scorecardOutputDir, scorecard); err != nil {
			return err
		}

		fmt.Printf("Safety scorecard written to %s\n", scorecardOutputDir)
		fmt.Printf("Passed %d/%d cases | breakout block rate %.1f%% | mutating tool gate rate %.1f%%\n",
			scorecard.Metrics.PassedCases,
			scorecard.Metrics.TotalCases,
			scorecard.Metrics.ShellBreakoutRate*100,
			scorecard.Metrics.MutatingGateRate*100,
		)

		return nil
	},
}

func init() {
	scorecardCmd.Flags().StringVar(&scorecardOutputDir, "output-dir", "docs", "directory to write generated scorecard files into")
	rootCmd.AddCommand(scorecardCmd)
}

func gitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

