package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHeredocTrailingWhitespaceIsNotRewritten(t *testing.T) {
	for _, command := range []string{"<<'0'\n0 ", "cat <<'EOF'\nEOF\t", "cat <<'EOF'\nEOF \n"} {
		if _, err := ParseShellCommand(command); err == nil {
			t.Fatalf("accepted an invalid literal delimiter: %q", command)
		}
		tool := NewShellToolWithApprovals([]string{"cat"}, nil, nil, "", nil)
		raw, _ := json.Marshal(ShellArgs{Command: command})
		if _, err := tool.Execute(context.Background(), raw); err == nil {
			t.Fatalf("executed malformed heredoc: %q", command)
		}
	}
}
