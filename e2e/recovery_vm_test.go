//go:build e2e && recoveryvm && !windows

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/recovery"
	"gopkg.in/yaml.v3"
)

type recoveryVM struct {
	t         *testing.T
	container string
}

func vmQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (v *recoveryVM) docker(input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	return cmd.CombinedOutput()
}
func (v *recoveryVM) mustDocker(args ...string) []byte {
	v.t.Helper()
	b, err := v.docker(nil, args...)
	if err != nil {
		v.t.Fatalf("docker %v: %v\n%s", args, err, b)
	}
	return b
}
func (v *recoveryVM) run(input []byte, command string) ([]byte, error) {
	return v.docker(input, "exec", "-i", v.container, "guest-ssh", command)
}
func (v *recoveryVM) must(command string) []byte {
	v.t.Helper()
	b, err := v.run(nil, command)
	if err != nil {
		v.t.Fatalf("guest command %s: %v\n%s", command, err, b)
	}
	return b
}
func (v *recoveryVM) write(path string, data []byte) {
	v.t.Helper()
	if out, err := v.run(data, "cat > "+vmQuote(path)+" && chmod 600 "+vmQuote(path)); err != nil {
		v.t.Fatalf("guest fixture: %v\n%s", err, out)
	}
}
func (v *recoveryVM) awaitSSH(oldBoot string) string {
	v.t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	var last []byte
	for time.Now().Before(deadline) {
		out, err := v.run(nil, "cat /proc/sys/kernel/random/boot_id")
		last = out
		boot := strings.TrimSpace(string(out))
		// First-contact host-key diagnostics can accompany the first result.
		if i := strings.LastIndexByte(boot, '\n'); i >= 0 {
			boot = boot[i+1:]
		}
		if err == nil && len(boot) == 36 && boot != oldBoot {
			return boot
		}
		if state := strings.TrimSpace(string(v.mustDocker("inspect", "--format", "{{.State.Running}}", v.container))); state != "true" {
			v.t.Fatalf("VM container stopped; last SSH result: %s", last)
		}
		time.Sleep(300 * time.Millisecond)
	}
	v.t.Fatalf("VM did not establish a new SSH connection/boot: %s", last)
	return ""
}

