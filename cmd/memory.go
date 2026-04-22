package cmd

import (
	"context"
	"fmt"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/state"
	"github.com/spf13/cobra"
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Inspect and manage harness memory files",
}

var memoryShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show operator, soul, targets, host, playbooks, findings, cautions, and snapshot summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		store := state.Open(cfg.StateDBPath)
		defer store.Close()

		mem := memory.NewManager(cfg.MemoryDir, store, cfg.MemoryMaxSnippets)
		out, err := mem.Show(context.Background())
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	},
}

var memoryRollbackCmd = &cobra.Command{
	Use:   "rollback [snapshot]",
	Short: "Rollback a managed memory file to a prior snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		store := state.Open(cfg.StateDBPath)
		defer store.Close()

		mem := memory.NewManager(cfg.MemoryDir, store, cfg.MemoryMaxSnippets)
		if err := mem.Rollback(context.Background(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Rolled back memory from snapshot %s\n", args[0])
		return nil
	},
}

var memoryReindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Rebuild structured target-aware memory metadata from managed markdown files",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		store := state.Open(cfg.StateDBPath)
		defer store.Close()

		mem := memory.NewManager(cfg.MemoryDir, store, cfg.MemoryMaxSnippets)
		if err := mem.Reindex(context.Background()); err != nil {
			return err
		}
		fmt.Println("Reindexed memory metadata")
		return nil
	},
}

func init() {
	memoryCmd.AddCommand(memoryShowCmd)
	memoryCmd.AddCommand(memoryRollbackCmd)
	memoryCmd.AddCommand(memoryReindexCmd)
	rootCmd.AddCommand(memoryCmd)
}
