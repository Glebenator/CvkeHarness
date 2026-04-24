package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestParseGoFuzzOutput(t *testing.T) {
	t.Parallel()

	output := `fuzz: elapsed: 0s, gathering baseline coverage: 22/22 completed
fuzz: elapsed: 3s, execs: 248278 (82754/sec), new interesting: 68 (total: 170)
PASS
ok  	github.com/coolcake/cvkeharness/tools	6.098s`

	result := parseGoFuzzOutput("FuzzParseShellCommand", "go test ./tools -run ^$ -fuzz FuzzParseShellCommand -fuzztime 5s", output)

	if !result.Passed {
		t.Fatal("expected PASS output to mark suite as passed")
	}
	if result.Execs != 248278 {
		t.Fatalf("expected latest exec count, got %d", result.Execs)
	}
	if result.CoverageExpandingInputCount != 170 {
		t.Fatalf("expected total coverage-expanding input count, got %d", result.CoverageExpandingInputCount)
	}
}

func TestParseGoFuzzOutputFailure(t *testing.T) {
	t.Parallel()

	output := `--- FAIL: FuzzParseShellCommand (0.21s)
    --- FAIL: FuzzParseShellCommand (0.00s)
        shell_fuzz_test.go:23: accepted command with raw newline: "\n0"

    Failing input written to testdata/fuzz/FuzzParseShellCommand/860d98baaccac1ba
    To re-run:
    go test -run=FuzzParseShellCommand/860d98baaccac1ba
FAIL`

	result := parseGoFuzzOutput("FuzzParseShellCommand", "go test ./tools -run ^$ -fuzz FuzzParseShellCommand", output)
	if result.Passed {
		t.Fatal("expected FAIL output to mark suite as failed")
	}
	if result.Failure == nil {
		t.Fatal("expected failure details to be parsed")
	}
	if !strings.Contains(result.Failure.FailingInput, "raw newline") {
		t.Fatalf("expected failing input summary, got %#v", result.Failure)
	}
	if result.Failure.ArtifactPath != "testdata/fuzz/FuzzParseShellCommand/860d98baaccac1ba" {
		t.Fatalf("unexpected artifact path %#v", result.Failure)
	}
	if result.Failure.Reproducer != "go test -run=FuzzParseShellCommand/860d98baaccac1ba" {
		t.Fatalf("unexpected reproducer %#v", result.Failure)
	}
}

func TestRenderFuzzMarkdown(t *testing.T) {
	t.Parallel()

	report := fuzzReport{
		GeneratedAt: time.Unix(0, 0).UTC(),
		Commit:      "abc123",
		Package:     "./tools",
		FuzzTime:    "5s",
		Metrics: fuzzReportMetrics{
			TotalSuites:                 1,
			PassedSuites:                1,
			TotalExecs:                  10,
			CoverageExpandingInputCount: 2,
		},
		Policy: buildShellPolicySummary(),
		Invariants: []fuzzInvariant{{
			Name:      "raw newlines are rejected before trimming",
			Status:    "passed",
			CoveredBy: []string{"FuzzParseShellCommand"},
		}},
		Suites: []fuzzSuiteResult{{
			Name:                        "FuzzParseShellCommand",
			Passed:                      true,
			Execs:                       10,
			CoverageExpandingInputCount: 2,
			DurationMs:                  123,
		}},
	}

	markdown := renderFuzzMarkdown(report)
	for _, want := range []string{
		"# Fuzz Report",
		"| Passed suites | `1 / 1` |",
		"Coverage-expanding inputs",
		"## Shell Policy Corpus",
		"## Invariants",
		"`FuzzParseShellCommand`",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, markdown)
		}
	}
}

func TestTailLines(t *testing.T) {
	t.Parallel()

	got := tailLines("a\nb\nc\nd\n", 2)
	if got != "c\nd" {
		t.Fatalf("expected last two lines, got %q", got)
	}
}
