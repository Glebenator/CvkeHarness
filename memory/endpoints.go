package memory

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/glebenator/cvkeharness/state"
)

// Match a complete, direct declaration only. Do not mine quotes, documents,
// conversation history, tool results, conditional requests, or compound tasks.
var endpointDeclarationPattern = regexp.MustCompile(`(?i)^(?:please +)?(?:(?:remember|save) +(?:that +)?)?my +([a-z][a-z0-9 '’-]{0,49}?)(?: +(ip address|ip|address|hostname|endpoint))? +is +([^\s]+?)[.!]?$`)
var endpointLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var endpointUserPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`)
var previousTargetPattern = regexp.MustCompile(`(?i)\b(that (server|host|machine)|on it|its (disk|logs|memory|cpu)|is it (running|reachable|online)|check it)\b`)

func refersToPreviousTarget(task string) bool {
	return previousTargetPattern.MatchString(task)
}

// ExplicitEndpointRequest distinguishes the optional request to persist from a fact.
func ExplicitEndpointRequest(message string) bool {
	message = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(message)), "please ")
	return strings.HasPrefix(message, "remember ") || strings.HasPrefix(message, "save ")
}

// RemoteEndpointName recognizes a bounded named remote reference, not arbitrary prose.
var remoteNamePattern = regexp.MustCompile(`(?i)\bmy +(homeserver|(?:[a-z0-9'’-]+ +)*?server|nas)\b`)

func RemoteEndpointName(message string) string {
	match := remoteNamePattern.FindStringSubmatch(message)
	if len(match) < 2 {
		return ""
	}
	return normalizeEndpointName(match[1])
}

type EndpointDeclaration struct {
	Name     string
	Endpoint string
}

// ParseEndpointDeclaration accepts a deliberately small grammar rather than
// giving a model authority to label arbitrary text as user-confirmed memory.
func ParseEndpointDeclaration(message string) (EndpointDeclaration, bool) {
	message = strings.TrimSpace(message)
	if len(message) > 320 || strings.ContainsAny(message, "\n\r\t\"`<>?;“”‘") {
		return EndpointDeclaration{}, false
	}
	match := endpointDeclarationPattern.FindStringSubmatch(message)
	if match == nil {
		return EndpointDeclaration{}, false
	}
	name := normalizeEndpointName(match[1])
	name = strings.TrimSuffix(strings.TrimSuffix(name, "'s"), "’s")
	// A possessive without an apostrophe ("servers address") is supported,
	// while a normal plural declaration is not silently made singular.
	if match[2] != "" && strings.HasSuffix(name, "servers") {
		name = strings.TrimSuffix(name, "s")
	}
	if name != "server" && name != "nas" && !strings.HasSuffix(name, " server") {
		return EndpointDeclaration{}, false
	}
	endpoint, ok := normalizeEndpoint(match[3])
	if !ok {
		return EndpointDeclaration{}, false
	}
	if match[2] == "" && !strings.ContainsAny(endpoint, ".:@-") {
		// "Remember my server is running" is a state claim, not an address.
		return EndpointDeclaration{}, false
	}
	return EndpointDeclaration{Name: name, Endpoint: endpoint}, true
}

func normalizeEndpointName(name string) string {
	name = strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
	return strings.ReplaceAll(name, "homeserver", "home server")
}

func normalizeEndpoint(value string) (string, bool) {
	user, host := "", value
	if strings.Contains(value, "@") {
		parts := strings.Split(value, "@")
		if len(parts) != 2 || !endpointUserPattern.MatchString(parts[0]) {
			return "", false
		}
		user, host = parts[0]+"@", parts[1]
	}
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil && addr.Zone() == "" {
		return user + addr.String(), true
	}
	if len(host) > 253 || host == "" || strings.Trim(host, "0123456789.") == "" {
		return "", false
	}
	host = strings.ToLower(host)
	for _, label := range strings.Split(host, ".") {
		if !endpointLabelPattern.MatchString(label) {
			return "", false
		}
	}
	return user + host, true
}

func endpointIntegrity(item state.UserEndpoint) string {
	return evidenceHash("direct_user_endpoint", item.Name, item.Endpoint, item.Declaration, item.DeclaredAt.UTC().Format(time.RFC3339Nano))
}

// RememberUserEndpoint must receive the original direct user message from the
// harness, never a model-generated extraction or tool argument.
func (m *Manager) RememberUserEndpoint(ctx context.Context, message string) (state.UserEndpoint, error) {
	r := m.CaptureUserEndpoint(ctx, message, "")
	if !r.Saved() {
		return state.UserEndpoint{}, fmt.Errorf("%s", r.Summary())
	}
	// Verify canonical readback before claiming the declaration is recallable.
	items, err := m.UserEndpoints(ctx)
	if err != nil {
		return state.UserEndpoint{}, err
	}
	for _, saved := range items {
		if saved.Name == r.Name && saved.Endpoint == r.Endpoint {
			return saved, nil
		}
	}
	return state.UserEndpoint{}, fmt.Errorf("endpoint save could not be verified")
}

func (m *Manager) UserEndpoints(ctx context.Context) ([]state.UserEndpoint, error) {
	if m.store == nil || !m.store.Available() {
		return nil, fmt.Errorf("SQLite state is unavailable; endpoint declarations cannot be read")
	}
	items, err := m.store.ListUserEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		parsed, ok := ParseEndpointDeclaration(item.Declaration)
		if !ok || parsed.Name != item.Name || parsed.Endpoint != item.Endpoint || item.DeclaredAt.IsZero() || endpointIntegrity(item) != item.EvidenceHash {
			return nil, fmt.Errorf("saved endpoint declaration %q failed integrity validation", item.Name)
		}
	}
	return items, nil
}

func (m *Manager) ForgetUserEndpoint(ctx context.Context, name string) error {
	if m.store == nil || !m.store.Available() {
		return fmt.Errorf("SQLite state is unavailable")
	}
	return m.store.ForgetUserEndpoint(ctx, normalizeEndpointName(name))
}

func (m *Manager) ShowUserEndpoints(ctx context.Context) (string, error) {
	items, err := m.UserEndpoints(ctx)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("User-declared endpoints (labels only; live identity and command approval remain separate):\n")
	if len(items) == 0 {
		out.WriteString("No endpoint declarations saved.\n")
	}
	for _, item := range items {
		fmt.Fprintf(&out, "- %s: %s (declared %s)\n", item.Name, item.Endpoint, item.DeclaredAt.UTC().Format(time.RFC3339))
	}
	return strings.TrimSpace(out.String()), nil
}

func (m *Manager) endpointHint(ctx context.Context, task string) (*targetHint, bool, error) {
	items, err := m.UserEndpoints(ctx)
	if err != nil {
		return nil, false, err
	}
	text := " " + normalizeEndpointName(task) + " "
	var matches []state.UserEndpoint
	for _, item := range items {
		pattern := regexp.MustCompile(`\bmy +` + regexp.QuoteMeta(item.Name) + `\b`)
		if pattern.MatchString(text) {
			matches = append(matches, item)
		}
	}
	// "My server" can refer to the sole explicitly declared server. Multiple
	// named servers require a specific name rather than an arbitrary choice.
	if len(matches) == 0 && regexp.MustCompile(`\bmy server\b`).MatchString(text) {
		for _, item := range items {
			if strings.HasSuffix(item.Name, " server") {
				matches = append(matches, item)
			}
		}
	}
	if len(matches) > 1 {
		return nil, true, nil
	}
	if len(matches) == 0 {
		return nil, false, nil
	}
	kind := TargetKindUnknown
	if strings.Contains(matches[0].Endpoint, "@") {
		kind = TargetKindSSH
	}
	return parseTargetToken(matches[0].Endpoint, kind), false, nil
}
