package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
)

// ShellTool runs restricted shell commands on the host.
type ShellTool struct {
	allowedCommands map[string]bool
	timeout         time.Duration
	judge           provider.Provider
	safetyModel     string
	primaryModel    string
}

// ShellArgs represents the LLM-provided arguments for the shell tool.
type ShellArgs struct {
	Command string `json:"command"`
}

var blockedShellFragments = []string{
	"&&",
	"||",
	";",
	"|",
	">",
	"<",
	"`",
	"$(",
	"\n",
	"\r",
	"&",
}

// NewShellTool creates a shell tool constrained to an allowlist and LLM judge.
func NewShellTool(allowed []string, judge provider.Provider, safetyModel, primaryModel string) *ShellTool {
	amap := make(map[string]bool)
	for _, a := range allowed {
		amap[a] = true
	}
	return &ShellTool{
		allowedCommands: amap,
		timeout:         30 * time.Duration(time.Second),
		judge:           judge,
		safetyModel:     safetyModel,
		primaryModel:    primaryModel,
	}
}

func (s *ShellTool) Name() string {
	return "shell_execute"
}

func (s *ShellTool) Description() string {
	return "Executes shell commands on the host via an LLM judge validation buffer."
}

func (s *ShellTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The exact shell command to execute"
			}
		},
		"required": ["command"]
	}`)
}

// ValidateShellCommand rejects obviously unsafe shell syntax before a command
// reaches the host shell.
func ValidateShellCommand(command string) error {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return fmt.Errorf("command cannot be empty")
	}

	for _, fragment := range blockedShellFragments {
		if strings.Contains(cmd, fragment) {
			return fmt.Errorf("blocked shell syntax %q", fragment)
		}
	}

	return nil
}

// ValidateAllowedShellCommand applies syntax checks plus the shell tool's
// command allowlist without executing anything.
func ValidateAllowedShellCommand(command string, allowed []string) error {
	allowedCommands := make(map[string]bool, len(allowed))
	for _, item := range allowed {
		allowedCommands[item] = true
	}

	_, err := validateAllowedShellCommand(command, allowedCommands)
	return err
}

func validateAllowedShellCommand(command string, allowedCommands map[string]bool) (string, error) {
	cmd := strings.TrimSpace(command)
	if err := ValidateShellCommand(cmd); err != nil {
		return "", err
	}

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", fmt.Errorf("command cannot be empty")
	}

	baseCmd := parts[0]

	// Handle composed commands like 'systemctl status' if the composite form is
	// explicitly allowlisted.
	if len(parts) > 1 && (parts[0] == "systemctl" || parts[0] == "journalctl") {
		composite := parts[0] + " " + parts[1]
		if allowedCommands[composite] {
			baseCmd = composite
		}
	}

	if !allowedCommands[baseCmd] {
		return "", fmt.Errorf("command %q is not in the allowlist", baseCmd)
	}

	return baseCmd, nil
}

func (s *ShellTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	logger := log.FromContext(ctx)

	var parsedArgs ShellArgs
	if err := json.Unmarshal(args, &parsedArgs); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	cmdStr := strings.TrimSpace(parsedArgs.Command)
	
	// Record telemetry for this zero-shot tool usage
	// Telemetry recording ignores errors to not degrade core functionality
	_ = telemetry.RecordCommand(s.primaryModel, cmdStr)

	_, err := validateAllowedShellCommand(cmdStr, s.allowedCommands)
	if err != nil {
		// Not auto-approved. Ask LLM Judge.
		logger.Warn("command not in auto-approve list, asking LLM judge", "command", cmdStr)
		if s.judge == nil || s.safetyModel == "" {
			return "", fmt.Errorf("security violation: %w (and LLM judge is not configured)", err)
		}
		
		prompt := fmt.Sprintf("An automated DevOps agent wants to run the following bash command:\n`%s`\nIs this command safe (will not permanently delete vital data, alter kernel, or install clearly malicious software)? Reply strictly with 'SAFE' or 'DANGEROUS'. Provide no other output.", cmdStr)
		
		req := &provider.ChatRequest{
			Model:       s.safetyModel,
			Messages:    []provider.Message{{Role: "user", Content: prompt}},
			Temperature: 0.0,
			MaxTokens:   10,
		}
		
		resp, judgeErr := s.judge.ChatCompletion(ctx, req)
		if judgeErr != nil {
			return "", fmt.Errorf("LLM judge failed to evaluate command: %w\nOriginal safety error: %v", judgeErr, err)
		}
		
		decision := strings.TrimSpace(strings.ToUpper(resp.Message.Content))
		if !strings.Contains(decision, "SAFE") || strings.Contains(decision, "DANGEROUS") {
			logger.Warn("rejected unsafe shell command by judge", "command", cmdStr, "decision", decision)
			return "", fmt.Errorf("safety constraint violated: supervisor model deemed this command dangerous")
		}
		
		logger.Info("command approved by LLM judge", "command", cmdStr)
	}

	logger.Info("executing shell command", "command", cmdStr)

	// Wrap context with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", cmdStr)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	result := out.String()

	// Truncate if output is too long to save token cost and prevent context explosion
	const maxOutput = 4096
	if len(result) > maxOutput {
		result = result[:maxOutput] + "\n... (output truncated)"
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return result, fmt.Errorf("command timed out after %v: %s", s.timeout, result)
		}
		return result, fmt.Errorf("command exited with error: %w. Output: %s", err, result)
	}

	return result, nil
}
