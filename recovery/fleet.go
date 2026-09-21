package recovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

// Fleet deliberately supports one pre-prepared file or NGINX operation per
// distinct target, serial dispatch, and explicit reverse-order recovery. SSH
// listener changes and snapshots have separate lifecycle/coverage and are not
// silently coerced into this workflow.
type FleetLimits struct {
	MaxHosts          int   `json:"max_hosts" yaml:"max_hosts"`
	MaxFiles          int   `json:"max_files" yaml:"max_files"`
	MaxBytes          int64 `json:"max_bytes" yaml:"max_bytes"`
	MaxServices       int   `json:"max_services" yaml:"max_services"`
	MaxRepairAttempts int   `json:"max_repair_attempts" yaml:"max_repair_attempts"`
	MaxAgeSeconds     int64 `json:"max_age_seconds" yaml:"max_age_seconds"`
}

func DefaultFleetLimits() FleetLimits { return FleetLimits{4, 100, 32 << 20, 2, 2, 900} }
func (l FleetLimits) validate() error {
	if l.MaxHosts < 1 || l.MaxHosts > 32 || l.MaxFiles < 1 || l.MaxFiles > 10000 || l.MaxBytes < 1 || l.MaxBytes > 1<<30 || l.MaxServices < 0 || l.MaxServices > 32 || l.MaxRepairAttempts < 1 || l.MaxRepairAttempts > 3 || l.MaxAgeSeconds < 1 || l.MaxAgeSeconds > 86400 {
		return fmt.Errorf("invalid fleet limits (maximum 32 hosts, 10000 files, 1 GiB, 3 repairs per target, 24 hours)")
	}
	return nil
}

type FleetHost struct {
	Name           string `json:"name" yaml:"name"`
	Address        string `json:"address" yaml:"address"`
	Port           int    `json:"port" yaml:"port"`
	User           string `json:"user" yaml:"user"`
	ExpectedTarget string `json:"expected_target" yaml:"expected_target"`
	SSHBinary      string `json:"ssh_binary" yaml:"ssh_binary"`
	IdentityFile   string `json:"identity_file" yaml:"identity_file"`
	KnownHostsFile string `json:"known_hosts_file" yaml:"known_hosts_file"`
	Executor       string `json:"executor" yaml:"executor"`
	StatePath      string `json:"state_path" yaml:"state_path"`
	TimeoutSeconds int    `json:"timeout_seconds" yaml:"timeout_seconds"`
}
type FleetConfig struct {
	Hosts  []FleetHost `json:"hosts" yaml:"hosts"`
	Limits FleetLimits `json:"limits" yaml:"limits"`
}

func hexID(s string, n int) bool { return len(s) == n && strings.Trim(s, "0123456789abcdef") == "" }
func plainName(s string) bool {
	return len(s) > 0 && len(s) <= 64 && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == "" && s[0] != '-'
}
func fleetPath(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && len(s) <= 1024 && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./-") == ""
}
func (f FleetConfig) validate(base string) error {
	if err := f.Limits.validate(); err != nil {
		return err
	}
	if len(f.Hosts) > 32 {
		return fmt.Errorf("at most 32 fleet hosts may be configured")
	}
	names, targets := map[string]bool{}, map[string]bool{}
	for _, h := range f.Hosts {
		if !plainName(h.Name) || !plainName(h.User) || net.ParseIP(h.Address) == nil || h.Port < 1 || h.Port > 65535 || !hexID(h.ExpectedTarget, 64) || h.TimeoutSeconds < 1 || h.TimeoutSeconds > 120 || names[h.Name] || targets[h.ExpectedTarget] {
			return fmt.Errorf("invalid or duplicate fleet host; literal IP and unique expected executor identity required")
		}
		names[h.Name], targets[h.ExpectedTarget] = true, true
		for _, p := range []string{h.SSHBinary, h.IdentityFile, h.KnownHostsFile, h.Executor, h.StatePath} {
			if !fleetPath(p) {
				return fmt.Errorf("fleet paths must be clean absolute paths with simple literal characters")
			}
		}
		// The controller's recovery root protection also protects these credentials
		// from typed file, service and snapshot workflows.
		if !within(h.IdentityFile, base) || !within(h.KnownHostsFile, base) || h.IdentityFile == h.KnownHostsFile {
			return fmt.Errorf("fleet identity and host-key files must be distinct and inside the protected state directory")
		}
	}
	return nil
}
func (e *Engine) FleetHosts() []FleetHost { return append([]FleetHost(nil), e.fleet.Hosts...) }

