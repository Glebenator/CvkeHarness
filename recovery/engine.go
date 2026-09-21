package recovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

type Options struct {
	Roots       []string
	Limits      Limits
	Policy      string
	Services    []NGINXService
	Snapshots   []SnapshotTarget
	SSHServices []SSHService
	Fleet       FleetConfig
	// Fault is an injected interruption hook for tests. It is never configured
	// from model arguments or environment variables in production builds.
	Fault func(stage string, index int) error
}

type Engine struct {
	store       *state.Store
	assets      string
	executable  string
	roots       []string
	limits      Limits
	policy      string
	target      string
	fault       func(string, int) error
	services    map[string]NGINXService
	snapshots   map[string]SnapshotTarget
	sshServices map[string]SSHService
	fleet       FleetConfig
	// Test-only transport substitution; production always invokes pinned SSH.
	fleetCall func(context.Context, FleetHostPlan, string, string, string) (Operation, error)
}

func New(store *state.Store, opts Options) (*Engine, error) {
	if !store.Available() || store.Path() == "" {
		return nil, fmt.Errorf("recovery requires a persistent state database")
	}
	if opts.Limits == (Limits{}) {
		opts.Limits = DefaultLimits()
	}
	if err := opts.Limits.validate(); err != nil {
		return nil, err
	}
	base, err := filepath.EvalSymlinks(filepath.Dir(store.Path()))
	if err != nil {
		return nil, err
	}
	e := &Engine{store: store, assets: filepath.Join(base, "recovery"), limits: opts.Limits, policy: opts.Policy, fault: opts.Fault}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("identify recovery executable: %w", err)
	}
	e.executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve recovery executable: %w", err)
	}
	e.fleet = opts.Fleet
	if e.fleet.Limits == (FleetLimits{}) {
		e.fleet.Limits = DefaultFleetLimits()
	}
	if err := e.fleet.validate(base); err != nil {
		return nil, err
	}
	e.services = make(map[string]NGINXService, len(opts.Services))
	e.snapshots = make(map[string]SnapshotTarget, len(opts.Snapshots))
	e.sshServices = make(map[string]SSHService, len(opts.SSHServices))
	if len(opts.SSHServices) > 16 {
		return nil, fmt.Errorf("at most 16 managed SSH services may be configured")
	}
	for _, service := range opts.SSHServices {
		if err := service.validate(); err != nil {
			return nil, err
		}
		if e.sshServices[service.Name].Name != "" {
			return nil, fmt.Errorf("managed SSH service names must be unique")
		}
		e.sshServices[service.Name] = service
	}
	for _, target := range opts.Snapshots {
		if target.Name == "" || e.snapshots[target.Name].Name != "" {
			return nil, fmt.Errorf("snapshot target names must be nonempty and unique")
		}
		e.snapshots[target.Name] = target
	}
	for _, service := range opts.Services {
		if service.Name == "" || e.services[service.Name].Name != "" {
			return nil, fmt.Errorf("service names must be nonempty and unique")
		}
		e.services[service.Name] = service
	}
	if e.fault == nil {
		e.fault = buildFaultHook()
	}
	for _, root := range opts.Roots {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("recovery root must be absolute")
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		if canonical == "/" {
			return nil, fmt.Errorf("whole-filesystem recovery root is prohibited")
		}
		d, err := openDirectory(canonical)
		if err != nil {
			return nil, err
		}
		d.Close()
		e.roots = append(e.roots, canonical)
	}
	sort.Slice(e.roots, func(i, j int) bool { return len(e.roots[i]) > len(e.roots[j]) })
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	// Linux target-side executors include machine-id. A hostname alone is not
	// sufficient to bind actions across machines sharing a host name.
	machine, _ := os.ReadFile("/etc/machine-id")
	if len(machine) == 0 {
		machine, err = platformMachineID()
		if err != nil {
			return nil, err
		}
	}
	e.target = sum([]byte(fmt.Sprintf("%s\x00%s\x00%d", host, strings.TrimSpace(string(machine)), os.Geteuid())))
	return e, nil
}

func (e *Engine) Target() string { return e.target }

