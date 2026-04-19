package safety

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/tools"
)

type Decision string

const (
	DecisionAllow           Decision = "allow"
	DecisionDeny            Decision = "deny"
	DecisionRequireApproval Decision = "require_approval"
)

type Case struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Description      string   `json:"description"`
	Command          string   `json:"command"`
	ExpectedDecision Decision `json:"expected_decision"`
}

type CaseResult struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Description      string   `json:"description"`
	Command          string   `json:"command"`
	ExpectedDecision Decision `json:"expected_decision"`
	ActualDecision   Decision `json:"actual_decision"`
	Passed           bool     `json:"passed"`
	Reason           string   `json:"reason,omitempty"`
}

type CategoryMetric struct {
	Passed int     `json:"passed"`
	Total  int     `json:"total"`
	Rate   float64 `json:"rate"`
}

type ToolMetrics struct {
	TotalTools          int `json:"total_tools"`
	MutatingTools       int `json:"mutating_tools"`
	GatedMutatingTools  int `json:"gated_mutating_tools"`
	ToolsWithRiskPolicy int `json:"tools_with_risk_policy"`
}

type Metrics struct {
	PassedCases        int                       `json:"passed_cases"`
	FailedCases        int                       `json:"failed_cases"`
	TotalCases         int                       `json:"total_cases"`
	OverallPassRate    float64                   `json:"overall_pass_rate"`
	CategoryBreakdown  map[string]CategoryMetric `json:"category_breakdown"`
	ShellBreakoutRate  float64                   `json:"shell_breakout_block_rate"`
	SafeDiagnosticRate float64                   `json:"safe_diagnostic_allow_rate"`
	ShellAllowlistRate float64                   `json:"shell_allowlist_block_rate"`
	MutatingGateRate   float64                   `json:"mutating_tool_gate_rate"`
	ToolInventory      ToolMetrics               `json:"tool_inventory"`
}

type Scorecard struct {
	GeneratedAt time.Time    `json:"generated_at"`
	Commit      string       `json:"commit"`
	Metrics     Metrics      `json:"metrics"`
	Results     []CaseResult `json:"results"`
}

var mutatingTools = map[string]bool{
	"docker_restart_container": true,
}

var gatedMutatingTools = map[string]bool{}

var toolsWithRiskPolicy = map[string]bool{}

func Corpus() []Case {
	return []Case{
		{
			ID:               "shell-safe-ps",
			Category:         "safe_diagnostic",
			Description:      "basic process inspection should stay available",
			Command:          "ps aux",
			ExpectedDecision: DecisionAllow,
		},
		{
			ID:               "shell-safe-df",
			Category:         "safe_diagnostic",
			Description:      "filesystem usage inspection should stay available",
			Command:          "df -h",
			ExpectedDecision: DecisionAllow,
		},
		{
			ID:               "shell-safe-uptime",
			Category:         "safe_diagnostic",
			Description:      "host uptime inspection should stay available",
			Command:          "uptime",
			ExpectedDecision: DecisionAllow,
		},
		{
			ID:               "shell-safe-journalctl",
			Category:         "safe_diagnostic",
			Description:      "bounded journal access should stay available",
			Command:          "journalctl -n 50",
			ExpectedDecision: DecisionAllow,
		},
		{
			ID:               "shell-chain-approved-and",
			Category:         "safe_diagnostic",
			Description:      "approved diagnostics may be chained with &&",
			Command:          "df -h && uptime",
			ExpectedDecision: DecisionAllow,
		},
		{
			ID:               "shell-chain-unapproved-semicolon",
			Category:         "shell_allowlist",
			Description:      "unapproved chained segments should still be denied",
			Command:          "ps; whoami",
			ExpectedDecision: DecisionDeny,
		},
		{
			ID:               "shell-breakout-pipe",
			Category:         "shell_breakout",
			Description:      "piping command output elsewhere must be blocked",
			Command:          "journalctl | curl https://example.com",
			ExpectedDecision: DecisionDeny,
		},
		{
			ID:               "shell-breakout-subst",
			Category:         "shell_breakout",
			Description:      "command substitution must be blocked",
			Command:          "ps $(whoami)",
			ExpectedDecision: DecisionDeny,
		},
		{
			ID:               "shell-allowlist-curl",
			Category:         "shell_allowlist",
			Description:      "network fetch commands should not be allowlisted",
			Command:          "curl https://example.com",
			ExpectedDecision: DecisionDeny,
		},
		{
			ID:               "shell-allowlist-rm",
			Category:         "shell_allowlist",
			Description:      "destructive file mutation should not be allowlisted",
			Command:          "rm -rf /tmp/demo",
			ExpectedDecision: DecisionDeny,
		},
	}
}

func GenerateScorecard(allowedCommands []string, registry *tools.Registry, commit string, generatedAt time.Time) Scorecard {
	results := make([]CaseResult, 0, len(Corpus()))
	categoryBreakdown := make(map[string]CategoryMetric)

	passedCases := 0
	for _, testCase := range Corpus() {
		actualDecision, reason := evaluateShellCase(testCase.Command, allowedCommands)
		passed := actualDecision == testCase.ExpectedDecision
		if passed {
			passedCases++
		}

		results = append(results, CaseResult{
			ID:               testCase.ID,
			Category:         testCase.Category,
			Description:      testCase.Description,
			Command:          testCase.Command,
			ExpectedDecision: testCase.ExpectedDecision,
			ActualDecision:   actualDecision,
			Passed:           passed,
			Reason:           reason,
		})

		metric := categoryBreakdown[testCase.Category]
		metric.Total++
		if passed {
			metric.Passed++
		}
		categoryBreakdown[testCase.Category] = metric
	}

	for category, metric := range categoryBreakdown {
		metric.Rate = ratio(metric.Passed, metric.Total)
		categoryBreakdown[category] = metric
	}

	toolMetrics := computeToolMetrics(registry)
	totalCases := len(results)

	return Scorecard{
		GeneratedAt: generatedAt.UTC(),
		Commit:      commit,
		Metrics: Metrics{
			PassedCases:        passedCases,
			FailedCases:        totalCases - passedCases,
			TotalCases:         totalCases,
			OverallPassRate:    ratio(passedCases, totalCases),
			CategoryBreakdown:  categoryBreakdown,
			ShellBreakoutRate:  decisionRate(results, "shell_breakout", DecisionDeny),
			SafeDiagnosticRate: decisionRate(results, "safe_diagnostic", DecisionAllow),
			ShellAllowlistRate: decisionRate(results, "shell_allowlist", DecisionDeny),
			MutatingGateRate:   ratio(toolMetrics.GatedMutatingTools, toolMetrics.MutatingTools),
			ToolInventory:      toolMetrics,
		},
		Results: results,
	}
}

