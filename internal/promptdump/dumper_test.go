package promptdump

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
)

func TestDumperWritesMarkdownAndHTML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dumper := New(true, dir)
	req := &provider.ChatRequest{
		Model:       "test-model",
		Temperature: 0.2,
		MaxTokens:   512,
		Messages: []provider.Message{
			{Role: "system", Content: "memory from guidance.md\nfull prompt content"},
			{Role: "user", Content: "do the thing"},
		},
		Tools: []provider.ToolDef{{
			Type: "function",
			Function: provider.ToolFuncDef{
				Name:        "shell_execute",
				Description: "Run a shell command",
				Parameters:  []byte(`{"type":"object"}`),
			},
		}},
	}

	err := dumper.Dump(context.Background(), Metadata{
		Phase:     core.PhaseExecution,
		Provider:  "codex",
		Model:     "test-model",
		TaskClass: core.TaskClassGeneral,
		Iteration: 2,
		Label:     "execution-loop",
	}, req)
	if err != nil {
		t.Fatalf("Dump returned error: %v", err)
	}

	var markdownPath, htmlPath, indexPath string
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".md") {
			markdownPath = path
		}
		if strings.HasSuffix(path, ".html") {
			if filepath.Base(path) == "index.html" {
				indexPath = path
			} else {
				htmlPath = path
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir returned error: %v", err)
	}
	if markdownPath == "" || htmlPath == "" || indexPath == "" {
		t.Fatalf("expected markdown, html, and index prompt dumps, got md=%q html=%q index=%q", markdownPath, htmlPath, indexPath)
	}

	md, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("ReadFile(markdown) returned error: %v", err)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("ReadFile(html) returned error: %v", err)
	}
	for _, want := range []string{"memory from guidance.md", "shell_execute", "Raw Request", "execution-loop", "Estimated prompt tokens"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("expected markdown dump to contain %q, got:\n%s", want, string(md))
		}
	}
	if !strings.Contains(string(html), "Prompt Dump") || !strings.Contains(string(html), "full prompt content") || !strings.Contains(string(html), "Estimated prompt tokens") {
		t.Fatalf("expected readable HTML dump, got:\n%s", string(html))
	}
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile(index) returned error: %v", err)
	}
	if !strings.Contains(string(index), "Prompt Dump Run Index") || !strings.Contains(string(index), filepath.Base(htmlPath)) || !strings.Contains(string(index), "est. prompt tokens") {
		t.Fatalf("expected index to link individual dump, got:\n%s", string(index))
	}
}

func TestEstimateRequestTokensIncludesTools(t *testing.T) {
	t.Parallel()

	req := &provider.ChatRequest{
		Model:    "test",
		Messages: []provider.Message{{Role: "user", Content: strings.Repeat("hello ", 100)}},
	}
	withoutTools := estimateRequestTokens(req)
	req.Tools = []provider.ToolDef{{
		Type: "function",
		Function: provider.ToolFuncDef{
			Name:        "large_tool",
			Description: strings.Repeat("schema ", 100),
			Parameters:  []byte(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		},
	}}
	withTools := estimateRequestTokens(req)
	if withTools <= withoutTools {
		t.Fatalf("expected tool schema to increase token estimate, without=%d with=%d", withoutTools, withTools)
	}
}

func TestDumperFinishUpdatesActualTokenCounts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dumper := New(true, dir)
	handle, err := dumper.Begin(context.Background(), Metadata{
		Phase:    core.PhaseChat,
		Provider: "codex",
		Model:    "requested-model",
		Label:    "chat-turn",
	}, &provider.ChatRequest{
		Model:    "requested-model",
		Messages: []provider.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	if err := dumper.Finish(handle, Result{
		ActualModel: "actual-model",
		Usage: provider.Usage{
			PromptTokens:     12,
			CompletionTokens: 5,
			TotalTokens:      17,
			PromptTokenDetails: &provider.PromptTokenDetails{
				CachedTokens: 3,
			},
		},
	}); err != nil {
		t.Fatalf("Finish returned error: %v", err)
	}

	var indexPath, markdownPath string
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case filepath.Base(path) == "index.html":
			indexPath = path
		case strings.HasSuffix(path, ".md"):
			markdownPath = path
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir returned error: %v", err)
	}
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile(index) returned error: %v", err)
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("ReadFile(markdown) returned error: %v", err)
	}
	for _, want := range []string{"12 actual prompt tokens", "5 completion tokens", "17 total tokens", "actual-model"} {
		if !strings.Contains(string(index), want) {
			t.Fatalf("expected index to contain %q, got:\n%s", want, string(index))
		}
	}
	for _, want := range []string{"Actual prompt tokens: `12`", "Completion tokens: `5`", "Total tokens: `17`", "Cached prompt tokens: `3`"} {
		if !strings.Contains(string(markdown), want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, string(markdown))
		}
	}
}

func TestDisabledDumperSkipsWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dumper := New(false, dir)
	if err := dumper.Dump(context.Background(), Metadata{}, &provider.ChatRequest{Model: "test"}); err != nil {
		t.Fatalf("Dump returned error: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected disabled dumper to skip writes, got %#v", entries)
	}
}

func TestDumperRedactsSecretsBeforePersistence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dumper := New(true, dir)
	err := dumper.Dump(context.Background(), Metadata{
		Phase:    core.PhaseExecution,
		Provider: "codex",
		Model:    "test-model",
		Label:    "secret-test",
	}, &provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    "user",
			Content: "Authorization: Bearer abcdefghijklmnop",
		}},
	})
	if err != nil {
		t.Fatalf("Dump returned error: %v", err)
	}

	var markdownPath string
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".md") {
			markdownPath = path
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir returned error: %v", err)
	}
	data, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if strings.Contains(string(data), "abcdefghijklmnop") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("expected persisted dump to redact secret-looking values, got:\n%s", string(data))
	}
}

func TestDumperPrunesExpiredDayDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	expiredDir := filepath.Join(dir, "2026-05-01")
	freshDir := filepath.Join(dir, "2026-05-14")
	if err := os.MkdirAll(expiredDir, 0700); err != nil {
		t.Fatalf("MkdirAll expired returned error: %v", err)
	}
	if err := os.MkdirAll(freshDir, 0700); err != nil {
		t.Fatalf("MkdirAll fresh returned error: %v", err)
	}
	dumper := NewWithRetentionDays(true, dir, 7)
	if err := dumper.pruneExpired(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("pruneExpired returned error: %v", err)
	}
	if _, err := os.Stat(expiredDir); !os.IsNotExist(err) {
		t.Fatalf("expected expired directory to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("expected fresh directory to remain, stat err=%v", err)
	}
}
