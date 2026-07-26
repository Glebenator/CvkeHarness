package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
)

// ShellTool runs restricted shell commands on the host.
type ShellTool struct {
	allowedCommands  map[string]bool
	timeout          time.Duration
	approver         ShellApprover
	primaryModel     string
	approvalStore    *state.Store
	approvalRequired bool
	unrestricted     bool
}

// ShellArgs represents the LLM-provided arguments for the shell tool.
type ShellArgs struct {
	Command string `json:"command"`
}

// ShellSegment is one shell command segment between control operators.
type ShellSegment struct {
	Command    string
	Normalized string
}

// ParsedShellCommand captures the supported chained command structure.
type ParsedShellCommand struct {
	Segments  []ShellSegment
	Operators []string
}

type streamCaptureWriter struct {
	ctx       context.Context
	limit     int
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func newStreamCaptureWriter(ctx context.Context, limit int) *streamCaptureWriter {
	return &streamCaptureWriter{
		ctx:   ctx,
		limit: limit,
	}
}

func (w *streamCaptureWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	EmitEvent(w.ctx, Event{
		Type:   EventShellOutput,
		Output: string(p),
	})

	w.mu.Lock()
	defer w.mu.Unlock()

	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buf.Write(p[:remaining])
		w.truncated = true
		return len(p), nil
	}
	_, _ = w.buf.Write(p)
	return len(p), nil
}

func (w *streamCaptureWriter) Result() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	result := w.buf.String()
	if w.truncated {
		result += "\n... (output truncated)"
	}
	return result
}

// NewShellTool creates a shell tool constrained to an allowlist and LLM judge.
func NewShellTool(allowed []string, judge provider.Provider, safetyModel, primaryModel string) *ShellTool {
	return NewShellToolWithApprovalStore(allowed, NewLLMJudgeApprover(judge, safetyModel), primaryModel, nil)
}

// NewShellToolWithApprover creates a shell tool with a configurable approval path.
func NewShellToolWithApprover(allowed []string, approver ShellApprover, primaryModel string) *ShellTool {
	return NewShellToolWithApprovalStore(allowed, approver, primaryModel, nil)
}

// NewShellToolWithApprovalStore creates a shell tool whose reusable approvals
// come only from the target-scoped, expiring state store.
func NewShellToolWithApprovalStore(allowed []string, approver ShellApprover, primaryModel string, approvalStore *state.Store) *ShellTool {
	amap := make(map[string]bool)
	for _, a := range allowed {
		amap[a] = true
	}
	return &ShellTool{
		allowedCommands: amap,
		timeout:         30 * time.Duration(time.Second),
		approver:        approver,
		primaryModel:    primaryModel,
		approvalStore:   approvalStore,
	}
}

func (s *ShellTool) Name() string {
	return "shell_execute"
}

