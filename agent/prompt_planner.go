package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
)

type promptPlan struct {
	SystemMessages []provider.Message
	PrefixHash     string
	PromptHash     string
	ToolNames      []string
}

func buildPromptPlan(retrieved memory.RetrievalResult, planningNotes string, volatile []provider.Message, toolDefs []provider.ToolDef) promptPlan {
	var systemMessages []provider.Message
	if stablePrefix := compiledGuidancePrefix(retrieved); stablePrefix != "" {
		systemMessages = append(systemMessages, provider.Message{
			Role:    "system",
			Content: stablePrefix,
		})
	}
	systemMessages = append(systemMessages, provider.Message{
		Role:    "system",
		Content: stableToolPolicy(toolDefs),
	})
	if brief := strings.TrimSpace(retrievedBrief(retrieved)); brief != "" {
		systemMessages = append(systemMessages, provider.Message{
			Role:    "system",
			Content: "Host-target-memory brief:\n" + brief,
		})
	}
	if notes := strings.TrimSpace(planningNotes); notes != "" {
		systemMessages = append(systemMessages, provider.Message{
			Role:    "system",
			Content: "Planning notes:\n" + notes,
		})
	}

	toolNames := toolNamesFromDefs(toolDefs)
	return promptPlan{
		SystemMessages: systemMessages,
		PrefixHash:     hashJSON([]any{compiledGuidancePrefix(retrieved), stableToolPolicy(toolDefs), toolDefs}),
		PromptHash:     hashJSON([]any{systemMessages, volatile, toolDefs}),
		ToolNames:      toolNames,
	}
}

func compiledGuidancePrefix(retrieved memory.RetrievalResult) string {
	var parts []string
	if builtIn := strings.TrimSpace(retrieved.BuiltInRules); builtIn != "" {
		parts = append(parts, builtIn)
	}
	if guidance := strings.TrimSpace(retrieved.Guidance); guidance != "" {
		parts = append(parts, guidance)
	}
	return strings.Join(parts, "\n\n")
}

func stableToolPolicy(toolDefs []provider.ToolDef) string {
	names := toolNamesFromDefs(toolDefs)
	if len(names) == 0 {
		return "Tool policy:\n- No tools are available for this turn."
	}
	return "Tool policy:\n- Use only the declared tools for this turn.\n- Tool schemas are authoritative; do not invent undeclared calls.\n- Available tools: " + strings.Join(names, ", ")
}

func toolNamesFromDefs(defs []provider.ToolDef) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if name := strings.TrimSpace(def.Function.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func hashJSON(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
