//go:build e2e && !windows

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/glebenator/cvkeharness/provider"
	"github.com/glebenator/cvkeharness/state"
)

// Exercise the real binary and terminal with a local fake model. Outbound
// network commands are denied by policy, so this never contacts a real server.
func TestEndpointCapturePendingRequestThroughPTY(t *testing.T) {
	var calls atomic.Int32
	var verifying atomic.Bool
	var fresh atomic.Bool
	const original = "how is the disk usage on my home server looking like"
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req modelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var msg provider.Message
		last := lastMessage(req)
		if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "Classify the user's current task") {
			w.Header().Set("Content-Type", "application/json")
			msg = provider.Message{Role: "assistant", Content: `{"task_class":"inspection","actionable":true}`}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}}})
			return
		}
		calls.Add(1)
		switch {
		case strings.Contains(last.Content, "assistant_final_output") && fresh.Load():
			msg = provider.Message{Role: "assistant", Content: `{"status":"satisfied","reason":"Address recalled.","missing_actions":[],"repair_instruction":""}`}
		case strings.Contains(last.Content, "assistant_final_output"):
			if !strings.Contains(last.Content, `"user_request": "`+original+`"`) || !strings.Contains(last.Content, `"status": "saved"`) {
				t.Errorf("verification lost pending task/capture: %s", last.Content)
			}
			verifying.Store(true)
			msg = provider.Message{Role: "assistant", Content: `{"status":"unsatisfied","reason":"Disk check requires network permission.","missing_actions":["shell_execute must perform the disk check"],"repair_instruction":"Use shell_execute after network permission changes"}`}
		case fresh.Load():
			raw, _ := json.Marshal(req.Messages)
			if !strings.Contains(string(raw), "192.168.50.69") {
				t.Error("new process did not recall endpoint")
			}
			msg = provider.Message{Role: "assistant", Content: "Recalled home server: 192.168.50.69"}
		case last.Role == "tool":
			msg = provider.Message{Role: "assistant", Content: "Disk check blocked by network policy."}
		default:
			msg = provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "disk-check", Type: "function", Function: provider.ToolFunction{Name: "shell_execute", Arguments: `{"command":"ssh 192.168.50.69 'df -h'"}`}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"model": "e2e-model", "choices": []any{map[string]any{"message": msg, "finish_reason": "stop"}}})
	}))
	defer model.Close()
	home := t.TempDir()
	paths := writeConfigWithSafety(t, home, model.URL+"/v1", "static_only")
	configPath := filepath.Join(paths.baseDir, "config.yaml")
	cfg, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg = append(cfg, []byte("\nsecurity:\n  profile: reasonable\n  overrides:\n    filesystem.append: allow\n    network.outbound: deny\n")...)
	if err := os.WriteFile(configPath, cfg, 0600); err != nil {
		t.Fatal(err)
	}
	output := runConsoleChatJourney(t, home, []consoleChatStep{
		{input: original, waitFor: "What is the address of your home server?", paste: true},
		{input: "my home server is 192.168.50.69", waitFor: "Disk check blocked by network policy.", paste: true},
	})
	if !strings.Contains(output, "Remembered:") {
		t.Fatal("no memory acknowledgment in terminal")
	}
	if !strings.Contains(output, "TARGET REQUIRED") || strings.Contains(output, "APPROVAL REQUIRED") {
		t.Fatal("missing target was presented as an approval")
	}
	if !verifying.Load() || calls.Load() != 3 {
		t.Fatalf("unexpected retries/calls: %d\n%s", calls.Load(), output)
	}
	store := state.Open(paths.stateDB)
	saved, err := store.ListUserEndpoints(context.Background())
	store.Close()
	if err != nil || len(saved) != 1 || saved[0].Endpoint != "192.168.50.69" {
		t.Fatalf("persisted state: %#v %v", saved, err)
	}
	// A new process uses a plain recall response. It has no previous disk task.
	fresh.Store(true)
	runConsoleChatJourney(t, home, []consoleChatStep{{input: "what is the address of my home server", waitFor: "Recalled home server: 192.168.50.69", paste: true}})
}