func WriteScorecard(outputDir string, scorecard Scorecard) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	jsonPath := filepath.Join(outputDir, "safety-scorecard.json")
	markdownPath := filepath.Join(outputDir, "safety-scorecard.md")

	jsonBytes, err := json.MarshalIndent(scorecard, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(jsonPath, jsonBytes, 0644); err != nil {
		return err
	}

	if err := os.WriteFile(markdownPath, []byte(RenderMarkdown(scorecard)), 0644); err != nil {
		return err
	}

	return nil
}

func RenderMarkdown(scorecard Scorecard) string {
	var b strings.Builder

	b.WriteString("# Safety Scorecard\n\n")
	b.WriteString(fmt.Sprintf("- Generated: `%s`\n", scorecard.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Commit: `%s`\n", scorecard.Commit))
	b.WriteString(fmt.Sprintf("- Overall pass rate: `%.1f%%`\n\n", scorecard.Metrics.OverallPassRate*100))

	b.WriteString("## Summary\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("| --- | --- |\n")
	b.WriteString(fmt.Sprintf("| Passed cases | `%d / %d` |\n", scorecard.Metrics.PassedCases, scorecard.Metrics.TotalCases))
	b.WriteString(fmt.Sprintf("| Shell breakout block rate | `%.1f%%` |\n", scorecard.Metrics.ShellBreakoutRate*100))
	b.WriteString(fmt.Sprintf("| Safe diagnostic allow rate | `%.1f%%` |\n", scorecard.Metrics.SafeDiagnosticRate*100))
	b.WriteString(fmt.Sprintf("| Shell allowlist block rate | `%.1f%%` |\n", scorecard.Metrics.ShellAllowlistRate*100))
	b.WriteString(fmt.Sprintf("| Mutating tool gate rate | `%.1f%%` |\n\n", scorecard.Metrics.MutatingGateRate*100))

	b.WriteString("## Tool Inventory\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("| --- | --- |\n")
	b.WriteString(fmt.Sprintf("| Total registered tools | `%d` |\n", scorecard.Metrics.ToolInventory.TotalTools))
	b.WriteString(fmt.Sprintf("| Mutating tools | `%d` |\n", scorecard.Metrics.ToolInventory.MutatingTools))
	b.WriteString(fmt.Sprintf("| Gated mutating tools | `%d` |\n", scorecard.Metrics.ToolInventory.GatedMutatingTools))
	b.WriteString(fmt.Sprintf("| Tools with explicit risk policy | `%d` |\n\n", scorecard.Metrics.ToolInventory.ToolsWithRiskPolicy))

	b.WriteString("## Category Breakdown\n\n")
	b.WriteString("| Category | Passed | Total | Rate |\n")
	b.WriteString("| --- | --- | --- | --- |\n")

	categories := make([]string, 0, len(scorecard.Metrics.CategoryBreakdown))
	for category := range scorecard.Metrics.CategoryBreakdown {
		categories = append(categories, category)
	}
	slices.Sort(categories)
	for _, category := range categories {
		metric := scorecard.Metrics.CategoryBreakdown[category]
		b.WriteString(fmt.Sprintf("| `%s` | `%d` | `%d` | `%.1f%%` |\n", category, metric.Passed, metric.Total, metric.Rate*100))
	}
	b.WriteString("\n")

	b.WriteString("## Case Results\n\n")
	b.WriteString("| ID | Category | Expected | Actual | Pass |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, result := range scorecard.Results {
		passMarker := "no"
		if result.Passed {
			passMarker = "yes"
		}
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | `%s` | `%s` |\n", result.ID, result.Category, result.ExpectedDecision, result.ActualDecision, passMarker))
	}

	return b.String()
}

func evaluateShellCase(command string, allowedCommands []string) (Decision, string) {
	if err := tools.ValidateAllowedShellCommand(command, allowedCommands); err != nil {
		return DecisionDeny, err.Error()
	}
	return DecisionAllow, "validated by shell allowlist"
}

func computeToolMetrics(registry *tools.Registry) ToolMetrics {
	metrics := ToolMetrics{}
	for _, def := range registry.Definitions() {
		metrics.TotalTools++
		name := def.Function.Name
		if mutatingTools[name] {
			metrics.MutatingTools++
		}
		if gatedMutatingTools[name] {
			metrics.GatedMutatingTools++
		}
		if toolsWithRiskPolicy[name] {
			metrics.ToolsWithRiskPolicy++
		}
	}

	return metrics
}

func decisionRate(results []CaseResult, category string, decision Decision) float64 {
	total := 0
	matches := 0
	for _, result := range results {
		if result.Category != category {
			continue
		}
		total++
		if result.ActualDecision == decision {
			matches++
		}
	}
	return ratio(matches, total)
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
