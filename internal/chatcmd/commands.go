// Package chatcmd defines local slash commands for the interactive operations
// console. Commands are parsed before prompts reach the agent runtime.
package chatcmd

import "strings"

// Action identifies a local chat command.
type Action string

const (
	None    Action = ""
	Help    Action = "help"
	New     Action = "new"
	Memory  Action = "memory"
	Export  Action = "export"
	Tools   Action = "tools"
	History Action = "history"
)

// Command is display metadata for one canonical slash command.
type Command struct {
	Action      Action
	Name        string
	Aliases     []string
	Description string
}

var definitions = []Command{
	{
		Action:      New,
		Name:        "/new",
		Aliases:     []string{"/clear"},
		Description: "Start a fresh in-process chat session",
	},
	{
		Action:      Memory,
		Name:        "/memory",
		Description: "Show memory used by the latest model call",
	},
	{
		Action:      Export,
		Name:        "/export",
		Description: "Write a private, redacted Markdown transcript",
	},
	{
		Action:      Tools,
		Name:        "/tools",
		Description: "List registered tools and the authorization boundary",
	},
	{
		Action:      History,
		Name:        "/history",
		Description: "Browse saved conversations",
	},
	{
		Action:      Help,
		Name:        "/help",
		Description: "Show the available chat commands",
	},
}

// Parse resolves an exact, case-insensitive slash command. Unrecognized input
// is not treated as a command.
func Parse(input string) Action {
	input = strings.ToLower(strings.TrimSpace(input))
	for _, command := range definitions {
		if input == command.Name {
			return command.Action
		}
		for _, alias := range command.Aliases {
			if input == alias {
				return command.Action
			}
		}
	}
	return None
}

// Available returns ordered command metadata for the console.
func Available() []Command {
	commands := make([]Command, 0, len(definitions))
	for _, definition := range definitions {
		command := definition
		command.Aliases = append([]string(nil), command.Aliases...)
		commands = append(commands, command)
	}
	return commands
}

// Matches returns commands whose canonical name or alias begins with input.
// It is intended for a local command palette while the first composer token is
// being entered.
func Matches(input string) []Command {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || !strings.HasPrefix(input, "/") || strings.HasPrefix(input, "//") || strings.ContainsAny(input, " \t\r\n") {
		return nil
	}
	commands := Available()
	matches := make([]Command, 0, len(commands))
	for _, command := range commands {
		if strings.HasPrefix(command.Name, input) {
			matches = append(matches, command)
			continue
		}
		for _, alias := range command.Aliases {
			if strings.HasPrefix(alias, input) {
				matches = append(matches, command)
				break
			}
		}
	}
	return matches
}

// IsUnknownSlash reports whether input looks like a local command but does not
// resolve. A leading double slash escapes command parsing so literal
// slash-prefixed prompts remain possible.
func IsUnknownSlash(input string) bool {
	input = strings.TrimSpace(input)
	return strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "//") && Parse(input) == None
}

// PromptText removes the double-slash escape used for literal slash-prefixed
// prompts. Other input is returned unchanged.
func PromptText(input string) string {
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "//") {
		return strings.TrimPrefix(trimmed, "/")
	}
	return trimmed
}

// Label formats a canonical command and its documented aliases.
func Label(command Command) string {
	if len(command.Aliases) == 0 {
		return command.Name
	}
	return command.Name + " (" + strings.Join(command.Aliases, ", ") + ")"
}

// Summary formats the available command names for compact banners and help.
func Summary(separator string) string {
	commands := Available()
	labels := make([]string, 0, len(commands))
	for _, command := range commands {
		labels = append(labels, Label(command))
	}
	return strings.Join(labels, separator)
}
