package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

func newRecoveryFleetCommand(open func() (*recovery.Engine, *state.Store, error), print func(*cobra.Command, any, error) error) *cobra.Command {
	root := &cobra.Command{Use: "fleet", Short: "Inspect and execute exact bounded batches through enrolled target-side executors"}
	for _, action := range []string{"hosts", "list"} {
		action := action
		root.AddCommand(&cobra.Command{Use: action, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			e, s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			if action == "hosts" {
				return print(cmd, e.FleetHosts(), nil)
			}
			b, err := e.ListBatches(cmd.Context())
			return print(cmd, b, err)
		}})
	}
	var path string
	prepare := &cobra.Command{Use: "prepare", Short: "Bind exact already-prepared operations and total impact before rollout", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if path == "" {
			return fmt.Errorf("--request is required")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 64<<10))
		d.DisallowUnknownFields()
		var refs []recovery.FleetReference
		if err = d.Decode(&refs); err != nil {
			return err
		}
		if err = d.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("unexpected trailing batch request")
		}
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		b, err := e.PrepareBatch(cmd.Context(), refs)
		return print(cmd, b, err)
	}}
	prepare.Flags().StringVar(&path, "request", "", "JSON array of exact host/id/digest references")
	root.AddCommand(prepare)
	for _, action := range []string{"inspect", "apply", "recover", "reconcile"} {
		action := action
		var digest string
		c := &cobra.Command{Use: action + " <batch-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			e, s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			var b recovery.FleetBatch
			switch action {
			case "inspect":
				b, err = e.InspectBatch(cmd.Context(), args[0])
			case "reconcile":
				b, err = e.ReconcileBatch(cmd.Context(), args[0])
			case "apply":
				cfg, loadErr := config.LoadConfig()
				if loadErr != nil {
					cfg = config.DefaultConfig()
				}
				policy, pErr := cfg.EffectiveSecurity()
				if pErr != nil {
					return pErr
				}
				payload, _ := json.Marshal(tools.FleetArguments{Action: action, ID: args[0], Digest: digest})
				if _, pErr = tools.NewToolSecurityGrant("recovery_fleet", string(payload), policy, time.Minute, "operator-fleet-cli"); pErr != nil {
					return pErr
				}
				b, err = e.ApplyBatch(cmd.Context(), args[0], digest)
			case "recover":
				b, err = e.RecoverBatch(cmd.Context(), args[0], digest)
			}
			return print(cmd, b, err)
		}}
		if action == "apply" || action == "recover" {
			c.Flags().StringVar(&digest, "confirm", "", "Exact reviewed batch digest; authorizes bounded dispatch")
		}
		root.AddCommand(c)
	}
	return root
}