func (s *ShellTool) Description() string {
	return "Executes shell commands on the host via an allowlist plus a configurable approval gate."
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

// ValidateShellCommand rejects unsupported shell syntax while allowing simple
// command chaining through &&, ||, ;, and pipelines.
func ValidateShellCommand(command string) error {
	_, err := ParseShellCommand(command)
	return err
}

// ValidateAllowedShellCommand applies syntax checks plus the shell tool's
// command allowlist without executing anything.
func ValidateAllowedShellCommand(command string, allowed []string) error {
	allowedCommands := make(map[string]bool, len(allowed))
	for _, item := range allowed {
		allowedCommands[item] = true
	}

	_, err := validateAllowedShellCommand(command, allowedCommands, nil)
	return err
}

func validateAllowedShellCommand(command string, allowedCommands, approvedCommands map[string]bool) (ParsedShellCommand, error) {
	parsed, err := ParseShellCommand(command)
	if err != nil {
		return ParsedShellCommand{}, err
	}

	for idx, segment := range parsed.Segments {
		if _, err := validateShellSegment(segment, allowedCommands, approvedCommands); err != nil {
			if len(parsed.Segments) > 1 {
				return ParsedShellCommand{}, fmt.Errorf("command segment %d %q is not in the auto-approved command list", idx+1, segment.Normalized)
			}
			return ParsedShellCommand{}, err
		}
	}

	return parsed, nil
}

func validateShellSegment(segment ShellSegment, allowedCommands, approvedCommands map[string]bool) (string, error) {
	parts := strings.Fields(segment.Command)
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

	if allowedCommands[baseCmd] {
		if baseCmd == "journalctl" && !safeJournalctlSegment(segment.Command) {
			return "", fmt.Errorf("journalctl mutation flags require explicit approval")
		}
		return baseCmd, nil
	}
	if approvedCommands != nil && approvedCommands[segment.Normalized] {
		return baseCmd, nil
	}

	return "", fmt.Errorf("command %q is not in the auto-approved command list", segment.Normalized)
}

func safeJournalctlSegment(command string) bool {
	if strings.ContainsAny(command, `\'"`) {
		return false
	}
	lower := strings.ToLower(command)
	for _, flag := range []string{"--rotate", "--vacuum", "--flush", "--sync", "--relinquish-var"} {
		if strings.Contains(lower, flag) {
			return false
		}
	}
	return true
}

// ParseShellCommand tokenizes a shell command into approved segments and
// control operators while rejecting unsupported shell features.
func ParseShellCommand(command string) (ParsedShellCommand, error) {
	if strings.ContainsAny(command, "\n\r") {
		for _, ch := range command {
			if ch == '\n' || ch == '\r' {
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", string(ch))
			}
		}
	}

	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ParsedShellCommand{}, fmt.Errorf("command cannot be empty")
	}

	var parsed ParsedShellCommand
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	flush := func(operator string) error {
		raw := strings.TrimSpace(current.String())
		if raw == "" {
			if operator == "" && len(parsed.Segments) == 0 {
				return fmt.Errorf("command cannot be empty")
			}
			if operator != "" {
				return fmt.Errorf("malformed shell command near %q", operator)
			}
			return nil
		}
		parsed.Segments = append(parsed.Segments, ShellSegment{
			Command:    raw,
			Normalized: normalizeShellWhitespace(raw),
		})
		current.Reset()
		if operator != "" {
			parsed.Operators = append(parsed.Operators, operator)
		}
		return nil
	}

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		switch {
		case escaped:
			current.WriteByte(ch)
			escaped = false
			continue
		case inSingle:
			current.WriteByte(ch)
			if ch == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			switch ch {
			case '\n', '\r':
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", string(ch))
			case '`':
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "`")
			case '$':
				if i+1 < len(cmd) && cmd[i+1] == '(' {
					return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "$(")
				}
			case '\\':
				current.WriteByte(ch)
				escaped = true
				continue
			case '"':
				inDouble = false
			}
			current.WriteByte(ch)
			continue
		}

		switch ch {
		case ' ', '\t':
			current.WriteByte(ch)
		case '\\':
			current.WriteByte(ch)
			escaped = true
		case '\'':
			current.WriteByte(ch)
			inSingle = true
		case '"':
			current.WriteByte(ch)
			inDouble = true
		case '\n', '\r':
			return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", string(ch))
		case '`':
			return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "`")
		case '$':
			if i+1 < len(cmd) && cmd[i+1] == '(' {
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "$(")
			}
			current.WriteByte(ch)
		case '>':
			return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", ">")
		case '<':
			return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "<")
		case '|':
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				if err := flush("||"); err != nil {
					return ParsedShellCommand{}, err
				}
				i++
				continue
			}
			if err := flush("|"); err != nil {
				return ParsedShellCommand{}, err
			}
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				if err := flush("&&"); err != nil {
					return ParsedShellCommand{}, err
				}
				i++
				continue
			}
			return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "&")
		case ';':
			if err := flush(";"); err != nil {
				return ParsedShellCommand{}, err
			}
		default:
			current.WriteByte(ch)
		}
	}

	if escaped {
		return ParsedShellCommand{}, fmt.Errorf("unterminated escape sequence")
	}
	if inSingle || inDouble {
		return ParsedShellCommand{}, fmt.Errorf("unterminated quoted string")
	}
	if err := flush(""); err != nil {
		return ParsedShellCommand{}, err
	}
	if len(parsed.Operators) >= len(parsed.Segments) {
		return ParsedShellCommand{}, fmt.Errorf("malformed shell command near %q", parsed.Operators[len(parsed.Operators)-1])
	}

	return parsed, nil
}

func normalizeShellWhitespace(segment string) string {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" {
		return ""
	}

	var normalized strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	pendingSpace := false

	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]

		switch {
		case escaped:
			if pendingSpace && normalized.Len() > 0 {
				normalized.WriteByte(' ')
				pendingSpace = false
			}
			normalized.WriteByte(ch)
			escaped = false
			continue
		case inSingle:
			normalized.WriteByte(ch)
			if ch == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			normalized.WriteByte(ch)
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}

		switch ch {
		case ' ', '\t':
			pendingSpace = normalized.Len() > 0
		case '\\':
			if pendingSpace && normalized.Len() > 0 {
				normalized.WriteByte(' ')
				pendingSpace = false
			}
			normalized.WriteByte(ch)
			escaped = true
		case '\'':
			if pendingSpace && normalized.Len() > 0 {
				normalized.WriteByte(' ')
				pendingSpace = false
			}
			normalized.WriteByte(ch)
			inSingle = true
		case '"':
			if pendingSpace && normalized.Len() > 0 {
				normalized.WriteByte(' ')
				pendingSpace = false
			}
			normalized.WriteByte(ch)
			inDouble = true
		default:
			if pendingSpace && normalized.Len() > 0 {
				normalized.WriteByte(' ')
				pendingSpace = false
			}
			normalized.WriteByte(ch)
		}
	}

	return strings.TrimSpace(normalized.String())
}

