package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/httputil"
	"github.com/coolcake/cvkeharness/internal/secrets"
)

const (
	WebSearchProviderTavily = "tavily"

	defaultWebSearchBaseURL         = "https://api.tavily.com"
	defaultWebSearchMaxResults      = 5
	defaultWebSearchDepth           = "basic"
	defaultWebSearchMaxFetchedChars = 12000
	absoluteWebSearchMaxResults     = 10
	absoluteWebFetchMaxChars        = 30000
	maxSearchSnippetChars           = 800
)

var (
	validSearchDepths = map[string]bool{
		"ultra-fast": true,
		"fast":       true,
		"basic":      true,
		"advanced":   true,
	}
	validSearchTopics = map[string]bool{
		"general": true,
		"news":    true,
		"finance": true,
	}
	validTimeRanges = map[string]bool{
		"day":   true,
		"week":  true,
		"month": true,
		"year":  true,
	}
	validExtractDepths = map[string]bool{
		"basic":    true,
		"advanced": true,
	}
	validExtractFormats = map[string]bool{
		"markdown": true,
		"text":     true,
	}
	internalHostnameSuffixes = []string{
		".internal",
		".local",
		".lan",
		".home",
		".corp",
	}
	metadataHostnames = map[string]bool{
		"metadata":                 true,
		"metadata.google.internal": true,
	}
	metadataIPs = map[string]bool{
		"169.254.169.254": true,
		"100.100.100.200": true,
	}
	urlInTextPattern  = regexp.MustCompile(`https?://[^\s"'<>]+`)
	siteFilterPattern = regexp.MustCompile(`(?i)\bsite:([A-Za-z0-9._-]+(?::\d+)?)`)
	hostTokenPattern  = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9-]*(?:\.[A-Za-z0-9][A-Za-z0-9-]*)+)(?::\d+)?\b`)
	alphaPattern      = regexp.MustCompile(`[A-Za-z]`)
)

// WebSearchOptions configures optional external web research tools.
type WebSearchOptions struct {
	Enabled         bool
	Provider        string
	APIKey          string
	BaseURL         string
	MaxResults      int
	SearchDepth     string
	MaxFetchedChars int
	AllowedDomains  []string
	BlockedDomains  []string
}

// NewWebSearchTools creates the configured web search and fetch tools.
func NewWebSearchTools(opts WebSearchOptions) ([]Tool, error) {
	normalized, err := normalizeWebSearchOptions(opts)
	if err != nil {
		return nil, err
	}
	if !normalized.Enabled {
		return nil, nil
	}
	client := newTavilyClient(normalized.APIKey, normalized.BaseURL)
	return []Tool{
		NewWebSearchTool(client, normalized),
		NewWebFetchTool(client, normalized),
	}, nil
}

func normalizeWebSearchOptions(opts WebSearchOptions) (WebSearchOptions, error) {
	opts.Provider = strings.ToLower(strings.TrimSpace(opts.Provider))
	if opts.Provider == "" {
		opts.Provider = WebSearchProviderTavily
	}
	opts.APIKey = strings.TrimSpace(opts.APIKey)
	opts.BaseURL = strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if opts.BaseURL == "" {
		opts.BaseURL = defaultWebSearchBaseURL
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = defaultWebSearchMaxResults
	}
	if opts.MaxResults > absoluteWebSearchMaxResults {
		opts.MaxResults = absoluteWebSearchMaxResults
	}
	opts.SearchDepth = strings.ToLower(strings.TrimSpace(opts.SearchDepth))
	if opts.SearchDepth == "" {
		opts.SearchDepth = defaultWebSearchDepth
	}
	if !validSearchDepths[opts.SearchDepth] {
		return opts, fmt.Errorf("web_search.search_depth must be one of: ultra-fast, fast, basic, advanced")
	}
	if opts.MaxFetchedChars <= 0 {
		opts.MaxFetchedChars = defaultWebSearchMaxFetchedChars
	}
	if opts.MaxFetchedChars > absoluteWebFetchMaxChars {
		opts.MaxFetchedChars = absoluteWebFetchMaxChars
	}
	opts.AllowedDomains = normalizeDomains(opts.AllowedDomains)
	opts.BlockedDomains = normalizeDomains(opts.BlockedDomains)

	if !opts.Enabled {
		return opts, nil
	}
	if err := validateDomainListPublic("web_search.allowed_domains", opts.AllowedDomains); err != nil {
		return opts, err
	}
	if err := validateDomainListPublic("web_search.blocked_domains", opts.BlockedDomains); err != nil {
		return opts, err
	}
	if opts.Provider != WebSearchProviderTavily {
		return opts, fmt.Errorf("unsupported web_search provider %q", opts.Provider)
	}
	if opts.APIKey == "" {
		return opts, fmt.Errorf("web_search is enabled but Tavily API key is missing; set api_keys.tavily or TAVILY_API_KEY")
	}
	return opts, nil
}

type tavilyClient struct {
	apiKey  string
	baseURL string
	http    *httputil.Client
}

func newTavilyClient(apiKey, baseURL string) *tavilyClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultWebSearchBaseURL
	}
	return &tavilyClient{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: baseURL,
		http: httputil.NewClient(20*time.Second, httputil.RetryConfig{
			MaxAttempts: 1,
		}),
	}
}

type tavilySearchRequest struct {
	Query             string   `json:"query"`
	SearchDepth       string   `json:"search_depth"`
	MaxResults        int      `json:"max_results"`
	Topic             string   `json:"topic"`
	TimeRange         string   `json:"time_range,omitempty"`
	IncludeAnswer     bool     `json:"include_answer"`
	IncludeRawContent bool     `json:"include_raw_content"`
	IncludeImages     bool     `json:"include_images"`
	IncludeDomains    []string `json:"include_domains,omitempty"`
	ExcludeDomains    []string `json:"exclude_domains,omitempty"`
	ExactMatch        bool     `json:"exact_match,omitempty"`
	IncludeUsage      bool     `json:"include_usage"`
}

type tavilySearchResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
		Favicon string  `json:"favicon"`
	} `json:"results"`
	Usage struct {
		Credits float64 `json:"credits"`
	} `json:"usage"`
	RequestID string `json:"request_id"`
}

func (c *tavilyClient) Search(ctx context.Context, req tavilySearchRequest) (tavilySearchResponse, error) {
	var out tavilySearchResponse
	if err := c.post(ctx, "/search", req, &out); err != nil {
		return out, err
	}
	return out, nil
}

type tavilyExtractRequest struct {
	URLs          []string `json:"urls"`
	Query         string   `json:"query,omitempty"`
	ExtractDepth  string   `json:"extract_depth"`
	Format        string   `json:"format"`
	IncludeImages bool     `json:"include_images"`
	IncludeUsage  bool     `json:"include_usage"`
}

type tavilyExtractResponse struct {
	Results []struct {
		URL        string `json:"url"`
		RawContent string `json:"raw_content"`
	} `json:"results"`
	FailedResults []map[string]any `json:"failed_results"`
	Usage         struct {
		Credits float64 `json:"credits"`
	} `json:"usage"`
	RequestID string `json:"request_id"`
}

func (c *tavilyClient) Extract(ctx context.Context, req tavilyExtractRequest) (tavilyExtractResponse, error) {
	var out tavilyExtractResponse
	if err := c.post(ctx, "/extract", req, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *tavilyClient) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("Tavily request failed with HTTP %d: %s", resp.StatusCode, msg)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("parse Tavily response: %w", err)
	}
	return nil
}

// WebSearchTool searches public web results through Tavily.
type WebSearchTool struct {
	client *tavilyClient
	opts   WebSearchOptions
}

type webSearchArgs struct {
	Query          string   `json:"query"`
	MaxResults     int      `json:"max_results,omitempty"`
	SearchDepth    string   `json:"search_depth,omitempty"`
	Topic          string   `json:"topic,omitempty"`
	TimeRange      string   `json:"time_range,omitempty"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
	ExactMatch     bool     `json:"exact_match,omitempty"`
}