func startRecoveryVM(t *testing.T) *recoveryVM {
	t.Helper()
	v := &recoveryVM{t: t, container: fmt.Sprintf("cvkeharness-vm-%d", time.Now().UnixNano())}
	const image = "cvkeharness-recovery-vm:alpine323"
	if out, err := v.docker(nil, "image", "inspect", image, "--format", "{{.Id}}"); err != nil {
		t.Fatalf("explicit VM lab needs its image; run bash scripts/build-recovery-vm.sh: %v\n%s", err, out)
	}
	binary := filepath.Join(t.TempDir(), "cvkeharness-linux-arm64")
	build := exec.Command("go", "build", "-tags=recoveryfault", "-o", binary, ".")
	build.Dir = repositoryRoot
	build.Env = envWith(os.Environ(), map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0"})
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guest CvkeHarness: %v\n%s", err, out)
	}
	v.mustDocker("create", "--name", v.container, "--label", "cvkeharness.test=recovery", "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "2g", "--cpus", "2", "--pids-limit", "128", image)
	t.Cleanup(func() {
		if t.Failed() {
			if logs, err := v.docker(nil, "logs", "--tail", "100", v.container); err == nil {
				t.Logf("VM console tail:\n%s", logs)
			}
		}
		if out, err := v.docker(nil, "rm", "-f", "-v", v.container); err != nil {
			t.Errorf("owned VM cleanup: %v\n%s", err, out)
		}
	})
	v.mustDocker("cp", binary, v.container+":/payload/cvkeharness")
	v.mustDocker("start", v.container)
	v.awaitSSH("")
	return v
}

func TestRecoveryVMKernelPersistenceAndOfflineRecovery(t *testing.T) {
	v := startRecoveryVM(t)
	outerBoot := strings.TrimSpace(string(v.mustDocker("exec", v.container, "cat", "/proc/sys/kernel/random/boot_id")))
	guestBoot := strings.TrimSpace(string(v.must("cat /proc/sys/kernel/random/boot_id")))
	if guestBoot == outerBoot {
		t.Fatal("container and guest share a kernel boot ID; this is not VM evidence")
	}
	t.Logf("guest kernel: %s", strings.TrimSpace(string(v.must("uname -r"))))
	if got := strings.TrimSpace(string(v.must("findmnt -n -o FSTYPE /data"))); got != "btrfs" {
		t.Fatalf("guest snapshot filesystem: %s", got)
	}
	v.must("mkdir -p /persist/target")
	v.write("/persist/target/config", []byte("original"))
	request, _ := json.Marshal(recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: "/persist/target/config", Content: "changed"}}})
	v.write("/persist/request.json", request)
	cli := "cvkeharness recovery --state /persist/state/state.db --root /persist/target "
	var op recovery.Operation
	if err := json.Unmarshal(v.must(cli+"prepare --request /persist/request.json"), &op); err != nil {
		t.Fatal(err)
	}
	out, err := v.run(nil, "CVKE_RECOVERY_FAULT=applied "+cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 86 {
		t.Fatalf("expected guest process death at apply, got %v\n%s", err, out)
	}
	if got := string(v.must("cat /persist/target/config")); got != "changed" {
		t.Fatalf("guest mutation not applied: %q", got)
	}
	// The connection drops because the guest kernel actually reboots. Both
	// SQLite state and target bytes must survive, with stable machine identity.
	_, _ = v.run(nil, "sync; reboot -f")
	newBoot := v.awaitSSH(guestBoot)
	if newBoot == guestBoot {
		t.Fatal("guest did not reboot")
	}
	var restored recovery.Operation
	v.must(cli + "reconcile " + vmQuote(op.ID))
	if err := json.Unmarshal(v.must(cli+"recover "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest)), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Status != recovery.Recovered {
		t.Fatalf("restoration status: %s", restored.Status)
	}
	if got := string(v.must("cat /persist/target/config")); got != "original" {
		t.Fatalf("offline reboot recovery failed: %q", got)
	}
}

