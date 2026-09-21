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

type SnapshotTarget struct {
	Name            string `json:"name" yaml:"name"`
	Path            string `json:"path" yaml:"path"`
	Store           string `json:"store" yaml:"store"`
	MaxEntries      int    `json:"max_entries" yaml:"max_entries"`
	MaxLogicalBytes int64  `json:"max_logical_bytes" yaml:"max_logical_bytes"`
}

// SnapshotInventory is part of the persisted manifest schema on every platform,
// even where snapshot execution is unavailable.
type SnapshotInventory struct {
	Entries      int    `json:"entries"`
	LogicalBytes int64  `json:"logical_bytes"`
	Digest       string `json:"digest"`
}

type SubvolumeIdentity struct {
	ID         uint64 `json:"id"`
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parent_uuid"`
	Filesystem string `json:"filesystem"`
	Generation uint64 `json:"generation"`
	ReadOnly   bool   `json:"read_only"`
}

type SnapshotPlan struct {
	Adapter      string            `json:"adapter"`
	Action       string            `json:"action"`
	Config       SnapshotTarget    `json:"config"`
	Source       SubvolumeIdentity `json:"source"`
	Before       SnapshotInventory `json:"before"`
	Parent       SnapshotNamespace `json:"parent"`
	Store        SnapshotNamespace `json:"store"`
	CheckpointID string            `json:"checkpoint_id,omitempty"`
	Checkpoint   SubvolumeIdentity `json:"checkpoint,omitempty"`
	Desired      SnapshotInventory `json:"desired"`
}

// Btrfs allocates synthetic st_dev values when subvolumes are mounted/opened.
// Use the filesystem's persistent root ID and inode across a guest reboot.
type SnapshotNamespace struct {
	Filesystem string `json:"filesystem"`
	RootID     uint64 `json:"root_id"`
	Inode      uint64 `json:"inode"`
}

type snapshotHandles struct {
	source, parent, store *os.File
	identity              SubvolumeIdentity
}

func (h *snapshotHandles) close() {
	if h.source != nil {
		h.source.Close()
	}
	if h.parent != nil {
		h.parent.Close()
	}
	if h.store != nil {
		h.store.Close()
	}
}

