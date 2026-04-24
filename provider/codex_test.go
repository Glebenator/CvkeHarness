package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCodexCLIAuthReadsChatGPTToken(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{
		"auth_mode": "chatgpt",
		"tokens": {
			"access_token": "chatgpt-access-token",
			"account_id": "workspace-123",
			"id_token": {
				"chatgpt_account_id": "workspace-from-id-token"
			}
		}
	}`), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	auth, err := LoadCodexCLIAuth(path)
	if err != nil {
		t.Fatalf("LoadCodexCLIAuth returned error: %v", err)
	}
	if auth.AccessToken != "chatgpt-access-token" {
		t.Fatalf("unexpected access token %q", auth.AccessToken)
	}
	if auth.AccountID != "workspace-123" {
		t.Fatalf("unexpected account id %q", auth.AccountID)
	}
}

func TestLoadCodexCLIAuthReadsStringIDToken(t *testing.T) {
	t.Parallel()

	claims := base64.RawURLEncoding.EncodeToString([]byte(`{
		"https://api.openai.com/auth": {
			"chatgpt_account_id": "workspace-from-jwt"
		}
	}`))
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{
		"auth_mode": "chatgpt",
		"OPENAI_API_KEY": null,
		"tokens": {
			"access_token": "chatgpt-access-token",
			"id_token": "header.`+claims+`.signature"
		}
	}`), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	auth, err := LoadCodexCLIAuth(path)
	if err != nil {
		t.Fatalf("LoadCodexCLIAuth returned error: %v", err)
	}
	if auth.AccessToken != "chatgpt-access-token" {
		t.Fatalf("unexpected access token %q", auth.AccessToken)
	}
	if auth.AccountID != "workspace-from-jwt" {
		t.Fatalf("unexpected account id %q", auth.AccountID)
	}
}

func TestLoadCodexCLIAuthRejectsAPIKeyMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{
		"auth_mode": "apiKey",
		"openai_api_key": "sk-test"
	}`), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := LoadCodexCLIAuth(path); err == nil {
		t.Fatal("expected API-key mode to be rejected")
	}
}

func TestLoadCodexCLIAuthRejectsUppercaseAPIKeyMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{
		"auth_mode": "apiKey",
		"OPENAI_API_KEY": "sk-test"
	}`), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := LoadCodexCLIAuth(path); err == nil {
		t.Fatal("expected uppercase API-key mode to be rejected")
	}
}

func TestCodexProviderSendsChatGPTHeaders(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{
		"auth_mode": "chatgpt",
		"tokens": {
			"access_token": "chatgpt-access-token",
			"account_id": "workspace-123"
		}
	}`), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-access-token" {
			t.Fatalf("unexpected auth header %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "workspace-123" {
			t.Fatalf("unexpected account id header %q", got)
		}
		if got := r.Header.Get("Originator"); got != "codex_cli_rs" {
			t.Fatalf("unexpected originator header %q", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Fatal("expected user agent header")
		}

		var body struct {
			Instructions string `json:"instructions"`
			Store        *bool  `json:"store"`
			Stream       bool   `json:"stream"`
			Input        []struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"input"`
			MaxOutputTokens *int `json:"max_output_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Instructions != "system rules" {
			t.Fatalf("unexpected instructions %q", body.Instructions)
		}
		if !body.Stream {
			t.Fatal("expected stream=true")
		}
		if body.Store == nil || *body.Store {
			t.Fatalf("expected store=false, got %#v", body.Store)
		}
		if len(body.Input) != 1 || body.Input[0].Role != "user" || body.Input[0].Content != "hello" {
			t.Fatalf("unexpected input %#v", body.Input)
		}
		if body.MaxOutputTokens != nil {
			t.Fatalf("did not expect max_output_tokens, got %#v", body.MaxOutputTokens)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.1-codex-max\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"))
	}))
	defer server.Close()

	client := newOpenAIWithCredential(server.URL, func() (openAICredential, error) {
		auth, err := LoadCodexCLIAuth(path)
		return openAICredential{Token: auth.AccessToken, ChatGPTAccountID: auth.AccountID}, err
	}, map[string]string{
		codexOriginatorHeader: codexOriginator,
		"User-Agent":          codexUserAgent(),
	}, openAIBackendCodex)

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Model: "gpt-5.1-codex-max",
		Messages: []Message{
			{Role: "system", Content: "system rules"},
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected server to receive request")
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("unexpected content %q", resp.Message.Content)
	}
}

func TestMarshalRequestUsesCodexSchema(t *testing.T) {
	t.Parallel()

	client := newOpenAIWithCredential("https://chatgpt.com/backend-api/codex", func() (openAICredential, error) {
		return openAICredential{Token: "test-token"}, nil
	}, map[string]string{
		codexOriginatorHeader: codexOriginator,
	}, openAIBackendCodex)

	body, err := client.marshalRequest(&ChatRequest{
		Model:     "gpt-5.2",
		MaxTokens: 321,
	}, "system rules", []json.RawMessage{json.RawMessage(`{"type":"message","role":"user","content":"hi"}`)})
	if err != nil {
		t.Fatalf("marshalRequest returned error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if parsed["instructions"] != "system rules" {
		t.Fatalf("unexpected instructions %#v", parsed["instructions"])
	}
	if parsed["stream"] != true {
		t.Fatalf("unexpected stream %#v", parsed["stream"])
	}
	if parsed["store"] != false {
		t.Fatalf("unexpected store %#v", parsed["store"])
	}
	if parsed["max_output_tokens"] != nil {
		t.Fatalf("did not expect max_output_tokens %#v", parsed["max_output_tokens"])
	}
}
