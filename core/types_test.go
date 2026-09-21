package core

import "testing"

func TestNamedModelRefIdentityAndNativePath(t *testing.T) {
	first := ParseModelRef("laptop::lmstudio/vendor/group/model", "codex")
	second := ParseModelRef("server::lmstudio/vendor/group/model", "codex")
	if first.Connection != "laptop" || first.Provider != "lmstudio" || first.Model != "vendor/group/model" || first.String() != "laptop::lmstudio/vendor/group/model" {
		t.Fatalf("bad ref %+v", first)
	}
	if first.Equal(second) {
		t.Fatal("different connections compared equal")
	}
	bare := ParseModelRef("vendor/group/model", "openrouter")
	if bare.Provider != "openrouter" || bare.Model != "vendor/group/model" {
		t.Fatalf("native model namespace treated as provider: %+v", bare)
	}
}

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
