package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/memory"
)

// FindingRecorder captures the subset of memory.Manager used by the tool.
type FindingRecorder interface {
	Dir() string
	PersistLessons(ctx context.Context, lessons []memory.Lesson) error
}

// MemoryRecordFindingTool lets the agent submit a concise candidate finding.
type MemoryRecordFindingTool struct {
	recorder FindingRecorder
}

type memoryRecordFindingArgs struct {
	Body       string  `json:"body"`
	Scope      string  `json:"scope,omitempty"`
	ToolName   string  `json:"tool_name,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// NewMemoryRecordFindingTool creates a tool for writing concise reusable findings.
func NewMemoryRecordFindingTool(recorder FindingRecorder) *MemoryRecordFindingTool {
	return &MemoryRecordFindingTool{recorder: recorder}
}

func (t *MemoryRecordFindingTool) Name() string {
	return "memory_record_finding"
}

func (t *MemoryRecordFindingTool) Description() string {
	return "Submits a concise untrusted memory candidate for operator review. It cannot create active memory, policy, permissions, credentials, host mappings, or command approvals."
}

func (t *MemoryRecordFindingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"body": {
				"type": "string",
				"description": "A single concise reusable lesson or finding to remember"
			},
			"scope": {
				"type": "string",
				"enum": ["global", "tool"],
				"description": "Use 'tool' for tool-specific findings, otherwise 'global'"
			},
			"tool_name": {
				"type": "string",
				"description": "Required when scope is 'tool'; for example 'shell_execute'"
			},
			"confidence": {
				"type": "number",
				"description": "Confidence from 0.0 to 1.0"
			}
		},
		"required": ["body"]
	}`)
}

func (t *MemoryRecordFindingTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if t.recorder == nil {
		return "", fmt.Errorf("memory recorder is unavailable")
	}

	var input memoryRecordFindingArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	body := strings.TrimSpace(input.Body)
	if body == "" {
		return "", fmt.Errorf("body is required")
	}

	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		if strings.TrimSpace(input.ToolName) != "" {
			scope = "tool"
		} else {
			scope = "global"
		}
	}
	if scope != "global" && scope != "tool" {
		return "", fmt.Errorf("scope must be one of: global, tool")
	}
	if scope == "tool" && strings.TrimSpace(input.ToolName) == "" {
		return "", fmt.Errorf("tool_name is required when scope is tool")
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 0.65
	}
	if confidence < 0 || confidence > 1 {
		return "", fmt.Errorf("confidence must be between 0.0 and 1.0")
	}

	lesson := memory.Lesson{
		Body:       body,
		Scope:      scope,
		ToolName:   strings.TrimSpace(input.ToolName),
		Phase:      core.PhaseExecution,
		Confidence: confidence,
	}
	if err := t.recorder.PersistLessons(ctx, []memory.Lesson{lesson}); err != nil {
		return "", err
	}

	findingsPath := filepath.Join(t.recorder.Dir(), memory.FindingsFile)
	return fmt.Sprintf("Submitted memory candidate for review; exported view: %s", findingsPath), nil
}
