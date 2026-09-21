package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityToolRoundTrip(t *testing.T) {
	stream := `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"private reasoning"},{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"signature-123"},{"functionCall":{"name":"read","args":{"path":"b"}},"thoughtSignature":"signature-456"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"totalTokenCount":15,"cachedContentTokenCount":4}}}` + "\n\n"
	response, err := parseAntigravityStream(strings.NewReader(stream), "gemini-test")
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "" || len(response.Message.ToolCalls) != 2 || response.FinishReason != "tool_calls" || response.Usage.CompletionTokens != 5 {
		t.Fatalf("unexpected response: %+v", response)
	}
	req := &ChatRequest{Model: "gemini-test", Messages: []Message{{Role: "system", Content: "Stay scoped"}, {Role: "user", Content: "Read files"}, response.Message, {Role: "tool", ToolCallID: response.Message.ToolCalls[0].ID, Content: "first"}, {Role: "tool", ToolCallID: response.Message.ToolCalls[1].ID, Content: "second"}}}
	b, err := antigravityRequest(req, "own-project")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "signature-123") || !strings.Contains(string(b), "signature-456") || strings.Contains(string(b), `"id":"ag_`) {
		t.Fatalf("signature lost or synthetic ID leaked: %s", b)
	}
	var body struct {
		Request struct {
			Contents []agContent `json:"contents"`
		} `json:"request"`
	}
	if err = json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Request.Contents) != 3 || len(body.Request.Contents[2].Parts) != 2 {
		t.Fatalf("tool results not grouped: %s", b)
	}
	req.Messages[2].ResponseItems = nil
	if _, err = antigravityRequest(req, "own-project"); err == nil {
		t.Fatal("accepted missing native tool state")
	}
}

func TestAntigravityRejectsIncompleteStreams(t *testing.T) {
	for _, stream := range []string{"", "data: not-json\n\n", `data: {"error":{"message":"secret backend detail"}}` + "\n\n", `data: {"response":{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}}` + "\n\n", `data: {"response":{"candidates":[{"content":{"parts":[{"text":"blocked"}]},"finishReason":"SAFETY"}]}}` + "\n\n"} {
		if _, err := parseAntigravityStream(strings.NewReader(stream), "gemini-test"); err == nil {
			t.Fatalf("accepted %q", stream)
		}
	}
}

func TestAntigravityHTTPAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := saveAntigravityAuth(path, AntigravityAuth{AccessToken: "test-secret", ProjectID: "project", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" || r.URL.Path != "/v1internal:streamGenerateContent" {
			t.Error("invalid request")
		}
		w.WriteHeader(status)
		if status == 200 {
			fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
		} else {
			fmt.Fprint(w, "test-secret private error")
		}
	}))
	defer server.Close()
	a := &Antigravity{client: server.Client(), endpoint: server.URL, authPath: path}
	for _, code := range []int{200, 401, 403, 429, 500} {
		status = code
		res, err := a.ChatCompletion(context.Background(), &ChatRequest{Model: "gemini-test", Messages: []Message{{Role: "user", Content: "hi"}}})
		if code == 200 {
			if err != nil || res.Message.Content != "hello" {
				t.Fatalf("%v %v", res, err)
			}
		} else if err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Fatalf("invalid error: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.ChatCompletion(ctx, &ChatRequest{Model: "gemini-test"}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

type agTestTransport func(*http.Request) (*http.Response, error)

func (f agTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAntigravityRefreshAndPrivateStorage(t *testing.T) {
	t.Setenv("CVKEHARNESS_ANTIGRAVITY_CLIENT_ID", "test-client-id")
	t.Setenv("CVKEHARNESS_ANTIGRAVITY_CLIENT_SECRET", "test-client-secret")
	path := filepath.Join(t.TempDir(), "auth.json")
	auth := AntigravityAuth{AccessToken: "old", RefreshToken: "refresh", ProjectID: "project", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := saveAntigravityAuth(path, auth); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: agTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != agTokenURL {
			t.Fatal("unexpected token destination")
		}
		r.ParseForm()
		if r.Form.Get("refresh_token") != "refresh" || r.Form.Get("client_id") != "test-client-id" || r.Form.Get("client_secret") != "test-client-secret" {
			t.Fatal("missing refresh token or configured OAuth client")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"new","expires_in":3600}`)), Header: make(http.Header)}, nil
	})}
	got, err := antigravityCredential(context.Background(), client, path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := LoadAntigravityAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new" || saved.RefreshToken != "refresh" || saved.ProjectID != "project" {
		t.Fatal("refresh lost state")
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatal("credential permissions not private")
	}
}
