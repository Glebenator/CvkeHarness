package chatcmd

import (
	"strings"
	"testing"
)

func TestParseResolvesConsoleCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Action
	}{
		{name: "new", input: "/new", want: New},
		{name: "clear alias", input: "  /CLEAR  ", want: New},
		{name: "memory", input: "/Memory", want: Memory},
		{name: "export", input: "/export", want: Export},
		{name: "tools", input: "/tools", want: Tools},
		{name: "history", input: "/history", want: History},
		{name: "removed cli exit", input: "/exit", want: None},
		{name: "prompt", input: "hello", want: None},
		{name: "command with arguments is not exact", input: "/new now", want: None},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Parse(test.input); got != test.want {
				t.Fatalf("Parse(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestMatchesFiltersCommandsAndAliases(t *testing.T) {
	t.Parallel()

	all := Matches("/")
	if len(all) != 6 {
		t.Fatalf("TUI command count = %d, want 6: %#v", len(all), all)
	}
	memory := Matches("/mem")
	if len(memory) != 1 || memory[0].Action != Memory {
		t.Fatalf("unexpected /mem matches: %#v", memory)
	}
	clear := Matches("/cl")
	if len(clear) != 1 || clear[0].Action != New {
		t.Fatalf("expected /clear alias to match /new, got %#v", clear)
	}
	if got := Matches("//literal"); len(got) != 0 {
		t.Fatalf("escaped prompt should not autocomplete, got %#v", got)
	}
}

func TestUnknownSlashAndLiteralEscape(t *testing.T) {
	t.Parallel()

	if !IsUnknownSlash("/missing") {
		t.Fatal("expected unknown slash command to be recognized locally")
	}
	if IsUnknownSlash("//var/log") {
		t.Fatal("expected double slash to escape local command parsing")
	}
	if got := PromptText("//var/log"); got != "/var/log" {
		t.Fatalf("PromptText returned %q, want /var/log", got)
	}
}

func TestSummaryDocumentsConsoleCommands(t *testing.T) {
	t.Parallel()

	summary := Summary(", ")
	for _, want := range []string{"/new (/clear)", "/memory", "/export", "/tools", "/history", "/help"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}

func TestAvailableReturnsDefensiveAliasCopies(t *testing.T) {
	t.Parallel()

	first := Available()
	first[0].Aliases[0] = "/changed"
	second := Available()
	if got := second[0].Aliases[0]; got != "/clear" {
		t.Fatalf("expected registry aliases to remain immutable, got %q", got)
	}
}
