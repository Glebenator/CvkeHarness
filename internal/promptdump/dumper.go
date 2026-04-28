package promptdump

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
)

type Metadata struct {
	Phase     core.Phase
	Provider  string
	Model     string
	TaskClass core.TaskClass
	Iteration int
	Label     string
}

type Dumper struct {
	enabled bool
	dir     string
	runID   string
	mu      sync.Mutex
	entries []*dumpEntry
	seq     atomic.Uint64
}

func New(enabled bool, dir string) *Dumper {
	return &Dumper{
		enabled: enabled,
		dir:     strings.TrimSpace(dir),
		runID:   "run-" + time.Now().UTC().Format("20060102-150405.000"),
	}
}

func (d *Dumper) Enabled() bool {
	return d != nil && d.enabled && d.dir != ""
}

func (d *Dumper) Dump(ctx context.Context, meta Metadata, req *provider.ChatRequest) error {
	_, err := d.Begin(ctx, meta, req)
	return err
}

type Handle struct {
	d         *Dumper
	dump      promptDump
	entry     *dumpEntry
	mdPath    string
	htmlPath  string
	indexPath string
	mdName    string
	htmlName  string
}

type Result struct {
	ActualModel string
	Usage       provider.Usage
	Err         error
}

func (d *Dumper) Begin(ctx context.Context, meta Metadata, req *provider.ChatRequest) (*Handle, error) {
	if !d.Enabled() || req == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	runDir := filepath.Join(d.dir, now.Format("2006-01-02"), d.runID)
	if err := os.MkdirAll(runDir, 0700); err != nil {
		return nil, err
	}
	seq := d.seq.Add(1)
	base := fmt.Sprintf("%s_%03d_%s_%s", now.Format("150405.000"), seq, slug(string(meta.Phase)), slug(firstNonEmpty(meta.Label, meta.Model, req.Model)))
	mdName := base + ".md"
	htmlName := base + ".html"
	mdPath := filepath.Join(runDir, mdName)
	htmlPath := filepath.Join(runDir, htmlName)
	indexPath := filepath.Join(runDir, "index.html")
	tokenEstimate := estimateRequestTokens(req)
	dump := promptDump{CreatedAt: now, Metadata: meta, Request: req, RunID: d.runID, TokenEstimate: tokenEstimate}
	if err := os.WriteFile(mdPath, []byte(renderMarkdown(dump, htmlName, "index.html")), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(htmlPath, []byte(renderHTML(dump, mdName, "index.html")), 0600); err != nil {
		return nil, err
	}

	entry := &dumpEntry{
		CreatedAt: now,
		Metadata:  meta,
		Model:     firstNonEmpty(meta.Model, req.Model),
		Messages:  len(req.Messages),
		Tools:     len(req.Tools),
		Tokens:    tokenEstimate,
		Markdown:  mdName,
		HTML:      htmlName,
	}
	d.mu.Lock()
	d.entries = append(d.entries, entry)
	index := renderRunIndex(d.runID, d.entries)
	d.mu.Unlock()
	if err := os.WriteFile(indexPath, []byte(index), 0600); err != nil {
		return nil, err
	}
	return &Handle{
		d: d, dump: dump, entry: entry, mdPath: mdPath, htmlPath: htmlPath,
		indexPath: indexPath, mdName: mdName, htmlName: htmlName,
	}, nil
}

func (d *Dumper) Finish(handle *Handle, result Result) error {
	if handle == nil || handle.d == nil || !handle.d.Enabled() {
		return nil
	}
	handle.dump.Result = result
	if err := os.WriteFile(handle.mdPath, []byte(renderMarkdown(handle.dump, handle.htmlName, "index.html")), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(handle.htmlPath, []byte(renderHTML(handle.dump, handle.mdName, "index.html")), 0600); err != nil {
		return err
	}

	handle.d.mu.Lock()
	if strings.TrimSpace(result.ActualModel) != "" {
		handle.entry.ActualModel = result.ActualModel
	}
	handle.entry.ActualPromptTokens = result.Usage.PromptTokens
	handle.entry.CompletionTokens = result.Usage.CompletionTokens
	handle.entry.TotalTokens = result.Usage.TotalTokens
	if cached, ok := result.Usage.CachedTokens(); ok {
		handle.entry.CachedTokens = cached
		handle.entry.CachedTokensKnown = true
	}
	if result.Err != nil {
		handle.entry.Error = result.Err.Error()
	}
	index := renderRunIndex(handle.d.runID, handle.d.entries)
	handle.d.mu.Unlock()
	return os.WriteFile(handle.indexPath, []byte(index), 0600)
}

type promptDump struct {
	CreatedAt     time.Time
	Metadata      Metadata
	Request       *provider.ChatRequest
	RunID         string
	TokenEstimate int
	Result        Result
}

type dumpEntry struct {
	CreatedAt          time.Time
	Metadata           Metadata
	Model              string
	Messages           int
	Tools              int
	Tokens             int
	ActualModel        string
	ActualPromptTokens int
	CompletionTokens   int
	TotalTokens        int
	CachedTokens       int
	CachedTokensKnown  bool
	Error              string
	Markdown           string
	HTML               string
}

func renderMarkdown(d promptDump, htmlName, indexName string) string {
	var b strings.Builder
	req := d.Request
	fmt.Fprintf(&b, "# Prompt Dump\n\n")
	fmt.Fprintf(&b, "- Run index: `%s`\n", indexName)
	fmt.Fprintf(&b, "- Created: `%s`\n", d.CreatedAt.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "- Run: `%s`\n", d.RunID)
	fmt.Fprintf(&b, "- Phase: `%s`\n", d.Metadata.Phase)
	fmt.Fprintf(&b, "- Provider: `%s`\n", d.Metadata.Provider)
	fmt.Fprintf(&b, "- Model: `%s`\n", firstNonEmpty(d.Metadata.Model, req.Model))
	fmt.Fprintf(&b, "- Task class: `%s`\n", d.Metadata.TaskClass)
	if d.Metadata.Iteration > 0 {
		fmt.Fprintf(&b, "- Iteration: `%d`\n", d.Metadata.Iteration)
	}
	if d.Metadata.Label != "" {
		fmt.Fprintf(&b, "- Label: `%s`\n", d.Metadata.Label)
	}
	fmt.Fprintf(&b, "- Temperature: `%g`\n", req.Temperature)
	fmt.Fprintf(&b, "- Max tokens: `%d`\n", req.MaxTokens)
	fmt.Fprintf(&b, "- Messages: `%d`\n", len(req.Messages))
	fmt.Fprintf(&b, "- Tools: `%d`\n", len(req.Tools))
	fmt.Fprintf(&b, "- Estimated prompt tokens: `%d`\n", d.TokenEstimate)
	if d.Result.ActualModel != "" {
		fmt.Fprintf(&b, "- Actual model: `%s`\n", d.Result.ActualModel)
	}
	if d.Result.Usage.TotalTokens > 0 || d.Result.Usage.PromptTokens > 0 || d.Result.Usage.CompletionTokens > 0 {
		fmt.Fprintf(&b, "- Actual prompt tokens: `%d`\n", d.Result.Usage.PromptTokens)
		fmt.Fprintf(&b, "- Completion tokens: `%d`\n", d.Result.Usage.CompletionTokens)
		fmt.Fprintf(&b, "- Total tokens: `%d`\n", d.Result.Usage.TotalTokens)
		if cached, ok := d.Result.Usage.CachedTokens(); ok {
			fmt.Fprintf(&b, "- Cached prompt tokens: `%d`\n", cached)
		}
	}
	if d.Result.Err != nil {
		fmt.Fprintf(&b, "- Error: `%s`\n", d.Result.Err.Error())
	}
	fmt.Fprintf(&b, "- HTML: `%s`\n\n", htmlName)

	b.WriteString("## Messages\n\n")
	for i, msg := range req.Messages {
		fmt.Fprintf(&b, "### %02d. %s\n\n", i+1, msg.Role)
		if msg.ToolCallID != "" {
			fmt.Fprintf(&b, "- Tool call ID: `%s`\n\n", msg.ToolCallID)
		}
		if len(msg.ToolCalls) > 0 {
			b.WriteString("Tool calls:\n\n```json\n")
			b.WriteString(jsonPretty(msg.ToolCalls))
			b.WriteString("\n```\n\n")
		}
		if msg.Content != "" {
			b.WriteString("```text\n")
			b.WriteString(msg.Content)
			if !strings.HasSuffix(msg.Content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
		if len(msg.ResponseItems) > 0 {
			b.WriteString("Provider response items:\n\n```json\n")
			b.WriteString(jsonPretty(msg.ResponseItems))
			b.WriteString("\n```\n\n")
		}
	}

	b.WriteString("## Tools\n\n")
	if len(req.Tools) == 0 {
		b.WriteString("_No tools supplied._\n\n")
	} else {
		for i, tool := range req.Tools {
			fmt.Fprintf(&b, "### %02d. %s\n\n", i+1, tool.Function.Name)
			b.WriteString("```json\n")
			b.WriteString(jsonPretty(tool))
			b.WriteString("\n```\n\n")
		}
	}

	b.WriteString("## Raw Request\n\n```json\n")
	b.WriteString(jsonPretty(req))
	b.WriteString("\n```\n")
	return b.String()
}

func renderHTML(d promptDump, mdName, indexName string) string {
	req := d.Request
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>Prompt Dump</title>")
	b.WriteString(`<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;background:#181716;color:#e7e1d8}
header{position:sticky;top:0;background:#24211f;border-bottom:1px solid #4b4641;padding:18px 28px;z-index:2}
main{padding:22px 28px;max-width:1180px}
.grid{display:grid;grid-template-columns:160px 1fr;gap:6px 14px;color:#c8bfb5}
.pill{display:inline-block;border:1px solid #6d6258;border-radius:5px;padding:2px 7px;margin:2px;color:#f0c38a}
details{border:1px solid #4b4641;border-radius:6px;margin:14px 0;background:#211f1d}
summary{cursor:pointer;padding:12px 14px;color:#f0c38a;font-weight:700}
pre{white-space:pre-wrap;word-break:break-word;margin:0;padding:14px;background:#11100f;color:#d8d0c8;overflow:auto}
.meta{padding:0 14px 14px;color:#9d948c}
a{color:#f0c38a}
</style></head><body>`)
	b.WriteString("<header><h1>Prompt Dump</h1><div>")
	fmt.Fprintf(&b, "<span class=\"pill\">%s</span><span class=\"pill\">%s</span><span class=\"pill\">%s</span>", html.EscapeString(string(d.Metadata.Phase)), html.EscapeString(d.Metadata.Provider), html.EscapeString(firstNonEmpty(d.Metadata.Model, req.Model)))
	fmt.Fprintf(&b, " <a href=\"%s\">Run index</a>", html.EscapeString(indexName))
	b.WriteString("</div></header><main>")
	b.WriteString("<section class=\"grid\">")
	htmlKV(&b, "Created", d.CreatedAt.Format(time.RFC3339Nano))
	htmlKV(&b, "Run", d.RunID)
	htmlKV(&b, "Task class", string(d.Metadata.TaskClass))
	htmlKV(&b, "Iteration", fmt.Sprintf("%d", d.Metadata.Iteration))
	htmlKV(&b, "Label", d.Metadata.Label)
	htmlKV(&b, "Temperature", fmt.Sprintf("%g", req.Temperature))
	htmlKV(&b, "Max tokens", fmt.Sprintf("%d", req.MaxTokens))
	htmlKV(&b, "Messages", fmt.Sprintf("%d", len(req.Messages)))
	htmlKV(&b, "Tools", fmt.Sprintf("%d", len(req.Tools)))
	htmlKV(&b, "Estimated prompt tokens", fmt.Sprintf("%d", d.TokenEstimate))
	htmlKV(&b, "Actual model", d.Result.ActualModel)
	if d.Result.Usage.TotalTokens > 0 || d.Result.Usage.PromptTokens > 0 || d.Result.Usage.CompletionTokens > 0 {
		htmlKV(&b, "Actual prompt tokens", fmt.Sprintf("%d", d.Result.Usage.PromptTokens))
		htmlKV(&b, "Completion tokens", fmt.Sprintf("%d", d.Result.Usage.CompletionTokens))
		htmlKV(&b, "Total tokens", fmt.Sprintf("%d", d.Result.Usage.TotalTokens))
		if cached, ok := d.Result.Usage.CachedTokens(); ok {
			htmlKV(&b, "Cached prompt tokens", fmt.Sprintf("%d", cached))
		}
	}
	if d.Result.Err != nil {
		htmlKV(&b, "Error", d.Result.Err.Error())
	}
	htmlKV(&b, "Markdown", mdName)
	b.WriteString("</section>")

	b.WriteString("<h2>Messages</h2>")
	for i, msg := range req.Messages {
		title := fmt.Sprintf("%02d. %s", i+1, msg.Role)
		fmt.Fprintf(&b, "<details open><summary>%s</summary>", html.EscapeString(title))
		if msg.ToolCallID != "" {
			fmt.Fprintf(&b, "<div class=\"meta\">tool_call_id: %s</div>", html.EscapeString(msg.ToolCallID))
		}
		if msg.Content != "" {
			fmt.Fprintf(&b, "<pre>%s</pre>", html.EscapeString(msg.Content))
		}
		if len(msg.ToolCalls) > 0 {
			fmt.Fprintf(&b, "<pre>%s</pre>", html.EscapeString(jsonPretty(msg.ToolCalls)))
		}
		if len(msg.ResponseItems) > 0 {
			fmt.Fprintf(&b, "<pre>%s</pre>", html.EscapeString(jsonPretty(msg.ResponseItems)))
		}
		b.WriteString("</details>")
	}

	b.WriteString("<h2>Tools</h2>")
	for i, tool := range req.Tools {
		fmt.Fprintf(&b, "<details><summary>%02d. %s</summary><pre>%s</pre></details>", i+1, html.EscapeString(tool.Function.Name), html.EscapeString(jsonPretty(tool)))
	}
	b.WriteString("<h2>Raw Request</h2><pre>")
	b.WriteString(html.EscapeString(jsonPretty(req)))
	b.WriteString("</pre></main></body></html>")
	return b.String()
}

func renderRunIndex(runID string, entries []*dumpEntry) string {
	var b strings.Builder
	totalTokens := 0
	actualPromptTokens := 0
	completionTokens := 0
	actualTotalTokens := 0
	for _, entry := range entries {
		totalTokens += entry.Tokens
		actualPromptTokens += entry.ActualPromptTokens
		completionTokens += entry.CompletionTokens
		actualTotalTokens += entry.TotalTokens
	}
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>Prompt Dump Run Index</title>")
	b.WriteString(`<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;background:#181716;color:#e7e1d8}
header{position:sticky;top:0;background:#24211f;border-bottom:1px solid #4b4641;padding:18px 28px;z-index:2}
main{padding:22px 28px;max-width:1180px}
table{border-collapse:collapse;width:100%;background:#211f1d;border:1px solid #4b4641}
th,td{border-bottom:1px solid #4b4641;padding:10px 12px;text-align:left;vertical-align:top}
th{color:#f0c38a;background:#24211f}
a{color:#f0c38a}.muted{color:#9d948c}.pill{display:inline-block;border:1px solid #6d6258;border-radius:5px;padding:2px 7px;color:#f0c38a}
</style></head><body>`)
	fmt.Fprintf(&b, "<header><h1>Prompt Dump Run Index</h1><div><span class=\"pill\">%s</span><span class=\"pill\">%d dumps</span><span class=\"pill\">%d est. prompt tokens</span>", html.EscapeString(runID), len(entries), totalTokens)
	if actualTotalTokens > 0 {
		fmt.Fprintf(&b, "<span class=\"pill\">%d actual prompt tokens</span><span class=\"pill\">%d completion tokens</span><span class=\"pill\">%d total tokens</span>", actualPromptTokens, completionTokens, actualTotalTokens)
	}
	b.WriteString("</div></header><main>")
	b.WriteString("<table><thead><tr><th>#</th><th>Time</th><th>Phase</th><th>Label</th><th>Model</th><th>Tokens</th><th>Size</th><th>Status</th><th>Links</th></tr></thead><tbody>")
	for i, entry := range entries {
		tokenCell := fmt.Sprintf("%d est.", entry.Tokens)
		if entry.TotalTokens > 0 || entry.ActualPromptTokens > 0 || entry.CompletionTokens > 0 {
			tokenCell = fmt.Sprintf("%d prompt<br>%d completion<br>%d total", entry.ActualPromptTokens, entry.CompletionTokens, entry.TotalTokens)
			if entry.CachedTokensKnown {
				tokenCell += fmt.Sprintf("<br><span class=\"muted\">%d cached</span>", entry.CachedTokens)
			}
		}
		status := "pending"
		if entry.Error != "" {
			status = "error: " + entry.Error
		} else if entry.TotalTokens > 0 || entry.ActualPromptTokens > 0 || entry.CompletionTokens > 0 {
			status = "complete"
		}
		model := firstNonEmpty(entry.ActualModel, entry.Model)
		fmt.Fprintf(&b, "<tr><td>%03d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td><span class=\"muted\">%d messages<br>%d tools</span></td><td>%s</td><td><a href=\"%s\">HTML</a> · <a href=\"%s\">Markdown</a></td></tr>",
			i+1,
			html.EscapeString(entry.CreatedAt.Format("15:04:05.000 UTC")),
			html.EscapeString(string(entry.Metadata.Phase)),
			html.EscapeString(entry.Metadata.Label),
			html.EscapeString(model),
			tokenCell,
			entry.Messages,
			entry.Tools,
			html.EscapeString(status),
			html.EscapeString(entry.HTML),
			html.EscapeString(entry.Markdown),
		)
	}
	b.WriteString("</tbody></table></main></body></html>")
	return b.String()
}

func estimateRequestTokens(req *provider.ChatRequest) int {
	if req == nil {
		return 0
	}
	chars := len([]rune(req.Model)) + 24
	for _, message := range req.Messages {
		chars += 12 + len([]rune(message.Role)) + len([]rune(message.Content)) + len([]rune(message.ToolCallID))
		if len(message.ToolCalls) > 0 {
			chars += len([]rune(jsonPretty(message.ToolCalls)))
		}
		if len(message.ResponseItems) > 0 {
			chars += len([]rune(jsonPretty(message.ResponseItems)))
		}
	}
	for _, tool := range req.Tools {
		chars += len([]rune(jsonPretty(tool)))
	}
	// A deliberately simple approximation for prompt-debug triage. Exact token
	// accounting remains provider/model-specific and is reported after calls.
	return (chars + 3) / 4
}

func htmlKV(b *strings.Builder, k, v string) {
	if strings.TrimSpace(v) == "" || v == "0" && k == "Iteration" {
		return
	}
	fmt.Fprintf(b, "<strong>%s</strong><span>%s</span>", html.EscapeString(k), html.EscapeString(v))
}

func jsonPretty(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "prompt"
	}
	re := regexp.MustCompile(`[^a-z0-9._-]+`)
	s = strings.Trim(re.ReplaceAllString(s, "-"), "-")
	if s == "" {
		return "prompt"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
