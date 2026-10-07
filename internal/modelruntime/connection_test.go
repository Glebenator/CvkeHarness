package modelruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebenator/cvkeharness/config"
	"github.com/glebenator/cvkeharness/core"
	"github.com/glebenator/cvkeharness/provider"
)

func TestSeparateConnectionsSendNativeModelsAndOwnCredentials(t *testing.T) {
	for _, kind := range []string{"lmstudio", "openrouter"} {
		t.Run(kind, func(t *testing.T) {
			seen := make(chan string, 2)
			newServer := func(key string) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						Model string `json:"model"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
						t.Errorf("wrong connection path/auth: path=%s", r.URL.Path)
					}
					seen <- key + ":" + req.Model
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
				}))
			}
			first := newServer("first-key")
			defer first.Close()
			second := newServer("second-key")
			defer second.Close()
			cfg := &config.Config{Connections: map[string]config.Connection{
				"first":  {Provider: kind, BaseURL: first.URL + "/v1/", APIKey: "first-key"},
				"second": {Provider: kind, BaseURL: second.URL + "/v1", APIKey: "second-key"},
			}, Models: config.ModelRoles{Primary: config.ModelBinding{Connection: "first", Model: "vendor/group/model"}, SafetyJudge: config.ModelBinding{Connection: "second", Model: "openrouter/auto"}}}
			for _, role := range []config.ModelRole{config.RolePrimary, config.RoleSafetyJudge} {
				client, resolved, err := ResolveRole(cfg, role)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := client.ChatCompletion(context.Background(), &provider.ChatRequest{Model: resolved.Model}); err != nil {
					t.Fatal(err)
				}
			}
			if got := <-seen; got != "first-key:vendor/group/model" {
				t.Fatal(got)
			}
			if got := <-seen; got != "second-key:openrouter/auto" {
				t.Fatal(got)
			}
		})
	}
}

func TestCodexPrimaryAndRemoteJudgeResolveIndependentlyWithoutReadingCredentials(t *testing.T) {
	cfg := &config.Config{Connections: map[string]config.Connection{
		"subscription": {Provider: "codex", AuthFile: "/nonexistent/not-read-until-request.json"},
		"review":       {Provider: "openrouter", APIKey: "test-only"},
	}, Models: config.ModelRoles{Primary: config.ModelBinding{Connection: "subscription", Model: "gpt-codex"}, SafetyJudge: config.ModelBinding{Connection: "review", Model: "vendor/judge"}}}
	primary, p, err := ResolveRole(cfg, config.RolePrimary)
	if err != nil {
		t.Fatal(err)
	}
	judge, j, err := ResolveRole(cfg, config.RoleSafetyJudge)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := primary.(*provider.OpenAI); !ok {
		t.Fatalf("wrong Codex client %T", primary)
	}
	if _, ok := judge.(*provider.OpenRouter); !ok {
		t.Fatalf("wrong judge client %T", judge)
	}
	if Ref(p).Connection != "subscription" || Ref(j).Connection != "review" {
		t.Fatal("connection identity lost")
	}
	if _, err := ResolveModel(cfg, core.ModelRef{Connection: "review", Provider: "lmstudio", Model: "model"}); err == nil {
		t.Fatal("protocol mismatch accepted")
	}
}

func TestExplicitProviderNamedConnectionRetainsIdentityAfterMigrationAndEdit(t *testing.T) {
	cfg := &config.Config{Provider: "lmstudio", DefaultModel: "same-model", BaseURL: "http://localhost:1234/v1"}
	legacy, err := cfg.ResolveRole(config.RolePrimary)
	if err != nil {
		t.Fatal(err)
	}
	if got := Ref(legacy); got.Connection != "" || got.String() != "lmstudio/same-model" {
		t.Fatalf("lazy legacy identity changed: %+v", got)
	}
	cfg.EnsureModelBindings()
	connection := cfg.Connections["lmstudio"]
	connection.BaseURL = "https://another-endpoint.example/v1"
	connection.APIKey = "different-account-key"
	cfg.Connections["lmstudio"] = connection
	explicit, err := cfg.ResolveRole(config.RolePrimary)
	if err != nil {
		t.Fatal(err)
	}
	if got := Ref(explicit); got.Connection != "lmstudio" || got.Provider != "lmstudio" || got.String() != "lmstudio::lmstudio/same-model" {
		t.Fatalf("explicit connection collapsed to provider identity: %+v", got)
	}
	if Ref(legacy).Equal(Ref(explicit)) {
		t.Fatal("explicit endpoint/account shares legacy approval identity")
	}
}
