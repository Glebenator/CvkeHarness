//go:build e2e && recoverydocker && !windows

package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/recovery"
	"gopkg.in/yaml.v3"
)

// These scenarios use the real Linux binary and actual NGINX master/workers.
// No mocked service process, host mounts, host networking or published ports.
func TestRecoveryDockerNGINX(t *testing.T) {
	const image = "nginx:stable-alpine@sha256:dc5069ad14f19660b141b21236140b91656bf89bbc3e2417c70ae650cd66104c"
	docker := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	}
	must := func(args ...string) []byte {
		t.Helper()
		b, err := docker(args...)
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, b)
		}
		return b
	}
	if out, err := docker("image", "inspect", "--format", "{{.Id}}", image); err != nil {
		t.Fatalf("explicit NGINX lab requires preloaded official image %s (docker pull it first): %v\n%s", image, err, out)
	}
	binary := filepath.Join(t.TempDir(), "cvkeharness-linux")
	build := exec.Command("go", "build", "-tags=recoveryfault", "-o", binary, ".")
	build.Dir = repositoryRoot
	build.Env = envWith(os.Environ(), map[string]string{"GOOS": "linux", "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0"})
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	container := fmt.Sprintf("cvkeharness-nginx-%d", time.Now().UnixNano())
	must("create", "--name", container, "--label", "cvkeharness.test=recovery", "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--ulimit", "nofile=512:512", "--entrypoint", "sleep", image, "600")
	t.Cleanup(func() {
		if out, err := docker("rm", "-f", "-v", container); err != nil {
			t.Errorf("owned container cleanup: %v\n%s", err, out)
		}
	})
	must("cp", binary, container+":/cvkeharness")
	must("start", container)
	must("exec", container, "sh", "-c", "mkdir -p /lab /state /root/.cvkeharness; chmod 700 /state /root/.cvkeharness; head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \\n' > /etc/machine-id")
	copyText := func(path string, data []byte) {
		t.Helper()
		p := filepath.Join(t.TempDir(), "fixture")
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
		// docker cp preserves the host UID. Create the destination inside the
		// cap-dropped target so the fixture has the target executor's ownership.
		must("cp", p, container+":/fixture-input")
		must("exec", container, "cp", "/fixture-input", path+".fixture")
		must("exec", container, "mv", path+".fixture", path)
	}
	healthHash := sha256.Sum256([]byte("healthy"))
	svc := recovery.NGINXService{Name: "lab", Binary: "/usr/sbin/nginx", Prefix: "/lab", ConfigPath: "/lab/nginx.conf", PIDFile: "/lab/nginx.pid", HealthURL: "http://127.0.0.1:8080/health", HealthStatus: 200, HealthSHA256: hex.EncodeToString(healthHash[:]), TimeoutSeconds: 3}
	cfg := config.DefaultConfig()
	cfg.Recovery.Roots = []string{"/lab"}
	cfg.Recovery.Services = []recovery.NGINXService{svc}
	cfg.StateDBPath = "/state/state.db"
	configBytes, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	copyText("/root/.cvkeharness/config.yaml", configBytes)
	original := `user root;
worker_processes 1;
pid /lab/nginx.pid;
error_log stderr notice;
events { worker_connections 128; }
http {
 access_log off;
 default_type text/plain;
 client_body_temp_path /lab/client_body_temp_path;
 proxy_temp_path /lab/proxy_temp_path;
 fastcgi_temp_path /lab/fastcgi_temp_path;
 uwsgi_temp_path /lab/uwsgi_temp_path;
 scgi_temp_path /lab/scgi_temp_path;
 server { listen 127.0.0.1:8080;
  location = /health { add_header X-Cvke-Worker $pid always; return 200 "healthy"; }
  location / { return 200 "original"; }
 }
}`
	copyText(svc.ConfigPath, []byte(original))
	must("exec", container, svc.Binary, "-p", svc.Prefix, "-c", svc.ConfigPath)
	run := func(fault string, args ...string) (recovery.Operation, []byte, error) {
		a := []string{"exec"}
		if fault != "" {
			a = append(a, "-e", "CVKE_RECOVERY_FAULT="+fault)
		}
		a = append(a, container, "/cvkeharness", "recovery", "--state", "/state/state.db")
		out, err := docker(append(a, args...)...)
		var op recovery.Operation
		if err == nil {
			if decodeErr := json.Unmarshal(out, &op); decodeErr != nil {
				t.Fatalf("decode operation: %v\n%s", decodeErr, out)
			}
		}
		return op, out, err
	}
	prepare := func(candidate string) recovery.Operation {
		t.Helper()
		b, _ := json.Marshal(recovery.Request{Service: svc.Name, Changes: []recovery.Change{{Action: "replace", Path: svc.ConfigPath, Content: candidate}}})
		copyText("/request.json", b)
		op, out, err := run("", "prepare", "--request", "/request.json")
		if err != nil {
			t.Fatalf("prepare: %v\n%s", err, out)
		}
		return op
	}
	assertOriginal := func() {
		t.Helper()
		if got := must("exec", container, "cat", svc.ConfigPath); string(got) != original {
			t.Fatalf("original file not restored: %s", got)
		}
		if got := must("exec", container, "wget", "-qO-", "http://127.0.0.1:8080/"); string(got) != "original" {
			t.Fatalf("original worker response not restored: %q", got)
		}
	}
	changed := strings.Replace(original, "original", "changed", 1)
	t.Run("fresh_descriptor_headroom_refusal", func(t *testing.T) {
		op := prepare(strings.Replace(changed, "worker_connections 128", "worker_connections 512", 1))
		if _, out, err := run("", "apply", op.ID, "--confirm", op.Digest); err == nil || !strings.Contains(string(out), "resource check refused") {
			t.Fatalf("descriptor bound not enforced: %v %s", err, out)
		}
		persisted, out, err := run("", "inspect", op.ID)
		if err != nil || persisted.Status != recovery.Ready {
			t.Fatalf("refusal changed status: %v %s", err, out)
		}
		found := false
		for _, check := range persisted.Checks {
			if check.Kind == "service_resources" {
				var evidence recovery.ServiceResourceEvidence
				if err = json.Unmarshal(check.Evidence, &evidence); err != nil {
					t.Fatal(err)
				}
				if evidence.MasterDescriptorLimit != 512 || evidence.Passed {
					t.Fatalf("wrong target measurement: %+v", evidence)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("resource measurement missing")
		}
		assertOriginal()
	})
	t.Run("operator_connection_impact_refusal", func(t *testing.T) {
		request, _ := json.Marshal(recovery.Request{Service: svc.Name, Changes: []recovery.Change{{Action: "replace", Path: svc.ConfigPath, Content: strings.Replace(changed, "worker_connections 128", "worker_connections 8192", 1)}}})
		copyText("/request.json", request)
		if _, out, err := run("", "prepare", "--request", "/request.json"); err == nil || !strings.Contains(string(out), "impact limits") {
			t.Fatalf("operator impact bound not enforced: %v %s", err, out)
		}
		assertOriginal()
	})
	t.Run("crash_after_resource_measurement", func(t *testing.T) {
		op := prepare(changed)
		_, out, err := run("service_resources_checked", "apply", op.ID, "--confirm", op.Digest)
		assertRecoveryFaultExit(t, err, out)
		persisted, out, err := run("", "reconcile", op.ID)
		if err != nil || persisted.Status != recovery.Ready {
			t.Fatalf("read-only resource boundary mutated: %v %s", err, out)
		}
		assertOriginal()
	})
	t.Run("commit_then_offline_restore", func(t *testing.T) {
		op := prepare(changed)
		applied, out, err := run("", "apply", op.ID, "--confirm", op.Digest)
		if err != nil || applied.Status != recovery.Committed {
			t.Fatalf("apply: %v\n%s", err, out)
		}
		if got := must("exec", container, "wget", "-qO-", "http://127.0.0.1:8080/"); string(got) != "changed" {
			t.Fatalf("candidate was not served: %q", got)
		}
		// The saved adapter is sufficient even when the whole application
		// configuration (including all provider and service settings) is gone.
		must("exec", container, "mv", "/root/.cvkeharness/config.yaml", "/root/.cvkeharness/config.saved")
		restored, out, err := run("", "recover", op.ID, "--confirm", op.Digest)
		if err != nil || restored.Status != recovery.Recovered {
			t.Fatalf("recover: %v\n%s", err, out)
		}
		assertOriginal()
		must("exec", container, "mv", "/root/.cvkeharness/config.saved", "/root/.cvkeharness/config.yaml")
	})
	t.Run("unhealthy_candidate_restores_automatically", func(t *testing.T) {
		op := prepare(strings.Replace(changed, "healthy", "broken", 1))
		_, out, err := run("", "apply", op.ID, "--confirm", op.Digest)
		if err == nil {
			t.Fatalf("unhealthy change reported success: %s", out)
		}
		if !strings.Contains(string(out), "service health did not verify") {
			t.Fatalf("failure did not exercise health verification: %s", out)
		}
		persisted, out, err := run("", "inspect", op.ID)
		if err != nil || persisted.Status != recovery.Recovered {
			t.Fatalf("automatic recovery not recorded: %v\n%s", err, out)
		}
		assertOriginal()
	})
	for _, stage := range []string{"service_validating", "service_validated", "service_signaled", "service_healthy"} {
		t.Run("crash_"+stage, func(t *testing.T) {
			op := prepare(changed)
			_, out, err := run(stage, "apply", op.ID, "--confirm", op.Digest)
			assertRecoveryFaultExit(t, err, out)
			reconciled, out, err := run("", "reconcile", op.ID)
			if err != nil || reconciled.Status != recovery.Unknown {
				t.Fatalf("outcome was not unknown: %v\n%s", err, out)
			}
			restored, out, err := run("", "recover", op.ID, "--confirm", op.Digest)
			if err != nil || restored.Status != recovery.Recovered {
				t.Fatalf("offline recovery: %v\n%s", err, out)
			}
			assertOriginal()
		})
	}
	t.Run("concurrent_writer_blocks_restore_and_reload", func(t *testing.T) {
		op := prepare(changed)
		_, out, err := run("service_signaled", "apply", op.ID, "--confirm", op.Digest)
		assertRecoveryFaultExit(t, err, out)
		concurrent := strings.Replace(original, "original", "concurrent", 1)
		copyText(svc.ConfigPath, []byte(concurrent))
		if _, out, err := run("", "recover", op.ID, "--confirm", op.Digest); err == nil {
			t.Fatalf("overwrote conflicting writer: %s", out)
		}
		persisted, out, err := run("", "inspect", op.ID)
		if err != nil || persisted.Status != recovery.RecoveryFailed {
			t.Fatalf("missing failure evidence: %v\n%s", err, out)
		}
		if got := must("exec", container, "cat", svc.ConfigPath); string(got) != concurrent {
			t.Fatal("concurrent config changed")
		}
		// Explicit fixture/operator conflict resolution, then deterministic
		// recovery of the original operation. No automatic forced overwrite.
		copyText(svc.ConfigPath, []byte(changed))
		if _, out, err := run("", "recover", op.ID, "--confirm", op.Digest); err != nil {
			t.Fatalf("resolved conflict recovery: %v\n%s", err, out)
		}
		assertOriginal()
	})
	t.Run("dead_master_never_reports_service_recovered", func(t *testing.T) {
		op := prepare(changed)
		if _, out, err := run("", "apply", op.ID, "--confirm", op.Digest); err != nil {
			t.Fatalf("apply: %v\n%s", err, out)
		}
		must("exec", container, "kill", "-QUIT", strconv.Itoa(op.Plan.Service.Master.PID))
		_, out, err := run("", "recover", op.ID, "--confirm", op.Digest)
		if err == nil {
			t.Fatalf("dead master recovery reported success: %s", out)
		}
		persisted, out, err := run("", "inspect", op.ID)
		if err != nil || persisted.Status != recovery.RecoveryFailed {
			t.Fatalf("missing failed service recovery: %v\n%s", err, out)
		}
		if got := must("exec", container, "cat", svc.ConfigPath); string(got) != original {
			t.Fatal("file restoration did not complete")
		}
	})
}

func assertRecoveryFaultExit(t *testing.T, err error, out []byte) {
	t.Helper()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 86 {
		t.Fatalf("expected instrumented process death (exit 86), got %v\n%s", err, out)
	}
}