// NewWebSearchTool creates a Tavily-backed web search tool.
func NewWebSearchTool(client *tavilyClient, opts WebSearchOptions) *WebSearchTool {
	return &WebSearchTool{client: client, opts: opts}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "Searches the public web for current documentation, issues, release notes, and error context. Never send secrets, private hostnames, or internal URLs."
}

func (t *WebSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Public web search query. Do not include secrets, credentials, private hostnames, or internal URLs."},
			"max_results": {"type": "number", "description": "Maximum results to return, capped at 10"},
			"search_depth": {"type": "string", "enum": ["ultra-fast", "fast", "basic", "advanced"]},
			"topic": {"type": "string", "enum": ["general", "news", "finance"]},
			"time_range": {"type": "string", "enum": ["day", "week", "month", "year"]},
			"include_domains": {"type": "array", "items": {"type": "string"}},
			"exclude_domains": {"type": "array", "items": {"type": "string"}},
			"exact_match": {"type": "boolean"}
		},
		"required": ["query"]
	}`)
}

func (t *WebSearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input webSearchArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if secrets.Contains(query) {
		return "", fmt.Errorf("query appears to contain a secret or credential")
	}
	if err := validateSearchQuery(query, t.opts); err != nil {
		return "", err
	}

	searchCap := t.opts.MaxResults
	if searchCap <= 0 || searchCap > absoluteWebSearchMaxResults {
		searchCap = absoluteWebSearchMaxResults
	}
	maxResults, err := boundedPositive(input.MaxResults, t.opts.MaxResults, searchCap, "max_results")
	if err != nil {
		return "", err
	}
	searchDepth := firstNonEmpty(strings.ToLower(strings.TrimSpace(input.SearchDepth)), t.opts.SearchDepth)
	if !validSearchDepths[searchDepth] {
		return "", fmt.Errorf("search_depth must be one of: ultra-fast, fast, basic, advanced")
	}
	topic := firstNonEmpty(strings.ToLower(strings.TrimSpace(input.Topic)), "general")
	if !validSearchTopics[topic] {
		return "", fmt.Errorf("topic must be one of: general, news, finance")
	}
	timeRange := strings.ToLower(strings.TrimSpace(input.TimeRange))
	if timeRange != "" && !validTimeRanges[timeRange] {
		return "", fmt.Errorf("time_range must be one of: day, week, month, year")
	}
	includeDomains, excludeDomains, err := mergedDomainFilters(input.IncludeDomains, input.ExcludeDomains, t.opts)
	if err != nil {
		return "", err
	}

	resp, err := t.client.Search(ctx, tavilySearchRequest{
		Query:             query,
		SearchDepth:       searchDepth,
		MaxResults:        maxResults,
		Topic:             topic,
		TimeRange:         timeRange,
		IncludeAnswer:     false,
		IncludeRawContent: false,
		IncludeImages:     false,
		IncludeDomains:    includeDomains,
		ExcludeDomains:    excludeDomains,
		ExactMatch:        input.ExactMatch,
		IncludeUsage:      true,
	})
	if err != nil {
		return "", err
	}

	type result struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score,omitempty"`
		Favicon string  `json:"favicon,omitempty"`
	}
	out := struct {
		OK           bool     `json:"ok"`
		Provider     string   `json:"provider"`
		Query        string   `json:"query"`
		RequestID    string   `json:"request_id,omitempty"`
		UsageCredits float64  `json:"usage_credits,omitempty"`
		Results      []result `json:"results"`
		Truncated    bool     `json:"truncated"`
	}{
		OK:           true,
		Provider:     WebSearchProviderTavily,
		Query:        firstNonEmpty(resp.Query, query),
		RequestID:    resp.RequestID,
		UsageCredits: resp.Usage.Credits,
	}
	for idx, item := range resp.Results {
		if idx >= maxResults {
			out.Truncated = true
			break
		}
		content, truncated := truncateRunes(strings.TrimSpace(item.Content), maxSearchSnippetChars)
		out.Truncated = out.Truncated || truncated
		out.Results = append(out.Results, result{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.URL),
			Content: content,
			Score:   item.Score,
			Favicon: strings.TrimSpace(item.Favicon),
		})
	}
	return marshalToolResult(out), nil
}

