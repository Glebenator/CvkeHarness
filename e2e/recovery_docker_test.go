//go:build e2e && recoverydocker && !windows

package e2e_test

import (
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

	"github.com/coolcake/cvkeharness/recovery"
)

// The explicitly selected Docker suite fails, rather than skips, when its
// runtime is unavailable. Every target is disposable and has no host mounts,
// published ports, network, Docker socket, or provider credentials.
func TestRecoveryDockerFaultLab(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("Docker is required for this explicitly selected suite")
	}
	linuxBinary := filepath.Join(t.TempDir(), "cvkeharness-linux")
	build := exec.Command("go", "build", "-tags=recoveryfault", "-o", linuxBinary, ".")
	build.Dir = repositoryRoot
	build.Env = envWith(os.Environ(), map[string]string{"GOOS": "linux", "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0"})
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Linux lab executable: %v\n%s", err, out)
	}
	container := fmt.Sprintf("cvkeharness-recovery-%d", time.Now().UnixNano())
	docker := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	}
	must := func(args ...string) []byte {
		t.Helper()
		out, err := docker(args...)
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	must("create", "--name", container, "--label", "cvkeharness.test=recovery", "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/smallstate/recovery:rw,size=64k,mode=0700", "--tmpfs", "/smalltarget:rw,size=64k,mode=0700", "alpine:latest", "sleep", "600")
	t.Cleanup(func() {
		if out, err := docker("rm", "-f", "-v", container); err != nil {
			t.Errorf("remove owned test container: %v\n%s", err, out)
		}
	})
	must("cp", linuxBinary, container+":/cvkeharness")
	must("start", container)
	must("exec", container, "sh", "-c", "mkdir -p /lab /state; chmod 700 /state; head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \\n' > /etc/machine-id")
	run := func(env string, args ...string) ([]byte, error) {
		a := []string{"exec"}
		if env != "" {
			a = append(a, "-e", "CVKE_RECOVERY_FAULT="+env)
		}
		a = append(a, container, "/cvkeharness", "recovery", "--state", "/state/state.db", "--root", "/lab")
		return docker(append(a, args...)...)
	}
	readOps := func() []recovery.Operation {
		out, err := run("", "list")
		if err != nil {
			t.Fatalf("list: %v\n%s", err, out)
		}
		var records []json.RawMessage
		if err = json.Unmarshal(out, &records); err != nil {
			t.Fatal(err)
		}
		var ops []recovery.Operation
		for _, r := range records {
			var op recovery.Operation
			if err = json.Unmarshal(r, &op); err != nil {
				t.Fatal(err)
			}
			ops = append(ops, op)
		}
		return ops
	}
	for _, stage := range []string{"preparing", "backup", "ready", "capacity_checked", "applying", "applied", "verifying", "committed", "rolling_back", "restored", "recovered"} {
		t.Run(stage, func(t *testing.T) {
			// Isolated names per case avoid changing another test's artifacts.
			dir := "/lab/" + stage
			must("exec", container, "mkdir", "-p", dir)
			must("exec", container, "sh", "-c", "printf original > "+dir+"/config")
			request := recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: dir + "/config", Content: "changed"}}}
			b, _ := json.Marshal(request)
			requestFile := filepath.Join(t.TempDir(), "request.json")
			// Synthetic fixture only; docker cp can preserve the host UID, and
			// the cap-dropped container intentionally cannot bypass file modes.
			if err := os.WriteFile(requestFile, b, 0644); err != nil {
				t.Fatal(err)
			}
			must("cp", requestFile, container+":/request.json")
			prepareOutput, err := run(stage, "prepare", "--request", "/request.json")
			ops := readOps()
			if len(ops) == 0 {
				t.Fatalf("missing durable operation: %v\n%s", err, prepareOutput)
			}
			op := ops[0]
			if err == nil {
				_, err = run(stage, "apply", op.ID, "--confirm", op.Digest)
			}
			if err == nil && (stage == "rolling_back" || stage == "restored" || stage == "recovered") {
				_, err = run(stage, "recover", op.ID, "--confirm", op.Digest)
			}
			assertRecoveryFaultExit(t, err, prepareOutput)
			out, err := run("", "reconcile", op.ID)
			if err != nil {
				t.Fatalf("reconcile: %v\n%s", err, out)
			}
			if stage != "preparing" && stage != "backup" && stage != "ready" && stage != "capacity_checked" {
				out, err = run("", "recover", op.ID, "--confirm", op.Digest)
				if err != nil {
					t.Fatalf("recover after crash: %v\n%s", err, out)
				}
			}
			got := must("exec", container, "cat", dir+"/config")
			if string(got) != "original" {
				t.Fatalf("independent target assertion failed: %q", got)
			}
		})
	}
	prepareExact := func(req recovery.Request) recovery.Operation {
		t.Helper()
		b, _ := json.Marshal(req)
		file := filepath.Join(t.TempDir(), "boundary.json")
		if err := os.WriteFile(file, b, 0644); err != nil {
			t.Fatal(err)
		}
		must("cp", file, container+":/boundary.json")
		out, err := run("", "prepare", "--request", "/boundary.json")
		if err != nil {
			t.Fatalf("prepare boundary: %v %s", err, out)
		}
		var op recovery.Operation
		if err = json.Unmarshal(out, &op); err != nil {
			t.Fatal(err)
		}
		return op
	}
	t.Run("partial_two_file_apply_then_restore", func(t *testing.T) {
		must("exec", container, "sh", "-c", "printf first-old > /lab/partial-first; printf second-old > /lab/partial-second")
		op := prepareExact(recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: "/lab/partial-first", Content: "first-new"}, {Action: "replace", Path: "/lab/partial-second", Content: "second-new"}}})
		out, err := run("applied", "apply", op.ID, "--confirm", op.Digest)
		assertRecoveryFaultExit(t, err, out)
		if got := string(must("exec", container, "cat", "/lab/partial-first")); got != "first-new" {
			t.Fatal(got)
		}
		if got := string(must("exec", container, "cat", "/lab/partial-second")); got != "second-old" {
			t.Fatal(got)
		}
		if out, err = run("", "recover", op.ID, "--confirm", op.Digest); err != nil {
			t.Fatalf("partial restore: %v %s", err, out)
		}
		if got := string(must("exec", container, "cat", "/lab/partial-first")); got != "first-old" {
			t.Fatal(got)
		}
		if got := string(must("exec", container, "cat", "/lab/partial-second")); got != "second-old" {
			t.Fatal(got)
		}
	})
	t.Run("parent_symlink_swap_refused", func(t *testing.T) {
		must("exec", container, "sh", "-c", "mkdir /lab/swap-parent /lab/sentinel; printf original > /lab/swap-parent/config; printf sensitive-fixture > /lab/sentinel/config")
		op := prepareExact(recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: "/lab/swap-parent/config", Content: "candidate"}}})
		must("exec", container, "sh", "-c", "mv /lab/swap-parent /lab/saved-parent; ln -s /lab/sentinel /lab/swap-parent")
		if out, err := run("", "apply", op.ID, "--confirm", op.Digest); err == nil {
			t.Fatalf("symlink swap followed: %s", out)
		}
		if got := string(must("exec", container, "cat", "/lab/sentinel/config")); got != "sensitive-fixture" {
			t.Fatal(got)
		}
		if got := string(must("exec", container, "cat", "/lab/saved-parent/config")); got != "original" {
			t.Fatal(got)
		}
	})
	t.Run("fresh_target_capacity_refusal", func(t *testing.T) {
		must("exec", container, "sh", "-c", "printf original > /smalltarget/settings")
		b, _ := json.Marshal(recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: "/smalltarget/settings", Content: "changed"}}})
		file := filepath.Join(t.TempDir(), "capacity.json")
		if err := os.WriteFile(file, b, 0644); err != nil {
			t.Fatal(err)
		}
		must("cp", file, container+":/capacity.json")
		a := []string{"exec", container, "/cvkeharness", "recovery", "--state", "/state/state.db", "--root", "/smalltarget"}
		out := must(append(a, "prepare", "--request", "/capacity.json")...)
		var op recovery.Operation
		if err := json.Unmarshal(out, &op); err != nil {
			t.Fatal(err)
		}
		out, err := docker(append(a, "apply", op.ID, "--confirm", op.Digest)...)
		if err == nil || !strings.Contains(string(out), "insufficient fresh target capacity") {
			t.Fatalf("expected measured target refusal: %v\n%s", err, out)
		}
		out = must(append(a, "inspect", op.ID)...)
		if err = json.Unmarshal(out, &op); err != nil {
			t.Fatal(err)
		}
		if op.Status != recovery.Ready || len(op.Checks) != 1 {
			t.Fatalf("missing pre-mutation capacity evidence: %s", out)
		}
		var evidence recovery.CapacityEvidence
		if err = json.Unmarshal(op.Checks[0].Evidence, &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.Passed || len(evidence.Filesystems) != 1 || evidence.Filesystems[0].AvailableBytes > 64<<10 {
			t.Fatalf("did not measure constrained target: %#v", evidence)
		}
		if got := must("exec", container, "cat", "/smalltarget/settings"); string(got) != "original" {
			t.Fatal("capacity refusal changed the target")
		}
	})
	t.Run("incorrect_numerical_input", func(t *testing.T) {
		c := recovery.Calculation{Operation: "multiply", Left: recovery.Quantity{Value: "75", Unit: "%"}, Right: recovery.Quantity{Value: "8", Unit: "GiB"}, OutputUnit: "MiB", Rounding: "exact", Expected: "6000"}
		b, _ := json.Marshal(c)
		file := filepath.Join(t.TempDir(), "math.json")
		if err := os.WriteFile(file, b, 0644); err != nil {
			t.Fatal(err)
		}
		must("cp", file, container+":/math.json")
		out, err := docker("exec", container, "/cvkeharness", "recovery", "calculate", "--request", "/math.json")
		if err == nil || !strings.Contains(string(out), `"value":"6144"`) || !strings.Contains(string(out), `"expected_matches":false`) {
			t.Fatalf("mistaken arithmetic accepted: %v\n%s", err, out)
		}
	})
	t.Run("backup_storage_exhaustion", func(t *testing.T) {
		must("exec", container, "sh", "-c", "printf original > /lab/enospc")
		request := recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: "/lab/enospc", Content: strings.Repeat("x", 256<<10)}}}
		b, _ := json.Marshal(request)
		file := filepath.Join(t.TempDir(), "full.json")
		if err := os.WriteFile(file, b, 0644); err != nil {
			t.Fatal(err)
		}
		must("cp", file, container+":/full.json")
		out, err := docker("exec", container, "/cvkeharness", "recovery", "--state", "/smallstate/state.db", "--root", "/lab", "prepare", "--request", "/full.json")
		if err == nil || !strings.Contains(string(out), "no space left on device") {
			t.Fatalf("expected real ENOSPC: %v\n%s", err, out)
		}
		if got := must("exec", container, "cat", "/lab/enospc"); string(got) != "original" {
			t.Fatalf("backup failure altered target: %s", got)
		}
	})
	t.Run("failed_restore_preserves_other_writer", func(t *testing.T) {
		must("exec", container, "sh", "-c", "printf original > /lab/conflict")
		request := recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: "/lab/conflict", Content: "ours"}}}
		b, _ := json.Marshal(request)
		file := filepath.Join(t.TempDir(), "conflict.json")
		if err := os.WriteFile(file, b, 0644); err != nil {
			t.Fatal(err)
		}
		must("cp", file, container+":/conflict.json")
		out, err := run("", "prepare", "--request", "/conflict.json")
		if err != nil {
			t.Fatalf("prepare: %v\n%s", err, out)
		}
		var op recovery.Operation
		if err = json.Unmarshal(out, &op); err != nil {
			t.Fatal(err)
		}
		if out, err = run("", "apply", op.ID, "--confirm", op.Digest); err != nil {
			t.Fatalf("apply: %v\n%s", err, out)
		}
		must("exec", container, "sh", "-c", "printf concurrent > /lab/conflict")
		out, err = run("", "recover", op.ID, "--confirm", op.Digest)
		if err == nil || !strings.Contains(string(out), recovery.RecoveryFailed) {
			t.Fatalf("expected recorded recovery failure: %v\n%s", err, out)
		}
		if got := must("exec", container, "cat", "/lab/conflict"); string(got) != "concurrent" {
			t.Fatalf("overwrote concurrent writer: %s", got)
		}
	})
}
