package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type configTab struct {
	loaded bool
	lines  []string
}

func newConfigTab() tabModel {
	return &configTab{}
}

func (t *configTab) Init(svc *Service) tea.Cmd {
	cfg := svc.Config()
	if cfg == nil {
		t.lines = []string{styleMuted.Render("Configuration not available")}
		t.loaded = true
		return nil
	}

	var lines []string
	lines = append(lines, renderKeyValue("Provider", cfg.Provider))
	lines = append(lines, renderKeyValue("Default Model", cfg.PrimaryModel()))
	if cfg.PlanningModel != "" {
		lines = append(lines, renderKeyValue("Planning Model", cfg.PlanningModel))
	}
	if cfg.ExecutionModel != "" {
		lines = append(lines, renderKeyValue("Execution Model", cfg.ExecutionModel))
	}
	if cfg.CurationModel != "" {
		lines = append(lines, renderKeyValue("Curation Model", cfg.CurationModel))
	}
	lines = append(lines, "")
	lines = append(lines, renderKeyValue("Safety Mode", cfg.SafetyMode))
	lines = append(lines, renderKeyValue("Safety Model", cfg.SafetyModel))
	lines = append(lines, "")
	lines = append(lines, renderKeyValue("Routing", func() string {
		if cfg.RoutingEnabled {
			return styleSuccess.Render("enabled") + styleMuted.Render(" ("+cfg.RoutingMode+")")
		}
		return styleMuted.Render("disabled")
	}()))
	lines = append(lines, renderKeyValue("Min Confidence", fmt.Sprintf("%.2f", cfg.RoutingMinConfidence)))
	lines = append(lines, "")
	lines = append(lines, renderKeyValue("Max Tokens", fmt.Sprintf("%d", cfg.MaxTokens)))
	lines = append(lines, renderKeyValue("Max Iterations", fmt.Sprintf("%d", cfg.MaxIterations)))
	lines = append(lines, renderKeyValue("Memory Dir", cfg.MemoryDir))
	lines = append(lines, renderKeyValue("State DB", cfg.StateDBPath))
	lines = append(lines, renderKeyValue("Max Snippets", fmt.Sprintf("%d", cfg.MemoryMaxSnippets)))
	lines = append(lines, renderKeyValue("Log Level", cfg.LogLevel))

	// Approved models
	if len(cfg.ApprovedModels) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleSectionTitle.Render("Approved Models"))
		for _, m := range cfg.ApprovedModels {
			lines = append(lines, styleMuted.Render("  • ")+styleBase.Render(m))
		}
	}

	// Favorite models
	if len(cfg.FavoriteModels) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleSectionTitle.Render("Favorite Models"))
		for _, m := range cfg.FavoriteModels {
			lines = append(lines, styleMuted.Render("  • ")+styleBase.Render(m))
		}
	}

	// Allowed commands
	if len(cfg.AllowedCommands) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleSectionTitle.Render("Allowed Commands"))
		// Compact display: join in groups
		cmds := make([]string, len(cfg.AllowedCommands))
		for i, c := range cfg.AllowedCommands {
			cmds[i] = styleBase.Render(c)
		}
		lines = append(lines, styleMuted.Render("  ")+strings.Join(cmds, styleMuted.Render(", ")))
	}

	// API Keys (show providers, mask keys)
	if len(cfg.APIKeys) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleSectionTitle.Render("API Keys"))
		for provider := range cfg.APIKeys {
			lines = append(lines, renderKeyValue("  "+provider, styleMuted.Render("••••••••")))
		}
	}

	t.lines = lines
	t.loaded = true
	return nil
}

func (t *configTab) Consuming() bool { return false }

func (t *configTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	return t, nil
}

func (t *configTab) View(width, height int) string {
	if !t.loaded {
		return styleMuted.Render("  Loading…")
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(styleSectionTitle.Render("Configuration"))
	b.WriteString("  ")
	b.WriteString(styleMuted.Render("(read-only)"))
	b.WriteString("\n\n")

	for _, line := range t.lines {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}
