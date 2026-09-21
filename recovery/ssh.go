package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func (e *Engine) LoadSSHServiceSpec(path string) (SSHService, error) {
	var s SSHService
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !within(path, filepath.Dir(e.assets)) {
		return s, fmt.Errorf("SSH startup specification must be inside the protected state directory")
	}
	f, b, err := sshFile(path, 64<<10)
	if err != nil {
		return s, err
	}
	if f.Mode&0077 != 0 {
		return s, fmt.Errorf("SSH startup specification must be private")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&s); err != nil {
		return s, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return s, fmt.Errorf("unexpected trailing SSH specification data")
	}
	return s, s.validate()
}

type SSHInputs struct {
	Binary, Session, HostKey, AuthorizedKeys Fingerprint
}

type SSHGuardIdentity struct {
	Process    ServiceProcess `json:"process"`
	Executable string         `json:"executable"`
	Binary     Fingerprint    `json:"binary"`
}

type SSHLease struct {
	Config      SSHService       `json:"config"`
	Guard       SSHGuardIdentity `json:"guard"`
	Master      ServiceProcess   `json:"master"`
	HeartbeatNS int64            `json:"heartbeat_ns"`
}

type SSHPlan struct {
	Adapter string           `json:"adapter"`
	Config  SSHService       `json:"config"`
	Inputs  SSHInputs        `json:"inputs"`
	Guard   SSHGuardIdentity `json:"guard"`
	Master  ServiceProcess   `json:"master"`
	OldPort int              `json:"old_port"`
	NewPort int              `json:"new_port"`
}

type SSHPending struct {
	ID                string    `json:"id"`
	Digest            string    `json:"digest"`
	Boot              string    `json:"boot"`
	DeadlineNS        int64     `json:"deadline_ns"`
	EstimatedRevertAt time.Time `json:"estimated_revert_at"`
}

type SSHReloadEvidence struct {
	AfterTicks uint64 `json:"after_ticks"`
	Boot       string `json:"boot"`
	Port       int    `json:"port"`
}

