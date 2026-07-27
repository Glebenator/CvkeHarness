package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coolcake/cvkeharness/memory"
)

type soulProfile struct {
	ID               string
	Label            string
	Description      string
	Tone             string
	Autonomy         string
	RiskPosture      string
	ExplanationDepth string
}

var soulProfiles = []soulProfile{
	{
		ID:               "balanced",
		Label:            "Balanced",
		Description:      "Clear, steady, and practical  ★",
		Tone:             "balanced",
		Autonomy:         "balanced",
		RiskPosture:      "standard",
		ExplanationDepth: "standard",
	},
	{
		ID:               "concise",
		Label:            "Concise",
		Description:      "Short answers with proactive execution",
		Tone:             "terse",
		Autonomy:         "proactive",
		RiskPosture:      "standard",
		ExplanationDepth: "brief",
	},
	{
		ID:               "cautious",
		Label:            "Cautious",
		Description:      "Ask-first and conservative around risk",
		Tone:             "balanced",
		Autonomy:         "ask-first",
		RiskPosture:      "conservative",
		ExplanationDepth: "standard",
	},
	{
		ID:               "mentor",
		Label:            "Mentor",
		Description:      "More explanatory and teaching-oriented",
		Tone:             "explanatory",
		Autonomy:         "balanced",
		RiskPosture:      "standard",
		ExplanationDepth: "detailed",
	},
}

const defaultSoulProfileID = "balanced"

func defaultSoulProfile() soulProfile {
	return soulProfileByID(defaultSoulProfileID)
}

func soulProfileByID(id string) soulProfile {
	for _, profile := range soulProfiles {
		if profile.ID == id {
			return profile
		}
	}
	return soulProfiles[0]
}

func soulProfileItems() [][2]string {
	items := make([][2]string, 0, len(soulProfiles))
	for _, profile := range soulProfiles {
		items = append(items, [2]string{profile.Label, profile.Description})
	}
	return items
}

func soulProfileIndexByID(id string) int {
	for i, profile := range soulProfiles {
		if profile.ID == id {
			return i
		}
	}
	return 0
}

func guidanceFilePath(memoryDir string) string {
	return filepath.Join(memoryDir, memory.GuidanceFile)
}

func soulBootstrapRequired(memoryDir string) (bool, error) {
	data, err := os.ReadFile(guidanceFilePath(memoryDir))
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	trimmed := strings.TrimSpace(string(data))
	return trimmed == "" || trimmed == "# Guidance", nil
}

func ensureSetupMemoryFiles(memoryDir string) error {
	return memory.NewManager(memoryDir, nil).EnsureFiles()
}

type setupHostNotesStatus int

const (
	setupHostNotesSkipped setupHostNotesStatus = iota
	setupHostNotesWritten
	setupHostNotesPreserved
)

func writeSetupSoul(memoryDir string, profile soulProfile) (bool, error) {
	if err := ensureSetupMemoryFiles(memoryDir); err != nil {
		return false, err
	}

	shouldWrite, err := soulBootstrapRequired(memoryDir)
	if err != nil {
		return false, err
	}
	if !shouldWrite {
		return false, nil
	}

	content := buildSoulMarkdown(profile)
	if err := os.WriteFile(guidanceFilePath(memoryDir), []byte(content), 0644); err != nil {
		return false, err
	}
	return true, nil
}

func writeSetupHostNotes(memoryDir string, notes []string) (setupHostNotesStatus, error) {
	notes = dedupeSetupNotes(notes)
	if len(notes) == 0 {
		return setupHostNotesSkipped, nil
	}
	if err := ensureSetupMemoryFiles(memoryDir); err != nil {
		return setupHostNotesSkipped, err
	}
	path := guidanceFilePath(memoryDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return setupHostNotesSkipped, err
	}
	content := string(data)
	if len(splitSetupNotes(extractSetupSection(content, "Runtime Host Notes"))) > 0 {
		return setupHostNotesPreserved, nil
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(content, "\n"))
	b.WriteString("\n\n## Runtime Host Notes\n\n")
	for _, note := range notes {
		b.WriteString("- ")
		b.WriteString(note)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return setupHostNotesSkipped, err
	}
	return setupHostNotesWritten, nil
}

func loadSetupHostNotes(memoryDir string) ([]string, error) {
	data, err := os.ReadFile(guidanceFilePath(memoryDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return splitSetupNotes(extractSetupSection(string(data), "Notes")), nil
}

func extractSetupSection(content, heading string) string {
	var lines []string
	inSection := false
	for _, raw := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			if inSection {
				break
			}
			section := strings.TrimSpace(strings.TrimLeft(line, "#"))
			inSection = section == heading
			continue
		}
		if inSection {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func splitSetupNotes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	hasBullets := false
	for _, rawLine := range lines {
		if strings.HasPrefix(strings.TrimSpace(rawLine), "- ") {
			hasBullets = true
			break
		}
	}

	var notes []string
	if hasBullets {
		for _, rawLine := range lines {
			note := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawLine), "- "))
			if note == "" {
				continue
			}
			notes = append(notes, note)
		}
		return dedupeSetupNotes(notes)
	}

	if strings.Contains(raw, "\n") {
		for _, block := range strings.Split(raw, "\n\n") {
			note := strings.Join(strings.Fields(strings.TrimSpace(block)), " ")
			if note == "" {
				continue
			}
			notes = append(notes, note)
		}
		return dedupeSetupNotes(notes)
	}

	for _, part := range strings.Split(raw, ";") {
		note := strings.Join(strings.Fields(strings.TrimSpace(part)), " ")
		if note == "" {
			continue
		}
		notes = append(notes, note)
	}
	return dedupeSetupNotes(notes)
}

func dedupeSetupNotes(notes []string) []string {
	var out []string
	seen := make(map[string]bool, len(notes))
	for _, note := range notes {
		note = strings.Join(strings.Fields(strings.TrimSpace(note)), " ")
		if note == "" || seen[note] {
			continue
		}
		seen[note] = true
		out = append(out, note)
	}
	return out
}

func buildSoulMarkdown(profile soulProfile) string {
	return fmt.Sprintf(`# Guidance

You are CvkeHarness, a local-first engineering agent for coding, debugging, systems work, and DevOps-style workflows.

## Purpose

Help the user make real progress in their local environment through careful execution, clear communication, and verifiable results.

## Priorities

1. Be correct before being clever.
2. Protect the user's system, data, and intent.
3. Make steady progress with small, reversible steps.
4. Be transparent about uncertainty, assumptions, and verification status.

## Working Style

Inspect the current state before making changes.
Prefer the simplest action that can validate the next assumption.
Use tools deliberately, read outputs carefully, and adapt when something fails.
Do not claim success unless the outcome was actually verified.

## Collaboration

Communicate clearly, directly, and concisely.
Keep the user informed as work progresses.
When a path is ambiguous, risky, or potentially destructive, pause and ask.
When the path is clear and low-risk, act decisively and keep momentum.

## Change Discipline

Prefer minimal diffs and reversible actions.
Respect existing user work and local conventions.
Avoid unnecessary churn.
Leave code, configuration, and documentation clearer than you found them.

## User Preferences

- Tone: %s
- Autonomy: %s
- Risk posture: %s
- Explanation depth: %s

## North Star

Leave the workspace in a better, safer, and more understandable state than you found it.
`, profile.Tone, profile.Autonomy, profile.RiskPosture, profile.ExplanationDepth)
}
