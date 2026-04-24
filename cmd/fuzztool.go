package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/shellpolicy"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

var fuzztoolOutputDir string
var fuzztoolFuzzTime string
var fuzztoolPackage string

type fuzzReport struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Commit      string             `json:"commit"`
	Package     string             `json:"package"`
	FuzzTime    string             `json:"fuzztime"`
	Metrics     fuzzReportMetrics  `json:"metrics"`
	Policy      shellPolicySummary `json:"policy"`
	Invariants  []fuzzInvariant    `json:"invariants"`
	Suites      []fuzzSuiteResult  `json:"suites"`
}

type fuzzReportMetrics struct {
	TotalSuites                 int `json:"total_suites"`
	PassedSuites                int `json:"passed_suites"`
	FailedSuites                int `json:"failed_suites"`
	TotalExecs                  int `json:"total_execs"`
	CoverageExpandingInputCount int `json:"coverage_expanding_input_count"`
}

type fuzzSuiteResult struct {
	Name                        string       `json:"name"`
	Command                     string       `json:"command"`
	Passed                      bool         `json:"passed"`
	DurationMs                  int64        `json:"duration_ms"`
	Execs                       int          `json:"execs"`
	CoverageExpandingInputCount int          `json:"coverage_expanding_input_count"`
	Failure                     *fuzzFailure `json:"failure,omitempty"`
	OutputTail                  string       `json:"output_tail,omitempty"`
	Error                       string       `json:"error,omitempty"`
}

type fuzzFailure struct {
	Message      string `json:"message,omitempty"`
	FailingInput string `json:"failing_input,omitempty"`
	Reproducer   string `json:"reproducer,omitempty"`
	ArtifactPath string `json:"artifact_path,omitempty"`
}

type shellPolicySummary struct {
	TotalCases    int                            `json:"total_cases"`
	AllowCases    int                            `json:"allow_cases"`
	DenyCases     int                            `json:"deny_cases"`
	ApprovalCases int                            `json:"approval_cases"`
	Mismatches    []shellPolicyMismatch          `json:"mismatches,omitempty"`
	Categories    map[string]shellPolicyCategory `json:"categories"`
	Examples      shellPolicyExamples            `json:"examples"`
}

type shellPolicyCategory struct {
	Total    int `json:"total"`
	Allow    int `json:"allow"`
	Deny     int `json:"deny"`
	Approval int `json:"approval"`
}

type shellPolicyMismatch struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Reason   string `json:"reason,omitempty"`
}

type shellPolicyExamples struct {
	Accepted         []shellPolicyExample `json:"accepted"`
	Denied           []shellPolicyExample `json:"denied"`
	ApprovalRequired []shellPolicyExample `json:"approval_required"`
}

type shellPolicyExample struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Command  string `json:"command"`
}

type fuzzInvariant struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	CoveredBy []string `json:"covered_by"`
}

var defaultFuzzSuites = []string{
	"FuzzParseShellCommand",
	"FuzzValidateAllowedShellCommand",
}

var fuzztoolCmd = &cobra.Command{
	Use:   "fuzztool",
	Short: "Run deterministic fuzz smoke checks and write a report",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := time.ParseDuration(fuzztoolFuzzTime); err != nil {
			return fmt.Errorf("invalid --fuzztime value %q: %w", fuzztoolFuzzTime, err)
		}

		report := runFuzzReport(cmd.Context(), fuzztoolPackage, fuzztoolFuzzTime, defaultFuzzSuites, gitCommit())
		if err := writeFuzzReport(fuzztoolOutputDir, report); err != nil {
			return err
		}

		fmt.Printf("Fuzz report written to %s\n", fuzztoolOutputDir)
		fmt.Printf("Passed %d/%d suites | execs %d | coverage-expanding inputs %d\n",
			report.Metrics.PassedSuites,
			report.Metrics.TotalSuites,
			report.Metrics.TotalExecs,
			report.Metrics.CoverageExpandingInputCount,
		)
		if report.Metrics.FailedSuites > 0 {
			return fmt.Errorf("%d fuzz suite(s) failed", report.Metrics.FailedSuites)
		}
		return nil
	},
}

func init() {
	fuzztoolCmd.Flags().StringVar(&fuzztoolOutputDir, "output-dir", "docs", "directory to write generated fuzz report files into")
	fuzztoolCmd.Flags().StringVar(&fuzztoolFuzzTime, "fuzztime", "30s", "duration to run each fuzz suite")
	fuzztoolCmd.Flags().StringVar(&fuzztoolPackage, "package", "./tools", "package containing fuzz suites")
	rootCmd.AddCommand(fuzztoolCmd)
}

