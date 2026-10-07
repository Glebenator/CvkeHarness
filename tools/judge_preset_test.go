package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
)

func TestLLMJudgePresetReviewsShellAndNonShellMutations(t *testing.T) {
	for _, toolName := range []string{"shell_execute", "schedule_manage"} {
		for _, verdict := range []string{"SAFE", "DANGEROUS"} {
			t.Run(toolName+"/"+verdict, func(t *testing.T) {
				marker := filepath.Join(t.TempDir(), "not-created")
				args := `{"action":"add","prompt":"restart worker"}`
				if toolName == "shell_execute" {
					data, _ := json.Marshal(map[string]string{"command": "touch " + marker})
					args = string(data)
				}
				judge := &fixedJudgeProvider{response: verdict}
				policy := resolvedProfile(t, securitypolicy.ProfileLLMJudge)
				registry, err := NewDefaultRegistryFromOptions(DefaultRegistryOptions{SecurityPolicy: &policy, SafetyMode: SafetyModeLLMJudge, Judge: judge, SafetyModel: "chosen-judge", BlockManualApprovals: true})
				if err != nil {
					t.Fatal(err)
				}
				registry.Register(fakeRegistryTool{name: "schedule_manage"})
				_, err = registry.ExecuteTool(context.Background(), provider.ToolCall{Function: provider.ToolFunction{Name: toolName, Arguments: args}})
				if judge.request == nil || judge.request.Model != "chosen-judge" {
					t.Fatal("action did not reach the selected judge")
				}
				if verdict == "SAFE" {
					if _, ok := IsApprovalRequired(err); !ok {
						t.Fatalf("SAFE must still await human approval: %v", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "dangerous") {
					t.Fatalf("DANGEROUS did not block execution: %v", err)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("unapproved command executed")
				}
			})
		}
	}
}

func TestLLMJudgePresetRetainsReadAllowAndHardBlocks(t *testing.T) {
	policy := resolvedProfile(t, securitypolicy.ProfileLLMJudge)
	for command, want := range map[string]securitypolicy.Decision{
		"echo read-only":              securitypolicy.DecisionAllow,
		"python3 -c 'print(1)'":       securitypolicy.DecisionLLMReview,
		"curl https://example.com":    securitypolicy.DecisionLLMReview,
		"rm -f /etc/hosts":            securitypolicy.DecisionAsk,
		"cat ~/.ssh/id_rsa":           securitypolicy.DecisionDeny,
		"dd if=/dev/zero of=/dev/sda": securitypolicy.DecisionDeny,
	} {
		assessment, err := AssessShellCommand(command, policy)
		if err != nil || assessment.Decision != want {
			t.Errorf("%q = %s, %v; want %s", command, assessment.Decision, err, want)
		}
	}
}
