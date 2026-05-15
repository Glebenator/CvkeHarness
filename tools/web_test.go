package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewDefaultRegistryFromOptionsRegistersWebToolsWhenEnabled(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistryFromOptions(DefaultRegistryOptions{
		AllowedCommands: []string{"echo"},
		SafetyMode:      SafetyModeUserConfirm,
		WebSearch: WebSearchOptions{
			Enabled: true,
			APIKey:  "tvly-test-key",
		},
	})
	if err != nil {
		t.Fatalf("NewDefaultRegistryFromOptions returned error: %v", err)
	}
	names := strings.Join(registry.Names(), ",")
	if !strings.Contains(names, "web_search") || !strings.Contains(names, "web_fetch") {
		t.Fatalf("expected web tools to be registered, got %v", registry.Names())
	}
}

func TestNewDefaultRegistryFromOptionsOmitsWebToolsWhenDisabled(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistryFromOptions(DefaultRegistryOptions{
		AllowedCommands: []string{"echo"},
		SafetyMode:      SafetyModeUserConfirm,
	})
	if err != nil {
		t.Fatalf("NewDefaultRegistryFromOptions returned error: %v", err)
	}
	if _, ok := registry.Get("web_search"); ok {
		t.Fatal("did not expect web_search to be registered")
	}
	if _, ok := registry.Get("web_fetch"); ok {
		t.Fatal("did not expect web_fetch to be registered")
	}
}

