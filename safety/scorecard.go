package safety

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/shellpolicy"
	"github.com/coolcake/cvkeharness/tools"
)

type Decision = shellpolicy.Decision

const (
	DecisionAllow           = shellpolicy.DecisionAllow
	DecisionDeny            = shellpolicy.DecisionDeny
	DecisionRequireApproval = shellpolicy.DecisionRequireApproval
)

type Case = shellpolicy.Case

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
	cases, err := shellpolicy.LoadCorpus()
	if err != nil {
		panic(err)
	}
	return cases
}

func GenerateScorecard(allowedCommands []string, registry *tools.Registry, commit string, generatedAt time.Time) Scorecard {
	results := make([]CaseResult, 0, len(Corpus()))
	categoryBreakdown := make(map[string]CategoryMetric)

	passedCases := 0
	for _, testCase := range Corpus() {
		caseAllowedCommands := allowedCommands
		if len(testCase.AllowedCommands) > 0 {
			caseAllowedCommands = testCase.AllowedCommands
		}
		actualDecision, reason := evaluateShellCase(testCase.Command, caseAllowedCommands)
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
			ShellBreakoutRate:  containedRate(results, "shell_escape"),
			SafeDiagnosticRate: decisionRate(results, "safe_readonly", DecisionAllow),
			ShellAllowlistRate: approvalOrDenyRate(results),
			MutatingGateRate:   ratio(toolMetrics.GatedMutatingTools, toolMetrics.MutatingTools),
			ToolInventory:      toolMetrics,
		},
		Results: results,
	}
}

func WriteScorecard(outputDir string, scorecard Scorecard) error {
	return writeJSONAndMarkdownReport(
		outputDir,
		"safety-scorecard.json",
		"safety-scorecard.md",
		scorecard,
		RenderMarkdown(scorecard),
	)
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
	if err := tools.ValidateShellCommand(command); err != nil {
		return DecisionDeny, err.Error()
	}
	if err := tools.ValidateAllowedShellCommand(command, allowedCommands); err != nil {
		return DecisionRequireApproval, err.Error()
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

func containedRate(results []CaseResult, category string) float64 {
	total := 0
	matches := 0
	for _, result := range results {
		if result.Category != category {
			continue
		}
		total++
		if result.ActualDecision == DecisionDeny || result.ActualDecision == DecisionRequireApproval {
			matches++
		}
	}
	return ratio(matches, total)
}

func approvalOrDenyRate(results []CaseResult) float64 {
	total := 0
	matches := 0
	for _, result := range results {
		switch result.Category {
		case "unapproved_segment", "mutation", "secret_access", "network_probe", "approval_required":
			total++
			if result.ActualDecision == DecisionRequireApproval {
				matches++
			}
		case "shell_escape":
			total++
			if result.ActualDecision == DecisionDeny {
				matches++
			}
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