func runFuzzReport(ctx context.Context, packagePath, fuzzTime string, suites []string, commit string) fuzzReport {
	report := fuzzReport{
		GeneratedAt: time.Now().UTC(),
		Commit:      commit,
		Package:     packagePath,
		FuzzTime:    fuzzTime,
		Policy:      buildShellPolicySummary(),
		Suites:      make([]fuzzSuiteResult, 0, len(suites)),
	}

	for _, suite := range suites {
		result := runFuzzSuite(ctx, packagePath, suite, fuzzTime)
		report.Suites = append(report.Suites, result)
		report.Metrics.TotalSuites++
		report.Metrics.TotalExecs += result.Execs
		report.Metrics.CoverageExpandingInputCount += result.CoverageExpandingInputCount
		if result.Passed {
			report.Metrics.PassedSuites++
		} else {
			report.Metrics.FailedSuites++
		}
	}
	report.Invariants = buildFuzzInvariants(report.Metrics.FailedSuites == 0)

	return report
}

func runFuzzSuite(ctx context.Context, packagePath, suite, fuzzTime string) fuzzSuiteResult {
	args := []string{"test", packagePath, "-run", "^$", "-fuzz", suite, "-fuzztime", fuzzTime}
	commandText := "go " + strings.Join(args, " ")
	start := time.Now()

	cmd := exec.CommandContext(ctx, "go", args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()

	result := parseGoFuzzOutput(suite, commandText, output.String())
	result.DurationMs = time.Since(start).Milliseconds()
	result.OutputTail = tailLines(output.String(), 30)
	if err != nil {
		result.Passed = false
		result.Error = err.Error()
	}
	return result
}

func parseGoFuzzOutput(suite, commandText, output string) fuzzSuiteResult {
	result := fuzzSuiteResult{
		Name:    suite,
		Command: commandText,
		Passed:  strings.Contains(output, "PASS") && !strings.Contains(output, "FAIL"),
	}

	execsPattern := regexp.MustCompile(`execs:\s+([0-9]+)`)
	for _, match := range execsPattern.FindAllStringSubmatch(output, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil {
			result.Execs = value
		}
	}

	interestingPattern := regexp.MustCompile(`new interesting:\s+[0-9]+\s+\(total:\s+([0-9]+)\)`)
	for _, match := range interestingPattern.FindAllStringSubmatch(output, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil {
			result.CoverageExpandingInputCount = value
		}
	}
	result.Failure = parseFuzzFailure(output)

	return result
}

func buildShellPolicySummary() shellPolicySummary {
	summary := shellPolicySummary{
		Categories: make(map[string]shellPolicyCategory),
	}
	cases, err := shellpolicy.LoadCorpus()
	if err != nil {
		summary.Mismatches = append(summary.Mismatches, shellPolicyMismatch{
			ID:     "corpus-load",
			Actual: "error",
			Reason: err.Error(),
		})
		return summary
	}

	defaultAllowed := config.DefaultConfig().AllowedCommands
	for _, testCase := range cases {
		allowed := defaultAllowed
		if len(testCase.AllowedCommands) > 0 {
			allowed = testCase.AllowedCommands
		}
		actual, reason := classifyShellPolicyCase(testCase.Command, allowed)
		summary.TotalCases++

		category := summary.Categories[testCase.Category]
		category.Total++
		switch actual {
		case shellpolicy.DecisionAllow:
			summary.AllowCases++
			category.Allow++
			summary.Examples.Accepted = appendPolicyExample(summary.Examples.Accepted, testCase)
		case shellpolicy.DecisionDeny:
			summary.DenyCases++
			category.Deny++
			summary.Examples.Denied = appendPolicyExample(summary.Examples.Denied, testCase)
		case shellpolicy.DecisionRequireApproval:
			summary.ApprovalCases++
			category.Approval++
			summary.Examples.ApprovalRequired = appendPolicyExample(summary.Examples.ApprovalRequired, testCase)
		}
		summary.Categories[testCase.Category] = category

		if actual != testCase.ExpectedDecision {
			summary.Mismatches = append(summary.Mismatches, shellPolicyMismatch{
				ID:       testCase.ID,
				Command:  testCase.Command,
				Expected: string(testCase.ExpectedDecision),
				Actual:   string(actual),
				Reason:   reason,
			})
		}
	}
	return summary
}

func classifyShellPolicyCase(command string, allowed []string) (shellpolicy.Decision, string) {
	if err := tools.ValidateShellCommand(command); err != nil {
		return shellpolicy.DecisionDeny, err.Error()
	}
	if err := tools.ValidateAllowedShellCommand(command, allowed); err != nil {
		return shellpolicy.DecisionRequireApproval, err.Error()
	}
	return shellpolicy.DecisionAllow, "validated by shell allowlist"
}

func appendPolicyExample(items []shellPolicyExample, testCase shellpolicy.Case) []shellPolicyExample {
	if len(items) >= 3 {
		return items
	}
	return append(items, shellPolicyExample{
		ID:       testCase.ID,
		Category: testCase.Category,
		Command:  testCase.Command,
	})
}

func buildFuzzInvariants(passed bool) []fuzzInvariant {
	status := "passed"
	if !passed {
		status = "failed"
	}
	return []fuzzInvariant{
		{Name: "raw newlines are rejected before trimming", Status: status, CoveredBy: []string{"FuzzParseShellCommand", "FuzzValidateAllowedShellCommand"}},
		{Name: "command substitution is rejected outside inert quoting", Status: status, CoveredBy: []string{"FuzzParseShellCommand", "FuzzValidateAllowedShellCommand"}},
		{Name: "redirection and bare backgrounding are rejected", Status: status, CoveredBy: []string{"FuzzParseShellCommand", "FuzzValidateAllowedShellCommand"}},
		{Name: "accepted normalized parses are idempotent", Status: status, CoveredBy: []string{"FuzzParseShellCommand"}},
		{Name: "accepted chained commands keep operators equal to segments minus one", Status: status, CoveredBy: []string{"FuzzParseShellCommand", "FuzzValidateAllowedShellCommand"}},
	}
}

func parseFuzzFailure(output string) *fuzzFailure {
	if !strings.Contains(output, "FAIL") {
		return nil
	}
	failure := &fuzzFailure{}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case failure.Message == "" && strings.Contains(trimmed, ": Fuzz"):
			failure.Message = trimmed
		case strings.Contains(trimmed, "Failing input written to"):
			failure.ArtifactPath = strings.TrimSpace(strings.TrimPrefix(trimmed, "Failing input written to"))
		case strings.HasPrefix(trimmed, "go test "):
			failure.Reproducer = trimmed
		case failure.FailingInput == "" && strings.Contains(trimmed, "accepted command"):
			failure.FailingInput = strings.TrimSpace(trimmed)
		}
	}
	return failure
}

