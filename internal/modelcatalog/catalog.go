// Package modelcatalog discovers model choices independently of any UI.
package modelcatalog

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

	"github.com/coolcake/cvkeharness/config"
)

const CustomModelID = "[ custom model ]"

type ModelOption struct{ ID, Description string }
type ModelResult struct {
	Items           []ModelOption
	Live            bool
	Source, Message string
	Timestamp       time.Time
}

type Loader struct {
	Client *http.Client
	Now    func() time.Time
}

func Fetch(ctx context.Context, connection config.Connection) ModelResult {
	return (Loader{}).Fetch(ctx, connection)
}

func (l Loader) Fetch(ctx context.Context, connection config.Connection) ModelResult {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	switch connection.Provider {
	case "codex":
		return FetchCodexModels(now, connection.AuthFile)
	case "antigravity":
		return fallback(nil, "antigravity", "Enter a model ID supported by your account; this catalog is not verified", now)
	case "openrouter", "openai", "lmstudio":
		return l.fetchAPI(ctx, connection, now)
	default:
		return fallback(nil, connection.Provider, "Choose a supported provider connection", now)
	}
}

func (l Loader) fetchAPI(ctx context.Context, connection config.Connection, now time.Time) ModelResult {
	base := strings.TrimRight(strings.TrimSpace(connection.BaseURL), "/")
	if base == "" {
		switch connection.Provider {
		case "openrouter":
			base = "https://openrouter.ai/api/v1"
		case "openai":
			base = "https://api.openai.com/v1"
		case "lmstudio":
			base = "http://localhost:1234/v1"
		}
	}
	if connection.Provider == "openai" && strings.TrimSpace(connection.APIKey) == "" {
		return fallback(providerFallback(connection.Provider), connection.Provider, "API key not configured", now)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return fallback(providerFallback(connection.Provider), connection.Provider, "Invalid model catalog endpoint", now)
	}
	if connection.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	}
	client := l.Client
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		message := "Could not reach the model catalog; check the connection and retry"
		if ctx.Err() != nil {
			message = "Model catalog request cancelled or timed out"
		}
		return fallback(providerFallback(connection.Provider), connection.Provider, message, now)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallback(providerFallback(connection.Provider), connection.Provider, fmt.Sprintf("Model catalog returned HTTP %d; check connection credentials", resp.StatusCode), now)
	}
	var data struct {
		Data []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			State        string `json:"state"`
			Architecture struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return fallback(providerFallback(connection.Provider), connection.Provider, "Could not read the model catalog response", now)
	}
	var items []ModelOption
	seen := make(map[string]bool)
	if connection.Provider == "openrouter" {
		items = append(items, ModelOption{"openrouter/auto", "Provider-selected model"}, ModelOption{"openrouter/free", "Provider-selected free model"})
		seen["openrouter/auto"], seen["openrouter/free"] = true, true
	}
	for _, model := range data.Data {
		if model.ID == "" || seen[model.ID] {
			continue
		}
		if connection.Provider == "openai" && !textModelID(model.ID) {
			continue
		}
		if len(model.Architecture.OutputModalities) > 0 && !contains(model.Architecture.OutputModalities, "text") {
			continue
		}
		seen[model.ID] = true
		description := model.Name
		if description == "" {
			description = "Listed by this connection"
		}
		if model.State == "loaded" {
			description += " · loaded"
		}
		if model.Pricing.Prompt == "0" && model.Pricing.Completion == "0" {
			description += " · free"
		}
		items = append(items, ModelOption{model.ID, description})
	}
	if len(items) == 0 {
		return fallback(providerFallback(connection.Provider), connection.Provider, "No text models were listed; enter a custom ID or check the connection", now)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return ModelResult{Items: appendCustom(items), Live: true, Source: connection.Provider, Timestamp: now}
}

// Exclude known non-text families, retaining unfamiliar future model names.
// Catalog presence is never treated as a successful inference check.
func textModelID(id string) bool {
	lower := strings.ToLower(id)
	for _, fragment := range []string{"embedding", "whisper", "tts", "dall-e", "moderation", "transcribe", "realtime", "audio", "image", "sora"} {
		if strings.Contains(lower, fragment) {
			return false
		}
	}
	return true
}

func FetchCodexModels(now time.Time, authFile string) ModelResult {
	data, err := os.ReadFile(codexCachePath(authFile))
	if err != nil {
		return fallback(nil, "codex-cache", "Codex model cache unavailable; open Codex to refresh, or enter a custom ID", now)
	}
	var cache struct {
		FetchedAt time.Time `json:"fetched_at"`
		Models    []struct {
			Slug           string `json:"slug"`
			DisplayName    string `json:"display_name"`
			Description    string `json:"description"`
			Visibility     string `json:"visibility"`
			Priority       int    `json:"priority"`
			SupportedInAPI bool   `json:"supported_in_api"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &cache) != nil {
		return fallback(nil, "codex-cache", "Could not read the Codex model cache", now)
	}
	sort.SliceStable(cache.Models, func(i, j int) bool {
		if cache.Models[i].Priority != cache.Models[j].Priority {
			return cache.Models[i].Priority < cache.Models[j].Priority
		}
		return cache.Models[i].Slug < cache.Models[j].Slug
	})
	var items []ModelOption
	seen := make(map[string]bool)
	for _, model := range cache.Models {
		if model.Slug == "" || seen[model.Slug] || !model.SupportedInAPI || (model.Visibility != "" && model.Visibility != "list") {
			continue
		}
		seen[model.Slug] = true
		description := model.DisplayName
		if description == "" {
			description = model.Slug
		}
		if model.Description != "" {
			description += " · " + model.Description
		}
		items = append(items, ModelOption{model.Slug, description})
	}
	if len(items) == 0 {
		return fallback(nil, "codex-cache", "Codex cache did not contain listable API models", now)
	}
	result := ModelResult{Items: appendCustom(items), Source: "codex-cache", Timestamp: cache.FetchedAt}
	result.Live = !cache.FetchedAt.IsZero() && now.Sub(cache.FetchedAt) <= 6*time.Hour
	if !result.Live {
		result.Message = "Cached choices may be outdated; open Codex to refresh, then reload"
	}
	return result
}

func codexCachePath(authFile string) string {
	if authFile != "" {
		return filepath.Join(filepath.Dir(authFile), "models_cache.json")
	}
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "models_cache.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "models_cache.json")
}

func providerFallback(provider string) []ModelOption {
	switch provider {
	case "openrouter":
		return []ModelOption{{"openrouter/auto", "Provider-selected model"}, {"openrouter/free", "Provider-selected free model"}}
	case "lmstudio":
		return []ModelOption{{"local-model", "Use the currently loaded local model"}}
	}
	return nil
}
func fallback(items []ModelOption, source, message string, now time.Time) ModelResult {
	return ModelResult{Items: appendCustom(items), Source: source, Message: message, Timestamp: now}
}
func appendCustom(items []ModelOption) []ModelOption {
	return append(items, ModelOption{CustomModelID, "Enter an exact model ID"})
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