func (s *ShellTool) Execute(ctx context.Context, args json.RawMessage) (resultStr string, execErr error) {
	logger := log.FromContext(ctx)

	var parsedArgs ShellArgs
	if err := json.Unmarshal(args, &parsedArgs); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	cmdStr := strings.TrimSpace(parsedArgs.Command)

	start := time.Now()
	approvalMode := "allowlist"
	historyNote := ""
	exitCode := 0
	exitCodeKnown := false

	if cmdStr != "" {
		EmitEvent(ctx, Event{
			Type:    EventShellCommandStarted,
			Command: cmdStr,
			Success: true,
		})
		defer func() {
			EmitEvent(ctx, Event{
				Type:          EventShellCommandFinished,
				Command:       cmdStr,
				ApprovalMode:  approvalMode,
				Success:       execErr == nil,
				ExitCode:      exitCode,
				ExitCodeKnown: exitCodeKnown,
				Duration:      time.Since(start),
				ErrorMessage:  errorString(execErr),
			})
		}()
	}

	parsedCommand, err := ParseShellCommand(cmdStr)
	if err != nil {
		return "", fmt.Errorf("security violation: %w", err)
	}

	if s.unrestricted {
		approvalMode = SafetyModeUnrestricted
	} else {
		approvedCommands := s.scopedApprovedCommands(ctx, parsedCommand)
		_, err = validateAllowedShellCommand(cmdStr, s.allowedCommands, approvedCommands)
	}
	if !s.unrestricted && (err != nil || s.approvalRequired) {
		validationMessage := "safety mode requires user approval before every command"
		if err != nil {
			validationMessage = err.Error()
		}
		logger.Warn("command not in auto-approved command list, asking approval gate", "command", cmdStr)
		payload, _ := json.Marshal(map[string]any{
			"tool_name": "shell_execute",
			"command":   cmdStr,
			"reason":    validationMessage,
		})
		_ = telemetry.Record(ctx, telemetry.Event{
			Type:    telemetry.EventApprovalRequested,
			Payload: payload,
		})
		if s.approver == nil {
			if err != nil {
				return "", fmt.Errorf("security violation: %w (and no approval path is configured)", err)
			}
			return "", fmt.Errorf("security violation: %s (and no approval path is configured)", validationMessage)
		}

		fields := telemetry.FieldsFromContext(ctx)
		targetEnvironment := ""
		if s.approvalStore != nil && s.approvalStore.Available() && fields.TargetID != "" {
			if target, targetErr := s.approvalStore.GetTarget(ctx, fields.TargetID); targetErr == nil {
				targetEnvironment = target.Environment
			}
		}
		action := ""
		if len(parsedCommand.Segments) == 1 {
			action = ShellSegmentAction(parsedCommand.Segments[0])
		}
		decision, approvalErr := s.approver.Approve(ctx, ShellApprovalRequest{
			Command:         cmdStr,
			TargetID:        fields.TargetID,
			Environment:     targetEnvironment,
			Action:          action,
			ValidationError: validationMessage,
		})
		if approvalErr != nil {
			return "", approvalErr
		}
		if !decision.Approved {
			return "", fmt.Errorf("safety constraint violated: approval gate did not approve command")
		}
		if decision.Mode == SafetyModeLLMJudge &&
			(fields.TargetID == "" || targetEnvironment == "" || targetEnvironment == state.EnvironmentUnknown) {
			return "", fmt.Errorf("safety constraint violated: LLM approval cannot authorize a command for an unresolved or ambiguous target")
		}
		approvalMode = decision.Mode
		historyNote = strings.TrimSpace(decision.HistoryNote)
		logger.Info("command approved by secondary gate", "command", cmdStr, "mode", decision.Mode, "remember", decision.Remember)
		if decision.Remember && !s.approvalRequired {
			s.rememberApprovedSegments(ctx, parsedCommand, decision)
		}
	}
	EmitEvent(ctx, Event{
		Type:         EventShellApproval,
		Command:      cmdStr,
		ApprovalMode: approvalMode,
		Success:      true,
	})
	payload, _ := json.Marshal(map[string]any{
		"tool_name":     "shell_execute",
		"command":       cmdStr,
		"approval_mode": approvalMode,
	})
	_ = telemetry.Record(ctx, telemetry.Event{
		Type:    telemetry.EventApprovalResolved,
		Payload: payload,
	})

	logger.Info("executing shell command", "command", cmdStr)

	// Wrap context with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", cmdStr)
	const maxOutput = 4096
	stream := newStreamCaptureWriter(ctx, maxOutput)
	cmd.Stdout = stream
	cmd.Stderr = stream

	err = cmd.Run()
	if cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			exitCode = code
			exitCodeKnown = true
		}
	}
	result := stream.Result()
	if historyNote != "" {
		if result == "" {
			result = historyNote
		} else {
			result = historyNote + "\n\n" + result
		}
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return result, fmt.Errorf("command timed out after %v: %s", s.timeout, result)
		}
		return result, fmt.Errorf("command exited with error: %w. Output: %s", err, result)
	}

	return result, nil
}

