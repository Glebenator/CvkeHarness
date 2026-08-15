// Package chatcmd defines the local slash commands shared by CvkeHarness chat
// surfaces. Commands are parsed before prompts reach the agent runtime.
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
	Exit    Action = "exit"
)

// Surface identifies a chat UI with its own supported command set.
type Surface uint8

const (
	CLI Surface = 1 << iota
	TUI
)

// Command is display metadata for one canonical slash command.
type Command struct {
	Action      Action
	Name        string
	Aliases     []string
	Description string
}

type definition struct {
	command  Command
	surfaces Surface
}

var definitions = []definition{
	{
		command: Command{
			Action:      New,
			Name:        "/new",
			Aliases:     []string{"/clear"},
			Description: "Start a fresh in-process chat session",
		},
		surfaces: CLI | TUI,
	},
	{
		command: Command{
			Action:      Memory,
			Name:        "/memory",
			Description: "Show memory used by the latest model call",
		},
		surfaces: CLI | TUI,
	},
	{
		command: Command{
			Action:      Export,
			Name:        "/export",
			Description: "Write a private, redacted Markdown transcript",
		},
		surfaces: CLI | TUI,
	},
	{
		command: Command{
			Action:      Tools,
			Name:        "/tools",
			Description: "List registered tools and the authorization boundary",
		},
		surfaces: CLI | TUI,
	},
	{
		command: Command{
			Action:      History,
			Name:        "/history",
			Description: "Browse saved conversations",
		},
		surfaces: TUI,
	},
	{
		command: Command{
			Action:      Help,
			Name:        "/help",
			Description: "Show the available chat commands",
		},
		surfaces: CLI | TUI,
	},
	{
		command: Command{
			Action:      Exit,
			Name:        "/exit",
			Description: "End chat",
		},
		surfaces: CLI,
	},
}

// Parse resolves an exact, case-insensitive slash command available on the
// requested surface. Unrecognized input is not treated as a command.
func Parse(input string, surface Surface) Action {
	input = strings.ToLower(strings.TrimSpace(input))
	for _, def := range definitions {
		if def.surfaces&surface == 0 {
			continue
		}
		if input == def.command.Name {
			return def.command.Action
		}
		for _, alias := range def.command.Aliases {
			if input == alias {
				return def.command.Action
			}
		}
	}
	return None
}

// Available returns ordered command metadata for a surface.
func Available(surface Surface) []Command {
	commands := make([]Command, 0, len(definitions))
	for _, def := range definitions {
		if def.surfaces&surface == 0 {
			continue
		}
		command := def.command
		command.Aliases = append([]string(nil), command.Aliases...)
		commands = append(commands, command)
	}
	return commands
}

// Matches returns commands whose canonical name or alias begins with input.
// It is intended for a local command palette while the first composer token is
// being entered.
func Matches(input string, surface Surface) []Command {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || !strings.HasPrefix(input, "/") || strings.HasPrefix(input, "//") || strings.ContainsAny(input, " \t\r\n") {
		return nil
	}
	commands := Available(surface)
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
// resolve on the requested surface. A leading double slash escapes command
// parsing so literal slash-prefixed prompts remain possible.
func IsUnknownSlash(input string, surface Surface) bool {
	input = strings.TrimSpace(input)
	return strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "//") && Parse(input, surface) == None
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
func Summary(surface Surface, separator string) string {
	commands := Available(surface)
	labels := make([]string, 0, len(commands))
	for _, command := range commands {
		labels = append(labels, Label(command))
	}
	return strings.Join(labels, separator)
}
