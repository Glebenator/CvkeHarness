package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/coolcake/cvkeharness/recovery"
)

type RecoveryFleetTool struct{ engine *recovery.Engine }

func NewRecoveryFleetTool(e *recovery.Engine) *RecoveryFleetTool { return &RecoveryFleetTool{e} }
func (t *RecoveryFleetTool) Name() string                        { return "recovery_fleet" }
func (t *RecoveryFleetTool) Description() string {
	return "Controller for operator-enrolled remote CvkeHarness executors. hosts lists pinned SSH targets. prepare takes exact host/id/digest references already prepared on each target, inspects their immutable file or NGINX manifests, and enforces aggregate host/file/byte/service limits. Review the returned batch and exact digest before apply. Hosts run serially; any uncertain or failed outcome stops rollout. Never retry apply on a dispatched batch. reconcile only inspects remote journals and never resumes. recover restores dispatched operations in reverse order with a durable per-target repair-attempt ceiling; conflicting writers are preserved. inspect/list show saved evidence without contacting targets. No model/provider is needed for the same recovery fleet CLI. The active context must be this local controller; this tool explicitly names enrolled remote hosts and never applies their changes locally. SSH listener changes and snapshots are currently outside batch coverage."
}
func (t *RecoveryFleetTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["hosts","list","prepare","inspect","apply","recover","reconcile"]},"id":{"type":"string"},"digest":{"type":"string"},"operations":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","properties":{"host":{"type":"string"},"id":{"type":"string"},"digest":{"type":"string"}},"required":["host","id","digest"],"additionalProperties":false}}},"required":["action"],"additionalProperties":false}`)
}

type FleetArguments struct {
	Action     string                    `json:"action"`
	ID         string                    `json:"id,omitempty"`
	Digest     string                    `json:"digest,omitempty"`
	Operations []recovery.FleetReference `json:"operations,omitempty"`
}

func decodeFleet(raw json.RawMessage) (FleetArguments, error) {
	var a FleetArguments
	if len(raw) > 64<<10 {
		return a, fmt.Errorf("fleet request exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return a, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return a, fmt.Errorf("unexpected trailing fleet arguments")
	}
	switch a.Action {
	case "hosts", "list":
		if a.ID != "" || a.Digest != "" || len(a.Operations) != 0 {
			return a, fmt.Errorf("discovery takes no operation arguments")
		}
	case "prepare":
		if a.ID != "" || a.Digest != "" || len(a.Operations) < 1 || len(a.Operations) > 32 {
			return a, fmt.Errorf("prepare takes only bounded exact target references")
		}
	case "inspect", "reconcile":
		if a.ID == "" || a.Digest != "" || len(a.Operations) != 0 {
			return a, fmt.Errorf("inspection requires only a batch ID")
		}
	case "apply", "recover":
		if a.ID == "" || a.Digest == "" || len(a.Operations) != 0 {
			return a, fmt.Errorf("dispatch requires only exact batch ID and digest")
		}
	default:
		return a, fmt.Errorf("unknown fleet action")
	}
	return a, nil
}
func (t *RecoveryFleetTool) Review(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := decodeFleet(raw)
	if err != nil {
		return "", err
	}
	if err = (&RecoveryManageTool{t.engine}).validateTarget(ctx); err != nil {
		return "", err
	}
	if a.Action != "apply" && a.Action != "recover" {
		return "", nil
	}
	b, err := t.engine.InspectBatch(ctx, a.ID)
	if err != nil {
		return "", err
	}
	if a.Digest != b.Digest {
		return "", fmt.Errorf("reviewed batch digest does not match")
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s batch %s from controller %s (%s)\nImpact: %d hosts, %d files, %d original+candidate bytes, %d services; serial dispatch; at most %d repair attempts per target.\n", a.Action, b.ID, b.Target, b.Status, b.Plan.Impact.Hosts, b.Plan.Impact.Files, b.Plan.Impact.Bytes, b.Plan.Impact.Services, b.Plan.Limits.MaxRepairAttempts)
	for i, en := range b.Plan.Entries {
		fmt.Fprintf(&out, "Host %s %s:%d / executor %s: operation %s digest %s; recorded %s; repairs %d\n", en.Host.Config.Name, en.Host.Config.Address, en.Host.Config.Port, en.Operation.Target, en.Operation.ID, en.Operation.Digest, b.Outcomes[i].State, b.Outcomes[i].Repairs)
		for _, file := range en.Operation.Plan.Entries {
			fmt.Fprintf(&out, "  %s %s: %d -> %d bytes\n", file.Action, file.Path, file.Before.Bytes, file.After.Bytes)
		}
		if s := en.Operation.Plan.Service; s != nil {
			fmt.Fprintf(&out, "  NGINX %s: bound master %d, fixed health %s status %d SHA256 %s; target-local bounded rollback\n", s.Config.Name, s.Master.PID, s.Config.HealthURL, s.Config.HealthStatus, s.Config.HealthSHA256)
		}
	}
	return out.String(), nil
}
func (t *RecoveryFleetTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := decodeFleet(raw)
	if err != nil {
		return "", err
	}
	if err = (&RecoveryManageTool{t.engine}).validateTarget(ctx); err != nil {
		return "", err
	}
	var result any
	switch a.Action {
	case "hosts":
		result = t.engine.FleetHosts()
	case "list":
		result, err = t.engine.ListBatches(ctx)
	case "prepare":
		result, err = t.engine.PrepareBatch(ctx, a.Operations)
	case "inspect":
		result, err = t.engine.InspectBatch(ctx, a.ID)
	case "apply":
		result, err = t.engine.ApplyBatch(ctx, a.ID, a.Digest)
	case "recover":
		result, err = t.engine.RecoverBatch(ctx, a.ID, a.Digest)
	case "reconcile":
		result, err = t.engine.ReconcileBatch(ctx, a.ID)
	}
	b, mErr := json.Marshal(result)
	if mErr != nil {
		return "", mErr
	}
	return string(b), err
}