// WebFetchTool extracts a single public web page through Tavily.
type WebFetchTool struct {
	client *tavilyClient
	opts   WebSearchOptions
}

type webFetchArgs struct {
	URL          string `json:"url"`
	Query        string `json:"query,omitempty"`
	Format       string `json:"format,omitempty"`
	ExtractDepth string `json:"extract_depth,omitempty"`
	MaxChars     int    `json:"max_chars,omitempty"`
}

// NewWebFetchTool creates a Tavily-backed public page extraction tool.
func NewWebFetchTool(client *tavilyClient, opts WebSearchOptions) *WebFetchTool {
	return &WebFetchTool{client: client, opts: opts}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string {
	return "Extracts one public http/https page found during research. Blocks localhost, private, metadata, and internal URLs."
}

func (t *WebFetchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "One public http/https URL to extract. Do not use internal URLs or URLs containing credentials."},
			"query": {"type": "string", "description": "Optional user intent to rerank extracted chunks"},
			"format": {"type": "string", "enum": ["markdown", "text"]},
			"extract_depth": {"type": "string", "enum": ["basic", "advanced"]},
			"max_chars": {"type": "number", "description": "Maximum returned content characters, capped by config and at 30000"}
		},
		"required": ["url"]
	}`)
}

func (t *WebFetchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input webFetchArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	rawURL := strings.TrimSpace(input.URL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	query := strings.TrimSpace(input.Query)
	if secrets.Contains(rawURL) || secrets.Contains(query) {
		return "", fmt.Errorf("url or query appears to contain a secret or credential")
	}
	normalizedURL, err := validatePublicURL(rawURL, t.opts)
	if err != nil {
		return "", err
	}
	format := firstNonEmpty(strings.ToLower(strings.TrimSpace(input.Format)), "markdown")
	if !validExtractFormats[format] {
		return "", fmt.Errorf("format must be one of: markdown, text")
	}
	extractDepth := firstNonEmpty(strings.ToLower(strings.TrimSpace(input.ExtractDepth)), "basic")
	if !validExtractDepths[extractDepth] {
		return "", fmt.Errorf("extract_depth must be one of: basic, advanced")
	}
	fetchCap := t.opts.MaxFetchedChars
	if fetchCap <= 0 || fetchCap > absoluteWebFetchMaxChars {
		fetchCap = absoluteWebFetchMaxChars
	}
	maxChars, err := boundedPositive(input.MaxChars, t.opts.MaxFetchedChars, fetchCap, "max_chars")
	if err != nil {
		return "", err
	}

	resp, err := t.client.Extract(ctx, tavilyExtractRequest{
		URLs:          []string{normalizedURL},
		Query:         query,
		ExtractDepth:  extractDepth,
		Format:        format,
		IncludeImages: false,
		IncludeUsage:  true,
	})
	if err != nil {
		return "", err
	}

	content := ""
	ok := false
	if len(resp.Results) > 0 {
		content = strings.TrimSpace(resp.Results[0].RawContent)
		ok = content != ""
	}
	content, truncated := truncateRunes(content, maxChars)
	out := struct {
		OK            bool             `json:"ok"`
		Provider      string           `json:"provider"`
		URL           string           `json:"url"`
		Content       string           `json:"content"`
		Chars         int              `json:"chars"`
		Truncated     bool             `json:"truncated"`
		FailedResults []map[string]any `json:"failed_results,omitempty"`
		RequestID     string           `json:"request_id,omitempty"`
		UsageCredits  float64          `json:"usage_credits,omitempty"`
	}{
		OK:            ok,
		Provider:      WebSearchProviderTavily,
		URL:           normalizedURL,
		Content:       content,
		Chars:         len([]rune(content)),
		Truncated:     truncated,
		FailedResults: resp.FailedResults,
		RequestID:     resp.RequestID,
		UsageCredits:  resp.Usage.Credits,
	}
	return marshalToolResult(out), nil
}

func boundedPositive(requested, configured, cap int, field string) (int, error) {
	if requested < 0 {
		return 0, fmt.Errorf("%s cannot be negative", field)
	}
	if configured <= 0 {
		configured = cap
	}
	if requested == 0 {
		requested = configured
	}
	if requested > cap {
		requested = cap
	}
	return requested, nil
}

func mergedDomainFilters(requestInclude, requestExclude []string, opts WebSearchOptions) ([]string, []string, error) {
	allowed := normalizeDomains(opts.AllowedDomains)
	blocked := normalizeDomains(opts.BlockedDomains)
	include := normalizeDomains(requestInclude)
	exclude := normalizeDomains(requestExclude)

	if err := validateDomainListPublic("include_domains", include); err != nil {
		return nil, nil, err
	}
	if err := validateDomainListPublic("exclude_domains", exclude); err != nil {
		return nil, nil, err
	}

	for _, domain := range include {
		if matchesAnyDomain(domain, blocked) {
			return nil, nil, fmt.Errorf("include_domains contains blocked domain %q", domain)
		}
		if len(allowed) > 0 && !matchesAnyDomain(domain, allowed) {
			return nil, nil, fmt.Errorf("include_domains contains domain outside configured allowlist: %q", domain)
		}
	}
	if len(allowed) > 0 && len(include) == 0 {
		include = allowed
	}
	exclude = append(exclude, blocked...)
	exclude = normalizeDomains(exclude)
	return include, exclude, nil
}

func validateSearchQuery(query string, opts WebSearchOptions) error {
	for _, rawURL := range urlInTextPattern.FindAllString(query, -1) {
		rawURL = trimQueryTargetToken(rawURL)
		if rawURL == "" {
			continue
		}
		if _, err := validatePublicURL(rawURL, opts); err != nil {
			return fmt.Errorf("query contains non-public or disallowed URL %q: %w", rawURL, err)
		}
	}

	for _, match := range siteFilterPattern.FindAllStringSubmatch(query, -1) {
		if len(match) < 2 {
			continue
		}
		domain := trimQueryTargetToken(match[1])
		if err := validateDomainForExternal("query site: filter", domain); err != nil {
			return err
		}
		if len(opts.AllowedDomains) > 0 && !matchesAnyDomain(domain, opts.AllowedDomains) {
			return fmt.Errorf("query site: filter %q is outside configured web_search.allowed_domains", domain)
		}
		if matchesAnyDomain(domain, opts.BlockedDomains) {
			return fmt.Errorf("query site: filter %q is blocked by web_search.blocked_domains", domain)
		}
	}

	for _, match := range hostTokenPattern.FindAllStringSubmatch(query, -1) {
		if len(match) < 2 || !alphaPattern.MatchString(match[1]) {
			continue
		}
		host := trimQueryTargetToken(match[1])
		if host == "" {
			continue
		}
		if isBlockedHost(host) {
			return fmt.Errorf("query contains non-public host %q", host)
		}
		if matchesAnyDomain(host, opts.BlockedDomains) {
			return fmt.Errorf("query contains blocked domain %q", host)
		}
	}

	return nil
}

func validatePublicURL(raw string, opts WebSearchOptions) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("url must use http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("url must not include userinfo")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return "", fmt.Errorf("url host is required")
	}
	if isBlockedHost(host) {
		return "", fmt.Errorf("url host %q is not allowed for web_fetch", host)
	}
	allowed := normalizeDomains(opts.AllowedDomains)
	blocked := normalizeDomains(opts.BlockedDomains)
	if len(allowed) > 0 && !matchesAnyDomain(host, allowed) {
		return "", fmt.Errorf("url host %q is outside configured web_search.allowed_domains", host)
	}
	if matchesAnyDomain(host, blocked) {
		return "", fmt.Errorf("url host %q is blocked by web_search.blocked_domains", host)
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

func validateDomainListPublic(field string, domains []string) error {
	for _, domain := range domains {
		if err := validateDomainForExternal(field, domain); err != nil {
			return err
		}
	}
	return nil
}

func validateDomainForExternal(field, domain string) error {
	domain = trimQueryTargetToken(domain)
	if domain == "" {
		return nil
	}
	host := domain
	if strings.Contains(host, "://") {
		parsed, err := url.Parse(host)
		if err != nil {
			return fmt.Errorf("%s contains invalid domain %q: %w", field, domain, err)
		}
		host = parsed.Hostname()
	}
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, ".")
	if strings.Contains(host, "/") {
		return fmt.Errorf("%s contains invalid domain %q", field, domain)
	}
	if isBlockedHost(host) {
		return fmt.Errorf("%s contains non-public domain %q", field, domain)
	}
	return nil
}

func isBlockedHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || metadataHostnames[host] {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			return true
		}
		addr = addr.Unmap()
		return metadataIPs[addr.String()] ||
			addr.IsLoopback() ||
			addr.IsPrivate() ||
			addr.IsLinkLocalUnicast() ||
			addr.IsLinkLocalMulticast() ||
			addr.IsUnspecified() ||
			addr.IsMulticast()
	}
	if strings.Trim(host, "0123456789.") == "" {
		return true
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range internalHostnameSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func trimQueryTargetToken(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`'\"")
	value = strings.TrimRight(value, ".,;:!?)]}")
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeDomains(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		item = strings.TrimPrefix(item, ".")
		if item == "" || slices.Contains(out, item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func matchesAnyDomain(host string, domains []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func truncateRunes(text string, max int) (string, bool) {
	if max <= 0 {
		return "", text != ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text, false
	}
	return string(runes[:max]), true
}

func marshalToolResult(v any) string {
	data, _ := json.MarshalIndent(v, "", "  ")
	return string(data)
}

func firstNonEmpty(items ...string) string {
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			return item
		}
	}
	return ""
}
