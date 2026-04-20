package cmd

import (
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/core"
)

func TestPromptModelApprovalRejectsByDefault(t *testing.T) {
	t.Parallel()

	approved, err := promptModelApprovalWithIO(strings.NewReader("\n"), &strings.Builder{}, core.NewModelRef("openrouter", "gpt-best"), "higher confidence for execution")
	if err != nil {
		t.Fatalf("promptModelApprovalWithIO returned unexpected error: %v", err)
	}
	if approved {
		t.Fatal("expected blank fallback input to keep the current approved model")
	}
}

func TestPromptModelApprovalAcceptsSecondOption(t *testing.T) {
	t.Parallel()

	approved, err := promptModelApprovalWithIO(strings.NewReader("2\n"), &strings.Builder{}, core.NewModelRef("openrouter", "gpt-best"), "higher confidence for execution")
	if err != nil {
		t.Fatalf("promptModelApprovalWithIO returned unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected choosing the second option to approve the recommended model")
	}
}
