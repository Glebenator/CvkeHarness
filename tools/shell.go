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
	"github.com/coolcake/cvkeharness/internal/secrets"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

// ShellTool runs restricted shell commands on the host.
type ShellTool struct {
	allowedCommands  map[string]bool
	approvedCommands map[string]bool
	sessionGrants    map[string]bool
	timeout          time.Duration
	approver         ShellApprover
	primaryModel     string
	approvalStore    *state.Store
	approvalRequired bool
	unrestricted     bool
	securityPolicy   *securitypolicy.EffectivePolicy
	humanApprover    ShellApprover
	llmApprover      ShellApprover
	maxOutput        int
}

// ShellArgs represents the LLM-provided arguments for the shell tool.
type ShellArgs struct {
	Command string `json:"command"`
}

// ShellSegment is one shell command segment between control operators.
type ShellSegment struct {
	Command    string
	Normalized string
	Heredoc    bool
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

	w.mu.Lock()
	defer w.mu.Unlock()

	// Keep bounded look-ahead so a secret split at the visible truncation
	// boundary is redacted before any output event is emitted.
	remaining := w.limit + 1024 - w.buf.Len()
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

	result := secrets.Mask(w.buf.String())
	if len(result) > w.limit {
		result = result[:w.limit]
		w.truncated = true
	}
	if w.truncated {
		result += "\n... (output truncated)"
	}
	if result != "" {
		EmitEvent(w.ctx, Event{Type: EventShellOutput, Output: result})
	}
	return result
}

// NewShellTool creates a shell tool constrained to an allowlist and LLM judge.
func NewShellTool(allowed []string, judge provider.Provider, safetyModel, primaryModel string) *ShellTool {
	return NewShellToolWithApprovals(allowed, nil, NewLLMJudgeApprover(judge, safetyModel), primaryModel, nil)
}

// NewShellToolWithApprover creates a shell tool with a configurable approval path.
func NewShellToolWithApprover(allowed []string, approver ShellApprover, primaryModel string) *ShellTool {
	return NewShellToolWithApprovals(allowed, nil, approver, primaryModel, nil)
}

// NewShellToolWithApprovals creates a shell tool with both static and learned
// approved command lists.
func NewShellToolWithApprovals(allowed, approved []string, approver ShellApprover, primaryModel string, approvalStore *state.Store) *ShellTool {
	amap := make(map[string]bool)
	for _, a := range allowed {
		amap[a] = true
	}
	pmap := make(map[string]bool)
	for _, item := range approved {
		normalized := normalizeApprovedShellCommand(item)
		if normalized == "" {
			continue
		}
		pmap[normalized] = true
	}
	return &ShellTool{
		allowedCommands:  amap,
		approvedCommands: pmap,
		sessionGrants:    make(map[string]bool),
		timeout:          30 * time.Duration(time.Second),
		approver:         approver,
		primaryModel:     primaryModel,
		approvalStore:    approvalStore,
		maxOutput:        4096,
	}
}

func (s *ShellTool) applySecurityPolicy(policy securitypolicy.EffectivePolicy, human, llm ShellApprover) {
	copyPolicy := policy
	s.securityPolicy = &copyPolicy
	s.humanApprover = human
	s.llmApprover = llm
	s.timeout = time.Duration(policy.Int(securitypolicy.SettingTimeoutSeconds)) * time.Second
	s.maxOutput = policy.Int(securitypolicy.SettingMaxOutputBytes)
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
// command chaining through newlines, &&, ||, ;, and pipelines.
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
	if !segment.Heredoc {
		if _, redirects, err := tokenizePolicySegment(segment.Command); err != nil {
			return "", err
		} else if len(redirects) > 0 {
			return "", fmt.Errorf("command %q uses redirection and requires effect-aware approval", segment.Normalized)
		}
	}
	parts := strings.Fields(segment.Command)
	if len(parts) == 0 {
		return "", fmt.Errorf("command cannot be empty")
	}

	baseCmd := parts[0]
	if baseCmd == "systemctl" && !systemctlReadOnly(parts[1:]) {
		return "", fmt.Errorf("systemctl mutation requires effect-aware approval")
	}
	if baseCmd == "journalctl" && (hasPrefixArg(parts[1:], "--vacuum") || containsArg(parts[1:], "--rotate", "--sync", "--flush", "--relinquish-var")) {
		return "", fmt.Errorf("journalctl mutation requires effect-aware approval")
	}

	// Handle composed commands like 'systemctl status' if the composite form is
	// explicitly allowlisted.
	if len(parts) > 1 && (parts[0] == "systemctl" || parts[0] == "journalctl") {
		composite := parts[0] + " " + parts[1]
		if allowedCommands[composite] {
			baseCmd = composite
		}
	}

	if allowedCommands[baseCmd] {
		if segment.Heredoc && (approvedCommands == nil || !approvedCommands[segment.Normalized]) {
			return "", fmt.Errorf("command %q uses a quoted heredoc and requires secondary approval", segment.Normalized)
		}
		return baseCmd, nil
	}
	if approvedCommands != nil && approvedCommands[segment.Normalized] {
		return baseCmd, nil
	}

	return "", fmt.Errorf("command %q is not in the auto-approved command list", segment.Normalized)
}

