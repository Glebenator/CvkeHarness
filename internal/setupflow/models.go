package setupflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type openRouterModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

type codexCache struct {
	FetchedAt     time.Time    `json:"fetched_at"`
	ClientVersion string       `json:"client_version"`
	Models        []codexModel `json:"models"`
}

type codexModel struct {
	Slug           string `json:"slug"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	Visibility     string `json:"visibility"`
	Priority       int    `json:"priority"`
	SupportedInAPI bool   `json:"supported_in_api"`
}

var openRouterFallbackModels = []ModelOption{
	{ID: "openrouter/auto", Description: "Auto-selected best model"},
	{ID: "openrouter/free", Description: "Auto-selected free model"},
	{ID: "anthropic/claude-sonnet-4.6", Description: "Anthropic Claude Sonnet 4.6"},
	{ID: "anthropic/claude-opus-4.6", Description: "Anthropic Claude Opus 4.6"},
	{ID: "openai/gpt-5.4", Description: "OpenAI GPT-5.4"},
	{ID: "x-ai/grok-4.1-fast", Description: "xAI Grok 4.1 Fast"},
}

var openAIFallbackModels = []ModelOption{
	{ID: "gpt-5.2-codex", Description: "GPT-5.2 Codex"},
	{ID: "gpt-5.1-codex", Description: "GPT-5.1 Codex"},
	{ID: "gpt-5.1-codex-mini", Description: "GPT-5.1 Codex Mini"},
	{ID: "gpt-5.2", Description: "GPT-5.2"},
	{ID: "gpt-5-mini", Description: "GPT-5 Mini"},
}

var lmStudioFallbackModels = []ModelOption{
	{ID: "local-model", Description: "Use the currently loaded local model"},
}

func fetchOpenRouterModels(ctx context.Context) ModelResult {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models?category=programming&order=top-weekly", nil)
	resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
	if err != nil {
		return fallbackModels(openRouterFallbackModels, "openrouter", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallbackModels(openRouterFallbackModels, "openrouter", fmt.Sprintf("status %d", resp.StatusCode))
	}
	var data struct {
		Data []openRouterModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fallbackModels(openRouterFallbackModels, "openrouter", err.Error())
	}
	items := []ModelOption{{ID: "openrouter/auto", Description: "Auto-selected best model"}, {ID: "openrouter/free", Description: "Auto-selected free model"}}
	seen := map[string]bool{"openrouter/auto": true, "openrouter/free": true}
	for _, model := range data.Data {
		if model.ID == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		desc := model.Name
		if desc == "" {
			desc = model.ID
		}
		if model.Pricing.Prompt == "0" && model.Pricing.Completion == "0" {
			desc += " · free"
		}
		items = append(items, ModelOption{ID: model.ID, Description: desc})
		if len(items) >= 22 {
			break
		}
	}
	return ModelResult{Items: appendCustomModel(items), Live: true, Source: "openrouter", Timestamp: time.Now()}
}

func fetchOpenAIModels(ctx context.Context, key string) ModelResult {
	if strings.TrimSpace(key) == "" {
		return fallbackModels(openAIFallbackModels, "openai", "API key not configured")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.openai.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
	if err != nil {
		return fallbackModels(openAIFallbackModels, "openai", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallbackModels(openAIFallbackModels, "openai", fmt.Sprintf("status %d", resp.StatusCode))
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fallbackModels(openAIFallbackModels, "openai", err.Error())
	}
	var items []ModelOption
	for _, model := range data.Data {
		if strings.Contains(model.ID, "codex") || strings.HasPrefix(model.ID, "gpt-5") {
			items = append(items, ModelOption{ID: model.ID, Description: "Available in your OpenAI account"})
		}
		if len(items) >= 22 {
			break
		}
	}
	if len(items) == 0 {
		return fallbackModels(openAIFallbackModels, "openai", "no visible Codex/GPT-5 models returned")
	}
	return ModelResult{Items: appendCustomModel(sortModelOptions(items)), Live: true, Source: "openai", Timestamp: time.Now()}
}

func fetchLMStudioModels(ctx context.Context, baseURL string) ModelResult {
	if baseURL == "" {
		baseURL = "http://localhost:1234/v1"
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 800 * time.Millisecond}).Do(req)
	if err != nil {
		return fallbackModels(lmStudioFallbackModels, "lmstudio", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallbackModels(lmStudioFallbackModels, "lmstudio", fmt.Sprintf("status %d", resp.StatusCode))
	}
	var data struct {
		Data []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fallbackModels(lmStudioFallbackModels, "lmstudio", err.Error())
	}
	var items []ModelOption
	for _, model := range data.Data {
		desc := "Available"
		if model.State == "loaded" {
			desc = "Loaded"
		}
		if model.ID != "" {
			items = append(items, ModelOption{ID: model.ID, Description: desc})
		}
	}
	if len(items) == 0 {
		return fallbackModels(lmStudioFallbackModels, "lmstudio", "no models returned")
	}
	return ModelResult{Items: appendCustomModel(items), Live: true, Source: "lmstudio", Timestamp: time.Now()}
}

func fetchCodexModels(now time.Time) ModelResult {
	path := codexModelsCachePath()
	if path == "" {
		return fallbackModels(nil, "codex", "Codex model cache path could not be resolved")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fallbackModels(nil, "codex", "Codex models cache unavailable at "+path)
	}
	var cache codexCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return fallbackModels(nil, "codex", err.Error())
	}
	list := make([]codexModel, 0, len(cache.Models))
	for _, model := range cache.Models {
		if model.Slug == "" || !model.SupportedInAPI {
			continue
		}
		if model.Visibility != "" && model.Visibility != "list" {
			continue
		}
		list = append(list, model)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Priority != list[j].Priority {
			return list[i].Priority < list[j].Priority
		}
		return list[i].Slug < list[j].Slug
	})
	var items []ModelOption
	for _, model := range list {
		desc := firstNonEmpty(model.DisplayName, model.Slug)
		if model.Description != "" {
			desc += " · " + model.Description
		}
		items = append(items, ModelOption{ID: model.Slug, Description: desc})
		if len(items) >= 22 {
			break
		}
	}
	if len(items) == 0 {
		return fallbackModels(nil, "codex", "Codex cache did not contain listable API models")
	}
	if cache.FetchedAt.IsZero() || now.Sub(cache.FetchedAt) > 6*time.Hour {
		return fallbackModels(nil, "codex", "Codex model cache is stale; run Codex once to refresh it")
	}
	return ModelResult{Items: appendCustomModel(items), Live: true, Source: "codex-cache", Message: cache.ClientVersion, Timestamp: cache.FetchedAt}
}

func appendCustomModel(items []ModelOption) []ModelOption {
	return append(items, ModelOption{ID: "[ custom model ]", Description: "Enter a custom model ID"})
}

func fallbackModels(items []ModelOption, source, message string) ModelResult {
	if len(items) == 0 {
		items = []ModelOption{{ID: "[ custom model ]", Description: "Enter a custom model ID"}}
	} else {
		items = appendCustomModel(append([]ModelOption(nil), items...))
	}
	return ModelResult{Items: items, Source: source, Message: message, Timestamp: time.Now()}
}

func codexModelsCachePath() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "models_cache.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "models_cache.json")
}
