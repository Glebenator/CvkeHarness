package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/recovery"
)

func TestRecoveryPlatformCalculator(t *testing.T) {
	base := t.TempDir()
	request := filepath.Join(base, "math.json")
	err := os.WriteFile(request, []byte(`{"operation":"multiply","left":{"value":"75","unit":"%"},"right":{"value":"8","unit":"GiB"},"output_unit":"MiB","rounding":"exact","expected":"6144"}`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := newRecoveryCommand()
	command.SetOut(&output)
	command.SetErr(&output)
	stateDir := filepath.Join(base, "unused-state")
	command.SetArgs([]string{"--state", filepath.Join(stateDir, "state.db"), "calculate", "--request", request})
	if err = command.Execute(); err != nil {
		t.Fatalf("calculator must not initialize a recovery executor: %v", err)
	}
	var result struct {
		Value           string `json:"value"`
		Unit            string `json:"unit"`
		ExpectedMatches bool   `json:"expected_matches"`
	}
	if err = json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "6144" || result.Unit != "MiB" || !result.ExpectedMatches {
		t.Fatalf("incorrect checked calculation: %+v", result)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("calculator unexpectedly touched recovery state: %v", err)
	}
}

func TestRecoveryPlatformCLIRefusal(t *testing.T) {
	if recovery.Supported() {
		t.Skip("unsupported-platform CLI contract")
	}
	stateDir := filepath.Join(t.TempDir(), "must-not-be-created")
	var output bytes.Buffer
	command := newRecoveryCommand()
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--state", filepath.Join(stateDir, "state.db"), "identity"})
	if err := command.Execute(); !errors.Is(err, recovery.ErrUnsupportedPlatform) {
		t.Fatalf("expected actionable unsupported-platform error, got %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("unsupported executor touched state: %v", err)
	}
}
