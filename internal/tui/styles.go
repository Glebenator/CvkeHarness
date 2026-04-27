package tui

import "github.com/charmbracelet/lipgloss"

// ── palette ─────────────────────────────────────────────────────────
// Warm neutrals. Nothing electric, nothing neon.
var (
	colorBase       = lipgloss.Color("#a8a29e") // warm grey — body text
	colorMuted      = lipgloss.Color("#78716c") // stone — borders, secondary
	colorSubtle     = lipgloss.Color("#57534e") // deeper stone — faint lines
	colorAccent     = lipgloss.Color("#d4a574") // warm amber — focus, headers
	colorSuccess    = lipgloss.Color("#87a987") // sage green — passing, enabled
	colorWarning    = lipgloss.Color("#c4a35a") // dusty gold — paused, pending
	colorError      = lipgloss.Color("#c47a5a") // terracotta — failures
	colorSurface    = lipgloss.Color("#292524") // raised surface
	colorBrightText = lipgloss.Color("#d6d3d1") // brighter text for emphasis
)

// ── shared styles ───────────────────────────────────────────────────

var (
	// Tab bar
	styleTab = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 2)

	styleActiveTab = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			Padding(0, 2).
			Underline(true)

	// Headings
	styleTitle = lipgloss.NewStyle().
			Foreground(colorBrightText).
			Bold(true)

	styleSectionTitle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

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
	colorHighlight = lipgloss.Color("#3a3533") // clearly raised surface for selection

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