func (e *Engine) SnapshotTargets() []SnapshotTarget {
	out := make([]SnapshotTarget, 0, len(e.snapshots))
	for _, target := range e.snapshots {
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func protectedSnapshotEntry(path string) error {
	for _, part := range strings.Split(path, "/") {
		switch part {
		case ".git", ".ssh", ".gnupg", ".aws", ".azure", ".codex", ".kube", ".docker":
			return fmt.Errorf("snapshot contains a protected credential/repository path")
		}
		if strings.HasPrefix(part, ".cvkeharness") {
			return fmt.Errorf("snapshot contains protected recovery assets")
		}
	}
	for _, protected := range []string{"/etc/passwd", "/etc/shadow", "/etc/group", "/etc/gshadow", "/etc/sudoers", "/proc", "/sys", "/dev", "/boot"} {
		if within(path, protected) {
			return fmt.Errorf("snapshot contains a protected system path")
		}
	}
	return nil
}

func (e *Engine) snapshotHandles(cfg SnapshotTarget) (*snapshotHandles, error) {
	if cfg.Name == "" || len(cfg.Name) > 64 || strings.Trim(cfg.Name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" || !filepath.IsAbs(cfg.Path) || filepath.Clean(cfg.Path) != cfg.Path || cfg.Path == "/" || strings.ContainsAny(cfg.Path, "\x00\n\r") || cfg.Store != filepath.Join(filepath.Dir(cfg.Path), ".cvkeharness-snapshots") || cfg.MaxEntries < 1 || cfg.MaxEntries > 10000 || cfg.MaxLogicalBytes < 1 || cfg.MaxLogicalBytes > 1<<30 {
		return nil, fmt.Errorf("invalid snapshot target (dedicated subvolume, private sibling store, at most 10000 entries/1 GiB)")
	}
	for _, protected := range []string{filepath.Dir(e.assets), "/proc", "/sys", "/dev", "/boot"} {
		if within(protected, cfg.Path) || within(cfg.Path, protected) {
			return nil, fmt.Errorf("snapshot source overlaps protected state/system paths")
		}
	}
	if err := protectedSnapshotEntry(cfg.Path); err != nil {
		return nil, err
	}
	if within(e.executable, cfg.Path) {
		return nil, fmt.Errorf("snapshot cannot replace the running recovery executable")
	}
	for _, service := range e.services {
		if within(service.ConfigPath, cfg.Path) || within(service.PIDFile, cfg.Path) || within(service.Binary, cfg.Path) {
			return nil, fmt.Errorf("snapshot restore cannot bypass a configured service transaction")
		}
	}
	for _, host := range e.fleet.Hosts {
		if within(host.SSHBinary, cfg.Path) || within(cfg.Path, host.SSHBinary) {
			return nil, fmt.Errorf("snapshot cannot replace fleet transport executable")
		}
	}
	for _, service := range e.sshServices {
		for _, protected := range []string{service.ConfigPath, service.PIDFile, service.Binary, service.SessionBinary, service.HostKeyPath, service.AuthorizedKeysPath} {
			if within(protected, cfg.Path) {
				return nil, fmt.Errorf("snapshot cannot bypass managed SSH protection")
			}
		}
	}
	h := &snapshotHandles{}
	var err error
	failed := true
	defer func() {
		if failed {
			h.close()
		}
	}()
	h.source, err = openDirectory(cfg.Path)
	if err != nil {
		return nil, err
	}
	h.identity, err = nativeSubvolume(h.source)
	if err != nil {
		return nil, err
	}
	if h.identity.ReadOnly {
		return nil, fmt.Errorf("active snapshot target must be writable")
	}
	h.parent, err = parentFor(cfg.Path)
	if err != nil {
		return nil, err
	}
	if err = nativeSameMount(h.source, h.parent); err != nil {
		return nil, err
	}
	fs, err := nativeFilesystem(h.parent)
	if err != nil || fs != h.identity.Filesystem {
		return nil, fmt.Errorf("snapshot source is a mount point or crosses a filesystem")
	}
	h.store, err = privateDirectory(cfg.Store)
	if err != nil {
		return nil, err
	}
	fs, err = nativeFilesystem(h.store)
	if err != nil || fs != h.identity.Filesystem {
		return nil, fmt.Errorf("snapshot store must use the same Btrfs filesystem")
	}
	if err = nativeSameMount(h.store, h.parent); err != nil {
		return nil, err
	}
	parentNS, err := nativeNamespace(h.parent)
	if err != nil {
		return nil, err
	}
	storeNS, err := nativeNamespace(h.store)
	if err != nil || storeNS.RootID != parentNS.RootID {
		return nil, fmt.Errorf("snapshot store must be an ordinary directory in the source's parent subvolume")
	}
	failed = false
	return h, nil
}

func newSnapshotOperation(e *Engine, p SnapshotPlan) (Operation, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Operation{}, err
	}
	op := Operation{RecoveryOperation: state.RecoveryOperation{ID: hex.EncodeToString(id), Target: e.target, Policy: e.policy}, Plan: Manifest{Version: 1, CreatedAt: time.Now().UTC(), Limits: e.limits, Snapshot: &p}}
	op.Digest = planDigest(op.Target, op.Policy, op.Plan)
	return op, nil
}

func (e *Engine) PrepareSnapshot(ctx context.Context, name string) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	if err = e.noArmedSSH(ctx); err != nil {
		return Operation{}, err
	}
	cfg, ok := e.snapshots[name]
	if !ok {
		return Operation{}, fmt.Errorf("snapshot target is not operator-configured")
	}
	h, err := e.snapshotHandles(cfg)
	if err != nil {
		return Operation{}, err
	}
	defer h.close()
	if err = nativeSync(h.source); err != nil {
		return Operation{}, err
	}
	before, err := snapshotInventory(ctx, h.source, cfg)
	if err != nil {
		return Operation{}, err
	}
	p := SnapshotPlan{Adapter: "btrfs-subvolume-v1", Action: "checkpoint", Config: cfg, Source: h.identity, Before: before, Desired: before}
	p.Parent, err = nativeNamespace(h.parent)
	if err != nil {
		return Operation{}, err
	}
	p.Store, err = nativeNamespace(h.store)
	if err != nil {
		return Operation{}, err
	}
	op, err := newSnapshotOperation(e, p)
	if err != nil {
		return op, err
	}
	err = e.save(ctx, &op, Ready, "")
	return op, err
}

func snapshotArtifact(op Operation) string {
	if op.Plan.Snapshot.Action == "checkpoint" {
		return op.ID + ".checkpoint"
	}
	return op.ID + ".displaced"
}

