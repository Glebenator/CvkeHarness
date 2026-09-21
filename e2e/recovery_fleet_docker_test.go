//go:build e2e && recoverydocker && !windows

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"gopkg.in/yaml.v3"
)

// Three disposable containers share an owned internal Docker network. They
// expose no ports, mounts, host credentials or Docker socket. Targets receive
// only OpenSSH's required setuid/setgid/chroot capabilities, not host privileges.
// This is container evidence for pinned SSH dispatch; reboot remains VM-tested.
func TestRecoveryDockerFleet(t *testing.T) {
	const image = "cvkeharness-recovery-vm:alpine323"
	docker := func(input []byte, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, "docker", args...)
		c.Stdin = bytes.NewReader(input)
		return c.CombinedOutput()
	}
	must := func(args ...string) []byte {
		t.Helper()
		b, err := docker(nil, args...)
		if err != nil {
			t.Fatalf("docker %v: %v\n%s", args, err, b)
		}
		return b
	}
	must("image", "inspect", image, "--format", "{{.Id}}")
	binary := filepath.Join(t.TempDir(), "cvkeharness-linux")
	build := exec.Command("go", "build", "-tags=recoveryfault", "-o", binary, ".")
	build.Dir = repositoryRoot
	build.Env = envWith(os.Environ(), map[string]string{"GOOS": "linux", "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0"})
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	name := fmt.Sprintf("cvkeharness-fleet-%d", time.Now().UnixNano())
	network := name + "-network"
	must("network", "create", "--internal", "--label", "cvkeharness.test=recovery", network)
	t.Cleanup(func() {
		if b, err := docker(nil, "network", "rm", network); err != nil {
			t.Errorf("owned network cleanup %v\n%s", err, b)
		}
	})
	containers := []string{name + "-controller", name + "-one", name + "-two"}
	controller := containers[0]
	for i, c := range containers {
		a := []string{"create", "--name", c, "--label", "cvkeharness.test=recovery", "--network", network, "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "96", "--memory", "256m", "--cpus", "1", "--entrypoint", "/bin/sh"}
		if i > 0 {
			a = append(a, "--cap-add", "SETUID", "--cap-add", "SETGID", "--cap-add", "SYS_CHROOT")
		}
		must(append(a, image, "-c", "sleep 1200")...)
		t.Cleanup(func() {
			if b, err := docker(nil, "rm", "-f", "-v", c); err != nil {
				t.Errorf("owned container cleanup %v\n%s", err, b)
			}
		})
		must("cp", binary, c+":/cvkeharness")
		must("start", c)
		must("exec", c, "sh", "-c", "mkdir -p /lab /state /root/.cvkeharness /run/sshd; chmod 700 /state /root/.cvkeharness; head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \\n' > /etc/machine-id")
	}
	write := func(container, path string, b []byte) {
		t.Helper()
		cmd := exec.Command("docker", "exec", "-i", container, "sh", "-c", "cat > '"+path+"' && chmod 600 '"+path+"'")
		cmd.Stdin = bytes.NewReader(b)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("write fixture %v %s", err, out)
		}
	}
	must("exec", controller, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", "/state/client-key")
	clientPub := must("exec", controller, "cat", "/state/client-key.pub")
	targetConfig := config.DefaultConfig()
	targetConfig.StateDBPath = "/state/state.db"
	targetConfig.Recovery.Roots = []string{"/lab"}
	targetYAML, _ := yaml.Marshal(targetConfig)
	cfg := config.DefaultConfig()
	cfg.StateDBPath = "/state/state.db"
	// This operator-authored lab policy explicitly permits asking to use its
	// generated identity. The default profile's credential denial is preserved.
	if err := cfg.Security.SetOverride(securitypolicy.SettingCredentialAccess, string(securitypolicy.DecisionAsk)); err != nil {
		t.Fatal(err)
	}
	cfg.Recovery.Fleet.Limits = recovery.DefaultFleetLimits()
	for i, c := range containers[1:] {
		// Reuse only the pinned guest's installed OpenSSH payload in the owned
		// container; no package downloads or image/host changes are needed.
		must("exec", c, "sh", "-c", "cp /guest/usr/sbin/sshd /usr/sbin/sshd; cp -a /guest/usr/lib/ssh /usr/lib/; cp /guest/etc/passwd /etc/passwd; cp /guest/etc/group /etc/group; cp /guest/etc/shadow /etc/shadow")
		write(c, "/state/authorized_keys", clientPub)
		write(c, "/root/.cvkeharness/config.yaml", targetYAML)
		must("exec", c, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", "/state/host-key")
		write(c, "/state/sshd_config", []byte("Port 2222\nListenAddress 0.0.0.0\nHostKey /state/host-key\nPidFile /state/sshd.pid\nAuthorizedKeysFile /state/authorized_keys\nPermitRootLogin prohibit-password\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nAllowUsers root\nAllowTcpForwarding no\nX11Forwarding no\nUseDNS no\n"))
		// A lab-only wrapper kills its own SSH session after the real executable
		// commits, proving an actual lost transport acknowledgement.
		write(c, "/fleet-executor", []byte(`#!/bin/sh
if [ "$6" = apply ] && [ -f /state/drop-ack ]; then
 /cvkeharness "$@" > /state/lost-response
 result=$?
 if [ "$result" = 0 ]; then
  listener=$(cat /state/sshd.pid)
  if [ "$PPID" != "$listener" ] && [ "$PPID" -gt 1 ]; then kill -KILL "$PPID"; fi
  exit 86
 fi
 exit "$result"
fi
exec /cvkeharness "$@"
`))
		must("exec", c, "chmod", "700", "/fleet-executor")
		must("exec", c, "/usr/sbin/sshd", "-t", "-f", "/state/sshd_config")
		must("exec", "-d", c, "/usr/sbin/sshd", "-D", "-e", "-f", "/state/sshd_config")
		identity := must("exec", c, "/cvkeharness", "recovery", "--state", "/state/state.db", "identity")
		var id map[string]string
		if err := json.Unmarshal(identity, &id); err != nil {
			t.Fatal(err)
		}
		ip := strings.TrimSpace(string(must("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", c)))
		pub := strings.Fields(string(must("exec", c, "cat", "/state/host-key.pub")))
		if len(pub) < 2 {
			t.Fatal("missing host public key")
		}
		known := fmt.Sprintf("/state/known-%d", i)
		write(controller, known, []byte(id["target"]+" "+pub[0]+" "+pub[1]+"\n"))
		cfg.Recovery.Fleet.Hosts = append(cfg.Recovery.Fleet.Hosts, recovery.FleetHost{Name: fmt.Sprintf("host%d", i), Address: ip, Port: 2222, User: "root", ExpectedTarget: id["target"], SSHBinary: "/usr/bin/ssh", IdentityFile: "/state/client-key", KnownHostsFile: known, Executor: "/fleet-executor", StatePath: "/state/state.db", TimeoutSeconds: 10})
	}
	saveConfig := func() { b, _ := yaml.Marshal(cfg); write(controller, "/root/.cvkeharness/config.yaml", b) }
	saveConfig()
	cli := func(container, fault string, args ...string) ([]byte, error) {
		a := []string{"exec"}
		if fault != "" {
			a = append(a, "-e", "CVKE_RECOVERY_FAULT="+fault)
		}
		a = append(a, container, "/cvkeharness", "recovery", "--state", "/state/state.db")
		return docker(nil, append(a, args...)...)
	}
	batchCLI := func(fault string, args ...string) (recovery.FleetBatch, []byte, error) {
		out, err := cli(controller, fault, append([]string{"fleet"}, args...)...)
		var b recovery.FleetBatch
		if err == nil {
			if dErr := json.Unmarshal(out, &b); dErr != nil {
				t.Fatalf("batch decode %v %s", dErr, out)
			}
		}
		return b, out, err
	}
	prepareRefs := func(testName string) []recovery.FleetReference {
		refs := []recovery.FleetReference{}
		for i, c := range containers[1:] {
			path := "/lab/" + testName
			write(c, path, []byte("original"))
			request, _ := json.Marshal(recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: path, Content: "changed"}}})
			write(c, "/state/request.json", request)
			out, err := cli(c, "", "prepare", "--request", "/state/request.json")
			if err != nil {
				t.Fatalf("target prepare %v %s", err, out)
			}
			var op recovery.Operation
			if err = json.Unmarshal(out, &op); err != nil {
				t.Fatal(err)
			}
			refs = append(refs, recovery.FleetReference{Host: fmt.Sprintf("host%d", i), ID: op.ID, Digest: op.Digest})
		}
		b, _ := json.Marshal(refs)
		write(controller, "/state/batch.json", b)
		return refs
	}
	prepareBatch := func(testName string) recovery.FleetBatch {
		prepareRefs(testName)
		b, out, err := batchCLI("", "prepare", "--request", "/state/batch.json")
		if err != nil {
			t.Fatalf("batch prepare %v %s", err, out)
		}
		return b
	}
	assertFile := func(i int, path, want string) {
		t.Helper()
		got := string(must("exec", containers[i+1], "cat", "/lab/"+path))
		if got != want {
			t.Fatalf("target %d: %q want %q", i, got, want)
		}
	}
	t.Run("serial_commit_and_offline_restore", func(t *testing.T) {
		b := prepareBatch("serial")
		b, out, err := batchCLI("", "apply", b.ID, "--confirm", b.Digest)
		if err != nil || b.Status != recovery.Committed {
			t.Fatalf("%v %s", err, out)
		}
		assertFile(0, "serial", "changed")
		assertFile(1, "serial", "changed")
		must("exec", controller, "rm", "/root/.cvkeharness/config.yaml")
		b, out, err = batchCLI("", "recover", b.ID, "--confirm", b.Digest)
		if err != nil || b.Status != recovery.Recovered {
			t.Fatalf("offline recovery %v %s", err, out)
		}
		assertFile(0, "serial", "original")
		assertFile(1, "serial", "original")
		saveConfig()
	})
	t.Run("disconnect_after_apply_stops_second_target", func(t *testing.T) {
		b := prepareBatch("disconnect")
		must("exec", containers[1], "touch", "/state/drop-ack")
		_, out, err := batchCLI("", "apply", b.ID, "--confirm", b.Digest)
		if err == nil {
			t.Fatal("dropped connection was reported committed")
		}
		t.Logf("lost acknowledgement stopped controller: %v", err)
		b, out, err = batchCLI("", "inspect", b.ID)
		if err != nil || b.Status != recovery.Unknown || b.Outcomes[1].Dispatched {
			t.Fatalf("%v %s", err, out)
		}
		assertFile(0, "disconnect", "changed")
		assertFile(1, "disconnect", "original")
		must("exec", containers[1], "rm", "/state/drop-ack")
		if _, _, err = batchCLI("", "apply", b.ID, "--confirm", b.Digest); err == nil {
			t.Fatal("uncertain apply replayed")
		}
		b, out, err = batchCLI("", "reconcile", b.ID)
		if err != nil || b.Outcomes[0].State != recovery.Committed {
			t.Fatalf("%v %s", err, out)
		}
		b, out, err = batchCLI("", "recover", b.ID, "--confirm", b.Digest)
		if err != nil || b.Status != recovery.Recovered {
			t.Fatalf("%v %s", err, out)
		}
		assertFile(0, "disconnect", "original")
	})
	for _, stage := range []string{"fleet_dispatched", "fleet_responded", "fleet_recorded", "fleet_repair_dispatched", "fleet_repair_responded"} {
		t.Run(stage, func(t *testing.T) {
			b := prepareBatch(stage)
			_, out, err := batchCLI(stage, "apply", b.ID, "--confirm", b.Digest)
			if strings.Contains(stage, "repair") {
				if err != nil {
					t.Fatalf("%v %s", err, out)
				}
				_, out, err = batchCLI(stage, "recover", b.ID, "--confirm", b.Digest)
			}
			assertRecoveryFaultExit(t, err, out)
			b, out, err = batchCLI("", "inspect", b.ID)
			if err != nil {
				t.Fatalf("%v %s", err, out)
			}
			if !strings.Contains(stage, "repair") && b.Outcomes[1].Dispatched {
				t.Fatal("later target dispatched after controller crash")
			}
			if _, _, err = batchCLI("", "apply", b.ID, "--confirm", b.Digest); err == nil {
				t.Fatal("crashed apply resumed")
			}
			if stage == "fleet_dispatched" {
				assertFile(0, stage, "original")
				b, out, err = batchCLI("", "recover", b.ID, "--confirm", b.Digest)
				if err != nil || b.Status != recovery.Recovered {
					t.Fatalf("cancellation: %v %s", err, out)
				}
				entry := b.Plan.Entries[0].Operation
				if _, err = cli(containers[1], "", "apply", entry.ID, "--confirm", entry.Digest); err == nil {
					t.Fatal("late apply ran after cancellation")
				}
				return
			}
			b, out, err = batchCLI("", "recover", b.ID, "--confirm", b.Digest)
			if err != nil || b.Status != recovery.Recovered {
				t.Fatalf("%v %s", err, out)
			}
			assertFile(0, stage, "original")
			assertFile(1, stage, "original")
		})
	}
	t.Run("fresh_target_conflict_stops_rollout", func(t *testing.T) {
		b := prepareBatch("conflict")
		write(containers[1], "/lab/conflict", []byte("external writer"))
		if _, _, err := batchCLI("", "apply", b.ID, "--confirm", b.Digest); err == nil {
			t.Fatal("stale target applied")
		}
		assertFile(0, "conflict", "external writer")
		assertFile(1, "conflict", "original")
	})
	t.Run("aggregate_budget", func(t *testing.T) {
		prepareRefs("budget")
		cfg.Recovery.Fleet.Limits.MaxBytes = 20
		saveConfig()
		if _, _, err := batchCLI("", "prepare", "--request", "/state/batch.json"); err == nil {
			t.Fatal("aggregate bytes exceeded")
		}
		assertFile(0, "budget", "original")
		assertFile(1, "budget", "original")
		cfg.Recovery.Fleet.Limits = recovery.DefaultFleetLimits()
		saveConfig()
	})
	t.Run("wrong_host_key_and_executor_identity", func(t *testing.T) {
		refs := prepareRefs("identity")
		saved := cfg.Recovery.Fleet.Hosts[0]
		// Point host0 at host1 but retain host0's exact pinned identity/key.
		cfg.Recovery.Fleet.Hosts[0].Address = cfg.Recovery.Fleet.Hosts[1].Address
		saveConfig()
		if _, _, err := batchCLI("", "prepare", "--request", "/state/batch.json"); err == nil {
			t.Fatal("wrong server host key accepted")
		}
		cfg.Recovery.Fleet.Hosts[0] = saved
		saveConfig()
		out, err := cli(containers[1], "", "--expect-target", cfg.Recovery.Fleet.Hosts[1].ExpectedTarget, "apply", refs[0].ID, "--confirm", refs[0].Digest)
		if err == nil || !bytes.Contains(out, []byte("executor identity")) {
			t.Fatalf("target-side identity guard missing %v %s", err, out)
		}
		assertFile(0, "identity", "original")
		assertFile(1, "identity", "original")
	})
	t.Run("rollback_conflict_and_durable_repair_limit", func(t *testing.T) {
		b := prepareBatch("repair")
		b, out, err := batchCLI("", "apply", b.ID, "--confirm", b.Digest)
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		write(containers[2], "/lab/repair", []byte("external writer"))
		for i := 0; i < 2; i++ {
			if _, _, err = batchCLI("", "recover", b.ID, "--confirm", b.Digest); err == nil {
				t.Fatal("conflict lost")
			}
		}
		_, out, err = batchCLI("", "recover", b.ID, "--confirm", b.Digest)
		if err == nil || !bytes.Contains(out, []byte("repair-attempt limit")) {
			t.Fatalf("budget %v %s", err, out)
		}
		b, out, err = batchCLI("", "inspect", b.ID)
		if err != nil || b.Outcomes[1].Repairs != 2 || b.Outcomes[0].Repairs != 0 {
			t.Fatalf("%v %s", err, out)
		}
		assertFile(0, "repair", "changed")
		assertFile(1, "repair", "external writer")
	})
}
