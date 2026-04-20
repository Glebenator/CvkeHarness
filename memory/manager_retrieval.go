package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

// Retrieve loads the current soul + learned context for a phase/model/task.
func (m *Manager) Retrieve(ctx context.Context, input core.RetrievalContext) (RetrievalResult, error) {
	if err := m.EnsureFiles(); err != nil {
		return RetrievalResult{}, err
	}

	soulBytes, err := os.ReadFile(m.managedPath(SoulFile))
	if err != nil {
		return RetrievalResult{}, err
	}
	operatorBytes, err := os.ReadFile(m.managedPath(OperatorFile))
	if err != nil {
		return RetrievalResult{}, err
	}

	entries, err := m.lookupEntries(ctx, input)
	if err != nil {
		return RetrievalResult{}, err
	}

	limit := input.MaxSnippets
	if limit <= 0 {
		limit = m.maxSnippets
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}

	return RetrievalResult{
		BuiltInRules: builtInRules(),
		Operator:     formatOperatorContext(m.dir, string(operatorBytes)),
		Soul:         strings.TrimSpace(string(soulBytes)),
		Learned:      formatLearnedContext(input, entries),
		Snippets:     entries,
	}, nil
}

func (m *Manager) lookupEntries(ctx context.Context, input core.RetrievalContext) ([]state.MemoryEntry, error) {
	if m.store != nil && m.store.Available() {
		fromDB, err := m.store.ListMemoryEntries(ctx, state.MemoryFilter{
			SourceFiles: managedSourceFiles(),
			Phase:       input.Phase,
			TaskClass:   input.TaskClass,
			Provider:    input.ActiveModel.Provider,
			ToolName:    primaryToolName(input),
			OnlyActive:  true,
		})
		if err == nil && len(fromDB) > 0 {
			return scoreEntries(input, fromDB), nil
		}
	}

	fileEntries, err := m.parseManagedFiles()
	if err != nil {
		return nil, err
	}
	return scoreEntries(input, fileEntries), nil
}

func primaryToolName(input core.RetrievalContext) string {
	if input.Trouble != nil && input.Trouble.Tool != "" {
		return input.Trouble.Tool
	}
	if len(input.ToolNames) > 0 {
		return input.ToolNames[0]
	}
	return ""
}

func scoreEntries(input core.RetrievalContext, entries []state.MemoryEntry) []state.MemoryEntry {
	type scored struct {
		entry state.MemoryEntry
		score float64
	}

	var scoredEntries []scored
	activeModel := strings.TrimSpace(input.ActiveModel.Model)
	actualModel := strings.TrimSpace(input.ActualModel.Model)
	activeProvider := strings.TrimSpace(input.ActiveModel.Provider)
	actualProvider := strings.TrimSpace(input.ActualModel.Provider)
	taskTerms := significantTerms(input.Task)

	for _, entry := range entries {
		if entry.Status != "" && entry.Status != "active" {
			continue
		}
		if entry.Provider != "" && !matchesAny(entry.Provider, activeProvider, actualProvider) {
			continue
		}
		if entry.Model != "" && !matchesAny(entry.Model, activeModel, actualModel) {
			continue
		}

		score := entry.Confidence
		relevant := false
		if entry.Phase == input.Phase {
			score += 0.4
		}
		if entry.TaskClass != "" && entry.TaskClass == input.TaskClass {
			score += 1.2
			relevant = true
		}
		if entry.Provider != "" && entry.Provider == activeProvider {
			score += 0.7
			relevant = true
		}
		if entry.Provider != "" && actualProvider != "" && entry.Provider == actualProvider && entry.Provider != activeProvider {
			score += 0.7
			relevant = true
		}
		if entry.Model != "" && entry.Model == activeModel {
			score += 1.1
			relevant = true
		}
		if entry.Model != "" && actualModel != "" && entry.Model == actualModel && entry.Model != activeModel {
			score += 1.2
			relevant = true
		}
		for _, tool := range input.ToolNames {
			if entry.ToolName != "" && entry.ToolName == tool {
				score += 1.2
				relevant = true
			}
		}
		if input.Trouble != nil {
			if entry.ToolName != "" && entry.ToolName == input.Trouble.Tool {
				score += 1.0
				relevant = true
			}
			if input.Trouble.DenialClass != "" && strings.Contains(strings.ToLower(entry.Body), strings.ToLower(input.Trouble.DenialClass)) {
				score += 0.6
				relevant = true
			}
			if input.Trouble.Repeated {
				score += 0.3
			}
		}
		if overlap := lexicalOverlapScore(taskTerms, entry.Body); overlap > 0 {
			score += overlap
			relevant = true
		}
		if entry.Scope == "global" {
			score += 0.1
		}
		if entry.SourceFile == MemoryFile {
			score += 0.2
		}
		if !relevant {
			continue
		}

		scoredEntries = append(scoredEntries, scored{entry: entry, score: score})
	}

	sort.SliceStable(scoredEntries, func(i, j int) bool {
		if scoredEntries[i].score == scoredEntries[j].score {
			return scoredEntries[i].entry.UpdatedAt.After(scoredEntries[j].entry.UpdatedAt)
		}
		return scoredEntries[i].score > scoredEntries[j].score
	})

	out := make([]state.MemoryEntry, 0, len(scoredEntries))
	for _, item := range scoredEntries {
		out = append(out, item.entry)
	}
	return out
}

