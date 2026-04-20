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

func soulFilePath(memoryDir string) string {
	return filepath.Join(memoryDir, memory.SoulFile)
}

func soulBootstrapRequired(memoryDir string) (bool, error) {
	data, err := os.ReadFile(soulFilePath(memoryDir))
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	trimmed := strings.TrimSpace(string(data))
	return trimmed == "" || trimmed == "# Soul", nil
}

func ensureSetupMemoryFiles(memoryDir string, maxSnippets int) error {
	return memory.NewManager(memoryDir, nil, maxSnippets).EnsureFiles()
}

func writeSetupSoul(memoryDir string, maxSnippets int, profile soulProfile) (bool, error) {
	if err := ensureSetupMemoryFiles(memoryDir, maxSnippets); err != nil {
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
	if err := os.WriteFile(soulFilePath(memoryDir), []byte(content), 0644); err != nil {
		return false, err
	}
	return true, nil
}

func buildSoulMarkdown(profile soulProfile) string {
	return fmt.Sprintf(`# Soul

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