func (e *Engine) SSHServices() []SSHService {
	out := make([]SSHService, 0, len(e.sshServices))
	for _, s := range e.sshServices {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sshFile(path string, limit int64) (Fingerprint, []byte, error) {
	d, err := parentFor(path)
	if err != nil {
		return Fingerprint{}, nil, err
	}
	defer d.Close()
	f, b, err := readRegular(d, filepath.Base(path), limit)
	if err != nil {
		return f, nil, err
	}
	if !f.Exists || f.UID != 0 || f.Mode&0022 != 0 {
		return f, nil, fmt.Errorf("managed SSH assets must be regular, root-owned and not group/world writable")
	}
	return f, b, nil
}

func sshInputs(s SSHService) (SSHInputs, error) {
	var out SSHInputs
	var err error
	if out.Binary, _, err = sshFile(s.Binary, 64<<20); err != nil {
		return out, err
	}
	if out.Session, _, err = sshFile(s.SessionBinary, 64<<20); err != nil {
		return out, err
	}
	if out.HostKey, _, err = sshFile(s.HostKeyPath, 1<<20); err != nil {
		return out, err
	}
	if out.AuthorizedKeys, _, err = sshFile(s.AuthorizedKeysPath, 1<<20); err != nil {
		return out, err
	}
	if out.Binary.Mode&0111 == 0 || out.Session.Mode&0111 == 0 || out.HostKey.Mode&0077 != 0 {
		return out, fmt.Errorf("managed SSH executables or private host-key permissions are unsafe")
	}
	return out, nil
}

func verifySSHInputs(p SSHPlan, identity bool) error {
	if p.Adapter != "openssh-port-v1" {
		return fmt.Errorf("unsupported SSH adapter")
	}
	if err := p.Config.validate(); err != nil {
		return err
	}
	got, err := sshInputs(p.Config)
	if err != nil {
		return err
	}
	if !matches(got.Binary, p.Inputs.Binary, identity) || !matches(got.Session, p.Inputs.Session, identity) || !matches(got.HostKey, p.Inputs.HostKey, identity) || !matches(got.AuthorizedKeys, p.Inputs.AuthorizedKeys, identity) {
		return fmt.Errorf("managed SSH executable/authentication assets changed")
	}
	return nil
}

func (e *Engine) sshLease(ctx context.Context, name string, fresh bool) (SSHLease, json.RawMessage, error) {
	b, pending, err := e.store.RecoveryGuard(ctx, e.target, name)
	var lease SSHLease
	if err != nil {
		return lease, nil, fmt.Errorf("target-local SSH supervisor unavailable: %w", err)
	}
	if err = json.Unmarshal(b, &lease); err != nil {
		return lease, pending, err
	}
	if lease.Config.Name != name {
		return lease, pending, fmt.Errorf("SSH supervisor name mismatch")
	}
	if fresh {
		boot, now, err := sshClock()
		if err != nil {
			return lease, pending, err
		}
		if lease.Guard.Process.Boot != boot || now < lease.HeartbeatNS || now-lease.HeartbeatNS > int64(3*time.Second) {
			return lease, pending, fmt.Errorf("target-local SSH watchdog is stale or from an earlier boot")
		}
		if err = verifySSHGuard(lease.Guard); err != nil {
			return lease, pending, err
		}
		if err = verifySSHProcess(lease.Master, lease.Config.Binary); err != nil {
			return lease, pending, err
		}
	}
	return lease, pending, nil
}

func (e *Engine) prepareSSH(ctx context.Context, name string, op Operation, original, candidates [][]byte) (*SSHPlan, error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("managed SSH currently requires a root-owned dedicated instance")
	}
	s, ok := e.sshServices[name]
	if !ok {
		return nil, fmt.Errorf("managed SSH service is not operator-configured")
	}
	if len(op.Plan.Entries) != 1 || op.Plan.Entries[0].Action != "replace" || op.Plan.Entries[0].Path != s.ConfigPath {
		return nil, fmt.Errorf("managed SSH requires exactly one replacement of its standalone configuration")
	}
	oldPort, newPort, err := validateManagedSSHChange(s, original[0], candidates[0])
	if err != nil {
		return nil, err
	}
	lease, pending, err := e.sshLease(ctx, name, true)
	if err != nil {
		return nil, err
	}
	if lease.Config != s || len(pending) != 0 {
		return nil, fmt.Errorf("SSH supervisor configuration differs or an operation is already armed")
	}
	inputs, err := sshInputs(s)
	if err != nil {
		return nil, err
	}
	return &SSHPlan{Adapter: "openssh-port-v1", Config: s, Inputs: inputs, Guard: lease.Guard, Master: lease.Master, OldPort: oldPort, NewPort: newPort}, nil
}

func (e *Engine) ApplySSH(ctx context.Context, id, digest string) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	op, err := e.Inspect(ctx, id)
	if err != nil {
		return op, err
	}
	p := op.Plan.SSH
	if p == nil || op.Plan.Service != nil || op.Plan.Snapshot != nil || digest != op.Digest || op.Policy != e.policy {
		return op, fmt.Errorf("SSH operation kind, digest or policy mismatch")
	}
	if op.Status == Committed {
		return op, nil
	}
	if err = e.noArmedSSH(ctx); err != nil {
		return op, err
	}
	if op.Status != Ready || time.Since(op.Plan.CreatedAt) > time.Duration(op.Plan.Limits.MaxAgeSeconds)*time.Second || e.sshServices[p.Config.Name] != p.Config {
		return op, fmt.Errorf("SSH plan is stale, started or no longer configured")
	}
	if len(op.Plan.Entries) != 1 || len(op.Checks) > 123 {
		return op, fmt.Errorf("SSH file/evidence budget exceeded")
	}
	en := op.Plan.Entries[0]
	if en.Path != p.Config.ConfigPath || en.Action != "replace" {
		return op, fmt.Errorf("SSH manifest scope mismatch")
	}
	if _, err = e.checkPath(en.Path); err != nil {
		return op, err
	}
	if err = e.checkServiceScope(en.Path, "ssh:"+p.Config.Name); err != nil {
		return op, err
	}
	if en.Before.Bytes+en.After.Bytes > e.limits.MaxBytes || max(en.Before.Bytes, en.After.Bytes) > e.limits.MaxFileBytes {
		return op, fmt.Errorf("current SSH byte budget exceeded")
	}
	if _, err = e.readMatching(en, en.Before, true); err != nil {
		return op, err
	}
	if err = e.verifyAssets(op, en); err != nil {
		return op, err
	}
	if err = verifySSHInputs(*p, true); err != nil {
		return op, err
	}
	lease, pending, err := e.sshLease(ctx, p.Config.Name, true)
	if err != nil {
		return op, err
	}
	if lease.Guard != p.Guard || lease.Master != p.Master || lease.Config != p.Config || len(pending) != 0 {
		return op, fmt.Errorf("SSH supervisor changed or already has a pending operation")
	}
	if err = e.checkCapacity(ctx, &op); err != nil {
		return op, err
	}
	if err = sshPortReady(ctx, p.Master, p.Config, p.OldPort); err != nil {
		return op, fmt.Errorf("original SSH listener failed: %w", err)
	}
	if err = validateSSHConfig(ctx, p.Config, filepath.Join(e.assets, op.ID, en.Candidate)); err != nil {
		return op, err
	}
	boot, now, err := sshClock()
	if err != nil {
		return op, err
	}
	window := time.Duration(p.Config.ConfirmationSeconds) * time.Second
	ref := SSHPending{ID: op.ID, Digest: op.Digest, Boot: boot, DeadlineNS: now + int64(window), EstimatedRevertAt: time.Now().UTC().Add(window)}
	raw, _ := json.Marshal(ref)
	// The durable guard reference is installed before either journal arming or
	// target mutation. A crash in between still leaves the watchdog an exact
	// plan to reconcile; the shared executor lock prevents premature handling.
	if err = e.store.ArmRecoveryGuard(ctx, e.target, p.Config.Name, raw); err != nil {
		return op, err
	}
	// Persist the same deadline in inspectable operation evidence. The UTC
	// estimate is for display only; boot-relative time governs every decision.
	deadlineCheck, err := e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "ssh_deadline", raw)
	if err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	op.Checks = append(op.Checks, deadlineCheck)
	if err = e.save(ctx, &op, SSHArmed, ""); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	if err = e.checkpoint("ssh_armed", -1); err != nil {
		return op, err
	}
	if err = e.save(ctx, &op, Applying, ""); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	if err = e.checkpoint("ssh_applying", -1); err != nil {
		return op, err
	}
	if err = e.applyEntry(op, en, 0); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	if err = e.checkpoint("ssh_applied", -1); err != nil {
		return op, err
	}
	if err = validateSSHConfig(ctx, p.Config, p.Config.ConfigPath); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	if err = sshReload(p.Master, p.Config.Binary); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	if err = e.checkpoint("ssh_signaled", -1); err != nil {
		return op, err
	}
	if err = sshPortReady(ctx, p.Master, p.Config, p.NewPort); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	ticks, err := sshTicksNow()
	if err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	evidence, _ := json.Marshal(SSHReloadEvidence{AfterTicks: ticks, Boot: boot, Port: p.NewPort})
	check, err := e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "ssh_reloaded", evidence)
	if err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	op.Checks = append(op.Checks, check)
	if _, err = e.readMatching(en, en.After, false); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	if err = e.checkpoint("ssh_reloaded", -1); err != nil {
		return op, err
	}
	if err = e.save(ctx, &op, AwaitingConfirmation, "new authenticated SSH transport required before target-local deadline"); err != nil {
		return e.sshApplyFailure(ctx, op, err)
	}
	return op, e.checkpoint("ssh_awaiting_confirmation", -1)
}

