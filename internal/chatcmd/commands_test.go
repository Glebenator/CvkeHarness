package chatcmd

import (
	"strings"
	"testing"
)

func TestParseResolvesSharedAndSurfaceSpecificCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		surface Surface
		want    Action
	}{
		{name: "cli new", input: "/new", surface: CLI, want: New},
		{name: "cli clear alias", input: "  /CLEAR  ", surface: CLI, want: New},
		{name: "tui memory", input: "/Memory", surface: TUI, want: Memory},
		{name: "cli export", input: "/export", surface: CLI, want: Export},
		{name: "tui tools", input: "/tools", surface: TUI, want: Tools},
		{name: "cli exit", input: "/exit", surface: CLI, want: Exit},
		{name: "tui history", input: "/history", surface: TUI, want: History},
		{name: "history unavailable in cli", input: "/history", surface: CLI, want: None},
		{name: "exit unavailable in tui", input: "/exit", surface: TUI, want: None},
		{name: "prompt", input: "hello", surface: TUI, want: None},
		{name: "command with arguments is not exact", input: "/new now", surface: CLI, want: None},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Parse(test.input, test.surface); got != test.want {
				t.Fatalf("Parse(%q, %d) = %q, want %q", test.input, test.surface, got, test.want)
			}
		})
	}
}

func TestMatchesFiltersCommandsAndAliases(t *testing.T) {
	t.Parallel()

	all := Matches("/", TUI)
	if len(all) != 6 {
		t.Fatalf("TUI command count = %d, want 6: %#v", len(all), all)
	}
	memory := Matches("/mem", TUI)
	if len(memory) != 1 || memory[0].Action != Memory {
		t.Fatalf("unexpected /mem matches: %#v", memory)
	}
	clear := Matches("/cl", TUI)
	if len(clear) != 1 || clear[0].Action != New {
		t.Fatalf("expected /clear alias to match /new, got %#v", clear)
	}
	if got := Matches("//literal", TUI); len(got) != 0 {
		t.Fatalf("escaped prompt should not autocomplete, got %#v", got)
	}
}

func TestUnknownSlashAndLiteralEscape(t *testing.T) {
	t.Parallel()

	if !IsUnknownSlash("/missing", TUI) {
		t.Fatal("expected unknown slash command to be recognized locally")
	}
	if IsUnknownSlash("//var/log", TUI) {
		t.Fatal("expected double slash to escape local command parsing")
	}
	if got := PromptText("//var/log"); got != "/var/log" {
		t.Fatalf("PromptText returned %q, want /var/log", got)
	}
}

func TestSummariesDocumentSharedCommands(t *testing.T) {
	t.Parallel()

	for _, surface := range []Surface{CLI, TUI} {
		summary := Summary(surface, ", ")
		for _, want := range []string{"/new (/clear)", "/memory", "/export", "/tools", "/help"} {
			if !strings.Contains(summary, want) {
				t.Fatalf("summary for surface %d missing %q: %s", surface, want, summary)
			}
		}
	}
}

func TestAvailableReturnsDefensiveAliasCopies(t *testing.T) {
	t.Parallel()

	first := Available(CLI)
	first[0].Aliases[0] = "/changed"
	second := Available(CLI)
	if got := second[0].Aliases[0]; got != "/clear" {
		t.Fatalf("expected registry aliases to remain immutable, got %q", got)
	}
}
