package provider

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAntigravityMissingClientStopsBeforeOAuth(t *testing.T) {
	for _, tc := range []struct {
		name, id, secret string
	}{
		{name: "both absent"},
		{name: "secret absent", id: "test-client-id"},
		{name: "id absent", secret: "test-client-secret"},
		{name: "whitespace", id: " \t", secret: " \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CVKEHARNESS_ANTIGRAVITY_CLIENT_ID", tc.id)
			t.Setenv("CVKEHARNESS_ANTIGRAVITY_CLIENT_SECRET", tc.secret)
			check := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "OAuth client is not configured") {
					t.Fatalf("expected client configuration error, got %v", err)
				}
				if strings.Contains(err.Error(), "test-client-") {
					t.Fatal("configuration error exposed a configured value")
				}
			}
			// A missing client must fail before opening the loopback listener or
			// printing an authorization URL, even when the context is live.
			var output strings.Builder
			check(LoginAntigravity(context.Background(), &output))
			if output.Len() != 0 {
				t.Fatal("login emitted an authorization URL without client configuration")
			}
			client := &http.Client{Transport: agTestTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("token exchange sent a request without client configuration")
				return &http.Response{Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			_, err := agExchange(context.Background(), client, agTokenURL, url.Values{"grant_type": {"refresh_token"}})
			check(err)
		})
	}
}