func (e *Engine) noArmedSSH(ctx context.Context) error {
	armed, err := e.store.RecoveryGuardArmed(ctx, e.target)
	if err != nil {
		return err
	}
	if armed {
		return fmt.Errorf("an SSH rollback is armed; resolve it before starting another recovery mutation")
	}
	return nil
}

func (e *Engine) locked() (func(), error) {
	d, err := privateDirectory(e.assets)
	if err != nil {
		return nil, err
	}
	unlock, err := lockDirectory(d)
	if err != nil {
		d.Close()
		return nil, err
	}
	return func() { unlock(); d.Close() }, nil
}

func (e *Engine) checkPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\n\r") {
		return "", fmt.Errorf("path must be clean, absolute and contain no control separators")
	}
	if path == e.executable {
		return "", fmt.Errorf("running recovery executable is protected")
	}
	for _, protected := range []string{filepath.Dir(e.assets), "/proc", "/sys", "/dev", "/boot"} {
		if within(path, protected) {
			return "", fmt.Errorf("path is protected: %s", path)
		}
	}
	for _, part := range strings.Split(path, "/") {
		switch part {
		case ".git", ".ssh", ".gnupg", ".aws", ".azure", ".codex", ".cvkeharness", ".kube", ".docker":
			return "", fmt.Errorf("credential/repository/recovery path is protected: %s", path)
		}
		if strings.HasPrefix(part, ".cvkeharness-") {
			return "", fmt.Errorf("recovery artifact path is protected")
		}
	}
	for _, p := range []string{"/etc/passwd", "/etc/shadow", "/etc/group", "/etc/gshadow", "/etc/sudoers"} {
		if within(path, p) {
			return "", fmt.Errorf("identity configuration is protected: %s", path)
		}
	}
	for _, root := range e.roots {
		if path != root && within(path, root) {
			return root, nil
		}
	}
	return "", fmt.Errorf("path is outside operator-configured recovery roots: %s", path)
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func (e *Engine) Prepare(ctx context.Context, req Request) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	if err = e.noArmedSSH(ctx); err != nil {
		return Operation{}, err
	}
	if req.Service != "" && req.SSHService != "" {
		return Operation{}, fmt.Errorf("one service adapter may be selected per operation")
	}
	if len(req.Changes) == 0 || len(req.Changes) > e.limits.MaxFiles {
		return Operation{}, fmt.Errorf("request must contain 1..%d exact file changes", e.limits.MaxFiles)
	}
	idBytes := make([]byte, 16)
	if _, err = rand.Read(idBytes); err != nil {
		return Operation{}, err
	}
	op := Operation{RecoveryOperation: state.RecoveryOperation{ID: hex.EncodeToString(idBytes), Target: e.target, Policy: e.policy, Status: Preparing}, Plan: Manifest{Version: 1, CreatedAt: time.Now().UTC(), Limits: e.limits}}
	seen := map[string]bool{}
	original := make([][]byte, 0, len(req.Changes))
	candidates := make([][]byte, 0, len(req.Changes))
	var total int64
	for i, ch := range req.Changes {
		if err = ctx.Err(); err != nil {
			return Operation{}, err
		}
		selected := req.Service
		if req.SSHService != "" {
			selected = "ssh:" + req.SSHService
		}
		if err = e.checkServiceScope(ch.Path, selected); err != nil {
			return Operation{}, err
		}
		root, err := e.checkPath(ch.Path)
		if err != nil {
			return Operation{}, err
		}
		if seen[ch.Path] {
			return Operation{}, fmt.Errorf("duplicate path")
		}
		seen[ch.Path] = true
		if ch.Action != "replace" && ch.Action != "create" && ch.Action != "delete" {
			return Operation{}, fmt.Errorf("unsupported file action %q", ch.Action)
		}
		if ch.Mode > 0777 || (ch.Action != "create" && ch.Mode != 0) {
			return Operation{}, fmt.Errorf("mode is only supported for create, without special bits")
		}
		if ch.Action == "delete" && ch.Content != "" {
			return Operation{}, fmt.Errorf("delete cannot contain replacement content")
		}
		parent, err := parentFor(ch.Path)
		if err != nil {
			return Operation{}, err
		}
		dev, ino, err := dirIdentity(parent)
		if err != nil {
			parent.Close()
			return Operation{}, err
		}
		rd, err := openDirectory(root)
		if err != nil {
			parent.Close()
			return Operation{}, err
		}
		rootDev, _, err := dirIdentity(rd)
		rd.Close()
		if err != nil || rootDev != dev {
			parent.Close()
			return Operation{}, fmt.Errorf("operation crosses a mount boundary")
		}
		before, b, err := readRegular(parent, filepath.Base(ch.Path), e.limits.MaxFileBytes)
		parent.Close()
		if err != nil {
			return Operation{}, fmt.Errorf("inspect %s: %w", ch.Path, err)
		}
		if before.Exists && before.Device != dev {
			return Operation{}, fmt.Errorf("file is a mount point")
		}
		if (ch.Action == "create") == before.Exists {
			return Operation{}, fmt.Errorf("%s existence precondition failed", ch.Path)
		}
		if ch.ExpectedSHA256 != "" && ch.ExpectedSHA256 != before.SHA256 {
			return Operation{}, fmt.Errorf("expected hash differs at %s", ch.Path)
		}
		if int64(len(ch.Content)) > e.limits.MaxFileBytes {
			return Operation{}, fmt.Errorf("candidate exceeds per-file byte limit")
		}
		cost := int64(len(b)) + int64(len(ch.Content))
		if cost > e.limits.MaxBytes-total {
			return Operation{}, fmt.Errorf("original plus candidate bytes exceed operation budget")
		}
		total += cost
		after := before
		if ch.Action == "delete" {
			after = Fingerprint{}
		} else {
			after.Exists = true
			after.SHA256 = sum([]byte(ch.Content))
			after.Bytes = int64(len(ch.Content))
			after.Inode = 0
			after.ModifiedNS = 0
			if ch.Action == "create" {
				after.Mode = ch.Mode
				if after.Mode == 0 {
					after.Mode = 0600
				}
				after.UID = uint32(os.Geteuid())
				after.GID = uint32(os.Getegid())
				after.Device = dev
			}
		}
		entry := Entry{Action: ch.Action, Path: ch.Path, Root: root, ParentDevice: dev, ParentInode: ino, Before: before, After: after, Backup: fmt.Sprintf("%04d.before", i), Candidate: fmt.Sprintf("%04d.after", i)}
		if ch.Action == "delete" {
			entry.Quarantine = ".cvkeharness-" + op.ID + fmt.Sprintf("-%04d.deleted", i)
		}
		op.Plan.Entries = append(op.Plan.Entries, entry)
		original = append(original, b)
		candidates = append(candidates, []byte(ch.Content))
	}
	if req.Service != "" {
		op.Plan.Service, err = e.prepareService(req.Service, op, original, candidates)
		if err != nil {
			return Operation{}, err
		}
	}
	if req.SSHService != "" {
		op.Plan.SSH, err = e.prepareSSH(ctx, req.SSHService, op, original, candidates)
		if err != nil {
			return Operation{}, err
		}
	}
	op.Digest = planDigest(op.Target, op.Policy, op.Plan)
	if err = e.save(ctx, &op, Preparing, ""); err != nil {
		return op, err
	}
	if err = e.checkpoint("preparing", -1); err != nil {
		return op, err
	}
	assets, err := privateDirectory(filepath.Join(e.assets, op.ID))
	if err != nil {
		return op, e.preparationFailed(ctx, &op, err)
	}
	defer assets.Close()
	for i, en := range op.Plan.Entries {
		if err = ctx.Err(); err != nil {
			return op, e.preparationFailed(ctx, &op, err)
		}
		if en.Before.Exists {
			if err = writeAt(assets, en.Backup, original[i], Fingerprint{}); err != nil {
				return op, e.preparationFailed(ctx, &op, err)
			}
		}
		if en.After.Exists {
			if err = writeAt(assets, en.Candidate, candidates[i], Fingerprint{}); err != nil {
				return op, e.preparationFailed(ctx, &op, err)
			}
		}
		if err = e.checkpoint("backup", i); err != nil {
			return op, err
		}
	}
	if err = e.save(ctx, &op, Ready, ""); err != nil {
		return op, err
	}
	return op, e.checkpoint("ready", -1)
}

