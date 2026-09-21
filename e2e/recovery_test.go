//go:build e2e && !windows

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/state"
)

func TestRecoveryConsoleApprovalThenOfflineRestore(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "target")
	if err = os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, "config")
	if err = os.WriteFile(path, []byte("original"), 0640); err != nil {
		t.Fatal(err)
	}
	var op recovery.Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var incoming modelRequest
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		last := lastMessage(incoming)
		var message provider.Message
		switch {
		case strings.Contains(last.Content, "current_user_prompt"):
			message = provider.Message{Role: "assistant", Content: `{"task_class":"shell_heavy","confidence":1,"reason":"Apply the prepared file operation"}`}
		case strings.Contains(last.Content, "assistant_final_output"):
			message = provider.Message{Role: "assistant", Content: `{"status":"satisfied","reason":"Recovery operation was committed.","missing_actions":[],"repair_instruction":""}`}
		case last.Role == "tool":
			if !strings.Contains(last.Content, `"status":"committed"`) {
				http.Error(w, "unexpected recovery tool result", 500)
				return
			}
			message = provider.Message{Role: "assistant", Content: "Recoverable change committed."}
		default:
			message = provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "recovery-apply", Type: "function", Function: provider.ToolFunction{Name: "recovery_manage", Arguments: mustJSON(map[string]string{"action": "apply", "id": op.ID, "digest": op.Digest})}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"model": "e2e-model", "choices": []any{map[string]any{"message": message, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	paths := writeConfigWithSafety(t, home, server.URL+"/v1", "user_confirm_all")
	f, err := os.OpenFile(filepath.Join(paths.baseDir, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "recovery:\n  roots: [%q]\n", target)
	f.Close()
	request := filepath.Join(home, "request.json")
	if err = os.WriteFile(request, []byte(mustJSON(recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: path, Content: "new"}}})), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, home, "", "recovery", "prepare", "--request", request)
	if err != nil {
		t.Fatalf("prepare: %v\n%s", err, out)
	}
	if err = json.Unmarshal([]byte(out), &op); err != nil {
		t.Fatal(err)
	}
	out = runChatApprovalDecision(t, home, "apply the prepared recovery operation to update the local configuration file", "Recoverable change committed.", true)
	assertContains(t, out, "recovery_manage")
	assertContains(t, out, "APPROVED ONCE")
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("actual target was not changed: %s", got)
	}
	s := state.Open(paths.stateDB)
	records, err := s.ListRecoveryOperations(context.Background())
	if err != nil || len(records) != 1 || records[0].Status != recovery.Committed {
		t.Fatalf("persisted outcome: %v %#v", err, records)
	}
	s.Close()
	server.Close() // Recovery must not use a functioning model endpoint.
	out, err = runCLI(t, home, "", "recovery", "recover", op.ID, "--confirm", op.Digest)
	if err != nil {
		t.Fatalf("offline restore: %v\n%s", err, out)
	}
	assertContains(t, out, `"status": "recovered"`)
	got, _ = os.ReadFile(path)
	if string(got) != "original" {
		t.Fatalf("offline recovery did not restore contents: %s", got)
	}
}
