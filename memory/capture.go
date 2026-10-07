package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/coolcake/cvkeharness/state"
)

// EndpointCapture is runtime-owned evidence, independent of command completion.
type EndpointCapture struct {
	Status   string `json:"status"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Reason   string `json:"reason,omitempty"`
}

func (r EndpointCapture) Saved() bool { return r.Status == "saved" || r.Status == "unchanged" }
func (r EndpointCapture) Summary() string {
	switch r.Status {
	case "saved":
		return fmt.Sprintf("Remembered: %s → %s.", r.Name, r.Endpoint)
	case "unchanged":
		return fmt.Sprintf("Already remembered: %s → %s.", r.Name, r.Endpoint)
	case "needs_clarification":
		return fmt.Sprintf("Address not changed: clarify whether %s should now refer to %s.", r.Name, r.Endpoint)
	default:
		return "Address save could not be verified: " + r.Reason
	}
}
func (m *Manager) CaptureUserEndpoint(ctx context.Context, message, turnID string) EndpointCapture {
	d, ok := ParseEndpointDeclaration(message)
	r := EndpointCapture{Status: "failed", Name: d.Name, Endpoint: d.Endpoint}
	if !ok {
		r.Reason = "no direct endpoint declaration"
		return r
	}
	if m.store == nil || !m.store.Available() {
		r.Reason = "endpoint database unavailable"
		return r
	}
	// Validate existing records before accepting an unchanged result.
	if _, err := m.UserEndpoints(ctx); err != nil {
		r.Reason = err.Error()
		return r
	}
	item := state.UserEndpoint{Name: d.Name, Endpoint: d.Endpoint, Declaration: strings.TrimSpace(message), DeclaredAt: m.now()}
	item.EvidenceHash = endpointIntegrity(item)
	status, err := m.store.CaptureUserEndpoint(ctx, item, turnID)
	if err != nil {
		r.Reason = err.Error()
		return r
	}
	r.Status = status
	if status == "needs_clarification" {
		return r
	}
	items, err := m.UserEndpoints(ctx)
	if err == nil {
		for _, saved := range items {
			if saved.Name == d.Name && saved.Endpoint == d.Endpoint {
				return r
			}
		}
	}
	r.Status = "failed"
	r.Reason = "canonical readback could not verify the saved address"
	return r
}