func (s *ShellTool) rememberApprovedSegments(ctx context.Context, parsed ParsedShellCommand, decision ShellApprovalDecision) {
	if decision.Mode != SafetyModeUserConfirm && decision.Mode != SafetyModeUserConfirmAll {
		return
	}
	if s.approvalStore == nil || !s.approvalStore.Available() {
		return
	}
	targetID := telemetry.FieldsFromContext(ctx).TargetID
	target, err := s.approvalStore.GetTarget(ctx, targetID)
	now := time.Now().UTC()
	if err != nil ||
		target.Status != state.MemoryStatusActive ||
		target.Environment == "" ||
		target.Environment == state.EnvironmentUnknown ||
		target.ExpiresAt.IsZero() ||
		!target.ExpiresAt.After(now) ||
		(target.Kind != "runtime" && target.RemoteIdentity == "") {
		log.FromContext(ctx).Warn("approval was not remembered because target scope is unresolved", "target_id", targetID)
		return
	}
	rationale := strings.TrimSpace(decision.HistoryNote)
	if rationale == "" {
		rationale = "approved after secondary review"
	}

	for _, segment := range parsed.Segments {
		if segment.Normalized == "" || !ReusableShellSegment(segment) {
			continue
		}
		if err := s.approvalStore.SaveCommandApproval(ctx, state.CommandApproval{
			TargetID:       target.ID,
			Environment:    target.Environment,
			RemoteIdentity: target.RemoteIdentity,
			Command:        segment.Normalized,
			Action:         ShellSegmentAction(segment),
			Status:         state.ApprovalStatusApproved,
			Source:         "user_confirm",
			Rationale:      rationale,
			PolicyVersion:  state.CommandApprovalPolicyVersion,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
		}); err != nil {
			log.FromContext(ctx).Warn("failed to persist command approval", "command", segment.Normalized, "error", err)
		}
	}
}

func (s *ShellTool) scopedApprovedCommands(ctx context.Context, parsed ParsedShellCommand) map[string]bool {
	out := make(map[string]bool)
	if s.approvalStore == nil || !s.approvalStore.Available() {
		return out
	}
	targetID := telemetry.FieldsFromContext(ctx).TargetID
	if targetID == "" {
		return out
	}
	target, err := s.approvalStore.GetTarget(ctx, targetID)
	now := time.Now().UTC()
	if err != nil ||
		target.Status != state.MemoryStatusActive ||
		target.Environment == state.EnvironmentUnknown ||
		target.RemoteIdentity == "" ||
		target.ExpiresAt.IsZero() ||
		!target.ExpiresAt.After(now) {
		return out
	}
	approvals, err := s.approvalStore.ListApprovedCommandApprovals(ctx, target.ID, target.Environment, now)
	if err != nil {
		return out
	}
	for _, approval := range approvals {
		for _, segment := range parsed.Segments {
			if approval.RemoteIdentity == target.RemoteIdentity &&
				segment.Normalized == approval.Command &&
				ShellSegmentAction(segment) == approval.Action &&
				ReusableShellSegment(segment) {
				out[segment.Normalized] = true
			}
		}
	}
	return out
}

// ReusableShellSegment reports whether an exact command segment is stable
// enough for a short-lived target-scoped approval. Runtime interpolation is
// intentionally excluded because the same text could point at another host.
func ReusableShellSegment(segment ShellSegment) bool {
	if segment.Normalized == "" || strings.ContainsAny(segment.Command, "$`*?[]{}~") {
		return false
	}
	fields := strings.Fields(segment.Command)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "echo", "systemctl", "service", "docker", "podman", "apt", "apt-get", "dnf", "yum", "apk", "brew", "pacman":
		return true
	default:
		return false
	}
}

// ShellSegmentAction returns the bounded action label used by scoped approvals.
func ShellSegmentAction(segment ShellSegment) string {
	fields := strings.Fields(segment.Command)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 1 && (fields[0] == "systemctl" || fields[0] == "journalctl") {
		return fields[0] + " " + fields[1]
	}
	return fields[0]
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
