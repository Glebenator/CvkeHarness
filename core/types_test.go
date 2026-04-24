package core

import "testing"

func TestParseModelRefRecognizesOpenAIProvider(t *testing.T) {
	t.Parallel()

	ref := ParseModelRef("openai/gpt-5.2-codex", "openrouter")
	if ref.Provider != "openai" || ref.Model != "gpt-5.2-codex" {
		t.Fatalf("unexpected model ref %#v", ref)
	}
}

func TestParseModelRefRecognizesCodexProvider(t *testing.T) {
	t.Parallel()

	ref := ParseModelRef("codex/gpt-5.1-codex-max", "openrouter")
	if ref.Provider != "codex" || ref.Model != "gpt-5.1-codex-max" {
		t.Fatalf("unexpected model ref %#v", ref)
	}
}
