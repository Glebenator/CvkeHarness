package recovery

import (
	"context"
	"encoding/json"
	"fmt"
)

// Resource budgets are operator inputs, bound to the reviewed service config.
// They bound configured impact; they are not CPU/RAM consumption predictions.
type NGINXResourceLimits struct {
	MaxWorkers              int `json:"max_workers" yaml:"max_workers"`
	MaxConnectionsPerWorker int `json:"max_connections_per_worker" yaml:"max_connections_per_worker"`
	MaxTotalConnections     int `json:"max_total_connections" yaml:"max_total_connections"`
	MaxResidentWorkers      int `json:"max_resident_workers" yaml:"max_resident_workers"`
	DescriptorReserve       int `json:"descriptor_reserve" yaml:"descriptor_reserve"`
}

func (l NGINXResourceLimits) effective() NGINXResourceLimits {
	if l == (NGINXResourceLimits{}) {
		return NGINXResourceLimits{4, 4096, 8192, 8, 64}
	}
	return l
}
func (l NGINXResourceLimits) validate() error {
	if l.MaxWorkers < 1 || l.MaxWorkers > 16 || l.MaxConnectionsPerWorker < 1 || l.MaxConnectionsPerWorker > 65536 || l.MaxTotalConnections < 1 || l.MaxTotalConnections > 1<<20 || l.MaxResidentWorkers < 2 || l.MaxResidentWorkers > 32 || l.DescriptorReserve < 32 || l.DescriptorReserve > 4096 {
		return fmt.Errorf("invalid NGINX resource limits")
	}
	return nil
}

type NGINXResources struct {
	Workers              int `json:"workers"`
	ConnectionsPerWorker int `json:"connections_per_worker"`
}

func validateNGINXResourceBudget(r NGINXResources, l NGINXResourceLimits) error {
	if r.Workers < 1 || r.Workers > 16 || r.ConnectionsPerWorker < 1 || r.ConnectionsPerWorker > 65536 {
		return fmt.Errorf("NGINX resource manifest is missing or invalid; prepare again")
	}
	// Operands are bounded before multiplication; no model-provided total is used.
	if r.Workers > l.MaxWorkers || r.ConnectionsPerWorker > l.MaxConnectionsPerWorker || r.Workers*r.ConnectionsPerWorker > l.MaxTotalConnections {
		return fmt.Errorf("NGINX candidate exceeds operator worker/connection impact limits")
	}
	return nil
}

type ServiceResourceEvidence struct {
	Passed                bool                `json:"passed"`
	Configured            NGINXResources      `json:"configured"`
	Limits                NGINXResourceLimits `json:"limits"`
	AffinityCPUs          int                 `json:"affinity_cpus"`
	MasterDescriptorLimit uint64              `json:"master_descriptor_limit"`
	ExistingWorkers       int                 `json:"existing_workers"`
	PeakWorkers           int                 `json:"peak_workers"`
}

func evaluateServiceResources(r NGINXResources, l NGINXResourceLimits, cpus, existing int, nofile uint64) ServiceResourceEvidence {
	v := ServiceResourceEvidence{Configured: r, Limits: l, AffinityCPUs: cpus, MasterDescriptorLimit: nofile, ExistingWorkers: existing, PeakWorkers: existing + r.Workers}
	v.Passed = validateNGINXResourceBudget(r, l) == nil && cpus >= r.Workers && existing >= 1 && v.PeakWorkers <= l.MaxResidentWorkers && uint64(r.ConnectionsPerWorker+l.DescriptorReserve) <= nofile
	return v
}
func (e *Engine) checkServiceResources(ctx context.Context, op *Operation) error {
	p := *op.Plan.Service
	cpus, workers, nofile, err := measureServiceResources(p)
	if err != nil {
		return err
	}
	v := evaluateServiceResources(p.Resources, p.Config.ResourceLimits.effective(), cpus, workers, nofile)
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	check, err := e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "service_resources", b)
	if err != nil {
		return err
	}
	op.Checks = append(op.Checks, check)
	if !v.Passed {
		return fmt.Errorf("fresh NGINX resource check refused worker count, reload overlap or descriptor headroom; target config unchanged")
	}
	return e.checkpoint("service_resources_checked", -1)
}