func (e *Engine) PrepareSnapshotRestore(ctx context.Context, id, digest string) (Operation, error) {
	unlock, err := e.locked()
	if err != nil {
		return Operation{}, err
	}
	defer unlock()
	if err = e.noArmedSSH(ctx); err != nil {
		return Operation{}, err
	}
	checkpoint, err := e.Inspect(ctx, id)
	if err != nil {
		return checkpoint, err
	}
	if checkpoint.Digest != digest || checkpoint.Status != Committed || checkpoint.Plan.Snapshot == nil || checkpoint.Plan.Snapshot.Action != "checkpoint" {
		return Operation{}, fmt.Errorf("restore requires a committed checkpoint and its exact digest")
	}
	cfg := checkpoint.Plan.Snapshot.Config
	h, err := e.snapshotHandles(cfg)
	if err != nil {
		return Operation{}, err
	}
	defer h.close()
	if h.identity.ReadOnly {
		return Operation{}, fmt.Errorf("active snapshot target is read-only")
	}
	backup, bi, err := openSnapshotArtifact(h.store, snapshotArtifact(checkpoint))
	if err != nil {
		return Operation{}, err
	}
	defer backup.Close()
	if !bi.ReadOnly || bi.ParentUUID != checkpoint.Plan.Snapshot.Source.UUID {
		return Operation{}, fmt.Errorf("checkpoint identity or read-only flag changed")
	}
	receipt, err := snapshotReceipt(checkpoint)
	// Snapshot creation updates the source root item's generation even when
	// that source is read-only. Pin native identity and the complete supported
	// tree inventory; generation is diagnostic evidence, not a content hash.
	if err != nil || bi.UUID != receipt.UUID || bi.Filesystem != receipt.Filesystem {
		return Operation{}, errors.Join(fmt.Errorf("checkpoint no longer matches its committed native identity"), err)
	}
	verified, err := snapshotInventory(ctx, backup, cfg)
	if err != nil || verified != checkpoint.Plan.Snapshot.Desired {
		return Operation{}, errors.Join(fmt.Errorf("checkpoint coverage/content changed"), err)
	}
	if err = nativeSync(h.source); err != nil {
		return Operation{}, err
	}
	before, err := snapshotInventory(ctx, h.source, cfg)
	if err != nil {
		return Operation{}, err
	}
	p := SnapshotPlan{Adapter: "btrfs-subvolume-v1", Action: "restore", Config: cfg, Source: h.identity, Before: before, Desired: verified, CheckpointID: id, Checkpoint: bi}
	p.Parent, err = nativeNamespace(h.parent)
	if err != nil {
		return Operation{}, err
	}
	p.Store, err = nativeNamespace(h.store)
	if err != nil {
		return Operation{}, err
	}
	op, err := newSnapshotOperation(e, p)
	if err != nil {
		return op, err
	}
	err = e.save(ctx, &op, Ready, "")
	return op, err
}

func (e *Engine) ApplySnapshot(ctx context.Context, id, digest string) (Operation, error) {
	return e.runSnapshot(ctx, id, digest, false)
}
func (e *Engine) RecoverSnapshot(ctx context.Context, id, digest string) (Operation, error) {
	return e.runSnapshot(ctx, id, digest, true)
}

