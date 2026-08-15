package state

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
)

func TestSQLitePersistenceMasksModelAndToolControlledSecrets(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	store := Open(path)
	if store.Err() != nil {
		t.Fatal(store.Err())
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = store.Close()
		}
	})

	argumentSecret := "sk-argumentsecretvalue123456789"
	toolMessageSecret := "sk-toolmessagesecretvalue123456789"
	assistantSecret := "sk-assistantsecretvalue123456789"
	runSecret := "sk-runfinalsecretvalue123456789"
	toolOutcomeSecret := "sk-tooloutcomesecretvalue123456789"

	sessionID, err := store.StartChatSession(ctx, ChatSession{Provider: "test", PinnedModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendChatTurn(ctx, sessionID, ChatTurn{
		UserInput:                  "normal user input",
		TaskClass:                  core.TaskClassInspection,
		RequestedModel:             "model",
		ActualModel:                "model",
		TaskState:                  TaskStateCompleted,
		Success:                    true,
		FinalOutput:                "assistant final " + assistantSecret,
		ErrorMessage:               "model error " + assistantSecret,
		VerificationReason:         "verified " + assistantSecret,
		VerificationMissingActions: "missing " + assistantSecret,
	}, []ChatMessage{
		{
			Role:          "assistant",
			Content:       "assistant call " + assistantSecret,
			ToolName:      "shell_execute",
			ToolArguments: `{"command":"echo ` + argumentSecret + `"}`,
			ToolCallsJSON: `[{"function":{"name":"shell_execute","arguments":"` + argumentSecret + `"}}]`,
		},
		{Role: "tool", Content: "tool result " + toolMessageSecret},
		{Role: "assistant", Content: "assistant final " + assistantSecret},
	}, []ToolOutcome{{
		Phase:             core.PhaseChat,
		Provider:          "test",
		Model:             "model",
		ToolName:          "shell_execute",
		Arguments:         `{"command":"echo ` + argumentSecret + `"}`,
		Command:           "echo " + argumentSecret,
		Success:           true,
		ErrorMessage:      "tool error " + toolOutcomeSecret,
		OutputInline:      "tool inline " + toolOutcomeSecret,
		OutputDigest:      "digest-must-not-change",
		OutputStoredBytes: 32,
	}})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := store.RecordRun(ctx, RunRecord{
		StartedAt:                  now,
		FinishedAt:                 now,
		Provider:                   "test",
		Task:                       "normal run task",
		TaskClass:                  core.TaskClassInspection,
		TaskState:                  TaskStateCompleted,
		Success:                    true,
		FinalOutput:                "run final " + runSecret,
		ErrorMessage:               "run error " + runSecret,
		VerificationReason:         "run verified " + runSecret,
		VerificationMissingActions: "run missing " + runSecret,
		Tools: []ToolOutcome{{
			Phase:        core.PhaseExecution,
			Provider:     "test",
			Model:        "model",
			ToolName:     "shell_execute",
			Arguments:    `{"command":"echo ` + argumentSecret + `"}`,
			Command:      "echo " + argumentSecret,
			Success:      true,
			OutputInline: "tool inline " + toolOutcomeSecret,
			OutputDigest: "run-digest-must-not-change",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	var chatTurn, chatMessages, chatTool, run, runTool, userInput, chatDigest, runDigest string
	queries := []struct {
		query string
		dest  *string
	}{
		{`SELECT final_output || error_message || verification_reason || verification_missing_actions FROM chat_turns LIMIT 1`, &chatTurn},
		{`SELECT group_concat(content || tool_arguments || tool_calls_json, '') FROM chat_messages`, &chatMessages},
		{`SELECT arguments || command || error_message || output_inline FROM chat_tool_outcomes LIMIT 1`, &chatTool},
		{`SELECT final_output || error_message || verification_reason || verification_missing_actions FROM runs LIMIT 1`, &run},
		{`SELECT arguments || command || error_message || output_inline FROM tool_outcomes WHERE run_id IS NOT NULL LIMIT 1`, &runTool},
		{`SELECT user_input FROM chat_turns LIMIT 1`, &userInput},
		{`SELECT output_digest FROM chat_tool_outcomes LIMIT 1`, &chatDigest},
		{`SELECT output_digest FROM tool_outcomes LIMIT 1`, &runDigest},
	}
	for _, item := range queries {
		if err := store.db.QueryRowContext(ctx, item.query).Scan(item.dest); err != nil {
			t.Fatalf("query %q: %v", item.query, err)
		}
	}
	combined := strings.Join([]string{chatTurn, chatMessages, chatTool, run, runTool}, "\n")
	for _, raw := range []string{argumentSecret, toolMessageSecret, assistantSecret, runSecret, toolOutcomeSecret} {
		if strings.Contains(combined, raw) {
			t.Fatalf("raw secret %q survived SQLite persistence: %q", raw, combined)
		}
	}
	if strings.Count(combined, "[REDACTED]") < 10 {
		t.Fatalf("expected redacted persisted values, got %q", combined)
	}
	if userInput != "normal user input" {
		t.Fatalf("user input changed unexpectedly: %q", userInput)
	}
	if chatDigest != "digest-must-not-change" || runDigest != "run-digest-must-not-change" {
		t.Fatalf("digest semantics changed: chat=%q run=%q", chatDigest, runDigest)
	}

	if _, err := store.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	var persisted []byte
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		persisted = append(persisted, data...)
	}
	for _, raw := range []string{argumentSecret, toolMessageSecret, assistantSecret, runSecret, toolOutcomeSecret} {
		if bytes.Contains(persisted, []byte(raw)) {
			t.Fatalf("raw secret %q found in SQLite files", raw)
		}
	}
	if !bytes.Contains(persisted, []byte("[REDACTED]")) {
		t.Fatal("expected redaction marker in SQLite file")
	}
}
