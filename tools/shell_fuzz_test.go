package tools

import (
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/shellpolicy"
)

func FuzzParseShellCommand(f *testing.F) {
	for _, seed := range shellFuzzSeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, command string) {
		parsed, err := ParseShellCommand(command)
		if err != nil {
			return
		}

		if containsBlockedShellSyntax(command) {
			t.Fatalf("accepted command with blocked shell syntax: %q", command)
		}
		if len(parsed.Operators) != len(parsed.Segments)-1 {
			t.Fatalf("operators/segments mismatch: %#v", parsed)
		}

		normalized := renderParsedShellCommand(parsed)
		reparsed, err := ParseShellCommand(normalized)
		if err != nil {
			t.Fatalf("normalized command did not parse: %q: %v", normalized, err)
		}
		if renderParsedShellCommand(reparsed) != normalized {
			t.Fatalf("normalized parsing is not idempotent: %q -> %#v", normalized, reparsed)
		}
	})
}

func FuzzValidateAllowedShellCommand(f *testing.F) {
	for _, seed := range shellFuzzSeeds() {
		f.Add(seed)
	}

	allowed := config.DefaultConfig().AllowedCommands
	f.Fuzz(func(t *testing.T, command string) {
		if err := ValidateAllowedShellCommand(command, allowed); err != nil {
			return
		}
		parsed, err := ParseShellCommand(command)
		if err != nil {
			t.Fatalf("allowed command failed parsing: %q: %v", command, err)
		}
		if containsBlockedShellSyntax(command) {
			t.Fatalf("allowed command with blocked shell syntax: %q", command)
		}
		if len(parsed.Operators) != len(parsed.Segments)-1 {
			t.Fatalf("operators/segments mismatch: %#v", parsed)
		}
	})
}

func shellFuzzSeeds() []string {
	seeds := []string{
		"ps aux",
		"df -h && uptime",
		"ps aux | grep docker",
		`printf "hello world"`,
		`printf hello\ world`,
		"ps > /tmp/output.txt",
		"ps $(whoami)",
		"ps &&",
		"",
	}
	if cases, err := shellpolicy.LoadCorpus(); err == nil {
		for _, testCase := range cases {
			seeds = append(seeds, testCase.Command)
		}
	}
	return seeds
}

func renderParsedShellCommand(parsed ParsedShellCommand) string {
	var b strings.Builder
	for idx, segment := range parsed.Segments {
		if idx > 0 {
			b.WriteByte(' ')
			b.WriteString(parsed.Operators[idx-1])
			b.WriteByte(' ')
		}
		b.WriteString(segment.Normalized)
	}
	return b.String()
}

func containsBlockedShellSyntax(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case escaped:
			escaped = false
			continue
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inDouble = false
			case '`':
				return true
			case '$':
				if i+1 < len(command) && command[i+1] == '(' {
					return true
				}
			}
			continue
		}

		switch ch {
		case '\\':
			escaped = true
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`', '>':
			return true
		case '<':
			_, end, err := parseQuotedHeredoc(command, i)
			if err != nil {
				return true
			}
			i = end
		case '$':
			if i+1 < len(command) && command[i+1] == '(' {
				return true
			}
		case '&':
			if i+1 >= len(command) || command[i+1] != '&' {
				return true
			}
			i++
		}
	}

	return false
}
