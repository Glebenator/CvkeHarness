package termui

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelectFallbackAcceptsYesNoForApplyCancel(t *testing.T) {
	opts := SelectOptions{
		Title: "Apply change?",
		Choices: []Choice{
			{Label: "Cancel", Description: "Do not write changes"},
			{Label: "Apply change", Description: "Write the reviewed update"},
		},
		InitialIndex: 0,
	}

	opts.In = strings.NewReader("yes\n")
	opts.Out = &bytes.Buffer{}
	got, err := selectFallback(opts.In, opts.Out, opts)
	if err != nil {
		t.Fatalf("selectFallback yes returned error: %v", err)
	}
	if got != 1 {
		t.Fatalf("selectFallback yes = %d, want 1", got)
	}

	opts.In = strings.NewReader("no\n")
	opts.Out = &bytes.Buffer{}
	got, err = selectFallback(opts.In, opts.Out, opts)
	if err != nil {
		t.Fatalf("selectFallback no returned error: %v", err)
	}
	if got != 0 {
		t.Fatalf("selectFallback no = %d, want 0", got)
	}
}
