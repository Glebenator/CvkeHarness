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

// Recovery deliberately does not initialize providers, memory curation or an
// agent. It stays usable when login, config, or the conversation is broken.
func newRecoveryCommand() *cobra.Command {
	var statePath, expectedTarget string
	var roots []string
	root := &cobra.Command{Use: "recovery", Short: "Prepare, inspect and restore recoverable system changes without a model"}
	root.AddCommand(newRecoveryCalculateCommand())
	root.PersistentFlags().StringVar(&statePath, "state", "", "State database path (defaults to configured path)")
	root.PersistentFlags().StringVar(&expectedTarget, "expect-target", "", "Refuse actions unless this executor identity matches")
	root.PersistentFlags().StringSliceVar(&roots, "root", nil, "Operator-authorized file root; repeat for multiple roots")
	open := func() (*recovery.Engine, *state.Store, error) {
		cfg, err := config.LoadConfig()
		if err != nil {
			// An explicit state path is the recovery escape hatch for damaged
			// model configuration. Never silently change a configured state path.
			if statePath == "" {
				return nil, nil, fmt.Errorf("load configuration: %w; pass --state to recover independently", err)
			}
			cfg = config.DefaultConfig()
		}
		if statePath != "" {
			cfg.StateDBPath = statePath
		}
		if roots != nil {
			cfg.Recovery.Roots = roots
		}
		policy, err := cfg.EffectiveSecurity()
		if err != nil {
			return nil, nil, err
		}
		opts := recoveryOptions(cfg)
		opts.Policy = policy.Hash
		s := state.Open(cfg.StateDBPath)
		e, err := recovery.New(s, opts)
		if err == nil && expectedTarget != "" && e.Target() != expectedTarget {
			err = fmt.Errorf("executor identity does not match --expect-target")
		}
		if err != nil {
			s.Close()
			return nil, nil, err
		}
		return e, s, nil
	}
	print := func(cmd *cobra.Command, v any, err error) error {
		if v != nil {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if e := encoder.Encode(v); e != nil {
				return e
			}
		}
		return err
	}
	root.AddCommand(newRecoverySSHCommand(open, print))
	root.AddCommand(newRecoveryFleetCommand(open, print))
	root.AddCommand(&cobra.Command{Use: "identity", Short: "Show this machine/principal executor identity for operator enrollment", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		return print(cmd, map[string]string{"target": e.Target()}, nil)
	}})
	var requestPath string
	plan := &cobra.Command{Use: "prepare", Short: "Capture private backups and an exact immutable file manifest", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if requestPath == "" {
			return fmt.Errorf("--request is required")
		}
		f, err := os.Open(requestPath)
		if err != nil {
			return err
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 12<<20))
		d.DisallowUnknownFields()
		var req recovery.Request
		if err = d.Decode(&req); err != nil {
			return err
		}
		if err = d.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("unexpected trailing request data")
		}
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		op, err := e.Prepare(cmd.Context(), req)
		return print(cmd, op, err)
	}}
	plan.Flags().StringVar(&requestPath, "request", "", "JSON request file containing an exact changes array")
	root.AddCommand(plan)
	snapshots := &cobra.Command{Use: "snapshot", Short: "Plan native Btrfs checkpoints and whole-subvolume restoration"}
	snapshots.AddCommand(&cobra.Command{Use: "targets", Short: "List operator-defined snapshot targets", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		return print(cmd, e.SnapshotTargets(), nil)
	}})
	snapshots.AddCommand(&cobra.Command{Use: "prepare <target-name>", Short: "Prepare a reviewed native checkpoint plan", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		op, err := e.PrepareSnapshot(cmd.Context(), args[0])
		return print(cmd, op, err)
	}})
	var checkpointDigest string
	restorePlan := &cobra.Command{Use: "restore-prepare <checkpoint-id>", Short: "Review current tree before restoring a committed checkpoint", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		op, err := e.PrepareSnapshotRestore(cmd.Context(), args[0], checkpointDigest)
		return print(cmd, op, err)
	}}
	restorePlan.Flags().StringVar(&checkpointDigest, "checkpoint-digest", "", "Exact digest of the committed checkpoint")
	snapshots.AddCommand(restorePlan)
	root.AddCommand(snapshots)
	root.AddCommand(&cobra.Command{Use: "services", Short: "List operator-configured recovery service instances", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		return print(cmd, e.Services(), nil)
	}})
	root.AddCommand(&cobra.Command{Use: "list", Short: "List recorded recovery operations", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		ops, err := e.List(cmd.Context())
		return print(cmd, ops, err)
	}})
	for _, action := range []string{"inspect", "apply", "recover", "reconcile"} {
		action := action
		var digest string
		c := &cobra.Command{Use: action + " <operation-id>", Short: map[string]string{"inspect": "Show the immutable operation manifest and recorded status", "apply": "Apply a reviewed manifest (requires --confirm digest)", "recover": "Restore this operation's unchanged outputs (requires --confirm digest)", "reconcile": "Mark interrupted execution for explicit recovery; does not mutate target files"}[action], Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			e, s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			var op recovery.Operation
			switch action {
			case "inspect":
				op, err = e.Inspect(cmd.Context(), args[0])
			case "apply":
				// --confirm is human authorization, not a policy-deny bypass.
				cfg, loadErr := config.LoadConfig()
				if loadErr != nil {
					cfg = config.DefaultConfig()
				}
				policy, policyErr := cfg.EffectiveSecurity()
				if policyErr != nil {
					return policyErr
				}
				op, err = e.Inspect(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				toolAction := "apply"
				if op.Plan.Service != nil {
					toolAction = "apply_service"
				}
				if op.Plan.Snapshot != nil {
					toolAction = "apply_snapshot"
				}
				if op.Plan.SSH != nil {
					toolAction = "apply_ssh"
				}
				payload, _ := json.Marshal(tools.RecoveryArguments{Action: toolAction, ID: args[0], Digest: digest})
				if _, policyErr = tools.NewToolSecurityGrant("recovery_manage", string(payload), policy, time.Minute, "operator-recovery-cli"); policyErr != nil {
					return policyErr
				}
				if op.Plan.SSH != nil {
					op, err = e.ApplySSH(cmd.Context(), args[0], digest)
				} else if op.Plan.Snapshot != nil {
					op, err = e.ApplySnapshot(cmd.Context(), args[0], digest)
				} else if op.Plan.Service != nil {
					op, err = e.ApplyService(cmd.Context(), args[0], digest)
				} else {
					op, err = e.Apply(cmd.Context(), args[0], digest)
				}
			case "recover":
				op, err = e.Inspect(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if op.Plan.SSH != nil {
					op, err = e.RecoverSSH(cmd.Context(), args[0], digest)
				} else if op.Plan.Snapshot != nil {
					op, err = e.RecoverSnapshot(cmd.Context(), args[0], digest)
				} else if op.Plan.Service != nil {
					op, err = e.RecoverService(cmd.Context(), args[0], digest)
				} else {
					op, err = e.Recover(cmd.Context(), args[0], digest)
				}
			case "reconcile":
				op, err = e.Reconcile(cmd.Context(), args[0])
			}
			return print(cmd, op, err)
		}}
		if action == "apply" || action == "recover" {
			c.Flags().StringVar(&digest, "confirm", "", "Exact reviewed manifest digest; explicitly authorizes this operation")
		}
		root.AddCommand(c)
	}
	return root
}

func init() { rootCmd.AddCommand(newRecoveryCommand()) }
