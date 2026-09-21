package recovery

import (
	"strings"
	"testing"
)

func testNGINXConfig() string {
	return `worker_processes 1;
pid /lab/nginx.pid;
error_log stderr notice;
events { worker_connections 128; }
http {
 access_log off;
 default_type text/plain;
 client_body_temp_path /lab/client_body_temp_path;
 proxy_temp_path /lab/proxy_temp_path;
 fastcgi_temp_path /lab/fastcgi_temp_path;
 uwsgi_temp_path /lab/uwsgi_temp_path;
 scgi_temp_path /lab/scgi_temp_path;
 server { listen 127.0.0.1:8080;
  location = /health { add_header X-Cvke-Worker $pid always; return 200 "healthy"; }
  location / { return 200 "original"; }
 }
}`
}

func testNGINXService() NGINXService {
	return NGINXService{Name: "test", Binary: "/usr/sbin/nginx", Prefix: "/lab", ConfigPath: "/lab/nginx.conf", PIDFile: "/lab/nginx.pid", HealthURL: "http://127.0.0.1:8080/health", HealthStatus: 200, HealthSHA256: sum([]byte("healthy")), TimeoutSeconds: 3}
}

func TestNGINXProtectedStructure(t *testing.T) {
	s, before := testNGINXService(), testNGINXConfig()
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	if err := validateNGINXChange(s, []byte(before), []byte(strings.Replace(before, "original", "changed", 1))); err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]string{
		"include":               before + "\ninclude /etc/passwd;",
		"module":                before + "\nload_module /tmp/module.so;",
		"log path":              strings.Replace(before, "error_log stderr notice", "error_log /etc/passwd notice", 1),
		"listener":              strings.Replace(before, "127.0.0.1:8080", "0.0.0.0:8080", 1),
		"fake worker":           strings.Replace(before, "$pid", "123", 1),
		"arbitrary header":      strings.Replace(before, "X-Cvke-Worker", "Authorization", 1),
		"PID":                   strings.Replace(before, "/lab/nginx.pid", "/lab/other.pid", 1),
		"temp path":             strings.Replace(before, "proxy_temp_path /lab/proxy_temp_path", "proxy_temp_path /etc", 1),
		"numeric overflow":      strings.Replace(before, "worker_processes 1", "worker_processes 999999999999999999999", 1),
		"fractional worker":     strings.Replace(before, "worker_processes 1", "worker_processes 1.5", 1),
		"malformed quote":       strings.Replace(before, `"healthy"`, `"healthy`, 1),
		"escaped interpolation": strings.Replace(before, `"healthy"`, `"\$pid"`, 1),
		"removed field":         strings.Replace(before, "default_type text/plain;", "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateNGINXChange(s, []byte(before), []byte(candidate)); err == nil {
				t.Fatal("unsafe or unsupported candidate accepted")
			}
		})
	}
}

func TestNGINXFixedHealthBounds(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:8080/health", "http://localhost:8080/health", "http://127.0.0.1:80/health", "http://127.0.0.1:8080/health?secret=1", "http://user:pass@127.0.0.1:8080/health", "http://127.0.0.1:8080/%68ealth"} {
		s := testNGINXService()
		s.HealthURL = endpoint
		if err := s.validate(); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func FuzzNGINXConfigBoundary(f *testing.F) {
	f.Add(testNGINXConfig())
	f.Add(`http { include "/etc/passwd"; }`)
	f.Fuzz(func(t *testing.T, candidate string) {
		if len(candidate) > 64<<10 {
			t.Skip()
		}
		_ = validateNGINXChange(testNGINXService(), []byte(testNGINXConfig()), []byte(candidate))
	})
}
