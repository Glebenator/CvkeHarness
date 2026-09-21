//go:build e2e && !windows

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/provider"
)

// Exercise the actual console input path at both sides of the responsive split.
// Detailed viewport-position assertions live with the TUI model; this journey
// checks that each layout can reveal persisted evidence and return to composing.
func TestChatActivityEvidenceAtRepresentativeWidths(t *testing.T) {
	for _, width := range []uint16{80, 100, 120, 144} {
		t.Run(fmt.Sprintf("%d_columns", width), func(t *testing.T) {
			home := t.TempDir()
			model := newMockModelServer(t)
			defer model.Close()
			writeConfig(t, home, model.URL+"/v1")

			output := runConsoleChatJourneyAtWidth(t, home, width, []consoleChatStep{
				{input: "run the shell command echo E2E_TOOL_OK then confirm the result", waitFor: "Tool-backed response complete."},
				{key: "\x14", waitFor: "ACTIVITY / TURN 1"},
				{key: "\r", waitFor: "RAW OUTPUT"},
				{key: "\x1b"},
				{key: "\x1b"},
				{input: "/help", waitFor: "Start a fresh in-process chat session"},
			})
			for _, expected := range []string{"ACTIVITY / TURN 1", "shell_execute", "RAW OUTPUT", "E2E_TOOL_OK", "SUCCEEDED"} {
				assertContains(t, output, expected)
			}
			if got := len(model.Requests()); got != 4 {
				t.Fatalf("activity inspection and local command made extra model requests: got %d, want 4", got)
			}
		})
	}
}

// A short new reply can begin below the center of the visible viewport while
// the previous long reply is still visible above it. Opening Activity at Latest
// must use the newest turn, not whichever historical turn covers that center.
func TestChatActivityTracksToolFreeFollowUpAtNarrowWidths(t *testing.T) {
	for _, width := range []uint16{80, 100} {
		t.Run(fmt.Sprintf("%d_columns", width), func(t *testing.T) {
			home := t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var incoming modelRequest
				if err := json.NewDecoder(request.Body).Decode(&incoming); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				last := lastMessage(incoming)
				message := provider.Message{Role: "assistant"}
				switch {
				case strings.Contains(last.Content, "current_user_prompt"):
					message.Content = `{"task_class":"general","actionable":false}`
				case strings.Contains(last.Content, "assistant_final_output"):
					message.Content = `{"status":"satisfied","reason":"The requested work is complete.","missing_actions":[],"repair_instruction":""}`
				case last.Role == "tool":
					for i := 1; i <= 20; i++ {
						message.Content += fmt.Sprintf("%d. The local echo result was checked.\n\n", i)
					}
					message.Content += "E2E_ACTIVITY_FIRST_READY"
				case strings.Contains(last.Content, "E2E_ACTIVITY_NO_TOOLS"):
					message.Content = "This turn answers directly.\n\nE2E_ACTIVITY_SECOND_READY"
				default:
					message.ToolCalls = []provider.ToolCall{{
						ID: "activity-echo", Type: "function",
						Function: provider.ToolFunction{Name: "shell_execute", Arguments: mustJSON(map[string]string{"command": "echo E2E_ACTIVITY_EVIDENCE"})},
					}}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "activity-response", "model": "e2e-model",
					"choices": []any{map[string]any{"message": message, "finish_reason": "stop"}},
				})
			}))
			defer server.Close()
			writeConfig(t, home, server.URL+"/v1")

			output := runConsoleChatJourneyAtWidth(t, home, width, []consoleChatStep{
				{input: "run the shell command echo E2E_ACTIVITY_EVIDENCE and report the result", waitFor: "E2E_ACTIVITY_FIRST_READY"},
				{input: "E2E_ACTIVITY_NO_TOOLS: give me a short direct reply", waitFor: "E2E_ACTIVITY_SECOND_READY"},
				{key: "\x14", waitFor: "ACTIVITY / TURN 2"},
			})
			assertContains(t, output, "No tool calls in this turn.")
		})
	}
}
