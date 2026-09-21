package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootIncludesSettingsCommand(t *testing.T) {
	t.Parallel()

	cmd, _, err := rootCmd.Find([]string{"settings"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected settings command to be registered")
	}
	if cmd.Use != "settings" {
		t.Fatalf("expected settings command, got %q", cmd.Use)
	}
}

func TestRootIncludesConsoleCommandAndTUIAlias(t *testing.T) {
	t.Parallel()

	cmd, _, err := rootCmd.Find([]string{"console"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected console command to be registered")
	}
	if cmd.Use != "console" {
		t.Fatalf("expected console command, got %q", cmd.Use)
	}

	alias, _, err := rootCmd.Find([]string{"tui"})
	if err != nil {
		t.Fatalf("Find alias returned error: %v", err)
	}
	if alias != cmd {
		t.Fatalf("expected tui to resolve to console command, got %#v", alias)
	}
}

func TestRootDoesNotIncludeChatCommand(t *testing.T) {
	t.Parallel()

	if _, _, err := rootCmd.Find([]string{"chat"}); err == nil {
		t.Fatal("expected removed chat command to be unknown")
	}
}

func TestRootIncludesCommandsCommand(t *testing.T) {
	t.Parallel()

	cmd, _, err := rootCmd.Find([]string{"commands"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected commands command to be registered")
	}
	if cmd.Use != "commands" {
		t.Fatalf("expected commands command, got %q", cmd.Use)
	}
}

func TestDefaultHelpListsRegisteredCommands(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs([]string{"--help"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	helpText := out.String() + errOut.String()
	expectedSnippets := []string{
		"Available Commands:",
		"console",
		"commands",
		"memory",
		"models",
		"run",
		"setup",
		"settings",
	}
	for _, snippet := range expectedSnippets {
		if !strings.Contains(helpText, snippet) {
			t.Fatalf("expected help output to contain %q, got:\n%s", snippet, helpText)
		}
	}
	if strings.Contains(helpText, "\n  chat") || strings.Contains(helpText, "\n  tui") {
		t.Fatalf("expected removed and compatibility command names to stay out of root help, got:\n%s", helpText)
	}
}