func (e *Engine) sshApplyFailure(ctx context.Context, op Operation, cause error) (Operation, error) {
	err := e.save(context.WithoutCancel(ctx), &op, Unknown, "SSH application incomplete; target-local watchdog remains armed")
	return op, errors.Join(cause, err)
}

func (e *Engine) ConfirmSSH(ctx context.Context, id, digest string) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	op, err := e.Inspect(ctx, id)
	if err != nil {
		return op, err
	}
	p := op.Plan.SSH
	if p == nil || digest != op.Digest {
		return op, fmt.Errorf("SSH confirmation kind/digest mismatch")
	}
	if op.Status == Committed {
		return op, nil
	}
	if op.Status != AwaitingConfirmation {
		return op, fmt.Errorf("SSH operation is not awaiting confirmation")
	}
	lease, raw, err := e.sshLease(ctx, p.Config.Name, true)
	if err != nil {
		return op, err
	}
	var ref SSHPending
	if json.Unmarshal(raw, &ref) != nil || ref.ID != id || ref.Digest != digest || lease.Guard != p.Guard || lease.Master != p.Master {
		return op, fmt.Errorf("SSH watchdog binding changed")
	}
	boot, now, err := sshClock()
	if err != nil {
		return op, err
	}
	if boot != ref.Boot || now >= ref.DeadlineNS {
		return op, fmt.Errorf("SSH confirmation deadline expired")
	}
	if err = verifySSHInputs(*p, true); err != nil {
		return op, err
	}
	if len(op.Plan.Entries) != 1 {
		return op, fmt.Errorf("invalid SSH manifest")
	}
	if _, err = e.readMatching(op.Plan.Entries[0], op.Plan.Entries[0].After, false); err != nil {
		return op, err
	}
	var reload SSHReloadEvidence
	for _, check := range op.Checks {
		if check.Kind == "ssh_reloaded" {
			if err := json.Unmarshal(check.Evidence, &reload); err != nil {
				return op, err
			}
		}
	}
	if reload.AfterTicks == 0 || reload.Boot != boot || reload.Port != p.NewPort {
		return op, fmt.Errorf("SSH reload evidence unavailable")
	}
	proof, err := newSSHConnectionProof(*p, reload.AfterTicks)
	if err != nil {
		return op, err
	}
	b, _ := json.Marshal(proof)
	check, err := e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "ssh_new_connection", b)
	if err != nil {
		return op, err
	}
	op.Checks = append(op.Checks, check)
	// Recheck the deadline after the proof, before committing the decision.
	_, now, err = sshClock()
	if err != nil || now >= ref.DeadlineNS {
		return op, fmt.Errorf("SSH confirmation deadline expired during verification")
	}
	if err = e.save(ctx, &op, Committed, ""); err != nil {
		return op, err
	}
	if err = e.checkpoint("ssh_confirmed", -1); err != nil {
		return op, err
	}
	return op, e.store.ClearRecoveryGuard(ctx, e.target, p.Config.Name, raw)
}