func (e *Engine) runSnapshot(ctx context.Context, id, digest string, resume bool) (Operation, error) {
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
	p := op.Plan.Snapshot
	if p == nil || p.Adapter != "btrfs-subvolume-v1" || op.Digest != digest {
		return op, fmt.Errorf("invalid native snapshot action or digest")
	}
	if op.Status == Committed || op.Status == Recovered {
		return op, nil
	}
	if !resume {
		if op.Status != Ready || op.Policy != e.policy || time.Since(op.Plan.CreatedAt) > time.Duration(op.Plan.Limits.MaxAgeSeconds)*time.Second {
			return op, fmt.Errorf("snapshot plan is stale, already started or has a different policy")
		}
		if p.Action == "checkpoint" && e.snapshots[p.Config.Name] != p.Config {
			return op, fmt.Errorf("snapshot target configuration changed")
		}
	} else if op.Status != Unknown && op.Status != Applying && op.Status != Verifying && op.Status != RecoveryFailed {
		return op, fmt.Errorf("snapshot recovery requires an interrupted or failed action")
	}
	h, err := e.snapshotHandles(p.Config)
	if err != nil {
		return op, err
	}
	defer h.close()
	ns, err := nativeNamespace(h.parent)
	if err != nil || ns != p.Parent {
		return op, fmt.Errorf("snapshot parent changed")
	}
	ns, err = nativeNamespace(h.store)
	if err != nil || ns != p.Store {
		return op, fmt.Errorf("snapshot store changed")
	}
	if !resume {
		if err = matchingSnapshotSource(ctx, h, p); err != nil {
			return op, err
		}
		if len(op.Checks) > 126 {
			return op, fmt.Errorf("snapshot evidence budget exhausted; prepare a fresh reviewed operation")
		}
		if err = e.checkSnapshotCapacity(ctx, &op, h.store); err != nil {
			return op, err
		}
	}
	if err = e.save(ctx, &op, Applying, ""); err != nil {
		return op, err
	}
	if err = e.checkpoint("snapshot_applying", -1); err != nil {
		return op, err
	}
	if p.Action == "checkpoint" {
		err = e.createCheckpoint(ctx, &op, h)
	} else if p.Action == "restore" {
		err = e.restoreSnapshot(ctx, &op, h)
	} else {
		err = fmt.Errorf("unsupported native snapshot action")
	}
	if err != nil {
		saveErr := e.save(context.WithoutCancel(ctx), &op, Unknown, "native snapshot action incomplete; retained artifacts require inspection and explicit recovery")
		return op, errors.Join(err, saveErr)
	}
	status := Committed
	if p.Action == "restore" {
		status = Recovered
	}
	if err = e.save(ctx, &op, status, ""); err != nil {
		return op, err
	}
	return op, e.checkpoint("snapshot_complete", -1)
}

func matchingSnapshotSource(ctx context.Context, h *snapshotHandles, p *SnapshotPlan) error {
	if h.identity.UUID != p.Source.UUID || h.identity.Filesystem != p.Source.Filesystem || h.identity.ReadOnly != p.Source.ReadOnly {
		return fmt.Errorf("snapshot source identity changed")
	}
	parent, err := parentFor(p.Config.Path)
	if err != nil {
		return err
	}
	ns, err := nativeNamespace(parent)
	parent.Close()
	if err != nil || ns != p.Parent {
		return fmt.Errorf("snapshot namespace parent changed")
	}
	current, err := openDirectory(p.Config.Path)
	if err != nil {
		return err
	}
	defer current.Close()
	identity, err := nativeSubvolume(current)
	if err != nil || identity.UUID != p.Source.UUID || identity.Filesystem != p.Source.Filesystem || identity.ReadOnly != p.Source.ReadOnly {
		return errors.Join(fmt.Errorf("snapshot namespace source changed"), err)
	}
	inventory, err := snapshotInventory(ctx, current, p.Config)
	if err != nil || inventory != p.Before {
		return errors.Join(fmt.Errorf("snapshot source changed after review"), err)
	}
	return nil
}

func (e *Engine) createCheckpoint(ctx context.Context, op *Operation, h *snapshotHandles) error {
	p := op.Plan.Snapshot
	name := snapshotArtifact(*op)
	artifact, identity, err := openSnapshotArtifact(h.store, name)
	if os.IsNotExist(err) {
		if err = matchingSnapshotSource(ctx, h, p); err != nil {
			return err
		}
		if err = nativeSnapshot(h.source, h.store, name, true); err != nil {
			return err
		}
		if err = e.checkpoint("snapshot_cloned", -1); err != nil {
			return err
		}
		artifact, identity, err = openSnapshotArtifact(h.store, name)
	}
	if err != nil {
		return err
	}
	defer artifact.Close()
	if !identity.ReadOnly || identity.ParentUUID != p.Source.UUID || identity.Filesystem != p.Source.Filesystem {
		return fmt.Errorf("checkpoint identity mismatch")
	}
	inventory, err := snapshotInventory(ctx, artifact, p.Config)
	if err != nil || inventory != p.Desired {
		return errors.Join(fmt.Errorf("checkpoint does not match reviewed contents"), err)
	}
	// A crash after recording the receipt but before committing the operation
	// must not consume another evidence slot just to recognize the same clone.
	for _, check := range op.Checks {
		if check.Kind == "snapshot_identity" {
			receipt, err := snapshotReceipt(*op)
			if err != nil || receipt.UUID != identity.UUID || receipt.Filesystem != identity.Filesystem {
				return errors.Join(fmt.Errorf("retained snapshot differs from its recorded identity"), err)
			}
			return nil
		}
	}
	b, _ := json.Marshal(identity)
	check, err := e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "snapshot_identity", b)
	if err != nil {
		return err
	}
	op.Checks = append(op.Checks, check)
	return e.checkpoint("snapshot_receipted", -1)
}