func formatLearnedContext(input core.RetrievalContext, entries []state.MemoryEntry) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("You are currently running as %s.", input.ActiveModel.String()))
	parts = append(parts, fmt.Sprintf("Active phase: %s. Task class: %s.", input.Phase, input.TaskClass))

	if len(entries) == 0 {
		parts = append(parts, "No learned context matched strongly enough to inject for this run.")
		return strings.Join(parts, "\n")
	}

	parts = append(parts, "Learned context:")
	for _, entry := range entries {
		scope := entry.Scope
		if scope == "" {
			scope = "global"
		}
		parts = append(parts, fmt.Sprintf("- [%s] %s", scope, strings.TrimSpace(entry.Body)))
	}
	return strings.Join(parts, "\n")
}

func builtInRules() string {
	return `You are CvkeHarness.
Keep the runtime rules compact and invariant.
Use tools deliberately, read errors carefully, and adapt before repeating a failed action.
If a required dependency is missing, confirm what is missing, ask before installing or otherwise mutating the system, and once approved perform the install instead of only handing the user manual steps.`
}

func formatOperatorContext(dir, operator string) string {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return ""
	}

	var parts []string
	parts = append(parts, "Runtime file locations:")
	parts = append(parts, "- Memory directory: "+dir)
	parts = append(parts, "- operator.md: "+filepath.Join(dir, OperatorFile))
	parts = append(parts, "- soul.md: "+filepath.Join(dir, SoulFile))
	parts = append(parts, "- memory.md: "+filepath.Join(dir, MemoryFile))
	parts = append(parts, "- findings.md: "+filepath.Join(dir, FindingsFile))
	parts = append(parts, "")
	parts = append(parts, operator)
	return strings.Join(parts, "\n")
}

func defaultOperatorMarkdown() string {
	return `# Operator Guide

Stable runtime guidance for how CvkeHarness is structured and how it should operate.

## Prompt Stack

Every run receives instructions in this order:

1. Built-in runtime rules
2. operator.md
3. soul.md
4. learned snippets from memory.md and findings.md

## File Roles

- operator.md: Stable harness-specific operating guidance. User-editable.
- soul.md: Persona, tone, and collaboration style. User-editable and never auto-edited by the harness.
- memory.md: Durable lessons promoted from repeated or clearly stable findings. Keep it concise and readable.
- findings.md: Provisional notes that may help a future run. Most runs should not add anything.

## Dependency Handling

If a task is blocked because a required tool, package, binary, or service is missing:

1. Confirm what is missing from the actual error or from a lightweight check.
2. Explain the install or system change you want to make.
3. Ask the user for permission before installing or otherwise mutating the system.
4. Once approved, perform the install yourself instead of only telling the user what to type.
5. Verify the dependency is available and then continue the original task.

## Ad Hoc Findings

You are allowed to write your own ad hoc findings during execution by calling the ` + "`memory_record_finding`" + ` tool.
That tool writes a concise note into ` + "`findings.md`" + `.

Write a finding when all of these are true:

1. The note is verified, not speculative.
2. The note is likely to help a future run, not just the current turn.
3. The note can be stated as a short reusable lesson, preference, or environment fact.
4. You would still want this note a few runs from now.

Good candidates:

1. A missing dependency, setup quirk, or local environment requirement you confirmed.
2. A tool-usage heuristic that clearly improved recovery after failure or denial.
3. A stable user preference that should affect future behavior.
4. A non-obvious fix whose key takeaway is likely to matter again.

Do not write a finding for:

1. Raw command output, logs, or transcripts.
2. Guesses, tentative theories, or unverified suspicions.
3. One-off status updates that will not matter in a later run.
4. Verbose summaries that belong in the final answer instead of memory.

## Memory Discipline

Prefer ad hoc notes in ` + "`findings.md`" + ` rather than durable memory.
Treat ` + "`findings.md`" + ` as a short list of provisional notes, not a log.
Treat ` + "`memory.md`" + ` as durable memory that should emerge after repetition or later curation.
When in doubt, write nothing or write a narrow finding instead of a broad permanent rule.
Edit operator.md or soul.md only when the user explicitly wants to change durable instructions or persona.

## Approval Boundary

Shell commands outside the allowlist may trigger a secondary approval gate.
Treat that as a normal boundary of the harness, not as a reason to stop helping.
If a safe install or setup step needs approval, ask for it clearly and continue once it is granted.

## Writing Style For Findings

Keep findings short, concrete, and future-facing.
Prefer one sentence.
Prefer narrow scope over broad scope.
Use ` + "`scope=tool`" + ` with a tool name when the lesson is tool-specific.
Use ` + "`scope=global`" + ` only when the lesson really applies across tasks.
`
}

func matchesAny(value string, options ...string) bool {
	for _, option := range options {
		if option != "" && value == option {
			return true
		}
	}
	return false
}

func lexicalOverlapScore(taskTerms map[string]bool, body string) float64 {
	if len(taskTerms) == 0 {
		return 0
	}

	matches := 0
	for term := range significantTerms(body) {
		if taskTerms[term] {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}

	score := 0.8 + (0.25 * float64(matches-1))
	if score > 1.3 {
		return 1.3
	}
	return score
}

func significantTerms(text string) map[string]bool {
	replacer := strings.NewReplacer("/", " ", "-", " ", "_", " ", ".", " ", ",", " ", ":", " ", ";", " ", "(", " ", ")", " ")
	text = replacer.Replace(strings.ToLower(text))
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
		"from": true, "into": true, "when": true, "then": true, "just": true, "your": true,
		"have": true, "after": true, "before": true, "need": true, "does": true, "about": true,
		"agent": true, "memory": true, "findings": true,
	}

	out := make(map[string]bool, len(fields))
	for _, field := range fields {
		if len(field) < 4 || stopWords[field] {
			continue
		}
		out[field] = true
	}
	return out
}
