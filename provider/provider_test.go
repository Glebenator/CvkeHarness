package provider

import (
	"encoding/json"
	"testing"
)

func TestUsageCachedTokens(t *testing.T) {
	t.Parallel()

	var usage Usage
	if err := json.Unmarshal([]byte(`{
		"prompt_tokens": 120,
		"completion_tokens": 45,
		"total_tokens": 165,
		"prompt_tokens_details": {
			"cached_tokens": 32
		}
	}`), &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}

	cachedTokens, ok := usage.CachedTokens()
	if !ok {
		t.Fatal("expected cached token details to be available")
	}
	if cachedTokens != 32 {
		t.Fatalf("expected 32 cached tokens, got %d", cachedTokens)
	}
}

func TestUsageCachedTokensAbsent(t *testing.T) {
	t.Parallel()

	usage := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	if cachedTokens, ok := usage.CachedTokens(); ok || cachedTokens != 0 {
		t.Fatalf("expected absent cached token details, got %d ok=%v", cachedTokens, ok)
	}
}
