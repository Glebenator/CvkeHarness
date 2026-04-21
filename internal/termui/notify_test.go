package termui

import "testing"

func TestNotificationSequence_UsesOSC9ForITerm(t *testing.T) {
	t.Parallel()

	seq := notificationSequence("Need input", "Reply in terminal", envMap(map[string]string{
		"TERM_PROGRAM": "iTerm.app",
	}))

	want := "\033]9;Need input: Reply in terminal\033\\"
	if seq != want {
		t.Fatalf("expected %q, got %q", want, seq)
	}
}

func TestNotificationSequence_UsesOSC777ForVTETerminals(t *testing.T) {
	t.Parallel()

	seq := notificationSequence("Approval required", "Choose an option", envMap(map[string]string{
		"VTE_VERSION": "7603",
	}))

	want := "\033]777;notify;Approval required;Choose an option\033\\"
	if seq != want {
		t.Fatalf("expected %q, got %q", want, seq)
	}
}

func TestNotificationSequence_WrapsForTmux(t *testing.T) {
	t.Parallel()

	seq := notificationSequence("Need input", "Reply in terminal", envMap(map[string]string{
		"TERM_PROGRAM": "WezTerm",
		"TMUX":         "/tmp/tmux-1000/default,123,0",
	}))

	want := "\033Ptmux;\033\033]9;Need input: Reply in terminal\033\033\\\033\\"
	if seq != want {
		t.Fatalf("expected %q, got %q", want, seq)
	}
}

func TestNotificationSequence_SanitizesPayload(t *testing.T) {
	t.Parallel()

	seq := notificationSequence("Need;\ninput", "Reply\tplease\x07", envMap(map[string]string{
		"TERM_PROGRAM": "iTerm.app",
	}))

	want := "\033]9;Need, input: Reply please\033\\"
	if seq != want {
		t.Fatalf("expected %q, got %q", want, seq)
	}
}

func TestNotificationSequence_ReturnsEmptyWhenUnsupported(t *testing.T) {
	t.Parallel()

	seq := notificationSequence("Need input", "Reply in terminal", envMap(nil))
	if seq != "" {
		t.Fatalf("expected empty sequence for unsupported terminal, got %q", seq)
	}
}

func envMap(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
