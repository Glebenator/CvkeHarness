package recovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NGINXService is operator configuration, never supplied by a model. This
// deliberately narrow adapter supports an already-running, standalone HTTP
// configuration. Includes, modules, upstreams, TLS and arbitrary log paths are
// refused. A fixed health location must expose X-Cvke-Worker: $pid.
type NGINXService struct {
	ResourceLimits NGINXResourceLimits `json:"resource_limits,omitzero" yaml:"resource_limits,omitempty"`
	Name           string              `json:"name" yaml:"name"`
	Binary         string              `json:"binary" yaml:"binary"`
	ConfigPath     string              `json:"config_path" yaml:"config_path"`
	Prefix         string              `json:"prefix" yaml:"prefix"`
	PIDFile        string              `json:"pid_file" yaml:"pid_file"`
	HealthURL      string              `json:"health_url" yaml:"health_url"`
	HealthStatus   int                 `json:"health_status" yaml:"health_status"`
	HealthSHA256   string              `json:"health_sha256" yaml:"health_sha256"`
	TimeoutSeconds int                 `json:"timeout_seconds" yaml:"timeout_seconds"`
}

type ServiceProcess struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
	Boot  string `json:"boot"`
}

type ServicePlan struct {
	Resources   NGINXResources     `json:"resources,omitzero"`
	Adapter     string             `json:"adapter"`
	Config      NGINXService       `json:"config"`
	Binary      Fingerprint        `json:"binary"`
	Master      ServiceProcess     `json:"master"`
	Directories []ServiceDirectory `json:"directories"`
}

type ServiceDirectory struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

var nginxTempDirectories = []string{"client_body_temp_path", "proxy_temp_path", "fastcgi_temp_path", "uwsgi_temp_path", "scgi_temp_path"}

