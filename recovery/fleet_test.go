package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/state"
)

type fleetFixture struct {
	controller *Engine
	targets    []*Engine
	paths      []string
	refs       []FleetReference
	calls      []string
	loseHost   string
	failRepair bool
}

func makeFleet(t *testing.T, n int) *fleetFixture {
	t.Helper()
	c, _ := fixture(t)
	f := &fleetFixture{controller: c}
	dir := filepath.Dir(c.store.Path())
	bin := filepath.Join(dir, "ssh-fixture")
	if err := os.WriteFile(bin, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		e, root := fixture(t)
		e.target = sum([]byte(fmt.Sprintf("synthetic-target-%d", i)))
		p := filepath.Join(root, "config")
		put(t, p, "original")
		op := prepare(t, e, Change{Action: "replace", Path: p, Content: "changed"})
		key, known := filepath.Join(dir, fmt.Sprintf("key%d", i)), filepath.Join(dir, fmt.Sprintf("known%d", i))
		for _, file := range []string{key, known} {
			if err := os.WriteFile(file, []byte("private fixture"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		host := FleetHost{Name: fmt.Sprintf("host%d", i), Address: "127.0.0.1", Port: 22, User: "root", ExpectedTarget: e.target, SSHBinary: bin, IdentityFile: key, KnownHostsFile: known, Executor: "/cvkeharness", StatePath: "/state/state.db", TimeoutSeconds: 5}
		c.fleet.Hosts = append(c.fleet.Hosts, host)
		f.targets = append(f.targets, e)
		f.paths = append(f.paths, p)
		f.refs = append(f.refs, FleetReference{Host: host.Name, ID: op.ID, Digest: op.Digest})
	}
	c.fleetCall = func(ctx context.Context, h FleetHostPlan, action, id, digest string) (Operation, error) {
		f.calls = append(f.calls, h.Config.Name+":"+action)
		var target *Engine
		for _, e := range f.targets {
			if e.target == h.Config.ExpectedTarget {
				target = e
			}
		}
		if target == nil {
			return Operation{}, fmt.Errorf("wrong target")
		}
		switch action {
		case "inspect":
			return target.Inspect(ctx, id)
		case "apply":
			op, err := target.Apply(ctx, id, digest)
			if h.Config.Name == f.loseHost && err == nil {
				return Operation{}, errors.New("lost acknowledgement after apply")
			}
			return op, err
		case "recover":
			if f.failRepair {
				return Operation{}, errors.New("repair transport failed")
			}
			return target.Recover(ctx, id, digest)
		}
		return Operation{}, errors.New("unexpected transport action")
	}
	return f
}
func (f *fleetFixture) prepare(t *testing.T) FleetBatch {
	t.Helper()
	b, err := f.controller.PrepareBatch(context.Background(), f.refs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFleetLostAckStopsLaterHostAndOfflineRecovery(t *testing.T) {
	f := makeFleet(t, 2)
	b := f.prepare(t)
	ctx := context.Background()
	f.loseHost = "host0"
	b, err := f.controller.ApplyBatch(ctx, b.ID, b.Digest)
	if err == nil || b.Status != Unknown {
		t.Fatalf("%s %v", b.Status, err)
	}
	content(t, f.paths[0], "changed")
	content(t, f.paths[1], "original")
	if b.Outcomes[1].Dispatched {
		t.Fatal("later host dispatched")
	}
	if _, err = f.controller.ApplyBatch(ctx, b.ID, b.Digest); err == nil {
		t.Fatal("uncertain apply retried")
	}
	// Reload the persisted controller without model/fleet config; saved identity
	// bindings and exact target references still support inspection/restoration.
	e, err := New(f.controller.store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.fleetCall = f.controller.fleetCall
	b, err = e.ReconcileBatch(ctx, b.ID)
	if err != nil || b.Status != FleetStopped || b.Outcomes[0].State != Committed {
		t.Fatalf("reconcile %s %v", b.Status, err)
	}
	b, err = e.RecoverBatch(ctx, b.ID, b.Digest)
	if err != nil || b.Status != Recovered {
		t.Fatalf("recover %s %v", b.Status, err)
	}
	content(t, f.paths[0], "original")
	content(t, f.paths[1], "original")
	if b.Outcomes[0].Repairs != 1 || b.Outcomes[1].Repairs != 0 {
		t.Fatal(b.Outcomes)
	}
	if _, err = e.RecoverBatch(ctx, b.ID, b.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = f.controller.PrepareBatch(ctx, f.refs); err == nil {
		t.Fatal("claimed target references reused")
	}
}

func TestFleetRepairBudgetDurableAndNoRewrap(t *testing.T) {
	f := makeFleet(t, 1)
	b := f.prepare(t)
	ctx := context.Background()
	b, err := f.controller.ApplyBatch(ctx, b.ID, b.Digest)
	if err != nil {
		t.Fatal(err)
	}
	f.failRepair = true
	for i := 1; i <= 2; i++ {
		b, err = f.controller.RecoverBatch(ctx, b.ID, b.Digest)
		if err == nil || b.Outcomes[0].Repairs != i {
			t.Fatalf("attempt %d %v %v", i, b.Outcomes, err)
		}
	}
	before := len(f.calls)
	b, err = f.controller.RecoverBatch(ctx, b.ID, b.Digest)
	if err == nil || !strings.Contains(err.Error(), "repair-attempt limit") || b.Status != RecoveryFailed {
		t.Fatalf("%v %s", err, b.Status)
	}
	for _, call := range f.calls[before:] {
		if strings.HasSuffix(call, ":recover") {
			t.Fatal("budget exceeded")
		}
	}
	// A different controller engine sharing the durable store cannot reset count.
	e, err := New(f.controller.store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.fleetCall = f.controller.fleetCall
	if _, err = e.RecoverBatch(ctx, b.ID, b.Digest); err == nil || !strings.Contains(err.Error(), "repair-attempt limit") {
		t.Fatal(err)
	}
	content(t, f.paths[0], "changed")
}

func TestFleetDispatchCrashStages(t *testing.T) {
	for _, stage := range []string{"fleet_dispatched", "fleet_responded", "fleet_recorded", "fleet_repair_dispatched", "fleet_repair_responded"} {
		t.Run(stage, func(t *testing.T) {
			f := makeFleet(t, 2)
			b := f.prepare(t)
			ctx := context.Background()
			crash := errors.New("process died")
			f.controller.fault = func(s string, i int) error {
				if s == stage {
					return crash
				}
				return nil
			}
			b, err := f.controller.ApplyBatch(ctx, b.ID, b.Digest)
			if strings.Contains(stage, "repair") {
				if err != nil {
					t.Fatal(err)
				}
				b, err = f.controller.RecoverBatch(ctx, b.ID, b.Digest)
			}
			if !errors.Is(err, crash) {
				t.Fatal(err)
			}
			persisted, err := f.controller.InspectBatch(ctx, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stage, "repair") {
				if persisted.Outcomes[1].Repairs != 1 {
					t.Fatal(persisted.Outcomes)
				}
			} else if !persisted.Outcomes[0].Dispatched || persisted.Outcomes[1].Dispatched {
				t.Fatal(persisted.Outcomes)
			}
			f.controller.fault = nil
			if _, err = f.controller.ApplyBatch(ctx, b.ID, b.Digest); err == nil {
				t.Fatal("crashed dispatch was replayed")
			}
			b, err = f.controller.ReconcileBatch(ctx, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "fleet_dispatched" {
				if b.Outcomes[0].State != Unknown {
					t.Fatal("ready inspection wrongly proved no in-flight command")
				}
				content(t, f.paths[0], "original")
				b, err = f.controller.RecoverBatch(ctx, b.ID, b.Digest)
				if err != nil || b.Status != Recovered {
					t.Fatalf("pre-dispatch cancellation: %s %v", b.Status, err)
				}
				if _, err = f.targets[0].Apply(ctx, f.refs[0].ID, f.refs[0].Digest); err == nil {
					t.Fatal("late apply ran after cancellation")
				}
			} else {
				b, err = f.controller.RecoverBatch(ctx, b.ID, b.Digest)
				if err != nil || b.Status != Recovered {
					t.Fatalf("%s %v", b.Status, err)
				}
				content(t, f.paths[0], "original")
				content(t, f.paths[1], "original")
			}
		})
	}
}

func TestFleetAggregateBoundsAndTargetClaims(t *testing.T) {
	for _, kind := range []string{"hosts", "files", "bytes", "duplicate", "identity", "digest"} {
		t.Run(kind, func(t *testing.T) {
			f := makeFleet(t, 2)
			switch kind {
			case "hosts":
				f.controller.fleet.Limits.MaxHosts = 1
			case "files":
				f.controller.fleet.Limits.MaxFiles = 1
			case "bytes":
				f.controller.fleet.Limits.MaxBytes = 20
			case "duplicate":
				f.refs[1] = f.refs[0]
			case "identity":
				f.controller.fleet.Hosts[0].ExpectedTarget = sum([]byte("wrong"))
			case "digest":
				f.refs[0].Digest = strings.Repeat("0", 64)
			}
			if _, err := f.controller.PrepareBatch(context.Background(), f.refs); err == nil {
				t.Fatal("invalid batch accepted")
			}
			for _, p := range f.paths {
				content(t, p, "original")
			}
		})
	}
	f := makeFleet(t, 1)
	b := f.prepare(t)
	if _, err := f.controller.PrepareBatch(context.Background(), f.refs); err == nil || !strings.Contains(err.Error(), "already belongs") {
		t.Fatalf("claim not enforced: %v", err)
	}
	// A denied duplicate preparation must leave the original batch usable.
	if _, err := f.controller.ApplyBatch(context.Background(), b.ID, b.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestFleetCurrentPolicyAndTransportBinding(t *testing.T) {
	for _, change := range []string{"policy", "transport", "budget", "digest"} {
		t.Run(change, func(t *testing.T) {
			f := makeFleet(t, 1)
			b := f.prepare(t)
			digest := b.Digest
			switch change {
			case "policy":
				f.controller.policy = "new"
			case "transport":
				f.controller.fleet.Hosts[0].Port++
			case "budget":
				f.controller.fleet.Limits.MaxBytes = 1
			case "digest":
				digest = strings.Repeat("0", 64)
			}
			if _, err := f.controller.ApplyBatch(context.Background(), b.ID, digest); err == nil {
				t.Fatal("changed authorization applied")
			}
			content(t, f.paths[0], "original")
		})
	}
}

func TestFleetSSHFixedArgumentsAndAssetBinding(t *testing.T) {
	f := makeFleet(t, 1)
	h := f.controller.fleet.Hosts[0]
	args, err := fleetSSHArguments(h, "apply", f.refs[0].ID, f.refs[0].Digest)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, required := range []string{"-F none", "StrictHostKeyChecking=yes", "IdentityAgent=none", "ControlPath=none", "CertificateFile=none", "--expect-target " + h.ExpectedTarget} {
		if !strings.Contains(joined, required) {
			t.Fatal(required)
		}
	}
	h.Executor = "/bin/sh -c exploit"
	if _, err = fleetSSHArguments(h, "apply", f.refs[0].ID, f.refs[0].Digest); err == nil {
		t.Fatal("remote shell injection accepted")
	}
	h = f.controller.fleet.Hosts[0]
	p, err := captureFleetHost(h)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(h.KnownHostsFile, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = callFleetSSH(context.Background(), p, "apply", f.refs[0].ID, f.refs[0].Digest); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal(err)
	}
	if err = os.Remove(h.IdentityFile); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(h.KnownHostsFile, h.IdentityFile); err != nil {
		t.Fatal(err)
	}
	if _, err = captureFleetHost(h); err == nil {
		t.Fatal("symlink identity accepted")
	}
}

func TestFleetStoreRejectsImmutableRewrite(t *testing.T) {
	f := makeFleet(t, 1)
	b := f.prepare(t)
	b.Plan.Impact.Files++
	if err := f.controller.saveBatch(context.Background(), &b, Applying, "", nil); err == nil {
		t.Fatal("immutable plan rewritten")
	}
	r, err := f.controller.store.GetRecoveryBatch(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := r
	r.Status = Applying
	if err = f.controller.store.SaveRecoveryBatch(context.Background(), &r, nil); err != nil {
		t.Fatal(err)
	}
	if err = f.controller.store.SaveRecoveryBatch(context.Background(), &stale, []state.RecoveryBatchClaim{}); err == nil {
		t.Fatal("stale state update accepted")
	}
}

func TestFleetServiceCountAndCheckedByteTotals(t *testing.T) {
	entries := []FleetEntry{{Operation: Operation{RecoveryOperation: state.RecoveryOperation{Target: "one"}, Plan: Manifest{Service: &ServicePlan{}, Entries: []Entry{{Before: Fingerprint{Bytes: 8}, After: Fingerprint{Bytes: 7}}}}}}, {Operation: Operation{RecoveryOperation: state.RecoveryOperation{Target: "two"}, Plan: Manifest{Service: &ServicePlan{}, Entries: []Entry{{Before: Fingerprint{Bytes: 8}, After: Fingerprint{Bytes: 7}}}}}}}
	i, err := measureFleet(entries)
	if err != nil || i.Services != 2 || i.Bytes != 30 {
		t.Fatalf("%+v %v", i, err)
	}
	l := DefaultFleetLimits()
	l.MaxServices = 1
	if i.within(l) {
		t.Fatal("service budget exceeded")
	}
	entries[0].Operation.Plan.Entries[0].Before.Bytes = 1 << 62
	if _, err = measureFleet(entries); err == nil {
		t.Fatal("overflowing target impact accepted")
	}
}