func (e *Engine) RecoverSSH(ctx context.Context, id, digest string) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	op, err := e.Inspect(ctx, id)
	if err != nil {
		return op, err
	}
	if op.Plan.SSH == nil || op.Digest != digest {
		return op, fmt.Errorf("SSH recovery kind/digest mismatch")
	}
	if op.Status == Recovered {
		return op, nil
	}
	if op.Status == Ready || op.Status == Preparing || op.Status == PreparationFailed {
		return op, fmt.Errorf("SSH operation has not started")
	}
	lease, pending, err := e.sshLease(ctx, op.Plan.SSH.Config.Name, false)
	if err != nil {
		return op, err
	}
	if len(pending) != 0 {
		var ref SSHPending
		if json.Unmarshal(pending, &ref) != nil || ref.ID != op.ID || ref.Digest != op.Digest {
			return op, fmt.Errorf("another SSH operation is pending")
		}
	}
	if lease.Config != op.Plan.SSH.Config {
		return op, fmt.Errorf("SSH supervisor configuration changed")
	}
	if verifySSHProcess(lease.Master, lease.Config.Binary) != nil {
		return e.rollbackSSH(ctx, op, nil)
	}
	return e.rollbackSSH(ctx, op, &lease)
}

// rollbackSSH is also used by the boot path, with no master yet. That path
// leaves RollingBack until the supervisor has started and checked the original
// listener; restored bytes alone never become a recovered running service.
func (e *Engine) rollbackSSH(ctx context.Context, op Operation, lease *SSHLease) (Operation, error) {
	p := op.Plan.SSH
	if p == nil || len(op.Plan.Entries) != 1 {
		return op, fmt.Errorf("invalid SSH restore manifest")
	}
	if err := e.save(ctx, &op, RollingBack, ""); err != nil {
		return op, err
	}
	fail := func(cause error) (Operation, error) {
		err := e.save(context.WithoutCancel(ctx), &op, RecoveryFailed, "SSH restoration failed; operator inspection required")
		return op, errors.Join(cause, err)
	}
	if err := verifySSHInputs(*p, false); err != nil {
		return fail(err)
	}
	if err := e.restoreEntry(op, op.Plan.Entries[0], 0); err != nil {
		return fail(err)
	}
	if err := e.checkpoint("ssh_restored", -1); err != nil {
		return op, err
	}
	if _, err := e.readMatching(op.Plan.Entries[0], op.Plan.Entries[0].Before, false); err != nil {
		return fail(err)
	}
	if err := validateSSHConfig(ctx, p.Config, p.Config.ConfigPath); err != nil {
		return fail(err)
	}
	if lease == nil {
		return op, nil
	}
	if err := sshReload(lease.Master, p.Config.Binary); err != nil {
		return fail(err)
	}
	if err := sshPortReady(ctx, lease.Master, p.Config, p.OldPort); err != nil {
		return fail(err)
	}
	if err := e.save(ctx, &op, Recovered, ""); err != nil {
		return op, err
	}
	_, raw, err := e.store.RecoveryGuard(ctx, e.target, p.Config.Name)
	if err != nil {
		return op, err
	}
	if len(raw) != 0 {
		var pending SSHPending
		if json.Unmarshal(raw, &pending) != nil || pending.ID != op.ID {
			return op, fmt.Errorf("different SSH operation is pending")
		}
		if err = e.store.ClearRecoveryGuard(ctx, e.target, p.Config.Name, raw); err != nil {
			return op, err
		}
	}
	return op, e.checkpoint("ssh_recovered", -1)
}
