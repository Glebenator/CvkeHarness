package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
)

func TestCalculatorUsesDeterministicRegistryPath(t *testing.T) {
	policy, err := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.ConfigureSecurity(policy, NewBlockingApprover(), nil)
	r.Register(&CalculateTool{})
	call := provider.ToolCall{ID: "calculation"}
	call.Function.Name = "safety_calculate"
	call.Function.Arguments = `{"operation":"multiply","left":{"value":"75","unit":"%"},"right":{"value":"8","unit":"GiB"},"output_unit":"MiB","rounding":"exact","expected":"6144"}`
	got, err := r.ExecuteTool(context.Background(), call)
	if err != nil || !strings.Contains(got, `"value":"6144"`) {
		t.Fatalf("calculation: %s / %v", got, err)
	}
	call.Function.Arguments = strings.Replace(call.Function.Arguments, `"expected":"6144"`, `"expected":"6000"`, 1)
	got, err = r.ExecuteTool(context.Background(), call)
	if err == nil || !strings.Contains(got, `"expected_matches":false`) {
		t.Fatalf("wrong model math did not fail: %s / %v", got, err)
	}
	if _, err := (&CalculateTool{}).Execute(context.Background(), json.RawMessage(`{"command":"rm -rf /"}`)); err == nil {
		t.Fatal("calculator accepted unknown executable arguments")
	}
}
