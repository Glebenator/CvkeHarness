//go:build e2e && !windows

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/glebenator/cvkeharness/provider"
)

func TestLLMAdvisorConsoleApprovalJourney(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(fmt.Sprintf("approve_%t", approve), func(t *testing.T) {
			home := t.TempDir()
			model := newMockModelServer(t)
			defer model.Close()
			var adviceCalls atomic.Int32
			advisor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Model    string             `json:"model"`
					Messages []provider.Message `json:"messages"`
					Tools    []any              `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", 400)
					return
				}
				adviceCalls.Add(1)
				if req.Model != "independent-advisor" || r.Header.Get("Authorization") != "Bearer advisor-test-key" || len(req.Tools) != 0 {
					t.Errorf("wrong advisor connection/model or tools: %+v", req)
				}
				content := `{"explanation":"Prints E2E_TOOL_OK to the terminal.","steps":[],"risks":[],"uncertainty":"","recommendation":"approve","reason":"Only prints a literal string."}`
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "choices": []any{map[string]any{"message": provider.Message{Role: "assistant", Content: content}}}})
			}))
			defer advisor.Close()
			paths := writeConfigWithSafety(t, home, model.URL+"/v1", "llm_advisor")
			path := filepath.Join(paths.baseDir, "config.yaml")
			cfg, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg = append(cfg, []byte(fmt.Sprintf(`security:
  profile: llm_advisor
  overrides:
    commands.read: ask
connections:
  executor:
    provider: lmstudio
    base_url: %q
  advisor-service:
    provider: openrouter
    base_url: %q
    api_key: advisor-test-key
models:
  primary:
    connection: executor
    model: e2e-model
  safety_advisor:
    connection: advisor-service
    model: independent-advisor
`, model.URL+"/v1", advisor.URL+"/v1"))...)
			if err := os.WriteFile(path, cfg, 0600); err != nil {
				t.Fatal(err)
			}
			output := runChatApprovalDecision(t, home, "run the shell command echo E2E_TOOL_OK then confirm the result", "Tool-backed response complete.", approve)
			for _, expected := range []string{"LLM ADVISOR", "Recommendation: approve", "Prints E2E_TOOL_OK", "independent-advisor"} {
				assertContains(t, output, expected)
			}
			if adviceCalls.Load() != 1 {
				t.Fatalf("advisor called %d times; one review expected", adviceCalls.Load())
			}
			requests := model.Requests()
			if !approve && len(requests) != 1 {
				t.Fatalf("model continued after unapproved advice: %d calls", len(requests))
			}
			if approve && len(requests) != 3 {
				t.Fatalf("expected execution, continuation, verification: %d calls", len(requests))
			}
		})
	}
}