func writeFuzzReport(outputDir string, report fuzzReport) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "fuzz-report.json"), jsonBytes, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputDir, "fuzz-report.md"), []byte(renderFuzzMarkdown(report)), 0644)
}

func renderFuzzMarkdown(report fuzzReport) string {
	var b strings.Builder
	b.WriteString("# Fuzz Report\n\n")
	b.WriteString(fmt.Sprintf("- Generated: `%s`\n", report.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Commit: `%s`\n", report.Commit))
	b.WriteString(fmt.Sprintf("- Package: `%s`\n", report.Package))
	b.WriteString(fmt.Sprintf("- Fuzz time: `%s`\n\n", report.FuzzTime))

	b.WriteString("## Summary\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("| --- | --- |\n")
	b.WriteString(fmt.Sprintf("| Passed suites | `%d / %d` |\n", report.Metrics.PassedSuites, report.Metrics.TotalSuites))
	b.WriteString(fmt.Sprintf("| Failed suites | `%d` |\n", report.Metrics.FailedSuites))
	b.WriteString(fmt.Sprintf("| Total executions | `%d` |\n", report.Metrics.TotalExecs))
	b.WriteString(fmt.Sprintf("| Coverage-expanding inputs | `%d` |\n\n", report.Metrics.CoverageExpandingInputCount))

	if report.Metrics.FailedSuites > 0 {
		b.WriteString("## Failures\n\n")
		for _, suite := range report.Suites {
			if suite.Passed {
				continue
			}
			b.WriteString(fmt.Sprintf("### %s\n\n", suite.Name))
			if suite.Failure != nil {
				if suite.Failure.FailingInput != "" {
					b.WriteString(fmt.Sprintf("- Failing input: `%s`\n", escapeMarkdownCell(suite.Failure.FailingInput)))
				}
				if suite.Failure.ArtifactPath != "" {
					b.WriteString(fmt.Sprintf("- Artifact: `%s`\n", suite.Failure.ArtifactPath))
				}
				if suite.Failure.Reproducer != "" {
					b.WriteString(fmt.Sprintf("- Reproduce: `%s`\n", suite.Failure.Reproducer))
				}
			}
			if suite.Error != "" {
				b.WriteString(fmt.Sprintf("- Error: `%s`\n", suite.Error))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Shell Policy Corpus\n\n")
	b.WriteString(fmt.Sprintf("Corpus cases are evaluated semantically as `allow`, `deny`, or `require_approval`; `coverage-expanding input` is Go fuzzing's term for inputs that reached new code coverage, not necessarily security-relevant inputs.\n\n"))
	b.WriteString("| Outcome | Count |\n")
	b.WriteString("| --- | ---: |\n")
	b.WriteString(fmt.Sprintf("| Allow | `%d` |\n", report.Policy.AllowCases))
	b.WriteString(fmt.Sprintf("| Deny | `%d` |\n", report.Policy.DenyCases))
	b.WriteString(fmt.Sprintf("| Require approval | `%d` |\n", report.Policy.ApprovalCases))
	b.WriteString(fmt.Sprintf("| Mismatches | `%d` |\n\n", len(report.Policy.Mismatches)))

	b.WriteString("### Category Outcomes\n\n")
	b.WriteString("| Category | Total | Allow | Deny | Approval |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: |\n")
	for _, category := range sortedPolicyCategories(report.Policy.Categories) {
		item := report.Policy.Categories[category]
		b.WriteString(fmt.Sprintf("| `%s` | `%d` | `%d` | `%d` | `%d` |\n", category, item.Total, item.Allow, item.Deny, item.Approval))
	}
	b.WriteString("\n")

	b.WriteString("### Sample Cases\n\n")
	writePolicyExamples(&b, "Accepted", report.Policy.Examples.Accepted)
	writePolicyExamples(&b, "Denied", report.Policy.Examples.Denied)
	writePolicyExamples(&b, "Approval required", report.Policy.Examples.ApprovalRequired)

	b.WriteString("## Invariants\n\n")
	b.WriteString("| Invariant | Status | Covered by |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, invariant := range report.Invariants {
		b.WriteString(fmt.Sprintf("| %s | `%s` | `%s` |\n", invariant.Name, invariant.Status, strings.Join(invariant.CoveredBy, ", ")))
	}
	b.WriteString("\n")

	b.WriteString("## Suites\n\n")
	b.WriteString("| Suite | Pass | Execs | Coverage-expanding inputs | Duration |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: |\n")
	for _, suite := range report.Suites {
		pass := "no"
		if suite.Passed {
			pass = "yes"
		}
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `%d` | `%d` | `%dms` |\n",
			suite.Name,
			pass,
			suite.Execs,
			suite.CoverageExpandingInputCount,
			suite.DurationMs,
		))
	}

	for _, suite := range report.Suites {
		if suite.Passed {
			continue
		}
		b.WriteString(fmt.Sprintf("\n## %s Output\n\n", suite.Name))
		if suite.Error != "" {
			b.WriteString(fmt.Sprintf("- Error: `%s`\n\n", suite.Error))
		}
		b.WriteString("```text\n")
		b.WriteString(strings.TrimSpace(suite.OutputTail))
		b.WriteString("\n```\n")
	}

	return b.String()
}

func sortedPolicyCategories(categories map[string]shellPolicyCategory) []string {
	out := make([]string, 0, len(categories))
	for category := range categories {
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

func writePolicyExamples(b *strings.Builder, title string, examples []shellPolicyExample) {
	b.WriteString(fmt.Sprintf("**%s**\n\n", title))
	if len(examples) == 0 {
		b.WriteString("- none\n\n")
		return
	}
	for _, example := range examples {
		b.WriteString(fmt.Sprintf("- `%s` (`%s`): `%s`\n", example.ID, example.Category, escapeMarkdownInline(example.Command)))
	}
	b.WriteString("\n")
}

func escapeMarkdownCell(text string) string {
	return strings.ReplaceAll(text, "`", "'")
}

func escapeMarkdownInline(text string) string {
	return strings.ReplaceAll(text, "`", "\\`")
}

func tailLines(text string, limit int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= limit {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-limit:], "\n")
}
