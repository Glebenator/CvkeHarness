package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAntigravityProjectDiscovery(t *testing.T) {
	type step struct {
		method, path, body string
		status             int
	}
	for _, tc := range []struct {
		name      string
		steps     []step
		want, err string
	}{
		{name: "existing string", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `{"cloudaicompanionProject":"mine"}`, 200}}, want: "mine"},
		{name: "second endpoint object", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `{}`, 200}, {"POST", "https://two.test/v1internal:loadCodeAssist", `{"cloudaicompanionProject":{"id":"mine"}}`, 200}}, want: "mine"},
		{name: "onboarding polling", steps: []step{
			{"POST", "https://one.test/v1internal:loadCodeAssist", `{"allowedTiers":[{"id":"offered","isDefault":true}]}`, 200},
			{"POST", "https://two.test/v1internal:loadCodeAssist", `{}`, 200},
			{"POST", "https://one.test/v1internal:onboardUser", `{"name":"operations/test-op","done":false}`, 200},
			{"GET", "https://one.test/v1internal/operations/test-op", `{"done":true,"response":{"cloudaicompanionProject":{"id":"mine"}}}`, 200},
		}, want: "mine"},
		{name: "reload after onboarding", steps: []step{
			{"POST", "https://one.test/v1internal:loadCodeAssist", `{"allowedTiers":[{"id":"offered","isDefault":true}]}`, 200},
			{"POST", "https://two.test/v1internal:loadCodeAssist", `{}`, 200},
			{"POST", "https://one.test/v1internal:onboardUser", `{"done":true}`, 200},
			{"POST", "https://one.test/v1internal:loadCodeAssist", `{"cloudaicompanionProject":"mine"}`, 200},
		}, want: "mine"},
		{name: "no guessed project", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `{}`, 200}, {"POST", "https://two.test/v1internal:loadCodeAssist", `{}`, 200}}, err: "discovery compatibility"},
		{name: "preserve existing tier", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `{"currentTier":{"id":"paid"},"allowedTiers":[{"id":"offered","isDefault":true}]}`, 200}, {"POST", "https://two.test/v1internal:loadCodeAssist", `{}`, 200}}, err: "current tier present: true"},
		{name: "no caller-owned project creation", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `{"allowedTiers":[{"id":"offered","isDefault":true,"userDefinedCloudaicompanionProject":true}]}`, 200}, {"POST", "https://two.test/v1internal:loadCodeAssist", `{}`, 200}}, err: "discovery compatibility"},
		{name: "no auth fallback", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `secret`, 403}}, err: "403"},
		{name: "no quota fallback", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `secret`, 429}}, err: "429"},
		{name: "invalid json", steps: []step{{"POST", "https://one.test/v1internal:loadCodeAssist", `not json secret`, 200}}, err: "invalid Google"},
		{name: "no credential exfiltration", steps: []step{
			{"POST", "https://one.test/v1internal:loadCodeAssist", `{"allowedTiers":[{"id":"offered","isDefault":true}]}`, 200},
			{"POST", "https://two.test/v1internal:loadCodeAssist", `{}`, 200},
			{"POST", "https://one.test/v1internal:onboardUser", `{"name":"https://evil.test/operations/steal"}`, 200},
		}, err: "unsupported"},
		{name: "operation error", steps: []step{
			{"POST", "https://one.test/v1internal:loadCodeAssist", `{"allowedTiers":[{"id":"offered","isDefault":true}]}`, 200},
			{"POST", "https://two.test/v1internal:loadCodeAssist", `{}`, 200},
			{"POST", "https://one.test/v1internal:onboardUser", `{"done":true,"error":{"message":"secret"}}`, 200},
		}, err: "onboarding error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := 0
			client := &http.Client{Transport: agTestTransport(func(r *http.Request) (*http.Response, error) {
				if index >= len(tc.steps) {
					t.Fatal("unexpected extra request")
				}
				step := tc.steps[index]
				index++
				if r.Method != step.method || r.URL.String() != step.path {
					t.Fatalf("got %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Fatal("missing auth")
				}
				if strings.HasSuffix(r.URL.Path, ":onboardUser") {
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("invalid onboarding body")
					}
					if body["tierId"] != "offered" || body["cloudaicompanionProject"] != nil {
						t.Fatal("wrong tier or guessed project")
					}
				}
				return &http.Response{StatusCode: step.status, Body: io.NopCloser(strings.NewReader(step.body)), Header: make(http.Header)}, nil
			})}
			got, err := discoverAntigravityProject(context.Background(), client, "secret", []string{"https://one.test", "https://two.test"}, 0, io.Discard)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) || strings.Contains(err.Error(), "secret") {
					t.Fatalf("unexpected error %v", err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q %v", got, err)
			}
			if index != len(tc.steps) {
				t.Fatal("missing requests")
			}
		})
	}
}

func TestAntigravityProjectPollingBounded(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: agTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := `{"name":"operations/pending","done":false}`
		if strings.HasSuffix(r.URL.Path, ":loadCodeAssist") {
			body = `{"allowedTiers":[{"id":"offered","isDefault":true}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	_, err := discoverAntigravityProject(context.Background(), client, "secret", []string{"https://one.test"}, 0, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "pending") || requests != 10 {
		t.Fatalf("polling was not bounded: requests=%d err=%v", requests, err)
	}
}

func TestAntigravityEligibilityDiagnostics(t *testing.T) {
	var data agProjectResponse
	raw := `{"allowedTiers":[{"id":"standard-tier","isDefault":true,"userDefinedCloudaicompanionProject":true}],"ineligibleTiers":[{"tierId":"free-tier","reasonCode":"VALIDATION_REQUIRED","reasonMessage":"private email@example.com","validationUrl":"https://example.com/?token=secret"},{"tierId":"injected-secret","reasonCode":"injected-secret"}]}`
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	got := agEligibilitySummary(data)
	for _, expected := range []string{"standard-tier(default=true,requires_project=true)", "free-tier=VALIDATION_REQUIRED", "unrecognized=unrecognized"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing %s from %s", expected, got)
		}
	}
	for _, private := range []string{"secret", "email@", "https:", "private"} {
		if strings.Contains(got, private) {
			t.Fatalf("sensitive field leaked: %s", got)
		}
	}
}

func TestAntigravityUnknownReasonEnumPreserved(t *testing.T) {
	if got := agDiagnosticReason("NEW_BACKEND_ELIGIBILITY_REASON"); got != "NEW_BACKEND_ELIGIBILITY_REASON" {
		t.Fatalf("new enum hidden: %s", got)
	}
	for _, code := range []string{"https://example.com/?token=secret", "private@example.com", "ERROR\nINJECTED", "\x1b[31mERROR", strings.Repeat("A", 97)} {
		if got := agDiagnosticReason(code); got != "unrecognized" {
			t.Fatalf("unsafe diagnostic accepted: %q", got)
		}
	}
}
