package shellpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const corpusPath = "testdata/safety/shell_policy_cases.json"

type Decision string

const (
	DecisionAllow           Decision = "allow"
	DecisionDeny            Decision = "deny"
	DecisionRequireApproval Decision = "require_approval"
)

type Case struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Description      string   `json:"description"`
	Command          string   `json:"command"`
	AllowedCommands  []string `json:"allowed_commands,omitempty"`
	ExpectedDecision Decision `json:"expected_decision"`
}

func LoadCorpus() ([]Case, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("failed to locate shell policy corpus loader")
	}

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, corpusPath))
	if err != nil {
		return nil, fmt.Errorf("read shell policy corpus: %w", err)
	}

	var cases []Case
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("parse shell policy corpus: %w", err)
	}
	return cases, nil
}