func (e *Engine) Services() []NGINXService {
	out := make([]NGINXService, 0, len(e.services))
	for _, s := range e.services {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (e *Engine) checkServiceScope(path, selected string) error {
	for _, h := range e.fleet.Hosts {
		if path == h.SSHBinary {
			return fmt.Errorf("fleet transport executable is protected")
		}
	}
	for _, s := range e.sshServices {
		for _, protected := range []string{s.Binary, s.SessionBinary, s.PIDFile, s.HostKeyPath, s.AuthorizedKeysPath} {
			if path == protected {
				return fmt.Errorf("managed SSH executable, identity and authentication assets are protected")
			}
		}
		if path == s.ConfigPath && selected != "ssh:"+s.Name {
			return fmt.Errorf("managed SSH config requires its guarded transaction")
		}
	}
	for _, service := range e.services {
		if path == service.Binary || path == service.PIDFile {
			return fmt.Errorf("service executable and PID file are protected")
		}
		if path == service.ConfigPath && selected != service.Name {
			return fmt.Errorf("configured service file requires its typed service transaction")
		}
	}
	return nil
}

func (s NGINXService) validate() error {
	if err := s.ResourceLimits.effective().validate(); err != nil {
		return err
	}
	if len(s.Name) == 0 || len(s.Name) > 64 || strings.Trim(s.Name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return fmt.Errorf("invalid service name")
	}
	for _, p := range []string{s.Binary, s.ConfigPath, s.Prefix, s.PIDFile} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, " \t\r\n\x00;$") {
			return fmt.Errorf("service paths must be clean absolute paths without whitespace or expansion")
		}
	}
	if s.Prefix == "/" || !within(s.ConfigPath, s.Prefix) || !within(s.PIDFile, s.Prefix) || s.ConfigPath == s.PIDFile {
		return fmt.Errorf("config and distinct PID file must be under a dedicated service prefix")
	}
	u, err := url.Parse(s.HealthURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path == "" {
		return fmt.Errorf("health URL must be a literal http://127.0.0.1:port/path without credentials or query")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1024 || port > 65535 || s.HealthStatus < 200 || s.HealthStatus > 299 || len(s.HealthSHA256) != 64 || strings.Trim(s.HealthSHA256, "0123456789abcdef") != "" || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 30 {
		return fmt.Errorf("invalid service health port/status/hash or timeout (1..30 seconds)")
	}
	return nil
}

func serviceBinary(s NGINXService) (Fingerprint, error) {
	p, err := parentFor(s.Binary)
	if err != nil {
		return Fingerprint{}, err
	}
	defer p.Close()
	f, _, err := readRegular(p, filepath.Base(s.Binary), 64<<20)
	if err != nil {
		return f, err
	}
	if !f.Exists || f.Mode&0111 == 0 || f.Mode&0022 != 0 {
		return f, fmt.Errorf("service executable must be regular, executable and not group/world writable")
	}
	return f, nil
}

func (e *Engine) prepareService(name string, op Operation, original, candidates [][]byte) (*ServicePlan, error) {
	s, ok := e.services[name]
	if !ok {
		return nil, fmt.Errorf("service is not operator-configured")
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	if len(op.Plan.Entries) != 1 || op.Plan.Entries[0].Action != "replace" || op.Plan.Entries[0].Path != s.ConfigPath {
		return nil, fmt.Errorf("NGINX transaction requires exactly one replacement of the configured standalone config")
	}
	if _, err := e.checkPath(s.PIDFile); err != nil {
		return nil, err
	}
	if err := validateNGINXChange(s, original[0], candidates[0]); err != nil {
		return nil, err
	}
	b, err := serviceBinary(s)
	if err != nil {
		return nil, err
	}
	master, err := currentServiceMaster(s)
	if err != nil {
		return nil, err
	}
	p := &ServicePlan{Adapter: "nginx-standalone-v1", Config: s, Binary: b, Master: master}
	if _, err := nginxShapeWithResources(s, string(candidates[0]), &p.Resources); err != nil {
		return nil, err
	}
	if err := validateNGINXResourceBudget(p.Resources, s.ResourceLimits.effective()); err != nil {
		return nil, err
	}
	for _, name := range nginxTempDirectories {
		path := filepath.Join(s.Prefix, name)
		d, err := openDirectory(path)
		if err != nil {
			return nil, fmt.Errorf("service temp directory must already exist without symlinks: %w", err)
		}
		dev, ino, err := dirIdentity(d)
		d.Close()
		if err != nil || dev != op.Plan.Entries[0].ParentDevice {
			return nil, fmt.Errorf("service temp directory crosses a mount or is unavailable")
		}
		p.Directories = append(p.Directories, ServiceDirectory{Path: path, Device: dev, Inode: ino})
	}
	if err := checkServiceCapability(*p); err != nil {
		return nil, err
	}
	return p, nil
}

func (e *Engine) servicePreflight(ctx context.Context, op *Operation) error {
	p := op.Plan.Service
	if p.Adapter != "nginx-standalone-v1" || e.services[p.Config.Name] != p.Config {
		return fmt.Errorf("service configuration changed or adapter is unsupported; prepare again")
	}
	if err := verifyServiceIdentity(*p); err != nil {
		return err
	}
	if err := checkServiceCapability(*p); err != nil {
		return err
	}
	if err := e.checkServiceResources(ctx, op); err != nil {
		return err
	}
	// Validation can open NGINX runtime files. It happens only under the
	// explicit service approval, with a durable record preceding execution.
	if err := e.save(ctx, op, Validating, ""); err != nil {
		return err
	}
	if err := e.checkpoint("service_validating", -1); err != nil {
		return err
	}
	validate := func() error {
		baselineCtx, cancel := context.WithTimeout(ctx, time.Duration(p.Config.TimeoutSeconds)*time.Second)
		defer cancel()
		if err := checkServiceHealth(baselineCtx, *p, nil); err != nil {
			return fmt.Errorf("baseline health failed: %w", err)
		}
		path := filepath.Join(e.assets, op.ID, op.Plan.Entries[0].Candidate)
		return validateServiceConfig(ctx, *p, path)
	}
	if err := validate(); err != nil {
		return errors.Join(err, e.save(context.WithoutCancel(ctx), op, ValidationFailed, "baseline health or staged syntax validation failed; target config unchanged"))
	}
	return e.checkpoint("service_validated", -1)
}

func verifyServiceIdentity(p ServicePlan) error {
	if p.Adapter != "nginx-standalone-v1" {
		return fmt.Errorf("unsupported service adapter")
	}
	if err := p.Config.validate(); err != nil {
		return err
	}
	b, err := serviceBinary(p.Config)
	if err != nil {
		return err
	}
	if !matches(b, p.Binary, true) {
		return fmt.Errorf("service executable changed; refusing service execution")
	}
	m, err := currentServiceMaster(p.Config)
	if err != nil {
		return err
	}
	if m != p.Master {
		return fmt.Errorf("service master changed or restarted; manual service reconciliation required")
	}
	if len(p.Directories) != len(nginxTempDirectories) {
		return fmt.Errorf("missing service directory identities")
	}
	for i, expected := range p.Directories {
		if expected.Path != filepath.Join(p.Config.Prefix, nginxTempDirectories[i]) {
			return fmt.Errorf("invalid service directory binding")
		}
		d, err := openDirectory(expected.Path)
		if err != nil {
			return err
		}
		dev, ino, err := dirIdentity(d)
		d.Close()
		if err != nil || dev != expected.Device || ino != expected.Inode {
			return fmt.Errorf("service temp directory changed; refusing service execution")
		}
	}
	return nil
}

func validateServiceConfig(ctx context.Context, p ServicePlan, path string) error {
	if err := verifyServiceIdentity(p); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Config.TimeoutSeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, p.Config.Binary, "-t", "-e", "stderr", "-p", p.Config.Prefix, "-c", path)
	command.Dir = p.Config.Prefix
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	// NGINX diagnostics can include raw configuration. Never return or persist
	// them, and never accumulate unbounded child output in memory.
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("NGINX syntax validation failed (diagnostic content suppressed): %w", err)
	}
	return nil
}

func (e *Engine) reloadService(ctx context.Context, op Operation) error {
	p := *op.Plan.Service
	// Validate the actual destination again: staged validation alone is not
	// evidence that the installed file is still valid.
	if err := validateServiceConfig(ctx, p, p.Config.ConfigPath); err != nil {
		return err
	}
	previous, err := serviceWorkers(p.Master)
	if err != nil {
		return err
	}
	if err = signalService(p); err != nil {
		return err
	}
	if err = e.checkpoint("service_signaled", -1); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Config.TimeoutSeconds)*time.Second)
	defer cancel()
	if err = checkServiceHealth(ctx, p, previous); err != nil {
		return err
	}
	return e.checkpoint("service_healthy", -1)
}