func (e *Engine) save(ctx context.Context, op *Operation, status, problem string) error {
	op.Status = status
	op.Problem = problem
	if err := encodePlan(op); err != nil {
		return err
	}
	return e.store.SaveRecoveryOperation(ctx, &op.RecoveryOperation)
}

func (e *Engine) checkpoint(stage string, index int) error {
	if e.fault != nil {
		return e.fault(stage, index)
	}
	return nil
}
func (e *Engine) preparationFailed(ctx context.Context, op *Operation, cause error) error {
	if err := e.save(context.WithoutCancel(ctx), op, PreparationFailed, "unable to preserve recovery assets"); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (e *Engine) Inspect(ctx context.Context, id string) (Operation, error) {
	r, err := e.store.GetRecoveryOperation(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	op := Operation{RecoveryOperation: r}
	if err = json.Unmarshal(r.Manifest, &op.Plan); err != nil {
		return op, err
	}
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" || op.Plan.Version != 1 || planDigest(op.Target, op.Policy, op.Plan) != op.Digest {
		return op, fmt.Errorf("invalid or altered recovery manifest")
	}
	if op.Target != e.target {
		return op, fmt.Errorf("operation belongs to a different machine or principal")
	}
	op.Checks, err = e.store.RecoveryChecks(ctx, id)
	if err != nil {
		return op, err
	}
	return op, nil
}

// List returns summaries including the immutable manifests, never file bytes.
func (e *Engine) List(ctx context.Context) ([]state.RecoveryOperation, error) {
	return e.store.ListRecoveryOperations(ctx)
}

func (e *Engine) Apply(ctx context.Context, id, digest string) (Operation, error) {
	return e.apply(ctx, id, digest, false)
}

// ApplyService includes validation, reload, verified worker health and one
// deterministic rollback attempt in the same locked, journaled operation.
func (e *Engine) ApplyService(ctx context.Context, id, digest string) (Operation, error) {
	return e.apply(ctx, id, digest, true)
}

func (e *Engine) apply(ctx context.Context, id, digest string, service bool) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	if err = e.noArmedSSH(ctx); err != nil {
		return Operation{}, err
	}
	op, err := e.Inspect(ctx, id)
	if err != nil {
		return op, err
	}
	if digest != op.Digest || op.Policy != e.policy {
		return op, fmt.Errorf("review digest or current policy does not match")
	}
	if op.Plan.SSH != nil {
		return op, fmt.Errorf("managed SSH plans require apply_ssh authorization")
	}
	if op.Plan.Snapshot != nil {
		return op, fmt.Errorf("snapshot operations require the native snapshot action")
	}
	if (op.Plan.Service != nil) != service {
		return op, fmt.Errorf("operation kind mismatch: service plans require apply_service authorization")
	}
	if op.Status == Committed {
		return op, nil
	}
	if op.Status != Ready {
		return op, fmt.Errorf("operation is %s; reconcile or recover instead of retrying apply", op.Status)
	}
	if time.Since(op.Plan.CreatedAt) > time.Duration(op.Plan.Limits.MaxAgeSeconds)*time.Second {
		return op, fmt.Errorf("prepared operation expired; prepare a fresh plan")
	}
	// Re-enforce current operator limits/roots; a stored plan cannot authorize a
	// mutation after the operator has reduced its permitted scope.
	if len(op.Plan.Entries) > e.limits.MaxFiles {
		return op, fmt.Errorf("current file budget exceeded")
	}
	var total int64
	for _, en := range op.Plan.Entries {
		serviceName := ""
		if op.Plan.Service != nil {
			serviceName = op.Plan.Service.Config.Name
		}
		if err = e.checkServiceScope(en.Path, serviceName); err != nil {
			return op, err
		}
		if _, err = e.checkPath(en.Path); err != nil {
			return op, err
		}
		cost := en.Before.Bytes + en.After.Bytes
		if cost > e.limits.MaxBytes-total || en.Before.Bytes > e.limits.MaxFileBytes || en.After.Bytes > e.limits.MaxFileBytes {
			return op, fmt.Errorf("current byte budget exceeded")
		}
		total += cost
		if _, err = e.readMatching(en, en.Before, true); err != nil {
			return op, err
		}
		if err = e.verifyAssets(op, en); err != nil {
			return op, err
		}
	}
	if err = e.checkCapacity(ctx, &op); err != nil {
		return op, err
	}
	if service {
		if err = e.servicePreflight(ctx, &op); err != nil {
			return op, err
		}
	}
	if err = e.save(ctx, &op, Applying, ""); err != nil {
		return op, err
	}
	if err = e.checkpoint("applying", -1); err != nil {
		return op, err
	}
	for i, en := range op.Plan.Entries {
		if err = ctx.Err(); err == nil {
			err = e.applyEntry(op, en, i)
		}
		if err != nil {
			if service {
				return e.serviceFailure(ctx, op, err)
			}
			// Preserve the ambiguous outcome. A later explicit recovery reconciles
			// each entry; never rerun a partially completed batch automatically.
			saveErr := e.save(context.WithoutCancel(ctx), &op, Unknown, "apply interrupted; inspect and recover")
			return op, errors.Join(err, saveErr)
		}
		if err = e.checkpoint("applied", i); err != nil {
			return op, err
		}
	}
	if err = e.save(ctx, &op, Verifying, ""); err != nil {
		if service {
			return e.serviceFailure(ctx, op, err)
		}
		return op, err
	}
	if err = e.checkpoint("verifying", -1); err != nil {
		return op, err
	}
	for _, en := range op.Plan.Entries {
		if _, err = e.readMatching(en, en.After, false); err != nil {
			if service {
				return e.serviceFailure(ctx, op, err)
			}
			_ = e.save(context.WithoutCancel(ctx), &op, Unknown, "postcondition failed")
			return op, err
		}
	}
	if service {
		if err = e.reloadService(ctx, op); err != nil {
			return e.serviceFailure(ctx, op, err)
		}
	}
	if err = e.save(ctx, &op, Committed, ""); err != nil {
		if service {
			return e.serviceFailure(ctx, op, err)
		}
		return op, err
	}
	return op, e.checkpoint("committed", -1)
}

