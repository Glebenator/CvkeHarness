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
)

// ShellTool runs restricted shell commands on the host.
type ShellTool struct {
	allowedCommands map[string]bool
	timeout         time.Duration
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

// NewShellTool creates a shell tool constrained to an allowlist.
func NewShellTool(allowed []string) *ShellTool {
	amap := make(map[string]bool)
	for _, a := range allowed {
		amap[a] = true
	}
	return &ShellTool{
		allowedCommands: amap,
		timeout:         30 * time.Duration(time.Second),
	}
}

func (s *ShellTool) Name() string {
	return "shell_execute"
}

func (s *ShellTool) Description() string {
	return "Executes a safe, read-only shell command on the host (e.g. df, free, uptime, ps, netstat)."
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
	_, err := validateAllowedShellCommand(cmdStr, s.allowedCommands)
	if err != nil {
		logger.Warn("rejected unsafe shell command", "command", cmdStr, "error", err)
		return "", fmt.Errorf("security violation: %w", err)
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
