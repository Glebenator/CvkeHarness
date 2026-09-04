package tui

import "github.com/charmbracelet/lipgloss"

// ── palette ─────────────────────────────────────────────────────────
// Warm neutrals. Nothing electric, nothing neon.
var (
	colorBase       = lipgloss.AdaptiveColor{Dark: "#c6c0b9", Light: "#49413b"} // warm grey — body text
	colorMuted      = lipgloss.AdaptiveColor{Dark: "#a69c92", Light: "#71665c"} // stone — borders, secondary
	colorSubtle     = lipgloss.AdaptiveColor{Dark: "#655b52", Light: "#b6aa9c"} // deeper stone — faint lines
	colorAccent     = lipgloss.AdaptiveColor{Dark: "#d6ad7b", Light: "#845321"} // warm amber — focus, headers
	colorSuccess    = lipgloss.AdaptiveColor{Dark: "#a1b99a", Light: "#3f6746"} // sage green — passing, enabled
	colorWarning    = lipgloss.AdaptiveColor{Dark: "#d1b17a", Light: "#795820"} // dusty gold — paused, pending
	colorError      = lipgloss.AdaptiveColor{Dark: "#dea08a", Light: "#a0442f"} // terracotta — failures
	colorSurface    = lipgloss.AdaptiveColor{Dark: "#2c2723", Light: "#eee7dd"} // raised surface
	colorBrightText = lipgloss.AdaptiveColor{Dark: "#eee8df", Light: "#302923"} // brighter text for emphasis
)

// ── shared styles ───────────────────────────────────────────────────

var (
	// Tab bar
	styleTab = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 2)

	styleActiveTab = lipgloss.NewStyle().
			Foreground(colorAccent).
			Background(colorHighlight).
			Bold(true).
			Padding(0, 2)

	// Headings
	styleTitle = lipgloss.NewStyle().
			Foreground(colorBrightText).
			Bold(true)

	styleSectionTitle = lipgloss.NewStyle().
				Foreground(colorBrightText).
				Bold(true)

	styleAccent = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	// Text variants
	styleBase = lipgloss.NewStyle().
			Foreground(colorBase)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleSubtle = lipgloss.NewStyle().
			Foreground(colorSubtle)

	styleBright = lipgloss.NewStyle().
			Foreground(colorBrightText)

	// Status colors
	styleSuccess = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleWarning = lipgloss.NewStyle().
			Foreground(colorWarning)

	styleError = lipgloss.NewStyle().
			Foreground(colorError)

	// Selected row highlight
	colorHighlight = lipgloss.AdaptiveColor{Dark: "#3c332a", Light: "#e4d6c4"} // clearly raised surface for selection

	styleSelectedRow = lipgloss.NewStyle().
				Background(colorHighlight).
				Foreground(colorBrightText).
				Bold(true)

	// Status bar at bottom
	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	// Borders
	styleBorder = lipgloss.NewStyle().
			BorderForeground(colorSubtle)

	// Key help
	styleKeyHelp = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleKeyHelpKey = lipgloss.NewStyle().
			Foreground(colorAccent)

	// Input fields
	styleInputLabel = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	styleInputPrompt = lipgloss.NewStyle().
				Foreground(colorMuted)

	styleInputActive = lipgloss.NewStyle().
				Foreground(colorBrightText)

	// Detail pane label/value
	styleDetailLabel = lipgloss.NewStyle().
				Foreground(colorMuted).
				Width(16)

	styleDetailValue = lipgloss.NewStyle().
				Foreground(colorBase)
)