func TestRecoveryVMNativeSnapshots(t *testing.T) {
	v := startRecoveryVM(t)
	cfg := config.DefaultConfig()
	cfg.StateDBPath = "/persist/state/state.db"
	cfg.Recovery.Snapshots = []recovery.SnapshotTarget{{Name: "work", Path: "/data/work", Store: "/data/.cvkeharness-snapshots", MaxEntries: 100, MaxLogicalBytes: 1 << 20}}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	v.must("mkdir -p /root/.cvkeharness /data/work/nested")
	v.write("/root/.cvkeharness/config.yaml", b)
	v.write("/data/work/config", []byte("original"))
	v.write("/data/work/nested/keep", []byte("keep"))
	cli := "cvkeharness recovery --state /persist/state/state.db "
	readOp := func(command string) recovery.Operation {
		t.Helper()
		out := v.must(command)
		var op recovery.Operation
		if err := json.Unmarshal(out, &op); err != nil {
			t.Fatalf("operation JSON: %v\n%s", err, out)
		}
		return op
	}
	checkpoint := readOp(cli + "snapshot prepare work")
	if checkpoint.Status != recovery.Ready {
		t.Fatalf("checkpoint preparation: %s", checkpoint.Status)
	}
	checkpoint = readOp(cli + "apply " + vmQuote(checkpoint.ID) + " --confirm " + vmQuote(checkpoint.Digest))
	if checkpoint.Status != recovery.Committed {
		t.Fatalf("checkpoint not committed: %s", checkpoint.Status)
	}
	cpPath := "/data/.cvkeharness-snapshots/" + checkpoint.ID + ".checkpoint"
	if got := string(v.must("btrfs property get -ts " + vmQuote(cpPath) + " ro")); !strings.Contains(got, "ro=true") {
		t.Fatalf("checkpoint is not natively read-only: %s", got)
	}
	v.must("rm /data/work/config")
	v.write("/data/work/nested/keep", []byte("changed"))
	v.write("/data/work/introduced", []byte("retain me"))
	restore := readOp(cli + "snapshot restore-prepare " + vmQuote(checkpoint.ID) + " --checkpoint-digest " + vmQuote(checkpoint.Digest))
	restored := readOp(cli + "apply " + vmQuote(restore.ID) + " --confirm " + vmQuote(restore.Digest))
	if restored.Status != recovery.Recovered {
		t.Fatalf("snapshot restore not verified: %s", restored.Status)
	}
	if got := string(v.must("cat /data/work/config")); got != "original" {
		t.Fatalf("native restore content: %q", got)
	}
	if got := string(v.must("cat /data/work/nested/keep")); got != "keep" {
		t.Fatalf("native restore nested content: %q", got)
	}
	v.must("test ! -e /data/work/introduced")
	displaced := "/data/.cvkeharness-snapshots/" + restore.ID + ".displaced"
	if got := string(v.must("cat " + vmQuote(displaced+"/introduced"))); got != "retain me" {
		t.Fatal("restore lost the displaced tree")
	}
	v.must("test ! -e " + vmQuote(displaced+"/config"))
	for _, stage := range []string{"snapshot_applying", "snapshot_cloned", "snapshot_exchanged", "snapshot_verified", "snapshot_complete"} {
		v.write("/data/work/config", []byte(stage))
		op := readOp(cli + "snapshot restore-prepare " + vmQuote(checkpoint.ID) + " --checkpoint-digest " + vmQuote(checkpoint.Digest))
		out, err := v.run(nil, "CVKE_RECOVERY_FAULT="+stage+" "+cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 86 {
			t.Fatalf("native stage %s did not die: %v\n%s", stage, err, out)
		}
		if stage == "snapshot_exchanged" {
			oldBoot := strings.TrimSpace(string(v.must("cat /proc/sys/kernel/random/boot_id")))
			_, _ = v.run(nil, "sync; reboot -f")
			v.awaitSSH(oldBoot)
			// Restore uses its persisted plan without the operator app config
			// that lived in the initramfs's old /root.
		}
		readOp(cli + "reconcile " + vmQuote(op.ID))
		op = readOp(cli + "recover " + vmQuote(op.ID) + " --confirm " + vmQuote(op.Digest))
		if op.Status != recovery.Recovered {
			t.Fatalf("native stage %s recovery: %s", stage, op.Status)
		}
		if got := string(v.must("cat /data/work/config")); got != "original" {
			t.Fatalf("native stage %s content: %q", stage, got)
		}
	}
	// The application config does not survive a guest reboot; restore it only
	// for new checkpoint preparation. Recovery above worked without it.
	v.must("mkdir -p /root/.cvkeharness")
	v.write("/root/.cvkeharness/config.yaml", b)
	for _, stage := range []string{"snapshot_applying", "snapshot_cloned", "snapshot_receipted", "snapshot_complete"} {
		op := readOp(cli + "snapshot prepare work")
		out, err := v.run(nil, "CVKE_RECOVERY_FAULT="+stage+" "+cli+"apply "+vmQuote(op.ID)+" --confirm "+vmQuote(op.Digest))
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 86 {
			t.Fatalf("checkpoint stage %s did not die: %v\n%s", stage, err, out)
		}
		readOp(cli + "reconcile " + vmQuote(op.ID))
		op = readOp(cli + "recover " + vmQuote(op.ID) + " --confirm " + vmQuote(op.Digest))
		if op.Status != recovery.Committed {
			t.Fatalf("checkpoint stage %s recovery: %s", stage, op.Status)
		}
	}
	stale := readOp(cli + "snapshot restore-prepare " + vmQuote(checkpoint.ID) + " --checkpoint-digest " + vmQuote(checkpoint.Digest))
	v.write("/data/work/concurrent", []byte("later writer"))
	if out, err := v.run(nil, cli+"apply "+vmQuote(stale.ID)+" --confirm "+vmQuote(stale.Digest)); err == nil || !strings.Contains(string(out), "changed after review") {
		t.Fatalf("stale restore did not refuse: %v\n%s", err, out)
	}
	if got := string(v.must("cat /data/work/concurrent")); got != "later writer" {
		t.Fatal("stale restore overwrote a later writer")
	}
	fresh := readOp(cli + "snapshot restore-prepare " + vmQuote(checkpoint.ID) + " --checkpoint-digest " + vmQuote(checkpoint.Digest))
	readOp(cli + "apply " + vmQuote(fresh.ID) + " --confirm " + vmQuote(fresh.Digest))
	v.must("test ! -e /data/work/concurrent")
	if got := string(v.must("cat " + vmQuote("/data/.cvkeharness-snapshots/"+fresh.ID+".displaced/concurrent"))); got != "later writer" {
		t.Fatal("fresh restore lost the retained later writer")
	}
	v.must("ln -s /persist/machine-id /data/work/link")
	if out, err := v.run(nil, cli+"snapshot prepare work"); err == nil {
		t.Fatalf("symlink snapshot accepted: %s", out)
	}
	v.must("rm /data/work/link")
	v.must("mkdir /data/work/mounted /data/bind-source && mount --bind /data/bind-source /data/work/mounted")
	if out, err := v.run(nil, cli+"snapshot prepare work"); err == nil || !strings.Contains(string(out), "mount") {
		t.Fatalf("bind mount was not refused: %v\n%s", err, out)
	}
	v.must("umount /data/work/mounted && rmdir /data/work/mounted")
	v.write("/data/bind-source/file", []byte("outside"))
	v.write("/data/work/mounted-file", []byte("inside"))
	v.must("mount --bind /data/bind-source/file /data/work/mounted-file")
	if out, err := v.run(nil, cli+"snapshot prepare work"); err == nil || !strings.Contains(string(out), "mounted file") {
		t.Fatalf("same-filesystem file bind mount was not refused: %v\n%s", err, out)
	}
	v.must("umount /data/work/mounted-file && rm /data/work/mounted-file")
	v.must("mkdir /data/work/.ssh")
	if out, err := v.run(nil, cli+"snapshot prepare work"); err == nil || !strings.Contains(string(out), "protected credential") {
		t.Fatalf("nested credential directory was not refused: %v\n%s", err, out)
	}
	v.must("rmdir /data/work/.ssh")
	// An administrator can turn a native checkpoint writable. Restoring the
	// read-only flag afterwards must not hide a content change from the adapter.
	v.must("btrfs property set -ts " + vmQuote(cpPath) + " ro false")
	v.write(cpPath+"/config", []byte("tampered"))
	v.must("btrfs property set -ts " + vmQuote(cpPath) + " ro true")
	if out, err := v.run(nil, cli+"snapshot restore-prepare "+vmQuote(checkpoint.ID)+" --checkpoint-digest "+vmQuote(checkpoint.Digest)); err == nil || !strings.Contains(string(out), "coverage/content changed") {
		t.Fatalf("tampered checkpoint accepted: %v\n%s", err, out)
	}
	// A nested subvolume is a snapshot boundary, not a recursively protected
	// directory. Refuse the whole checkpoint instead of silently excluding it.
	v.must("btrfs subvolume create /data/work/nested-volume")
	if out, err := v.run(nil, cli+"snapshot prepare work"); err == nil || !strings.Contains(string(out), "nested subvolume") {
		t.Fatalf("nested subvolume was not refused: %v\n%s", err, out)
	}
}
