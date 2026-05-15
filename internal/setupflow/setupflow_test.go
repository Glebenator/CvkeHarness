package setupflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRunner struct {
	paths map[string]string
	runs  map[string]string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (r fakeRunner) LookPath(file string) (string, error) {
	if path, ok := r.paths[file]; ok {
		return path, nil
	}
	return "", fmt.Errorf("%s not found", file)
}

func (r fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if out, ok := r.runs[key]; ok {
		return out, nil
	}
	return "", nil
}

func TestPlanMissingPythonFound(t *testing.T) {
	t.Parallel()

	plan := PlanMissingPython(HostProfile{Python: ToolStatus{Name: "python3", Found: true, Path: "/usr/bin/python3"}})
	if plan.Available {
		t.Fatalf("expected no install plan when Python exists, got %#v", plan)
	}
}

func TestPlanMissingPythonWithSupportedPackageManager(t *testing.T) {
	t.Parallel()

	plan := PlanMissingPython(HostProfile{
		Python:          ToolStatus{Name: "python3"},
		PackageManagers: []ToolStatus{{Name: "brew", Found: true, Path: "/opt/homebrew/bin/brew"}},
	})
	if !plan.Available {
		t.Fatalf("expected install plan, got %#v", plan)
	}
	if got := CommandString(plan.Command); got != "brew install python" {
		t.Fatalf("expected brew install command, got %q", got)
	}
}

func TestPlanMissingPythonWithoutInstaller(t *testing.T) {
	t.Parallel()

	plan := PlanMissingPython(HostProfile{Python: ToolStatus{Name: "python3"}})
	if plan.Available {
		t.Fatalf("expected no install plan without package manager, got %#v", plan)
	}
}

func TestDetectDaemonPlanRequiresLinuxSystemd(t *testing.T) {
	t.Parallel()

	unsupported := DetectDaemonPlan(fakeRunner{}, "darwin")
	if unsupported.Supported {
		t.Fatalf("expected darwin to be unsupported, got %#v", unsupported)
	}

	linuxMissingSystemd := DetectDaemonPlan(fakeRunner{}, "linux")
	if linuxMissingSystemd.Supported {
		t.Fatalf("expected linux without systemctl to be unsupported, got %#v", linuxMissingSystemd)
	}

	linuxSystemd := DetectDaemonPlan(fakeRunner{paths: map[string]string{"systemctl": "/bin/systemctl"}}, "linux")
	if !linuxSystemd.Supported {
		t.Fatalf("expected linux with systemctl to be supported, got %#v", linuxSystemd)
	}
}

func TestScannerDetectsToolsAndPython(t *testing.T) {
	t.Parallel()

	runner := fakeRunner{
		paths: map[string]string{
			"python3": "/usr/bin/python3",
			"brew":    "/opt/homebrew/bin/brew",
		},
		runs: map[string]string{
			"python3 --version": "Python 3.13.0\n",
		},
	}

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}

	profile := (Scanner{Runner: runner, Client: client, GOOS: "darwin", GOARCH: "arm64"}).Scan(context.Background(), "lmstudio")
	if profile.OS != "darwin" || profile.Arch != "arm64" {
		t.Fatalf("expected injected platform, got %#v", profile)
	}
	if !profile.Python.Found || profile.Python.Version != "Python 3.13.0" {
		t.Fatalf("expected python probe, got %#v", profile.Python)
	}
	if len(profile.PackageManagers) == 0 {
		t.Fatalf("expected package manager probes, got %#v", profile.PackageManagers)
	}
}

func TestValidateTavilyKeySendsAuthAndPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tvly-test-key" {
			t.Fatalf("expected Tavily bearer auth, got %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected JSON content type, got %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["query"] == "" || payload["include_answer"] != false || payload["include_raw_content"] != false || payload["include_images"] != false {
			t.Fatalf("unexpected validation payload: %#v", payload)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	if err := validateTavilyKeyWithEndpoint(context.Background(), "tvly-test-key", server.URL, server.Client()); err != nil {
		t.Fatalf("expected Tavily validation to succeed, got %v", err)
	}
}

func TestValidateTavilyKeyRejectsEmptyBeforeHTTP(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	err := validateTavilyKeyWithEndpoint(context.Background(), "", server.URL, server.Client())
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected empty key error, got %v", err)
	}
	if called {
		t.Fatal("expected empty key to be rejected before HTTP")
	}
}

func TestValidateTavilyKeyMapsHTTPFailures(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()

	err := validateTavilyKeyWithEndpoint(context.Background(), "bad-key", server.URL, server.Client())
	if err == nil || !strings.Contains(err.Error(), "invalid Tavily credential") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("expected invalid credential error, got %v", err)
	}
}

func TestParseHostCapacityProbes(t *testing.T) {
	t.Parallel()

	mem := parseFreeMemoryBytes("              total        used\nMem:     17179869184  1024\n")
	if mem != 17179869184 {
		t.Fatalf("expected memory bytes, got %d", mem)
	}

	free := parseDFFreeBytes("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/disk 100 20 80 20%% /\n")
	if free != 80*1024 {
		t.Fatalf("expected df free bytes, got %d", free)
	}
}