func TestNewDefaultRegistryFromOptionsRequiresTavilyKeyWhenEnabled(t *testing.T) {
	t.Parallel()

	_, err := NewDefaultRegistryFromOptions(DefaultRegistryOptions{
		AllowedCommands: []string{"echo"},
		SafetyMode:      SafetyModeUserConfirm,
		WebSearch: WebSearchOptions{
			Enabled: true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "Tavily API key is missing") {
		t.Fatalf("expected missing Tavily key error, got %v", err)
	}
}

func TestWebSearchToolSendsTavilyPayloadAndMapsResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("expected /search path, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tvly-test-key" {
			t.Fatalf("unexpected Authorization header %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if payload["query"] != "kubernetes 1.30 release notes" {
			t.Fatalf("unexpected query payload: %#v", payload)
		}
		if payload["max_results"] != float64(3) || payload["search_depth"] != "advanced" || payload["topic"] != "general" {
			t.Fatalf("unexpected search options: %#v", payload)
		}
		if payload["include_answer"] != false || payload["include_raw_content"] != false || payload["include_images"] != false {
			t.Fatalf("expected answer/raw/images to be disabled, got %#v", payload)
		}
		if payload["include_usage"] != true {
			t.Fatalf("expected usage to be requested, got %#v", payload)
		}
		_, _ = w.Write([]byte(`{
			"query": "kubernetes 1.30 release notes",
			"results": [
				{"title":"Kubernetes v1.30","url":"https://kubernetes.io/blog/release","content":"Release notes","score":0.91,"favicon":"https://kubernetes.io/favicon.ico"}
			],
			"usage": {"credits": 1},
			"request_id": "req-search"
		}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(newTavilyClient("tvly-test-key", server.URL), WebSearchOptions{
		MaxResults:  5,
		SearchDepth: "basic",
	})
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{
		"query":"kubernetes 1.30 release notes",
		"max_results":3,
		"search_depth":"advanced"
	}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var out struct {
		OK           bool `json:"ok"`
		Provider     string
		RequestID    string `json:"request_id"`
		UsageCredits int    `json:"usage_credits"`
		Results      []struct {
			Title   string
			URL     string
			Content string
		}
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("Unmarshal returned error: %v\n%s", err, raw)
	}
	if !out.OK || out.Provider != WebSearchProviderTavily || out.RequestID != "req-search" || out.UsageCredits != 1 {
		t.Fatalf("unexpected web_search output: %#v", out)
	}
	if len(out.Results) != 1 || out.Results[0].Title != "Kubernetes v1.30" || out.Results[0].Content != "Release notes" {
		t.Fatalf("unexpected results: %#v", out.Results)
	}
}

func TestWebFetchToolSendsExtractPayloadAndTruncatesContent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extract" {
			t.Fatalf("expected /extract path, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tvly-test-key" {
			t.Fatalf("unexpected Authorization header %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		urls, _ := payload["urls"].([]any)
		if len(urls) != 1 || urls[0] != "https://docs.example.com/runbook" {
			t.Fatalf("unexpected urls payload: %#v", payload)
		}
		if payload["query"] != "restart guidance" || payload["format"] != "text" || payload["extract_depth"] != "advanced" {
			t.Fatalf("unexpected extract payload: %#v", payload)
		}
		_, _ = w.Write([]byte(`{
			"results": [{"url":"https://docs.example.com/runbook","raw_content":"abcdefghijklmnopqrstuvwxyz"}],
			"failed_results": [],
			"usage": {"credits": 1},
			"request_id": "req-fetch"
		}`))
	}))
	defer server.Close()

	tool := NewWebFetchTool(newTavilyClient("tvly-test-key", server.URL), WebSearchOptions{
		MaxFetchedChars: 12,
		AllowedDomains:  []string{"example.com"},
	})
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{
		"url":"https://docs.example.com/runbook",
		"query":"restart guidance",
		"format":"text",
		"extract_depth":"advanced",
		"max_chars":10
	}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var out struct {
		OK        bool
		URL       string
		Content   string
		Chars     int
		Truncated bool
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("Unmarshal returned error: %v\n%s", err, raw)
	}
	if !out.OK || out.URL != "https://docs.example.com/runbook" || out.Content != "abcdefghij" || out.Chars != 10 || !out.Truncated || out.RequestID != "req-fetch" {
		t.Fatalf("unexpected web_fetch output: %#v", out)
	}
}

func TestWebToolReturnsHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer server.Close()

	tool := NewWebSearchTool(newTavilyClient("bad", server.URL), WebSearchOptions{
		MaxResults:  5,
		SearchDepth: "basic",
	})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"public docs"}`))
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected HTTP 401 error, got %v", err)
	}
}

func TestWebSearchCapsRequestedMaxResultsAtConfiguredLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if payload["max_results"] != float64(4) {
			t.Fatalf("expected max_results to be capped at configured limit, got %#v", payload["max_results"])
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(newTavilyClient("key", server.URL), WebSearchOptions{
		MaxResults:  4,
		SearchDepth: "basic",
	})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"public docs","max_results":99}`)); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
}

func TestWebSearchValidationRejectsEmptyAndSecretQueries(t *testing.T) {
	t.Parallel()

	tool := NewWebSearchTool(newTavilyClient("key", "http://127.0.0.1"), WebSearchOptions{
		MaxResults:  5,
		SearchDepth: "basic",
	})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":" "}`)); err == nil {
		t.Fatal("expected empty query to be rejected")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"api_key=sk-12345678901234567890"}`)); err == nil {
		t.Fatal("expected secret-looking query to be rejected")
	}
}

func TestWebSearchRejectsInternalQueryTargetsBeforeHTTP(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatal("web_search should reject internal query targets before HTTP")
	}))
	defer server.Close()

	tool := NewWebSearchTool(newTavilyClient("key", server.URL), WebSearchOptions{
		MaxResults:  5,
		SearchDepth: "basic",
	})
	for _, query := range []string{
		"site:jenkins.internal deploy error",
		"https://service.local/health is failing",
		"prod-db.corp timeout",
		"http://169.254.169.254/latest/meta-data",
	} {
		if _, err := tool.Execute(context.Background(), mustMarshalJSON(t, map[string]any{"query": query})); err == nil {
			t.Fatalf("expected internal query %q to be rejected", query)
		}
	}
	if called {
		t.Fatal("unexpected Tavily request for rejected query")
	}
}

func TestWebDomainFiltersAndPublicURLValidation(t *testing.T) {
	t.Parallel()

	include, exclude, err := mergedDomainFilters(
		[]string{"docs.example.com"},
		[]string{"old.example.com"},
		WebSearchOptions{
			AllowedDomains: []string{"example.com"},
			BlockedDomains: []string{"bad.example.com"},
		},
	)
	if err != nil {
		t.Fatalf("mergedDomainFilters returned error: %v", err)
	}
	if strings.Join(include, ",") != "docs.example.com" || strings.Join(exclude, ",") != "old.example.com,bad.example.com" {
		t.Fatalf("unexpected merged domains include=%v exclude=%v", include, exclude)
	}

	if _, _, err := mergedDomainFilters([]string{"outside.com"}, nil, WebSearchOptions{AllowedDomains: []string{"example.com"}}); err == nil {
		t.Fatal("expected include domain outside allowlist to be rejected")
	}
	if _, _, err := mergedDomainFilters([]string{"bad.example.com"}, nil, WebSearchOptions{BlockedDomains: []string{"bad.example.com"}}); err == nil {
		t.Fatal("expected blocked include domain to be rejected")
	}
	if _, _, err := mergedDomainFilters([]string{"jenkins.internal"}, nil, WebSearchOptions{}); err == nil {
		t.Fatal("expected internal include domain to be rejected")
	}
	if _, _, err := mergedDomainFilters(nil, []string{"service.local"}, WebSearchOptions{}); err == nil {
		t.Fatal("expected internal exclude domain to be rejected")
	}

	opts := WebSearchOptions{
		AllowedDomains: []string{"example.com"},
		BlockedDomains: []string{"bad.example.com"},
	}
	if got, err := validatePublicURL("https://docs.example.com/path", opts); err != nil || got != "https://docs.example.com/path" {
		t.Fatalf("expected public URL to pass, got %q err=%v", got, err)
	}
	for _, raw := range []string{
		"http://localhost:8080",
		"http://127.0.0.1",
		"http://10.0.0.5",
		"http://169.254.169.254/latest/meta-data",
		"https://service.internal/path",
		"https://bad.example.com/path",
		"https://outside.com/path",
	} {
		if _, err := validatePublicURL(raw, opts); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}

func mustMarshalJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	return data
}