func snapshotReceipt(op Operation) (SubvolumeIdentity, error) {
	for i := len(op.Checks) - 1; i >= 0; i-- {
		if op.Checks[i].Kind == "snapshot_identity" {
			var identity SubvolumeIdentity
			if err := json.Unmarshal(op.Checks[i].Evidence, &identity); err != nil {
				return identity, err
			}
			return identity, nil
		}
	}
	return SubvolumeIdentity{}, fmt.Errorf("committed snapshot receipt is missing")
}

func (e *Engine) restoreSnapshot(ctx context.Context, op *Operation, h *snapshotHandles) error {
	p := op.Plan.Snapshot
	checkpoint, identity, err := openSnapshotArtifact(h.store, p.CheckpointID+".checkpoint")
	if err != nil {
		return err
	}
	defer checkpoint.Close()
	if identity.UUID != p.Checkpoint.UUID || identity.ParentUUID != p.Checkpoint.ParentUUID || !identity.ReadOnly || identity.Filesystem != p.Source.Filesystem {
		return fmt.Errorf("original checkpoint changed")
	}
	verified, err := snapshotInventory(ctx, checkpoint, p.Config)
	if err != nil || verified != p.Desired {
		return errors.Join(fmt.Errorf("checkpoint contents changed"), err)
	}
	name := snapshotArtifact(*op)
	staged, si, err := openSnapshotArtifact(h.store, name)
	if os.IsNotExist(err) {
		if err = matchingSnapshotSource(ctx, h, p); err != nil {
			return err
		}
		if err = nativeSnapshot(checkpoint, h.store, name, false); err != nil {
			return err
		}
		if err = e.checkpoint("snapshot_cloned", -1); err != nil {
			return err
		}
		staged, si, err = openSnapshotArtifact(h.store, name)
	}
	if err != nil {
		return err
	}
	defer staged.Close()
	if si.ReadOnly {
		return fmt.Errorf("staged/displaced subvolume is unexpectedly read-only")
	}
	if h.identity.UUID == p.Source.UUID {
		if si.ParentUUID != p.Checkpoint.UUID {
			return fmt.Errorf("restore clone does not derive from checkpoint")
		}
		stagedTree, err := snapshotInventory(ctx, staged, p.Config)
		if err != nil || stagedTree != p.Desired {
			return errors.Join(fmt.Errorf("restore clone changed"), err)
		}
		if err = matchingSnapshotSource(ctx, h, p); err != nil {
			return err
		}
		// Atomic namespace exchange retains the entire old tree. Open file
		// descriptors held by other programs continue to refer to that tree.
		if err = nativeExchange(h.parent, filepath.Base(p.Config.Path), h.store, name); err != nil {
			return err
		}
		if err = e.checkpoint("snapshot_exchanged", -1); err != nil {
			return err
		}
	} else if si.UUID != p.Source.UUID || h.identity.ParentUUID != p.Checkpoint.UUID {
		return fmt.Errorf("ambiguous source/displaced snapshot identities")
	}
	if err = e.save(ctx, op, Verifying, ""); err != nil {
		return err
	}
	current, err := openDirectory(p.Config.Path)
	if err != nil {
		return err
	}
	defer current.Close()
	ci, err := nativeSubvolume(current)
	if err != nil || ci.ParentUUID != p.Checkpoint.UUID {
		return errors.Join(fmt.Errorf("restored root identity changed"), err)
	}
	currentTree, err := snapshotInventory(ctx, current, p.Config)
	if err != nil || currentTree != p.Desired {
		return errors.Join(fmt.Errorf("restored tree verification failed"), err)
	}
	displaced, di, err := openSnapshotArtifact(h.store, name)
	if err != nil {
		return err
	}
	defer displaced.Close()
	if di.UUID != p.Source.UUID {
		return fmt.Errorf("displaced tree identity changed")
	}
	displacedTree, err := snapshotInventory(ctx, displaced, p.Config)
	if err != nil || displacedTree != p.Before {
		return errors.Join(fmt.Errorf("concurrent writer changed the retained displaced tree; stop writers and inspect"), err)
	}
	return e.checkpoint("snapshot_verified", -1)
}
