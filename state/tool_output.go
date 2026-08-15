package state

import (
	"crypto/sha256"
	"encoding/hex"
	"unicode/utf8"

	"github.com/coolcake/cvkeharness/internal/secrets"
)

const maxInlineToolOutputBytes = 4096

// SummarizeToolOutput redacts tool-controlled text before producing a bounded
// durable representation. The digest covers the complete redacted output.
func SummarizeToolOutput(raw string) (inline string, originalBytes, storedBytes int64, truncated bool, digest string) {
	originalBytes = int64(len([]byte(raw)))
	redacted := secrets.Mask(raw)
	sum := sha256.Sum256([]byte(redacted))
	digest = hex.EncodeToString(sum[:])
	data := []byte(redacted)
	if len(data) > maxInlineToolOutputBytes {
		data = data[:maxInlineToolOutputBytes]
		truncated = true
		// Avoid persisting an invalid partial UTF-8 rune.
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	inline = string(data)
	storedBytes = int64(len(data))
	return inline, originalBytes, storedBytes, truncated, digest
}

func sanitizeToolOutcome(tool ToolOutcome) ToolOutcome {
	tool.Arguments = secrets.Mask(tool.Arguments)
	tool.Command = secrets.Mask(tool.Command)
	tool.ErrorMessage = secrets.Mask(tool.ErrorMessage)
	tool.OutputInline = secrets.Mask(tool.OutputInline)
	return tool
}

func sanitizeChatMessage(message ChatMessage) ChatMessage {
	message.Content = secrets.Mask(message.Content)
	message.ToolCallID = secrets.Mask(message.ToolCallID)
	message.ToolName = secrets.Mask(message.ToolName)
	message.ToolArguments = secrets.Mask(message.ToolArguments)
	message.ToolCallsJSON = secrets.Mask(message.ToolCallsJSON)
	return message
}

func sanitizeModelOutcomeFields(finalOutput, errorMessage, verificationReason, verificationMissingActions string) (string, string, string, string) {
	return secrets.Mask(finalOutput), secrets.Mask(errorMessage), secrets.Mask(verificationReason), secrets.Mask(verificationMissingActions)
}
