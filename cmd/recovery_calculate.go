package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

func newRecoveryCalculateCommand() *cobra.Command {
	var path string
	command := &cobra.Command{Use: "calculate", Short: "Check exact arithmetic, units and integer rounding without a model", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if path == "" {
			return fmt.Errorf("--request is required")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil {
			return err
		}
		if len(b) > 4096 {
			return fmt.Errorf("calculation request exceeds 4 KiB")
		}
		// Reuse the same strict argument boundary as the agent-facing tool.
		result, err := (&tools.CalculateTool{}).Execute(cmd.Context(), b)
		if result != "" {
			fmt.Fprintln(cmd.OutOrStdout(), result)
		}
		return err
	}}
	command.Flags().StringVar(&path, "request", "", "JSON calculation with explicit input/output units and rounding")
	return command
}