func matches(actual, want Fingerprint, identity bool) bool {
	if actual.Exists != want.Exists {
		return false
	}
	if !want.Exists {
		return true
	}
	if actual.SHA256 != want.SHA256 || actual.Bytes != want.Bytes || actual.Mode != want.Mode || actual.UID != want.UID || actual.GID != want.GID {
		return false
	}
	return !identity || (actual.Device == want.Device && actual.Inode == want.Inode && actual.ModifiedNS == want.ModifiedNS)
}

func (e *Engine) entryParent(en Entry) (*os.File, error) {
	p, err := parentFor(en.Path)
	if err != nil {
		return nil, err
	}
	dev, ino, err := dirIdentity(p)
	if err != nil || dev != en.ParentDevice || ino != en.ParentInode {
		p.Close()
		return nil, fmt.Errorf("parent identity changed: %s", en.Path)
	}
	return p, nil
}

func (e *Engine) readMatching(en Entry, want Fingerprint, identity bool) (Fingerprint, error) {
	p, err := e.entryParent(en)
	if err != nil {
		return Fingerprint{}, err
	}
	defer p.Close()
	// Restoration remains bounded by the prepared manifest even if an
	// operator subsequently lowers limits for new mutations.
	got, _, err := readRegular(p, filepath.Base(en.Path), max(en.Before.Bytes, en.After.Bytes, 1))
	if err != nil {
		return got, err
	}
	if !matches(got, want, identity) {
		return got, fmt.Errorf("file conflict: %s", en.Path)
	}
	return got, nil
}

