package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

func newRecoverySSHCommand(open func() (*recovery.Engine, *state.Store, error), print func(*cobra.Command, any, error) error) *cobra.Command {
	root := &cobra.Command{Use: "ssh", Short: "Manage guarded SSH port changes and target-local supervision"}
	root.AddCommand(&cobra.Command{Use: "services", Short: "List operator-defined managed SSH instances", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		return print(cmd, e.SSHServices(), nil)
	}})
	var spec string
	supervise := &cobra.Command{Use: "supervise", Short: "Run one dedicated sshd and its persistent recovery watchdog under an init manager", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if spec == "" {
			return fmt.Errorf("--spec is required")
		}
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		service, err := e.LoadSSHServiceSpec(spec)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return e.SuperviseSSH(ctx, service)
	}}
	supervise.Flags().StringVar(&spec, "spec", "", "Private root-owned JSON service specification inside the state directory")
	root.AddCommand(supervise)
	var digest string
	confirm := &cobra.Command{Use: "confirm <operation-id>", Short: "Commit only through a fresh authenticated SSH connection to the proposed port", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		e, s, err := open()
		if err != nil {
			return err
		}
		defer s.Close()
		cfg, err := config.LoadConfig()
		if err != nil {
			cfg = config.DefaultConfig()
		}
		policy, err := cfg.EffectiveSecurity()
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(tools.RecoveryArguments{Action: "confirm_ssh", ID: args[0], Digest: digest})
		if _, err = tools.NewToolSecurityGrant("recovery_manage", string(payload), policy, time.Minute, "operator-recovery-cli"); err != nil {
			return err
		}
		op, err := e.ConfirmSSH(cmd.Context(), args[0], digest)
		return print(cmd, op, err)
	}}
	confirm.Flags().StringVar(&digest, "confirm", "", "Exact reviewed SSH operation digest")
	root.AddCommand(confirm)
	return root
}
