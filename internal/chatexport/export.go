// Package chatexport writes private, redacted Markdown records of persisted
// CvkeHarness chat sessions.
package chatexport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/secrets"
	"github.com/coolcake/cvkeharness/state"
)

const maxDiagnosticFieldRunes = 8192

// DirectoryForStateDB returns the private export directory next to the local
// CvkeHarness state database.
func DirectoryForStateDB(stateDBPath string) (string, error) {
	stateDBPath = strings.TrimSpace(stateDBPath)
	if stateDBPath == "" {
		return "", fmt.Errorf("state database path is empty")
	}
	absolute, err := filepath.Abs(stateDBPath)
	if err != nil {
		return "", fmt.Errorf("resolve state database path: %w", err)
	}
	return filepath.Join(filepath.Dir(absolute), "exports"), nil
}

// WriteMarkdown exports one persisted chat session into exportDir. The export
// directory and file are private to the current user. Obvious credential
// patterns are masked, but the generated file still needs operator review
// before it is shared.
func WriteMarkdown(exportDir string, detail state.ChatSessionDetail, now time.Time) (string, error) {
	exportDir = strings.TrimSpace(exportDir)
	if exportDir == "" {
		return "", fmt.Errorf("chat export directory is empty")
	}
	if detail.Session.ID <= 0 {
		return "", fmt.Errorf("chat session is not persisted yet")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	if err := os.MkdirAll(exportDir, 0o700); err != nil {
		return "", fmt.Errorf("create chat export directory: %w", err)
	}
	if err := os.Chmod(exportDir, 0o700); err != nil {
		return "", fmt.Errorf("secure chat export directory: %w", err)
	}

	base := fmt.Sprintf("chat-%d-%s", detail.Session.ID, now.Format("20060102T150405Z"))
	path, file, err := createPrivateFile(exportDir, base)
	if err != nil {
		return "", err
	}

	content := renderMarkdown(detail, now)
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write chat export: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close chat export: %w", err)
	}
	return path, nil
}

func createPrivateFile(dir, base string) (string, *os.File, error) {
	for suffix := 1; suffix <= 1000; suffix++ {
		name := base + ".md"
		if suffix > 1 {
			name = fmt.Sprintf("%s-%d.md", base, suffix)
		}
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return path, file, nil
		}
		if os.IsExist(err) {
			continue
		}
		return "", nil, fmt.Errorf("create chat export: %w", err)
	}
	return "", nil, fmt.Errorf("create chat export: too many files named %s", base)
}

func renderMarkdown(detail state.ChatSessionDetail, exportedAt time.Time) string {
	var b strings.Builder
	session := detail.Session
	fmt.Fprintf(&b, "# CvkeHarness chat export\n\n")
	fmt.Fprintf(&b, "- Session: %d\n", session.ID)
	fmt.Fprintf(&b, "- Started: %s\n", formatTime(session.StartedAt))
	if !session.FinishedAt.IsZero() {
		fmt.Fprintf(&b, "- Finished: %s\n", formatTime(session.FinishedAt))
	}
	fmt.Fprintf(&b, "- Exported: %s\n", formatTime(exportedAt))
	fmt.Fprintf(&b, "- Provider: %s\n", inlineValue(session.Provider))
	fmt.Fprintf(&b, "- Pinned model: %s\n", inlineValue(session.PinnedModel))
	fmt.Fprintf(&b, "- Turns: %d\n", len(detail.Turns))
	if strings.TrimSpace(session.ExitReason) != "" {
		fmt.Fprintf(&b, "- Exit reason: %s\n", inlineValue(session.ExitReason))
	}
	b.WriteString("\n> Obvious credential patterns were masked. This file can still contain private operational context; review it before sharing.\n")

	turns := append([]state.ChatTurn(nil), detail.Turns...)
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].TurnIndex < turns[j].TurnIndex })
	for _, turn := range turns {
		fmt.Fprintf(&b, "\n## Turn %d\n\n", turn.TurnIndex)
		b.WriteString("### You\n\n")
		writeTextBlock(&b, turn.UserInput)
		b.WriteString("\n### CvkeHarness\n\n")
		writeTextBlock(&b, turn.FinalOutput)

		b.WriteString("\n### Outcome\n\n")
		fmt.Fprintf(&b, "- State: %s\n", inlineValue(string(turn.TaskState)))
		fmt.Fprintf(&b, "- Success: %t\n", turn.Success)
		fmt.Fprintf(&b, "- Model: %s\n", inlineValue(firstNonEmpty(turn.ActualModel, turn.RequestedModel)))
		fmt.Fprintf(&b, "- Latency: %d ms\n", turn.LatencyMs)
		fmt.Fprintf(&b, "- Tokens: %d total (%d prompt, %d completion)\n", turn.TotalTokens, turn.PromptTokens, turn.CompletionTokens)
		if strings.TrimSpace(turn.VerificationStatus) != "" {
			fmt.Fprintf(&b, "- Verification: %s\n", inlineValue(turn.VerificationStatus))
		}
		if strings.TrimSpace(turn.VerificationReason) != "" {
			fmt.Fprintf(&b, "- Verification reason: %s\n", inlineValue(turn.VerificationReason))
		}
		if strings.TrimSpace(turn.ErrorMessage) != "" {
			fmt.Fprintf(&b, "- Error: %s\n", inlineValue(boundDiagnostic(turn.ErrorMessage)))
		}

		outcomes := detail.ToolsByTurnID[turn.ID]
		if len(outcomes) == 0 {
			continue
		}
		b.WriteString("\n### Tools\n")
		for _, outcome := range outcomes {
			status := "SUCCEEDED"
			if outcome.PolicyDenied {
				status = "DENIED"
			} else if !outcome.Success {
				status = "FAILED"
			}
			fmt.Fprintf(&b, "\n#### %s: %s\n\n", inlineHeading(outcome.ToolName), status)
			fmt.Fprintf(&b, "- Duration: %d ms\n", outcome.DurationMs)
			if strings.TrimSpace(outcome.Command) != "" {
				b.WriteString("- Command:\n\n")
				writeTextBlock(&b, boundDiagnostic(outcome.Command))
			}
			if strings.TrimSpace(outcome.Arguments) != "" {
				b.WriteString("\n- Arguments:\n\n")
				writeTextBlock(&b, boundDiagnostic(outcome.Arguments))
			}
			if strings.TrimSpace(outcome.ErrorMessage) != "" {
				fmt.Fprintf(&b, "\n- Error: %s\n", inlineValue(boundDiagnostic(outcome.ErrorMessage)))
			}
		}
	}
	return b.String()
}

func writeTextBlock(b *strings.Builder, value string) {
	value = secrets.Mask(strings.TrimSpace(value))
	if value == "" {
		value = "(none)"
	}
	fence := "```"
	for strings.Contains(value, fence) {
		fence += "`"
	}
	fmt.Fprintf(b, "%s\n%s\n%s\n", fence, value, fence)
}

func inlineValue(value string) string {
	value = secrets.Mask(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return value
}

func inlineHeading(value string) string {
	value = inlineValue(value)
	return strings.ReplaceAll(value, "#", "")
}

func boundDiagnostic(value string) string {
	runes := []rune(value)
	if len(runes) <= maxDiagnosticFieldRunes {
		return value
	}
	return string(runes[:maxDiagnosticFieldRunes]) + "\n[TRUNCATED]"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339)
}