type FleetHostPlan struct {
	Config     FleetHost   `json:"config"`
	SSHBinary  Fingerprint `json:"ssh_binary"`
	Identity   Fingerprint `json:"identity"`
	KnownHosts Fingerprint `json:"known_hosts"`
}
type FleetReference struct {
	Host   string `json:"host"`
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type FleetEntry struct {
	Host      FleetHostPlan `json:"host"`
	Operation Operation     `json:"operation"`
}
type FleetImpact struct {
	Hosts    int   `json:"hosts"`
	Files    int   `json:"files"`
	Bytes    int64 `json:"bytes"` // original plus candidate logical bytes; no deletion credit
	Services int   `json:"services"`
}
type FleetPlan struct {
	Version   int          `json:"version"`
	CreatedAt time.Time    `json:"created_at"`
	Limits    FleetLimits  `json:"limits"`
	Impact    FleetImpact  `json:"impact"`
	Entries   []FleetEntry `json:"entries"`
}
type FleetOutcome struct {
	Dispatched       bool   `json:"dispatched"`
	State            string `json:"state"`
	Repairs          int    `json:"repairs"`
	ObservedRevision int64  `json:"observed_revision,omitempty"`
}
type FleetBatch struct {
	state.RecoveryBatch
	Plan     FleetPlan      `json:"plan"`
	Outcomes []FleetOutcome `json:"outcomes"`
}

const FleetStopped = "rollout_stopped"

func batchDigest(target, policy string, p FleetPlan) string {
	b, _ := json.Marshal(struct {
		Target, Policy string
		Plan           FleetPlan
	}{target, policy, p})
	return sum(b)
}
func (e *Engine) saveBatch(ctx context.Context, b *FleetBatch, status, problem string, claims []state.RecoveryBatchClaim) error {
	b.Status, b.Problem = status, problem
	var err error
	b.Manifest, err = json.Marshal(b.Plan)
	if err != nil {
		return err
	}
	b.Progress, err = json.Marshal(b.Outcomes)
	if err != nil {
		return err
	}
	return e.store.SaveRecoveryBatch(ctx, &b.RecoveryBatch, claims)
}
func (e *Engine) InspectBatch(ctx context.Context, id string) (FleetBatch, error) {
	r, err := e.store.GetRecoveryBatch(ctx, id)
	if err != nil {
		return FleetBatch{}, err
	}
	b := FleetBatch{RecoveryBatch: r}
	if err = json.Unmarshal(r.Manifest, &b.Plan); err != nil {
		return b, err
	}
	if err = json.Unmarshal(r.Progress, &b.Outcomes); err != nil {
		return b, err
	}
	if !hexID(id, 32) || b.Target != e.target || b.Plan.Version != 1 || batchDigest(b.Target, b.Policy, b.Plan) != b.Digest || len(b.Outcomes) != len(b.Plan.Entries) || len(b.Outcomes) < 1 || len(b.Outcomes) > 32 {
		return b, fmt.Errorf("invalid, altered or foreign-controller batch")
	}
	for _, o := range b.Outcomes {
		if o.Repairs < 0 || o.Repairs > b.Plan.Limits.MaxRepairAttempts {
			return b, fmt.Errorf("invalid repair evidence")
		}
	}
	return b, nil
}
func (e *Engine) ListBatches(ctx context.Context) ([]state.RecoveryBatch, error) {
	return e.store.ListRecoveryBatches(ctx)
}
func (e *Engine) callFleet(ctx context.Context, h FleetHostPlan, action, id, digest string) (Operation, error) {
	if e.fleetCall != nil {
		return e.fleetCall(ctx, h, action, id, digest)
	}
	return callFleetSSH(ctx, h, action, id, digest)
}
func validateFleetOperation(op Operation, ref FleetReference, target string) error {
	if !hexID(ref.ID, 32) || !hexID(ref.Digest, 64) || op.ID != ref.ID || op.Digest != ref.Digest || op.Target != target || op.Plan.Version != 1 || planDigest(op.Target, op.Policy, op.Plan) != op.Digest {
		return fmt.Errorf("remote operation identity or digest mismatch")
	}
	if op.Plan.SSH != nil || op.Plan.Snapshot != nil || len(op.Plan.Entries) < 1 || len(op.Plan.Entries) > 10000 {
		return fmt.Errorf("fleet supports bounded regular-file and NGINX plans only")
	}
	return nil
}
func measureFleet(entries []FleetEntry) (FleetImpact, error) {
	impact := FleetImpact{Hosts: len(entries)}
	seen := map[string]bool{}
	for _, en := range entries {
		if seen[en.Operation.Target] {
			return impact, fmt.Errorf("a batch may affect each machine/principal only once")
		}
		seen[en.Operation.Target] = true
		impact.Files += len(en.Operation.Plan.Entries)
		if en.Operation.Plan.Service != nil {
			impact.Services++
		}
		for _, file := range en.Operation.Plan.Entries {
			for _, n := range []int64{file.Before.Bytes, file.After.Bytes} {
				if n < 0 || n > 1<<30 || impact.Bytes > 1<<30-n {
					return impact, fmt.Errorf("aggregate byte impact exceeds 1 GiB")
				}
				impact.Bytes += n
			}
		}
	}
	return impact, nil
}
func (i FleetImpact) within(l FleetLimits) bool {
	return i.Hosts <= l.MaxHosts && i.Files <= l.MaxFiles && i.Bytes <= l.MaxBytes && i.Services <= l.MaxServices
}
func (e *Engine) PrepareBatch(ctx context.Context, refs []FleetReference) (FleetBatch, error) {
	unlock, err := e.locked()
	if err != nil {
		return FleetBatch{}, err
	}
	defer unlock()
	if err = e.noArmedSSH(ctx); err != nil {
		return FleetBatch{}, err
	}
	if len(refs) < 1 || len(refs) > e.fleet.Limits.MaxHosts {
		return FleetBatch{}, fmt.Errorf("batch exceeds configured host limit")
	}
	b := FleetBatch{RecoveryBatch: state.RecoveryBatch{Target: e.target, Policy: e.policy}, Plan: FleetPlan{Version: 1, CreatedAt: time.Now().UTC(), Limits: e.fleet.Limits}}
	var claims []state.RecoveryBatchClaim
	for _, ref := range refs {
		var host FleetHost
		for _, h := range e.fleet.Hosts {
			if h.Name == ref.Host {
				host = h
				break
			}
		}
		if host.Name == "" || host.ExpectedTarget == e.target {
			return b, fmt.Errorf("fleet host must be operator-configured and different from controller")
		}
		h, err := captureFleetHost(host)
		if err != nil {
			return b, err
		}
		op, err := e.callFleet(ctx, h, "inspect", ref.ID, ref.Digest)
		if err != nil {
			return b, err
		}
		if err = validateFleetOperation(op, ref, host.ExpectedTarget); err != nil {
			return b, err
		}
		if op.Status != Ready {
			return b, fmt.Errorf("remote operation must be ready")
		}
		// Avoid retaining duplicate encoded manifests and volatile measurement history.
		op.Manifest = nil
		op.Checks = nil
		b.Plan.Entries = append(b.Plan.Entries, FleetEntry{h, op})
		b.Outcomes = append(b.Outcomes, FleetOutcome{State: Ready})
		claims = append(claims, state.RecoveryBatchClaim{Target: op.Target, OperationID: op.ID})
	}
	b.Plan.Impact, err = measureFleet(b.Plan.Entries)
	if err != nil {
		return b, err
	}
	if !b.Plan.Impact.within(e.fleet.Limits) {
		return b, fmt.Errorf("aggregate file, byte or service impact exceeds operator limits")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return b, err
	}
	b.ID = hex.EncodeToString(random[:])
	b.Digest = batchDigest(b.Target, b.Policy, b.Plan)
	err = e.saveBatch(ctx, &b, Ready, "", claims)
	return b, err
}
func (e *Engine) currentFleetPlan(b FleetBatch) error {
	if b.Policy != e.policy {
		return fmt.Errorf("controller policy changed; application refused")
	}
	if !b.Plan.Impact.within(e.fleet.Limits) {
		return fmt.Errorf("batch exceeds current operator impact limits")
	}
	if time.Since(b.Plan.CreatedAt) > time.Duration(min(b.Plan.Limits.MaxAgeSeconds, e.fleet.Limits.MaxAgeSeconds))*time.Second || b.Plan.CreatedAt.After(time.Now().Add(time.Second)) {
		return fmt.Errorf("batch approval manifest expired")
	}
	for _, entry := range b.Plan.Entries {
		found := false
		for _, h := range e.fleet.Hosts {
			if h == entry.Host.Config {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("fleet transport configuration changed; application refused")
		}
	}
	return nil
}
func (e *Engine) inspectFleetEntry(ctx context.Context, en FleetEntry) (Operation, error) {
	op, err := e.callFleet(ctx, en.Host, "inspect", en.Operation.ID, en.Operation.Digest)
	if err != nil {
		return op, err
	}
	err = validateFleetOperation(op, FleetReference{ID: en.Operation.ID, Digest: en.Operation.Digest}, en.Operation.Target)
	return op, err
}
func (e *Engine) ApplyBatch(ctx context.Context, id, digest string) (FleetBatch, error) {
	unlock, err := e.locked()
	if err != nil {
		return FleetBatch{}, err
	}
	defer unlock()
	b, err := e.InspectBatch(ctx, id)
	if err != nil {
		return b, err
	}
	if digest != b.Digest {
		return b, fmt.Errorf("exact batch digest required")
	}
	if b.Status != Ready {
		return b, fmt.Errorf("a dispatched batch cannot be applied again; inspect/reconcile/recover")
	}
	if err = e.noArmedSSH(ctx); err != nil {
		return b, err
	}
	if err = e.currentFleetPlan(b); err != nil {
		return b, err
	}
	// Inspect every target before the first dispatch, then re-inspect each one
	// immediately before its dispatch. The target executor enforces live file,
	// service, policy, age and capacity preconditions under its own local lock.
	for _, en := range b.Plan.Entries {
		op, err := e.inspectFleetEntry(ctx, en)
		if err != nil {
			return b, err
		}
		if op.Status != Ready {
			return b, fmt.Errorf("a target operation is no longer ready")
		}
	}
	for i, en := range b.Plan.Entries {
		if err = ctx.Err(); err == nil {
			var op Operation
			op, err = e.inspectFleetEntry(ctx, en)
			if err == nil && op.Status != Ready {
				err = fmt.Errorf("target is no longer ready")
			}
		}
		if err != nil {
			return b, errors.Join(err, e.saveBatch(context.WithoutCancel(ctx), &b, FleetStopped, "pre-dispatch inspection failed; rollout stopped", nil))
		}
		b.Outcomes[i].Dispatched = true
		b.Outcomes[i].State = Unknown
		if err = e.saveBatch(ctx, &b, Applying, "", nil); err != nil {
			return b, err
		}
		if err = e.checkpoint("fleet_dispatched", i); err != nil {
			return b, err
		}
		op, callErr := e.callFleet(ctx, en.Host, "apply", en.Operation.ID, en.Operation.Digest)
		if err = e.checkpoint("fleet_responded", i); err != nil {
			return b, err
		}
		if callErr == nil {
			callErr = validateFleetOperation(op, FleetReference{ID: en.Operation.ID, Digest: en.Operation.Digest}, en.Operation.Target)
		}
		if callErr != nil {
			return b, errors.Join(callErr, e.saveBatch(context.WithoutCancel(ctx), &b, Unknown, "dispatched target outcome is unknown; rollout stopped; inspect before recovery", nil))
		}
		b.Outcomes[i].State, b.Outcomes[i].ObservedRevision = op.Status, op.Revision
		if op.Status != Committed {
			return b, errors.Join(fmt.Errorf("target did not commit"), e.saveBatch(context.WithoutCancel(ctx), &b, FleetStopped, "target did not commit; later hosts untouched", nil))
		}
		if err = e.saveBatch(ctx, &b, Applying, "", nil); err != nil {
			return b, err
		}
		if err = e.checkpoint("fleet_recorded", i); err != nil {
			return b, err
		}
	}
	err = e.saveBatch(ctx, &b, Committed, "", nil)
	return b, err
}

// ReconcileBatch only inspects. It never retries apply and never resumes a
// stopped rollout. A fresh target CLI read cannot prove an in-flight command
// will never finish, so Ready after dispatch is still an unknown outcome.
func (e *Engine) ReconcileBatch(ctx context.Context, id string) (FleetBatch, error) {
	unlock, err := e.locked()
	if err != nil {
		return FleetBatch{}, err
	}
	defer unlock()
	b, err := e.InspectBatch(ctx, id)
	if err != nil {
		return b, err
	}
	if b.Status == Ready {
		return b, nil
	}
	allCommitted, allRecovered := true, true
	var problems []error
	for i, en := range b.Plan.Entries {
		if !b.Outcomes[i].Dispatched {
			allCommitted = false
			continue
		}
		op, err := e.inspectFleetEntry(ctx, en)
		if err != nil {
			problems = append(problems, err)
			b.Outcomes[i].State = Unknown
		} else {
			b.Outcomes[i].State, b.Outcomes[i].ObservedRevision = op.Status, op.Revision
			if op.Status == Ready {
				b.Outcomes[i].State = Unknown
			}
		}
		allCommitted = allCommitted && b.Outcomes[i].State == Committed
		allRecovered = allRecovered && b.Outcomes[i].State == Recovered
	}
	status, problem := FleetStopped, "inspected partial rollout; apply will not resume; explicit recovery remains available"
	if allCommitted {
		status, problem = Committed, ""
	} else if allRecovered {
		status, problem = Recovered, ""
	}
	if len(problems) > 0 {
		status, problem = Unknown, "one or more target outcomes could not be inspected"
	}
	err = e.saveBatch(ctx, &b, status, problem, nil)
	return b, errors.Join(append(problems, err)...)
}
func (e *Engine) RecoverBatch(ctx context.Context, id, digest string) (FleetBatch, error) {
	unlock, err := e.locked()
	if err != nil {
		return FleetBatch{}, err
	}
	defer unlock()
	b, err := e.InspectBatch(ctx, id)
	if err != nil {
		return b, err
	}
	if digest != b.Digest {
		return b, fmt.Errorf("exact batch digest required")
	}
	if b.Status == Ready {
		return b, fmt.Errorf("batch has not dispatched; nothing to restore")
	}
	if b.Status == Recovered {
		return b, nil
	}
	if err = e.noArmedSSH(ctx); err != nil {
		return b, err
	}
	// Recovery keeps the saved transport/identity and does not require model
	// configuration or a fresh application TTL. Current limits can only lower
	// the persisted repair ceiling. Read-only inspection never consumes repairs.
	for i := len(b.Plan.Entries) - 1; i >= 0; i-- {
		if !b.Outcomes[i].Dispatched {
			continue
		}
		en := b.Plan.Entries[i]
		op, err := e.inspectFleetEntry(ctx, en)
		if err != nil {
			return b, errors.Join(err, e.saveBatch(context.WithoutCancel(ctx), &b, Unknown, "recovery inspection unavailable; no retry was dispatched", nil))
		}
		if op.Status == Recovered {
			b.Outcomes[i].State = Recovered
			b.Outcomes[i].ObservedRevision = op.Revision
			continue
		}
		if b.Outcomes[i].Repairs >= min(b.Plan.Limits.MaxRepairAttempts, e.fleet.Limits.MaxRepairAttempts) {
			return b, errors.Join(fmt.Errorf("target repair-attempt limit reached; use the target-side operator recovery CLI after inspection"), e.saveBatch(ctx, &b, RecoveryFailed, "repair budget exhausted; automatic/controller repair stopped", nil))
		}
		b.Outcomes[i].Repairs++
		b.Outcomes[i].State = Unknown
		if err = e.saveBatch(ctx, &b, RollingBack, "", nil); err != nil {
			return b, err
		}
		if err = e.checkpoint("fleet_repair_dispatched", i); err != nil {
			return b, err
		}
		op, err = e.callFleet(ctx, en.Host, "recover", en.Operation.ID, en.Operation.Digest)
		if faultErr := e.checkpoint("fleet_repair_responded", i); faultErr != nil {
			return b, faultErr
		}
		if err == nil {
			err = validateFleetOperation(op, FleetReference{ID: en.Operation.ID, Digest: en.Operation.Digest}, en.Operation.Target)
		}
		if err != nil {
			return b, errors.Join(err, e.saveBatch(context.WithoutCancel(ctx), &b, Unknown, "repair outcome unknown; inspect before another bounded attempt", nil))
		}
		b.Outcomes[i].State, b.Outcomes[i].ObservedRevision = op.Status, op.Revision
		if op.Status != Recovered {
			return b, errors.Join(fmt.Errorf("target recovery did not verify"), e.saveBatch(ctx, &b, RecoveryFailed, "target recovery did not verify; remaining repairs stopped", nil))
		}
		if err = e.saveBatch(ctx, &b, RollingBack, "", nil); err != nil {
			return b, err
		}
	}
	err = e.saveBatch(ctx, &b, Recovered, "", nil)
	return b, err
}
