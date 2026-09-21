package recovery

import (
	"context"
	"encoding/json"
	"fmt"
)

// ReserveModelRepair is called after the tool's exact scoped authorization but
// before repair dispatch. Every failed/uncertain attempt consumes a durable
// slot. CLI operator recovery stays available when this automation budget is
// exhausted; models cannot raise it through tool arguments.
func (e *Engine) ReserveModelRepair(ctx context.Context, id, digest string) error {
	unlock, err := e.locked()
	if err != nil {
		return err
	}
	defer unlock()
	op, err := e.Inspect(ctx, id)
	if err != nil {
		return err
	}
	if digest != op.Digest {
		return fmt.Errorf("repair digest does not match")
	}
	if op.Status == Recovered {
		return nil
	}
	limit := func(n int) int {
		if n == 0 {
			return 2
		}
		return n
	}
	ceiling := min(limit(e.limits.MaxRepairAttempts), limit(op.Plan.Limits.MaxRepairAttempts))
	used := 0
	for _, c := range op.Checks {
		if c.Kind == "model_repair_attempt" {
			used++
		}
	}
	if used >= ceiling {
		return fmt.Errorf("model repair-attempt budget exhausted; inspect and use the provider-independent operator recovery CLI")
	}
	evidence, _ := json.Marshal(struct{ Attempt, Limit int }{used + 1, ceiling})
	_, err = e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "model_repair_attempt", evidence)
	return err
}
