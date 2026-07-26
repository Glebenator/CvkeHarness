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
	Short: "Inspect, review, import, and export target-scoped operational memory",
}

func openMemoryManager() (*memory.Manager, *state.Store, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	store := state.Open(cfg.StateDBPath)
	if !store.Available() {
		err := store.Err()
		_ = store.Close()
		return nil, nil, fmt.Errorf("state database unavailable: %w", err)
	}
	return memory.NewManager(cfg.MemoryDir, store), store, nil
}

var memoryShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show generated memory views and snapshot count",
	RunE: func(cmd *cobra.Command, args []string) error {
		mem, store, err := openMemoryManager()
		if err != nil {
			return err
		}
		defer store.Close()
		out, err := mem.Show(context.Background())
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	},
}

var memoryInboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "List candidate facts, playbooks, findings, and cautions awaiting review",
	RunE: func(cmd *cobra.Command, args []string) error {
		mem, store, err := openMemoryManager()
		if err != nil {
			return err
		}
		defer store.Close()
		out, err := mem.ReviewInbox(context.Background())
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	},
}

func memoryTransitionCommand(use, short string, transition func(*memory.Manager, context.Context, string, string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use + " [kind] [id]",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, store, err := openMemoryManager()
			if err != nil {
				return err
			}
			defer store.Close()
			if err := transition(mem, context.Background(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("%s %s %s\n", use, args[0], args[1])
			return nil
		},
	}
}

var memoryPromoteCmd = memoryTransitionCommand("promote", "Promote one reviewed candidate into bounded active memory",
	func(mem *memory.Manager, ctx context.Context, kind, id string) error {
		return mem.PromoteMemory(ctx, kind, id)
	})

var memoryRejectCmd = memoryTransitionCommand("reject", "Reject one candidate",
	func(mem *memory.Manager, ctx context.Context, kind, id string) error {
		return mem.RejectMemory(ctx, kind, id)
	})

var memoryRevokeCmd = memoryTransitionCommand("revoke", "Immediately revoke one active memory item",
	func(mem *memory.Manager, ctx context.Context, kind, id string) error {
		return mem.RevokeMemory(ctx, kind, id)
	})

var memoryDeleteCmd = memoryTransitionCommand("delete", "Delete one item from canonical operational memory",
	func(mem *memory.Manager, ctx context.Context, kind, id string) error {
		return mem.DeleteMemory(ctx, kind, id)
	})

var memoryExportCmd = &cobra.Command{
	Use:   "export [directory]",
	Short: "Generate Markdown views from canonical SQLite memory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mem, store, err := openMemoryManager()
		if err != nil {
			return err
		}
		defer store.Close()
		dir := ""
		if len(args) == 1 {
			dir = args[0]
		}
		if err := mem.Export(context.Background(), dir); err != nil {
			return err
		}
		fmt.Println("Exported operational memory views")
		return nil
	},
}

var memoryImportCmd = &cobra.Command{
	Use:   "import [directory]",
	Short: "Validate Markdown views and replace canonical SQLite memory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mem, store, err := openMemoryManager()
		if err != nil {
			return err
		}
		defer store.Close()
		dir := ""
		if len(args) == 1 {
			dir = args[0]
		}
		if err := mem.Import(context.Background(), dir); err != nil {
			return err
		}
		fmt.Println("Imported validated operational memory")
		return nil
	},
}

var memoryTargetCmd = &cobra.Command{
	Use:   "target",
	Short: "Manage live target identity bindings",
}

var memoryTargetSetEnvironmentCmd = &cobra.Command{
	Use:   "set-environment [target-id] [environment] [remote-identity]",
	Short: "Bind a provisional target to an environment and remote identity",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		mem, store, err := openMemoryManager()
		if err != nil {
			return err
		}
		defer store.Close()
		if err := mem.SetTargetEnvironment(context.Background(), args[0], args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("Bound target %s to environment %s\n", args[0], args[1])
		return nil
	},
}

func init() {
	memoryTargetCmd.AddCommand(memoryTargetSetEnvironmentCmd)
	memoryCmd.AddCommand(
		memoryShowCmd,
		memoryInboxCmd,
		memoryPromoteCmd,
		memoryRejectCmd,
		memoryRevokeCmd,
		memoryDeleteCmd,
		memoryExportCmd,
		memoryImportCmd,
		memoryTargetCmd,
	)
	rootCmd.AddCommand(memoryCmd)
}
