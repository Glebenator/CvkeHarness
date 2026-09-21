package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/state"
)

type executionTargetKey struct{}
type ExecutionTarget struct {
	RuntimeID, TargetID, Kind string
	Ambiguous                 bool
}

// WithExecutionTarget carries trusted runtime resolution, never model arguments.
func WithExecutionTarget(ctx context.Context, target ExecutionTarget) context.Context {
	return context.WithValue(ctx, executionTargetKey{}, target)
}

type RecoveryManageTool struct{ engine *recovery.Engine }

func NewRecoveryManageTool(engine *recovery.Engine) *RecoveryManageTool {
	return &RecoveryManageTool{engine}
}
func (t *RecoveryManageTool) Name() string { return "recovery_manage" }
func (t *RecoveryManageTool) Description() string {
	return "Prepare, inspect, apply or recover exact bounded file changes on the active local runtime target. Prefer this tool over shell writes when supported. prepare saves private backups and an immutable plan/digest. services lists operator-defined NGINX instances; prepare with service selects one and requires a supported standalone config replacement. Service plans MUST use apply_service/recover_service, which validate, reload and verify health; failure triggers one bounded rollback. snapshot_targets lists native Btrfs targets. snapshot_prepare plans a checkpoint; apply_snapshot creates it. snapshot_restore_prepare plans whole-tree restoration from a checkpoint ID/digest; review that new plan before apply_snapshot. recover_snapshot resumes an interrupted native operation. Native snapshots retain displaced trees, exclude nested mounts/subvolumes and do not recover service state. ssh_services lists dedicated managed SSH instances; prepare with ssh_service selects the fixed port-change workflow. Use apply_ssh to arm target-local timed rollback, confirm_ssh only through a newly authenticated connection, or recover_ssh to restore. Awaiting confirmation is not committed. All mutations need scoped approval. Roots, budgets and service health criteria are operator-configured. Remote targets require a target-side executor; arbitrary shell actions have no recovery coverage. Never claim recovery until status is recovered."
}
func (t *RecoveryManageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["prepare","list","services","inspect","apply","recover","apply_service","recover_service","reconcile","snapshot_targets","snapshot_prepare","snapshot_restore_prepare","apply_snapshot","recover_snapshot","ssh_services","apply_ssh","recover_ssh","confirm_ssh"]},"id":{"type":"string"},"digest":{"type":"string"},"service":{"type":"string"},"ssh_service":{"type":"string"},"snapshot":{"type":"string"},"changes":{"type":"array","items":{"type":"object","properties":{"action":{"type":"string","enum":["replace","create","delete"]},"path":{"type":"string"},"content":{"type":"string"},"expected_sha256":{"type":"string"},"mode":{"type":"integer"}},"required":["action","path"],"additionalProperties":false}}},"required":["action"],"additionalProperties":false}`)
}

type RecoveryArguments struct {
	Action     string            `json:"action"`
	ID         string            `json:"id,omitempty"`
	Digest     string            `json:"digest,omitempty"`
	Changes    []recovery.Change `json:"changes,omitempty"`
	Service    string            `json:"service,omitempty"`
	Snapshot   string            `json:"snapshot,omitempty"`
	SSHService string            `json:"ssh_service,omitempty"`
}

func decodeRecovery(raw json.RawMessage) (RecoveryArguments, error) {
	var a RecoveryArguments
	if len(raw) > 12<<20 {
		return a, fmt.Errorf("recovery request exceeds 12 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return a, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return a, fmt.Errorf("unexpected trailing recovery arguments")
	}
	if a.SSHService != "" && (a.Action != "prepare" || a.Service != "") {
		return a, fmt.Errorf("ssh_service selects exactly one adapter during preparation")
	}
	switch a.Action {
	case "prepare":
		if a.ID != "" || a.Digest != "" || a.Snapshot != "" {
			return a, fmt.Errorf("prepare cannot select an existing operation")
		}
	case "snapshot_prepare":
		if a.Snapshot == "" || a.ID != "" || a.Digest != "" || len(a.Changes) != 0 || a.Service != "" {
			return a, fmt.Errorf("snapshot_prepare takes only an operator-defined snapshot target")
		}
	case "list", "services", "snapshot_targets", "ssh_services":
		if a.ID != "" || a.Digest != "" || len(a.Changes) != 0 || a.Service != "" || a.Snapshot != "" {
			return a, fmt.Errorf("list takes no operation or changes")
		}
	case "inspect", "reconcile", "apply", "recover", "apply_service", "recover_service", "snapshot_restore_prepare", "apply_snapshot", "recover_snapshot", "apply_ssh", "recover_ssh", "confirm_ssh":
		if a.ID == "" || len(a.Changes) != 0 || a.Service != "" || a.Snapshot != "" {
			return a, fmt.Errorf("action requires id and cannot alter prepared changes")
		}
	default:
		return a, fmt.Errorf("unknown recovery action")
	}
	return a, nil
}

func (t *RecoveryManageTool) validateTarget(ctx context.Context) error {
	target, ok := ctx.Value(executionTargetKey{}).(ExecutionTarget)
	if !ok {
		return fmt.Errorf("recovery tool requires trusted execution target context")
	}
	if target.Ambiguous || target.Kind != "runtime" || target.TargetID == "" || target.TargetID != target.RuntimeID {
		return fmt.Errorf("recovery tool cannot mutate the runtime host while a remote or ambiguous target is active; use a target-side executor")
	}
	return nil
}

// Review validates immutable action binding before authorization and supplies
// exact path/impact context to the same approval UI used for shell operations.
func (t *RecoveryManageTool) Review(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := decodeRecovery(raw)
	if err != nil {
		return "", err
	}
	if err = t.validateTarget(ctx); err != nil {
		return "", err
	}
	if a.Action != "apply" && a.Action != "recover" && a.Action != "apply_service" && a.Action != "recover_service" && a.Action != "apply_snapshot" && a.Action != "recover_snapshot" && !strings.HasSuffix(a.Action, "_ssh") {
		return "", nil
	}
	op, err := t.engine.Inspect(ctx, a.ID)
	if err != nil {
		return "", err
	}
	if a.Digest != op.Digest {
		return "", fmt.Errorf("reviewed operation digest does not match")
	}
	if (op.Plan.Service != nil) != strings.HasSuffix(a.Action, "_service") {
		return "", fmt.Errorf("operation kind mismatch; service plans require apply_service/recover_service")
	}
	if (op.Plan.Snapshot != nil) != strings.HasSuffix(a.Action, "_snapshot") {
		return "", fmt.Errorf("snapshot plans require apply_snapshot/recover_snapshot authorization")
	}
	if (op.Plan.SSH != nil) != strings.HasSuffix(a.Action, "_ssh") {
		return "", fmt.Errorf("SSH plans require explicit guarded SSH action authorization")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s operation %s on executor %s (%s)\n", a.Action, op.ID, op.Target, op.Status)
	for _, en := range op.Plan.Entries {
		fmt.Fprintf(&b, "%s %s: %d -> %d bytes\n", en.Action, en.Path, en.Before.Bytes, en.After.Bytes)
	}
	if service := op.Plan.Service; service != nil {
		fmt.Fprintf(&b, "NGINX %s: validate and reload master PID %d; health %s status %d SHA256 %s; one rollback on failure\n", service.Config.Name, service.Master.PID, service.Config.HealthURL, service.Config.HealthStatus, service.Config.HealthSHA256)
	}
	if snapshot := op.Plan.Snapshot; snapshot != nil {
		fmt.Fprintf(&b, "Native Btrfs %s: %s; %d entries / %d logical bytes; desired %d entries / %d bytes. Entire directory namespace is affected. Old tree is retained in %s; open file handles and service health are outside coverage.\n", snapshot.Action, snapshot.Config.Path, snapshot.Before.Entries, snapshot.Before.LogicalBytes, snapshot.Desired.Entries, snapshot.Desired.LogicalBytes, snapshot.Config.Store)
	}
	if ssh := op.Plan.SSH; ssh != nil {
		fmt.Fprintf(&b, "Managed SSH %s: port %d -> %d; root-owned supervisor PID %d; target-local rollback unless a new authenticated transport confirms within %d seconds. Authentication and identity files remain fixed.\n", ssh.Config.Name, ssh.OldPort, ssh.NewPort, ssh.Guard.Process.PID, ssh.Config.ConfirmationSeconds)
	}
	return b.String(), nil
}

func (t *RecoveryManageTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := decodeRecovery(raw)
	if err != nil {
		return "", err
	}
	if err = t.validateTarget(ctx); err != nil {
		return "", err
	}
	if a.Action == "recover" || a.Action == "recover_service" || a.Action == "recover_snapshot" || a.Action == "recover_ssh" {
		if err = t.engine.ReserveModelRepair(ctx, a.ID, a.Digest); err != nil {
			return "", err
		}
	}
	var result any
	switch a.Action {
	case "ssh_services":
		result = t.engine.SSHServices()
	case "apply_ssh":
		result, err = t.engine.ApplySSH(ctx, a.ID, a.Digest)
	case "recover_ssh":
		result, err = t.engine.RecoverSSH(ctx, a.ID, a.Digest)
	case "confirm_ssh":
		result, err = t.engine.ConfirmSSH(ctx, a.ID, a.Digest)
	case "snapshot_targets":
		result = t.engine.SnapshotTargets()
	case "snapshot_prepare":
		result, err = t.engine.PrepareSnapshot(ctx, a.Snapshot)
	case "snapshot_restore_prepare":
		result, err = t.engine.PrepareSnapshotRestore(ctx, a.ID, a.Digest)
	case "apply_snapshot":
		result, err = t.engine.ApplySnapshot(ctx, a.ID, a.Digest)
	case "recover_snapshot":
		result, err = t.engine.RecoverSnapshot(ctx, a.ID, a.Digest)
	case "prepare":
		result, err = t.engine.Prepare(ctx, recovery.Request{Changes: a.Changes, Service: a.Service, SSHService: a.SSHService})
	case "services":
		result = t.engine.Services()
	case "list":
		result, err = t.engine.List(ctx)
	case "inspect":
		result, err = t.engine.Inspect(ctx, a.ID)
	case "apply":
		result, err = t.engine.Apply(ctx, a.ID, a.Digest)
	case "recover":
		result, err = t.engine.Recover(ctx, a.ID, a.Digest)
	case "apply_service":
		result, err = t.engine.ApplyService(ctx, a.ID, a.Digest)
	case "recover_service":
		result, err = t.engine.RecoverService(ctx, a.ID, a.Digest)
	case "reconcile":
		result, err = t.engine.Reconcile(ctx, a.ID)
	}
	if op, ok := result.(recovery.Operation); ok {
		if a.Action == "apply" || a.Action == "recover" || a.Action == "reconcile" || strings.HasSuffix(a.Action, "_service") || strings.HasSuffix(a.Action, "_snapshot") || strings.HasSuffix(a.Action, "_ssh") {
			result = struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Digest  string `json:"digest"`
				Target  string `json:"target"`
				Files   int    `json:"files"`
				Problem string `json:"problem,omitempty"`
			}{op.ID, op.Status, op.Digest, op.Target, len(op.Plan.Entries), op.Problem}
		} else {
			result = struct {
				ID     string                `json:"id"`
				Status string                `json:"status"`
				Digest string                `json:"digest"`
				Target string                `json:"target"`
				Plan   recovery.Manifest     `json:"plan"`
				Checks []state.RecoveryCheck `json:"checks,omitempty"`
			}{op.ID, op.Status, op.Digest, op.Target, op.Plan, op.Checks}
		}
	}
	b, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return "", marshalErr
	}
	// Return the persisted status even when execution failed. Callers must not
	// equate an error with proof that no changes occurred.
	return string(b), err
}
