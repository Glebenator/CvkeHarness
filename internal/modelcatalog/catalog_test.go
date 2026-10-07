package modelcatalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebenator/cvkeharness/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCatalogUsesNamedEndpointAndReturnsFullTextList(t *testing.T) {
	t.Parallel()
	var models []string
	for i := 0; i < 70; i++ {
		models = append(models, fmt.Sprintf(`{"id":"future-text-%02d"}`, i))
	}
	models = append(models, `{"id":"future-text-01"}`, `{"id":"text-embedding-3-small"}`, `{"id":"gpt-image-1"}`)
	loader := Loader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://named-server.test:1234/v1/models" {
			t.Fatalf("wrong connection endpoint: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer named-secret" {
			t.Fatal("wrong connection credential")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[` + strings.Join(models, ",") + `]}`))}, nil
	})}}
	result := loader.Fetch(context.Background(), config.Connection{Provider: "openai", BaseURL: "http://named-server.test:1234/v1/", APIKey: "named-secret"})
	if !result.Live || len(result.Items) != 71 {
		t.Fatalf("catalog truncated or retained non-text/duplicate models: %d, %+v", len(result.Items), result)
	}
	if result.Items[69].ID != "future-text-69" || result.Items[70].ID != CustomModelID {
		t.Fatal("complete sorted catalog/custom option missing")
	}
}

func TestCatalogFailuresAreExplicitAndDoNotExposeCredentials(t *testing.T) {
	t.Parallel()
	loader := Loader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("sensitive request error named-secret")
	})}}
	result := loader.Fetch(context.Background(), config.Connection{Provider: "openai", APIKey: "named-secret"})
	if result.Live || len(result.Items) != 1 || result.Items[0].ID != CustomModelID || result.Message == "" {
		t.Fatalf("failure presented as verified choices: %+v", result)
	}
	if strings.Contains(result.Message, "named-secret") {
		t.Fatal("connection failure leaked secret")
	}
}

func TestCatalogCodexUsesConnectionAuthDirectoryAndRetainsStaleChoices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := `{"fetched_at":"2020-01-01T00:00:00Z","models":[{"slug":"selected-account-model","visibility":"list","supported_in_api":true},{"slug":"hidden","visibility":"hide","supported_in_api":true}]}`
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	result := (Loader{Now: func() time.Time { return time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC) }}).Fetch(context.Background(), config.Connection{Provider: "codex", AuthFile: filepath.Join(dir, "auth.json")})
	if result.Live || result.Source != "codex-cache" || result.Items[0].ID != "selected-account-model" || len(result.Items) != 2 || result.Message == "" {
		t.Fatalf("bad stale account result: %+v", result)
	}
}

func TestCatalogLMStudioConnectionsRemainIndependent(t *testing.T) {
	t.Parallel()
	loader := Loader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"` + r.URL.Host + `","state":"loaded"}]}`))}, nil
	})}}
	for _, host := range []string{"laptop.test:1234", "server.test:5678"} {
		result := loader.Fetch(context.Background(), config.Connection{Provider: "lmstudio", BaseURL: "http://" + host + "/v1"})
		if !result.Live || result.Items[0].ID != host || !strings.Contains(result.Items[0].Description, "loaded") {
			t.Fatalf("named local catalog crossed connections: %+v", result)
		}
	}
}

func TestCatalogUnsupportedProviderDoesNotUseOpenRouter(t *testing.T) {
	t.Parallel()
	loader := Loader{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported provider made a network call")
		return nil, nil
	})}}
	result := loader.Fetch(context.Background(), config.Connection{Provider: "unknown"})
	if result.Live || result.Source != "unknown" || result.Message == "" {
		t.Fatal("unsupported provider silently fell back to another provider")
	}
}
