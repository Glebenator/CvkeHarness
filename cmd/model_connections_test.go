package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

func TestChatRuntimeSeparatesExecutionClassificationSafetyAndVerificationHTTP(t *testing.T) {
	for _, judgeKind := range []string{"lmstudio", "openrouter"} {
		t.Run(judgeKind, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			newServer := func(endpoint, key string) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						Model    string             `json:"model"`
						Messages []provider.Message `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						http.Error(w, "bad request", 400)
						return
					}
					if r.Header.Get("Authorization") != "Bearer "+key {
						t.Errorf("wrong credentials at %s", endpoint)
					}
					first := ""
					if len(req.Messages) > 0 {
						first = req.Messages[0].Content
					}
					kind := "execution"
					content := "finished"
					message := provider.Message{Role: "assistant"}
					switch {
					case strings.Contains(first, "Classify the user's current task"):
						kind = "classification"
						content = `{"task_class":"shell_heavy","actionable":true}`
					case strings.Contains(first, "Review the shell command"):
						kind = "safety"
						content = "SAFE"
					case strings.Contains(first, "completion verifier"):
						kind = "verification"
						content = `{"status":"satisfied","reason":"echo completed"}`
					}
					mu.Lock()
					calls = append(calls, endpoint+":"+req.Model+":"+kind)
					mu.Unlock()
					message.Content = content
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "choices": []any{map[string]any{"message": message}}})
				}))
			}
			executor := newServer("executor", "executor-key")
			defer executor.Close()
			judge := newServer("judge", "judge-key")
			defer judge.Close()
			cfg := config.DefaultConfig()
			cfg.MemoryDir = filepath.Join(t.TempDir(), "memory")
			cfg.StateDBPath = filepath.Join(t.TempDir(), "state.db")
			cfg.Connections = map[string]config.Connection{
				"executor": {Provider: "lmstudio", BaseURL: executor.URL + "/v1", APIKey: "executor-key"},
				"judge":    {Provider: judgeKind, BaseURL: judge.URL + "/v1", APIKey: "judge-key"},
			}
			cfg.Models = config.ModelRoles{Primary: config.ModelBinding{Connection: "executor", Model: "native/primary"}, SafetyJudge: config.ModelBinding{Connection: "judge", Model: "vendor/judge"}}
			if judgeKind == "openrouter" {
				cfg.Models.Verifier = config.ModelBinding{Inherit: config.RoleSafetyJudge}
			}
			if err := cfg.Security.SetOverride(securitypolicy.SettingReadCommands, string(securitypolicy.DecisionLLMReview)); err != nil {
				t.Fatal(err)
			}
			cfg.Normalize()
			store := state.Open(cfg.StateDBPath)
			defer store.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			agent, err := newChatAgent(ctx, cfg, store, nil, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			conversation, _, err := agent.StartChat(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conversation.Turn(ctx, "run echo connection-test"); err != nil {
				t.Fatalf("chat failed: %v; calls=%v", err, calls)
			}
			// Exercise the exact shared registry constructor with manual approval
			// blocked. The advisory request must reach the judge connection, and
			// SAFE must still leave the action waiting for human authorization.
			clients, err := resolveRuntimeModels(cfg)
			if err != nil {
				t.Fatal(err)
			}
			registry, err := defaultRegistryFromConfig(cfg, store, nil, clients.Judge, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			_, err = registry.ExecuteTool(ctx, provider.ToolCall{ID: "echo-test", Type: "function", Function: provider.ToolFunction{Name: "shell_execute", Arguments: `{"command":"echo connection-test"}`}})
			if err == nil || !strings.Contains(err.Error(), "approval") {
				t.Fatalf("advisory SAFE must not authorize execution: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			want := []string{"judge:vendor/judge:classification", "executor:native/primary:execution", "executor:native/primary:verification", "judge:vendor/judge:safety"}
			if judgeKind == "openrouter" {
				want[2] = "judge:vendor/judge:verification"
			}
			if strings.Join(calls, "\n") != strings.Join(want, "\n") {
				t.Fatalf("role requests went to wrong endpoints/models:\n%v", calls)
			}
		})
	}
}

func mixedModelConfig() *config.Config {
	return &config.Config{Connections: map[string]config.Connection{
		"subscription": {Provider: "codex"},
		"review":       {Provider: "openrouter", APIKey: "test-key"},
		"laptop":       {Provider: "lmstudio", BaseURL: "http://localhost:1234/v1"},
		"server":       {Provider: "lmstudio", BaseURL: "http://localhost:5678/v1"},
	}, Models: config.ModelRoles{
		Primary:     config.ModelBinding{Connection: "subscription", Model: "gpt-codex"},
		SafetyJudge: config.ModelBinding{Connection: "review", Model: "vendor/judge"},
		Classifier:  config.ModelBinding{Connection: "laptop", Model: "classifier"},
		Execution:   config.ModelBinding{Connection: "server", Model: "executor"},
	}}
}

func TestRuntimeConstructsEachModelRoleFromItsOwnConnection(t *testing.T) {
	cfg := mixedModelConfig()
	clients, err := resolveRuntimeModels(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := clients.Primary.(*provider.OpenAI); !ok {
		t.Fatalf("primary type %T", clients.Primary)
	}
	if _, ok := clients.Judge.(*provider.OpenRouter); !ok {
		t.Fatalf("judge type %T", clients.Judge)
	}
	if _, ok := clients.Classifier.(*provider.LMStudio); !ok {
		t.Fatalf("classifier type %T", clients.Classifier)
	}
	if clients.PrimaryModel.Model != "gpt-codex" || clients.JudgeModel.Model != "vendor/judge" || clients.ClassifierModel.Model != "classifier" {
		t.Fatal("wrong native models")
	}
	if !clients.Verifier.IsZero() {
		t.Fatal("default verifier must inherit actual routed executor")
	}
	cfg.Models.Verifier = config.ModelBinding{Inherit: config.RoleSafetyJudge}
	clients, err = resolveRuntimeModels(cfg)
	if err != nil || clients.Verifier.Connection != "review" || clients.Verifier.Model != "vendor/judge" {
		t.Fatalf("explicit verifier: %+v %v", clients.Verifier, err)
	}
}

func TestRuntimeRejectsMissingIndependentJudgeCredentialBeforeCallingPrimary(t *testing.T) {
	cfg := mixedModelConfig()
	connection := cfg.Connections["review"]
	connection.APIKey = ""
	cfg.Connections["review"] = connection
	if _, err := resolveRuntimeModels(cfg); err == nil || !strings.Contains(err.Error(), "safety_judge") {
		t.Fatalf("missing judge credential was not diagnosed: %v", err)
	}
}

func TestRoutingPreservesExplicitConnectionForRunAndChat(t *testing.T) {
	cfg := mixedModelConfig()
	cfg.ApprovedModels = []string{"laptop::lmstudio/shared", "server::lmstudio/shared"}
	routing := routingConfigFromConfig(cfg, nil)
	if routing.DefaultModel.Connection != "subscription" || routing.DefaultModel.Provider != "codex" {
		t.Fatalf("wrong primary: %+v", routing.DefaultModel)
	}
	for _, phase := range []core.Phase{core.PhaseExecution, core.PhaseChat} {
		ref := routing.PhaseModels[phase]
		if ref.Connection != "server" || ref.Provider != "lmstudio" || ref.Model != "executor" {
			t.Fatalf("%s lost endpoint: %+v", phase, ref)
		}
	}
	approved := routing.ApprovedSet()
	if _, ok := approved["laptop::lmstudio/shared"]; !ok {
		t.Fatal("laptop approval lost")
	}
	if _, ok := approved["server::lmstudio/shared"]; !ok {
		t.Fatal("server approval lost")
	}
	if _, ok := approved["lmstudio/shared"]; ok {
		t.Fatal("named approval widened to all provider endpoints")
	}
}

func TestProviderNamedExplicitConnectionScopesRoutingAndApprovals(t *testing.T) {
	cfg := &config.Config{Connections: map[string]config.Connection{"openrouter": {Provider: "openrouter", APIKey: "new-account-key"}}, Models: config.ModelRoles{Primary: config.ModelBinding{Connection: "openrouter", Model: "vendor/model"}}, ApprovedModels: []string{"openrouter/vendor/model"}}
	routing := routingConfigFromConfig(cfg, nil)
	if routing.DefaultModel.Connection != "openrouter" {
		t.Fatal("explicit connection reused provider-only routing")
	}
	for _, phase := range []core.Phase{core.PhaseChat, core.PhaseExecution} {
		if routing.PhaseModels[phase].Connection != "openrouter" {
			t.Fatalf("%s lost connection identity", phase)
		}
	}
	approved := routing.ApprovedSet()
	if _, ok := approved["openrouter::openrouter/vendor/model"]; !ok {
		t.Fatal("connection approval missing")
	}
	if _, ok := approved["openrouter/vendor/model"]; ok {
		t.Fatal("explicit account approval became provider-wide")
	}
	got, err := normalizeModelArg(cfg, "openrouter/vendor/model")
	if err != nil || got != "openrouter::openrouter/vendor/model" {
		t.Fatalf("CLI approval lost connection identity: %s %v", got, err)
	}
}

func TestModelArgUsesPrimaryConnectionWithoutParsingNativeVendorPath(t *testing.T) {
	cfg := mixedModelConfig()
	cfg.Models.Primary = config.ModelBinding{Connection: "review", Model: "vendor/primary"}
	got, err := normalizeModelArg(cfg, "vendor/group/model")
	if err != nil || got != "review::openrouter/vendor/group/model" {
		t.Fatalf("native path parsed as provider: %q %v", got, err)
	}
	got, err = normalizeModelArg(cfg, "server::lmstudio/raw/model")
	if err != nil || got != "server::lmstudio/raw/model" {
		t.Fatalf("named ref lost: %q %v", got, err)
	}
	got, err = normalizeModelArg(cfg, "openrouter/auto")
	if err != nil || got != "review::openrouter/openrouter/auto" {
		t.Fatalf("OpenRouter native alias changed: %q %v", got, err)
	}
}