func (e *Engine) artifact(op Operation, name, hash string) ([]byte, error) {
	if filepath.Base(name) != name {
		return nil, fmt.Errorf("invalid artifact name")
	}
	d, err := openDirectory(filepath.Join(e.assets, op.ID))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	f, b, err := readRegular(d, name, op.Plan.Limits.MaxFileBytes)
	if err != nil {
		return nil, err
	}
	if !f.Exists || f.SHA256 != hash || f.Mode != 0600 || f.UID != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("recovery artifact missing, altered or not private")
	}
	return b, nil
}

func (e *Engine) verifyAssets(op Operation, en Entry) error {
	if en.Before.Exists {
		if _, err := e.artifact(op, en.Backup, en.Before.SHA256); err != nil {
			return err
		}
	}
	if en.After.Exists {
		if _, err := e.artifact(op, en.Candidate, en.After.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) applyEntry(op Operation, en Entry, index int) error {
	p, err := e.entryParent(en)
	if err != nil {
		return err
	}
	defer p.Close()
	actual, _, err := readRegular(p, filepath.Base(en.Path), e.limits.MaxFileBytes)
	if err != nil {
		return err
	}
	if !matches(actual, en.Before, true) {
		return fmt.Errorf("file changed after preparation: %s", en.Path)
	}
	if en.Action == "delete" {
		qd, err := childPrivateDirectory(p, en.Quarantine, true)
		if err != nil {
			return err
		}
		defer qd.Close()
		q, _, err := readRegular(qd, "original", e.limits.MaxFileBytes)
		if err != nil {
			return err
		}
		if q.Exists {
			return fmt.Errorf("quarantine destination already exists")
		}
		return moveAt(p, filepath.Base(en.Path), qd, "original")
	}
	b, err := e.artifact(op, en.Candidate, en.After.SHA256)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf(".cvkeharness-%s-%04d.pending", op.ID, index)
	if err = writeAt(p, tmp, b, en.After); err != nil {
		return err
	}
	// Recheck after staging, immediately before replacing the reviewed path.
	actual, _, err = readRegular(p, filepath.Base(en.Path), e.limits.MaxFileBytes)
	if err != nil || !matches(actual, en.Before, true) {
		return errors.Join(fmt.Errorf("file changed while staging: %s", en.Path), err)
	}
	return renameAt(p, tmp, filepath.Base(en.Path))
}

// Recover only restores entries which still match this operation's output.
// It never force-overwrites a conflicting destination or invents compensations.
func (e *Engine) Recover(ctx context.Context, id, digest string) (Operation, error) {
	return e.recover(ctx, id, digest, false)
}

func (e *Engine) RecoverService(ctx context.Context, id, digest string) (Operation, error) {
	return e.recover(ctx, id, digest, true)
}

func (e *Engine) recover(ctx context.Context, id, digest string, service bool) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	if err = e.noArmedSSH(ctx); err != nil {
		return Operation{}, err
	}
	op, err := e.Inspect(ctx, id)
	if err != nil {
		return op, err
	}
	if digest != op.Digest {
		return op, fmt.Errorf("recovery digest does not match")
	}
	if op.Plan.SSH != nil {
		return op, fmt.Errorf("managed SSH restoration requires recover_ssh authorization")
	}
	if op.Plan.Snapshot != nil {
		return op, fmt.Errorf("snapshot recovery requires the native snapshot action")
	}
	if (op.Plan.Service != nil) != service {
		return op, fmt.Errorf("operation kind mismatch: service plans require recover_service authorization")
	}
	return e.recoverLocked(ctx, op)
}

func (e *Engine) recoverLocked(ctx context.Context, op Operation) (Operation, error) {
	var err error
	if op.Status == Recovered {
		return op, nil
	}
	if op.Status == Ready {
		// A remote controller can die after recording dispatch but before the
		// command arrives. Under this same target lock, verify the unchanged
		// original then make the plan terminal. A delayed Apply sees Recovered
		// and cannot run later. No target file is written by this cancellation.
		for _, en := range op.Plan.Entries {
			if _, err = e.readMatching(en, en.Before, true); err != nil {
				return op, err
			}
		}
		if op.Plan.Service != nil {
			if err = verifyServiceIdentity(*op.Plan.Service); err != nil {
				return op, err
			}
			healthCtx, cancel := context.WithTimeout(ctx, time.Duration(op.Plan.Service.Config.TimeoutSeconds)*time.Second)
			err = checkServiceHealth(healthCtx, *op.Plan.Service, nil)
			cancel()
			if err != nil {
				return op, err
			}
		}
		if err = e.save(ctx, &op, Recovered, "cancelled before application; original state verified"); err != nil {
			return op, err
		}
		return op, e.checkpoint("recovered", -1)
	}
	if op.Status == Preparing || op.Status == PreparationFailed || op.Status == ValidationFailed {
		return op, fmt.Errorf("operation has not started applying; target restoration is unnecessary")
	}
	if err = e.save(ctx, &op, RollingBack, ""); err != nil {
		return op, err
	}
	if err = e.checkpoint("rolling_back", -1); err != nil {
		return op, err
	}
	var failures []error
	for i := len(op.Plan.Entries) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if err = e.restoreEntry(op, op.Plan.Entries[i], i); err != nil {
			failures = append(failures, err)
		}
		if err = e.checkpoint("restored", i); err != nil {
			return op, err
		}
	}
	if len(failures) > 0 {
		err = e.save(context.WithoutCancel(ctx), &op, RecoveryFailed, "restoration incomplete; destination conflict or recovery artifact unavailable")
		return op, errors.Join(append(failures, err)...)
	}
	for _, en := range op.Plan.Entries {
		if _, err = e.readMatching(en, en.Before, false); err != nil {
			_ = e.save(context.WithoutCancel(ctx), &op, RecoveryFailed, "restoration verification failed")
			return op, err
		}
	}
	if op.Plan.Service != nil {
		if err = e.reloadService(ctx, op); err != nil {
			saveErr := e.save(context.WithoutCancel(ctx), &op, RecoveryFailed, "files restored but service reload/health verification failed")
			return op, errors.Join(err, saveErr)
		}
	}
	if err = e.save(ctx, &op, Recovered, ""); err != nil {
		return op, err
	}
	return op, e.checkpoint("recovered", -1)
}