// At most one automatic repair. A caller disconnect cannot cancel the bounded
// rollback; process death still requires the provider-independent recovery CLI.
func (e *Engine) serviceFailure(ctx context.Context, op Operation, cause error) (Operation, error) {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(op.Plan.Service.Config.TimeoutSeconds*3+5)*time.Second)
	defer cancel()
	restored, err := e.recoverLocked(recoveryCtx, op)
	if err != nil {
		return restored, errors.Join(cause, fmt.Errorf("automatic service recovery failed: %w", err))
	}
	return restored, fmt.Errorf("service change failed; original configuration and worker health restored: %w", cause)
}

func checkServiceHealth(ctx context.Context, p ServicePlan, previous map[int]string) error {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, ResponseHeaderTimeout: time.Second, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	successes := 0
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("service health did not verify on three fresh connections: %w", err)
		}
		good := false
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Config.HealthURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
			resp.Body.Close()
			pid, pidErr := strconv.Atoi(resp.Header.Get("X-Cvke-Worker"))
			workers, workerErr := serviceWorkers(p.Master)
			start, member := workers[pid]
			oldStart, old := previous[pid]
			good = readErr == nil && len(body) <= 64<<10 && resp.StatusCode == p.Config.HealthStatus && sum(body) == p.Config.HealthSHA256 && pidErr == nil && workerErr == nil && member && (!old || oldStart != start)
		}
		if good {
			successes++
		} else {
			successes = 0
		}
		if successes == 3 {
			return nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
