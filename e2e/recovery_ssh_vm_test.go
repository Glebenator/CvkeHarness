//go:build e2e && recoveryvm && !windows

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/recovery"
	"gopkg.in/yaml.v3"
)

func (v *recoveryVM) runPort(port int, command string) ([]byte, error) {
	return v.docker(nil, "exec", "-e", fmt.Sprintf("CVKE_LAB_SSH_PORT=%d", port), "-i", v.container, "guest-ssh", command)
}

func (v *recoveryVM) opPort(port int, command string) recovery.Operation {
	v.t.Helper()
	out, err := v.runPort(port, command)
	if err != nil {
		v.t.Fatalf("SSH %d command %s: %v\n%s", port, command, err, out)
	}
	var op recovery.Operation
	// First contact on a new forwarded port can print a host-key notice on
	// stderr. Command errors were checked above; decode the CLI JSON body.
	i := strings.IndexByte(string(out), '{')
	if i < 0 {
		v.t.Fatalf("missing operation JSON: %s", out)
	}
	if err := json.Unmarshal(out[i:], &op); err != nil {
		v.t.Fatalf("operation JSON: %v\n%s", err, out)
	}
	return op
}

func (v *recoveryVM) awaitSSHRecovery(cli string, op recovery.Operation, baseline string) recovery.Operation {
	v.t.Helper()
	deadline := time.Now().Add(75 * time.Second)
	var last []byte
	for time.Now().Before(deadline) {
		for _, port := range []int{2222, 2223} {
			out, err := v.runPort(port, cli+"inspect "+vmQuote(op.ID))
			last = out
			if err != nil {
				continue
			}
			i := strings.IndexByte(string(out), '{')
			if i < 0 || json.Unmarshal(out[i:], &op) != nil {
				continue
			}
			if op.Status == recovery.RecoveryFailed {
				v.t.Fatalf("SSH guard restoration failed: %s", out)
			}
			if op.Status == recovery.Recovered {
				if got := string(v.must("cat /persist/ssh/sshd_config")); got != baseline {
					v.t.Fatalf("SSH original config not restored: %q", got)
				}
				return op
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	v.t.Fatalf("SSH recovery never verified: %s", last)
	return op
}

func TestRecoveryVMSSHGuard(t *testing.T) {
	v := startRecoveryVM(t)
	var service recovery.SSHService
	if err := json.Unmarshal(v.must("cat /persist/state/ssh-server.json"), &service); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.StateDBPath = "/persist/state/state.db"
	cfg.Recovery.Roots = []string{"/persist/ssh"}
	cfg.Recovery.SSHServices = []recovery.SSHService{service}
	configBytes, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	v.must("mkdir -p /root/.cvkeharness")
	v.write("/root/.cvkeharness/config.yaml", configBytes)
	baseline := string(v.must("cat /persist/ssh/sshd_config"))
	cli := "cvkeharness recovery --state /persist/state/state.db "
	prepare := func() recovery.Operation {
		t.Helper()
		candidate := strings.Replace(baseline, "Port 22\n", "Port 2223\n", 1)
		request, _ := json.Marshal(recovery.Request{SSHService: service.Name, Changes: []recovery.Change{{Action: "replace", Path: service.ConfigPath, Content: candidate}}})
		v.write("/persist/request.json", request)
		op := v.opPort(2222, cli+"prepare --request /persist/request.json")
		if op.Status != recovery.Ready || op.Plan.SSH == nil {
			t.Fatalf("SSH preparation did not return its typed plan: %+v", op)
		}
		return op
	}

	t.Run("new_transport_confirmation_and_offline_restore", func(t *testing.T) {
		// Keep a genuinely old authenticated transport alive across the reload.
		v.mustDocker("exec", v.container, "ssh", "-F", "/dev/null", "-i", "/keys/id_ed25519", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/keys/known_hosts", "-o", "GlobalKnownHostsFile=/dev/null", "-o", "ControlMaster=yes", "-S", "/keys/old-control", "-fNT", "-p", "2222", "root@127.0.0.1")
		op := prepare()
		op = v.opPort(2222, cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if op.Status != recovery.AwaitingConfirmation {
			t.Fatalf("SSH was committed before a new connection: %s", op.Status)
		}
		hasDeadline := false
		for _, check := range op.Checks {
			hasDeadline = hasDeadline || check.Kind == "ssh_deadline"
		}
		if !hasDeadline {
			t.Fatal("armed SSH deadline is not inspectable")
		}
		if out, err := v.runPort(2222, "true"); err == nil {
			t.Fatalf("old SSH port still accepts new connections: %s", out)
		}
		out, err := v.docker(nil, "exec", v.container, "ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-S", "/keys/old-control", "-p", "2222", "root@127.0.0.1", "SSH_CONNECTION='10.0.2.2 50000 10.0.2.15 2223' "+cli+"ssh confirm "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if err == nil || !strings.Contains(string(out), "existing SSH transport") {
			t.Fatalf("old multiplexed transport confirmed with spoofed environment: %v\n%s", err, out)
		}
		op = v.opPort(2223, cli+"ssh confirm "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if op.Status != recovery.Committed {
			t.Fatalf("fresh SSH connection not committed: %s", op.Status)
		}
		hasProof := false
		for _, check := range op.Checks {
			hasProof = hasProof || check.Kind == "ssh_new_connection"
		}
		if !hasProof {
			t.Fatal("new-connection proof not persisted")
		}
		v.runPort(2223, "rm /root/.cvkeharness/config.yaml")
		op = v.opPort(2223, cli+"recover "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if op.Status != recovery.Recovered {
			t.Fatalf("offline SSH restoration failed: %s", op.Status)
		}
		if got := string(v.must("cat /persist/ssh/sshd_config")); got != baseline {
			t.Fatal("SSH baseline bytes not restored")
		}
		v.write("/root/.cvkeharness/config.yaml", configBytes)
	})

	for _, stage := range []string{"ssh_armed", "ssh_applying", "ssh_applied", "ssh_signaled", "ssh_reloaded", "ssh_awaiting_confirmation"} {
		t.Run("client_death_"+stage, func(t *testing.T) {
			op := prepare()
			out, err := v.runPort(2222, "CVKE_RECOVERY_FAULT="+stage+" "+cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 86 {
				t.Fatalf("SSH stage %s did not die: %v\n%s", stage, err, out)
			}
			v.awaitSSHRecovery(cli, op, baseline)
		})
	}

	t.Run("client_death_while_restoring", func(t *testing.T) {
		op := prepare()
		op = v.opPort(2222, cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		out, err := v.runPort(2223, "CVKE_RECOVERY_FAULT=ssh_restored "+cli+"recover "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 86 {
			t.Fatalf("restoration did not die at its durable boundary: %v\n%s", err, out)
		}
		v.awaitSSHRecovery(cli, op, baseline)
	})

	t.Run("confirmation_survives_lost_acknowledgement", func(t *testing.T) {
		op := prepare()
		op = v.opPort(2222, cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		out, err := v.runPort(2223, "CVKE_RECOVERY_FAULT=ssh_confirmed "+cli+"ssh confirm "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 86 {
			t.Fatalf("confirmation did not die after commit: %v\n%s", err, out)
		}
		time.Sleep(time.Duration(service.ConfirmationSeconds+1) * time.Second)
		op = v.opPort(2223, cli+"inspect "+vmQuote(op.ID))
		if op.Status != recovery.Committed {
			t.Fatalf("watchdog undid confirmed operation: %s", op.Status)
		}
		// An unrelated preparation proves the committed guard reference was
		// cleared after expiry, even though the confirming client died first.
		request, _ := json.Marshal(recovery.Request{Changes: []recovery.Change{{Action: "create", Path: "/persist/ssh/after-confirmation", Content: "prepared only"}}})
		out, err = v.docker(request, "exec", "-e", "CVKE_LAB_SSH_PORT=2223", "-i", v.container, "guest-ssh", "cat > /persist/request.json")
		if err != nil {
			t.Fatalf("prepare fixture: %v\n%s", err, out)
		}
		other := v.opPort(2223, cli+"prepare --request /persist/request.json")
		if other.Status != recovery.Ready {
			t.Fatalf("confirmed guard remained armed: %s", other.Status)
		}
		out, err = v.runPort(2223, "CVKE_RECOVERY_FAULT=ssh_recovered "+cli+"recover "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if !errors.As(err, &exited) || exited.ExitCode() != 86 {
			t.Fatalf("restore did not die after completion: %v\n%s", err, out)
		}
		op = v.opPort(2222, cli+"recover "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if op.Status != recovery.Recovered {
			t.Fatalf("idempotent restore after lost acknowledgement: %s", op.Status)
		}
	})

	t.Run("stale_watchdog_refuses_apply", func(t *testing.T) {
		op := prepare()
		pid := op.Plan.SSH.Guard.Process.PID
		v.must(fmt.Sprintf("kill -STOP %d", pid))
		time.Sleep(4 * time.Second)
		out, err := v.runPort(2222, cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		v.must(fmt.Sprintf("kill -CONT %d", pid))
		if err == nil || !strings.Contains(string(out), "watchdog is stale") {
			t.Fatalf("stale watchdog accepted mutation: %v\n%s", err, out)
		}
		if got := string(v.must("cat /persist/ssh/sshd_config")); got != baseline {
			t.Fatal("stale watchdog did not prevent mutation")
		}
		time.Sleep(time.Second)
	})

	t.Run("conflicting_writer_stops_automatic_repairs", func(t *testing.T) {
		op := prepare()
		op = v.opPort(2222, cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if out, err := v.runPort(2223, "printf 'external writer\\n' > /persist/ssh/sshd_config"); err != nil {
			t.Fatalf("write conflict fixture: %v\n%s", err, out)
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			op = v.opPort(2223, cli+"inspect "+vmQuote(op.ID))
			if op.Status == recovery.RecoveryFailed {
				break
			}
			time.Sleep(time.Second)
		}
		if op.Status != recovery.RecoveryFailed {
			t.Fatalf("conflicting SSH restore not recorded: %s", op.Status)
		}
		revision := op.Revision
		time.Sleep(2 * time.Second)
		op = v.opPort(2223, cli+"inspect "+vmQuote(op.ID))
		if op.Revision != revision {
			t.Fatal("watchdog kept retrying a failed repair")
		}
		out, err := v.runPort(2223, "cat /persist/ssh/sshd_config")
		if err != nil || string(out) != "external writer\n" {
			t.Fatalf("conflicting writer was overwritten: %v\n%s", err, out)
		}
		// Resolve the fixture explicitly to the reviewed candidate, then ask
		// the model-independent recovery CLI to restore the original service.
		candidate := strings.Replace(baseline, "Port 22\n", "Port 2223\n", 1)
		out, err = v.docker([]byte(candidate), "exec", "-e", "CVKE_LAB_SSH_PORT=2223", "-i", v.container, "guest-ssh", "cat > /persist/ssh/sshd_config")
		if err != nil {
			t.Fatalf("resolve fixture conflict: %v\n%s", err, out)
		}
		op = v.opPort(2223, cli+"recover "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		if op.Status != recovery.Recovered {
			t.Fatalf("explicit conflict recovery: %s", op.Status)
		}
	})

	t.Run("watchdog_process_restart", func(t *testing.T) {
		op := prepare()
		op = v.opPort(2222, cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		_, _ = v.runPort(2223, fmt.Sprintf("kill -KILL %d", op.Plan.SSH.Guard.Process.PID))
		v.awaitSSHRecovery(cli, op, baseline)
	})
	t.Run("guest_reboot_before_confirmation", func(t *testing.T) {
		boot := strings.TrimSpace(string(v.must("cat /proc/sys/kernel/random/boot_id")))
		op := prepare()
		op = v.opPort(2222, cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		_, _ = v.runPort(2223, "sync; reboot -f")
		v.awaitSSH(boot)
		v.awaitSSHRecovery(cli, op, baseline)
	})
}
