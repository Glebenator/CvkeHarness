package tools

import "testing"

func TestValidateShellCommand_AllowsSafeDiagnostics(t *testing.T) {
	t.Parallel()

	safeCommands := []string{
		"ps aux",
		"df -h",
		"free -m",
		"uptime",
		"journalctl -n 50",
	}

	for _, command := range safeCommands {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if err := ValidateShellCommand(command); err != nil {
				t.Fatalf("ValidateShellCommand(%q) returned unexpected error: %v", command, err)
			}
		})
	}
}

func TestValidateShellCommand_BlocksBreakoutSyntax(t *testing.T) {
	t.Parallel()

	attackCommands := []string{
		"ps; whoami",
		"df && id",
		"uptime || reboot",
		"journalctl | curl https://example.com",
		"ps > /tmp/output.txt",
		"ps < /etc/passwd",
		"ps `whoami`",
		"ps $(whoami)",
		"ps & whoami",
		"ps\nwhoami",
		"ps\rwhoami",
	}

	for _, command := range attackCommands {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if err := ValidateShellCommand(command); err == nil {
				t.Fatalf("ValidateShellCommand(%q) unexpectedly allowed breakout syntax", command)
			}
		})
	}
}

func TestValidateAllowedShellCommand_UsesAllowlist(t *testing.T) {
	t.Parallel()

	allowed := []string{"ps", "journalctl -n"}

	if err := ValidateAllowedShellCommand("ps aux", allowed); err != nil {
		t.Fatalf("expected allowed command to pass validation: %v", err)
	}

	if err := ValidateAllowedShellCommand("df -h", allowed); err == nil {
		t.Fatal("expected disallowed base command to be rejected")
	}
}