func (e *Engine) restoreEntry(op Operation, en Entry, index int) error {
	p, err := e.entryParent(en)
	if err != nil {
		return err
	}
	defer p.Close()
	actual, _, err := readRegular(p, filepath.Base(en.Path), op.Plan.Limits.MaxFileBytes)
	if err != nil {
		return err
	}
	if matches(actual, en.Before, false) {
		return nil
	}
	if !matches(actual, en.After, false) {
		return fmt.Errorf("restore conflict: %s", en.Path)
	}
	if !en.Before.Exists {
		return removeAt(p, filepath.Base(en.Path))
	}
	b, err := e.artifact(op, en.Backup, en.Before.SHA256)
	if err != nil {
		return err
	}
	if en.Action == "delete" {
		qd, qerr := childPrivateDirectory(p, en.Quarantine, false)
		if qerr != nil && !os.IsNotExist(qerr) {
			return qerr
		}
		if qerr == nil {
			defer qd.Close()
			q, _, err := readRegular(qd, "original", op.Plan.Limits.MaxFileBytes)
			if err != nil {
				return err
			}
			if q.Exists {
				if !matches(q, en.Before, true) {
					return fmt.Errorf("quarantined file changed; refusing ambiguous restore")
				}
				return moveAt(qd, "original", p, filepath.Base(en.Path))
			}
		}
	}
	tmp := fmt.Sprintf(".cvkeharness-%s-%04d.restore", op.ID, index)
	// A prior crash may have left a fully written staging file. Verify its
	// content and metadata before reusing it, never truncate it in place.
	staged, _, err := readRegular(p, tmp, op.Plan.Limits.MaxFileBytes)
	if err != nil {
		return err
	}
	if staged.Exists {
		if !matches(staged, en.Before, false) {
			return fmt.Errorf("restore staging conflict")
		}
	} else if err = writeAt(p, tmp, b, en.Before); err != nil {
		return err
	}
	actual, _, err = readRegular(p, filepath.Base(en.Path), op.Plan.Limits.MaxFileBytes)
	if err != nil || !matches(actual, en.After, false) {
		return errors.Join(fmt.Errorf("destination changed during restore"), err)
	}
	return renameAt(p, tmp, filepath.Base(en.Path))
}

// Reconcile performs no target mutations and never interprets an incomplete
// operation as success. Explicit recovery remains available for interrupted work.
func (e *Engine) Reconcile(ctx context.Context, id string) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	op, err := e.Inspect(ctx, id)
	if err != nil {
		return op, err
	}
	switch op.Status {
	case Preparing:
		err = e.save(ctx, &op, PreparationFailed, "preparation interrupted; create a fresh plan")
	case Validating, Applying, Verifying, RollingBack:
		err = e.save(ctx, &op, Unknown, "execution interrupted; compare target state and recover explicitly")
	}
	return op, err
}