// ParseShellCommand tokenizes a shell command into approved segments and
// control operators while rejecting unsupported shell features. Unquoted line
// breaks are command separators, matching normal shell behavior; line breaks
// inside quotes are preserved as command content.
func ParseShellCommand(command string) (ParsedShellCommand, error) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ParsedShellCommand{}, fmt.Errorf("command cannot be empty")
	}

	var parsed ParsedShellCommand
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	currentHeredoc := false

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
		normalized := normalizeShellWhitespace(raw)
		if currentHeredoc {
			// Heredoc bodies can be whitespace-sensitive programs or data.
			// Remembered approval must bind to the exact body, not a collapsed
			// representation that could alias a different script.
			normalized = raw
		}
		parsed.Segments = append(parsed.Segments, ShellSegment{
			Command:    raw,
			Normalized: normalized,
			Heredoc:    currentHeredoc,
		})
		current.Reset()
		currentHeredoc = false
		if operator != "" {
			parsed.Operators = append(parsed.Operators, operator)
		}
		return nil
	}

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		switch {
		case escaped:
			if ch == '\n' || ch == '\r' {
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "line continuation")
			}
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
			// Treat line breaks like semicolons, but tolerate blank lines and
			// line breaks after another operator (for example, "cmd &&\nnext").
			// A CRLF pair represents one separator.
			if ch == '\r' && i+1 < len(cmd) && cmd[i+1] == '\n' {
				i++
			}
			if strings.TrimSpace(current.String()) != "" {
				if err := flush("\n"); err != nil {
					return ParsedShellCommand{}, err
				}
			}
		case '`':
			return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "`")
		case '$':
			if i+1 < len(cmd) && cmd[i+1] == '(' {
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "$(")
			}
			current.WriteByte(ch)
		case '>':
			if i+1 < len(cmd) && cmd[i+1] == '(' {
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", ">(")
			}
			current.WriteByte(ch)
			if i+1 < len(cmd) && cmd[i+1] == '>' {
				current.WriteByte(cmd[i+1])
				i++
			}
		case '<':
			if i+1 < len(cmd) && cmd[i+1] == '(' {
				return ParsedShellCommand{}, fmt.Errorf("blocked shell syntax %q", "<(")
			}
			if i+1 < len(cmd) && cmd[i+1] == '<' {
				heredoc, end, err := parseQuotedHeredoc(cmd, i)
				if err != nil {
					return ParsedShellCommand{}, err
				}
				current.WriteString(heredoc)
				// The heredoc body is opaque input, so shell-looking text inside it
				// is intentionally not interpreted as another command segment.
				currentHeredoc = true
				i = end
				continue
			}
			current.WriteByte(ch)
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
			if strings.HasSuffix(strings.TrimSpace(current.String()), ">") {
				current.WriteByte(ch)
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

func normalizeApprovedShellCommand(command string) string {
	parsed, err := ParseShellCommand(command)
	if err == nil && len(parsed.Segments) == 1 && len(parsed.Operators) == 0 {
		return parsed.Segments[0].Normalized
	}
	return normalizeShellWhitespace(command)
}

// parseQuotedHeredoc accepts the deliberately narrow heredoc form used to
// pass literal scripts or data to a command: <<'TAG' or <<"TAG", followed by a
// newline and a line containing only TAG. Quoting the delimiter disables shell
// expansion in the body. Other redirection forms remain unsupported.
func parseQuotedHeredoc(command string, start int) (string, int, error) {
	if start+2 >= len(command) || command[start:start+2] != "<<" || command[start+2] == '<' {
		return "", 0, fmt.Errorf("blocked shell syntax %q", "<")
	}

	i := start + 2
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
	if i >= len(command) || (command[i] != '\'' && command[i] != '"') {
		return "", 0, fmt.Errorf("blocked shell syntax %q", "unquoted heredoc")
	}
	quote := command[i]
	delimiterStart := i + 1
	delimiterEnd := strings.IndexByte(command[delimiterStart:], quote)
	if delimiterEnd < 0 {
		return "", 0, fmt.Errorf("unterminated quoted heredoc delimiter")
	}
	delimiterEnd += delimiterStart
	delimiter := command[delimiterStart:delimiterEnd]
	if delimiter == "" || strings.ContainsAny(delimiter, " \t\r\n\\`$<>&|;\"'") {
		return "", 0, fmt.Errorf("unsupported heredoc delimiter %q", delimiter)
	}

	headerEnd := delimiterEnd + 1
	for headerEnd < len(command) && (command[headerEnd] == ' ' || command[headerEnd] == '\t') {
		headerEnd++
	}
	if headerEnd >= len(command) || (command[headerEnd] != '\n' && command[headerEnd] != '\r') {
		return "", 0, fmt.Errorf("quoted heredoc delimiter must end the command line")
	}
	if command[headerEnd] == '\r' && headerEnd+1 < len(command) && command[headerEnd+1] == '\n' {
		headerEnd++
	}

	bodyStart := headerEnd + 1
	lineStart := bodyStart
	for lineStart <= len(command) {
		lineEnd := lineStart
		for lineEnd < len(command) && command[lineEnd] != '\n' && command[lineEnd] != '\r' {
			lineEnd++
		}
		if command[lineStart:lineEnd] == delimiter {
			return command[start:lineEnd], lineEnd - 1, nil
		}
		if lineEnd == len(command) {
			break
		}
		if command[lineEnd] == '\r' && lineEnd+1 < len(command) && command[lineEnd+1] == '\n' {
			lineEnd++
		}
		lineStart = lineEnd + 1
	}

	return "", 0, fmt.Errorf("unterminated quoted heredoc %q", delimiter)
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
		maskedCommand := secrets.Mask(cmdStr)
		EmitEvent(ctx, Event{
			Type:    EventShellCommandStarted,
			Command: maskedCommand,
			Success: true,
		})
		defer func() {
			EmitEvent(ctx, Event{
				Type:          EventShellCommandFinished,
				Command:       maskedCommand,
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

	if s.securityPolicy != nil {
		assessment, policyErr := AssessShellCommand(cmdStr, *s.securityPolicy)
		if policyErr != nil {
			return "", fmt.Errorf("security violation: %w", policyErr)
		}
		approvalMode = "profile:" + string(s.securityPolicy.Profile)
		if assessment.Decision == securitypolicy.DecisionDeny {
			return "", fmt.Errorf("security violation: %s", assessment.Reason)
		}
		if (assessment.Decision == securitypolicy.DecisionAsk || assessment.Decision == securitypolicy.DecisionLLMReview) && s.approvalStore != nil && s.approvalStore.Available() {
			grant, _, grantErr := shellSecurityGrantBinding(cmdStr, *s.securityPolicy)
			if grantErr != nil {
				return "", fmt.Errorf("security grant binding: %w", grantErr)
			}
			consumed, consumeErr := s.approvalStore.ConsumeSecurityActionGrant(ctx, grant, time.Now().UTC())
			if consumeErr != nil {
				return "", fmt.Errorf("security grant lookup: %w", consumeErr)
			}
			if consumed {
				assessment.Decision = securitypolicy.DecisionAllow
				approvalMode = "scoped_one_time_grant"
			}
		}
		if assessment.Decision == securitypolicy.DecisionAsk {
			grant, _, grantErr := shellSecurityGrantBinding(cmdStr, *s.securityPolicy)
			if grantErr != nil {
				return "", fmt.Errorf("session grant binding: %w", grantErr)
			}
			if s.sessionGrants[grant.Digest] {
				assessment.Decision = securitypolicy.DecisionAllow
				approvalMode = "session_scoped_grant"
			}
		}
		if assessment.Decision == securitypolicy.DecisionAsk || assessment.Decision == securitypolicy.DecisionLLMReview {
			approver := s.humanApprover
			requestGrant, _, grantErr := shellSecurityGrantBinding(cmdStr, *s.securityPolicy)
			if grantErr != nil {
				return "", fmt.Errorf("security grant binding: %w", grantErr)
			}
			request := ShellApprovalRequest{
				Command:         cmdStr,
				ValidationError: assessment.Reason,
				Effects:         assessment.Effects,
				ActionKind:      "shell_execute",
				ActionPayload:   cmdStr,
				GrantDigest:     requestGrant.Digest,
				Grant:           requestGrant,
			}
			if assessment.Decision == securitypolicy.DecisionLLMReview {
				if s.llmApprover != nil {
					advisory := request
					advisory.Command = secrets.Mask(advisory.Command)
					if _, advisoryErr := s.llmApprover.Approve(ctx, advisory); advisoryErr != nil {
						return "", advisoryErr
					}
					request.ValidationError = "advisory model found no immediate hazard; human approval is still required; " + request.ValidationError
				}
			}
			logger.Warn("shell effects require approval", "command", secrets.Mask(cmdStr), "decision", assessment.Decision, "reason", assessment.Reason)
			payload, _ := json.Marshal(map[string]any{
				"tool_name": "shell_execute",
				"command":   secrets.Mask(cmdStr),
				"decision":  assessment.Decision,
				"reason":    assessment.Reason,
				"effects":   assessment.Effects,
				"policy":    s.securityPolicy.Hash,
			})
			_ = telemetry.Record(ctx, telemetry.Event{Type: telemetry.EventApprovalRequested, Payload: payload})
			if approver == nil {
				return "", ApprovalRequiredError{Request: request}
			}
			decision, approvalErr := approver.Approve(ctx, request)
			if approvalErr != nil {
				return "", approvalErr
			}
			if !decision.Approved {
				return "", fmt.Errorf("security violation: approval gate did not approve command")
			}
			approvalMode = decision.Mode
			historyNote = strings.TrimSpace(decision.HistoryNote)
			if decision.Remember && s.securityPolicy.Bool(securitypolicy.SettingRememberApprovals) && assessment.Decision == securitypolicy.DecisionAsk {
				grant, _, grantErr := shellSecurityGrantBinding(cmdStr, *s.securityPolicy)
				if grantErr != nil {
					return "", fmt.Errorf("session grant binding: %w", grantErr)
				}
				s.sessionGrants[grant.Digest] = true
			}
		}
	} else if s.unrestricted {
		approvalMode = SafetyModeUnrestricted
	} else {
		_, err = validateAllowedShellCommand(cmdStr, s.allowedCommands, s.approvedCommands)
	}
	if s.securityPolicy == nil && !s.unrestricted && (err != nil || s.approvalRequired) {
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

		decision, approvalErr := s.approver.Approve(ctx, ShellApprovalRequest{
			Command:         cmdStr,
			ValidationError: validationMessage,
		})
		if approvalErr != nil {
			return "", approvalErr
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
		Command:      secrets.Mask(cmdStr),
		ApprovalMode: approvalMode,
		Success:      true,
	})
	payload, _ := json.Marshal(map[string]any{
		"tool_name":     "shell_execute",
		"command":       secrets.Mask(cmdStr),
		"approval_mode": approvalMode,
	})
	_ = telemetry.Record(ctx, telemetry.Event{
		Type:    telemetry.EventApprovalResolved,
		Payload: payload,
	})

	logger.Info("executing shell command", "command", secrets.Mask(cmdStr))

	// Wrap context with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", cmdStr)
	maxOutput := s.maxOutput
	if maxOutput <= 0 {
		maxOutput = 4096
	}
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
	source := decision.Mode
	if source == "" {
		source = "unknown"
	}
	rationale := strings.TrimSpace(decision.HistoryNote)
	if rationale == "" {
		rationale = "approved after secondary review"
	}

	for _, segment := range parsed.Segments {
		if segment.Normalized == "" {
			continue
		}
		s.approvedCommands[segment.Normalized] = true
		if s.securityPolicy != nil {
			// New policy approvals are deliberately process-local. Legacy durable
			// command strings lack target, policy, and expiry binding.
			continue
		}

		if s.approvalStore == nil || !s.approvalStore.Available() {
			continue
		}
		if err := s.approvalStore.SaveCommandApproval(ctx, state.CommandApproval{
			Command:    segment.Normalized,
			Status:     state.ApprovalStatusApproved,
			Source:     source,
			Rationale:  rationale,
			ApprovedAt: time.Now().UTC(),
		}); err != nil {
			log.FromContext(ctx).Warn("failed to persist command approval", "command", segment.Normalized, "error", err)
		}
	}
}

func allSegmentsApproved(parsed ParsedShellCommand, approved map[string]bool) bool {
	if len(parsed.Segments) == 0 {
		return false
	}
	for _, segment := range parsed.Segments {
		if !approved[segment.Normalized] {
			return false
		}
	}
	return true
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
