package termui

import (
	"fmt"
	"strings"
)

// RenderWizardHeader draws a shared full-screen header for setup-style flows.
func RenderWizardHeader(appName, subtitle string) {
	fmt.Print(ClearScreen)
	fmt.Println()
	fmt.Printf("  %s%s%s\n", ANSIBold+FGWhite, appName, ANSIReset)
	fmt.Printf("  %s%s%s\n", FGMuted, subtitle, ANSIReset)
	fmt.Printf("  %s%s%s\n\n", FGSubtle, HeaderSeparator, ANSIReset)
}

// RenderWizardStep prints a standard step progress row and title.
func RenderWizardStep(step, total int, title string) {
	var sb strings.Builder
	for i := 1; i <= total; i++ {
		switch {
		case i == step:
			sb.WriteString(FGAccent + ANSIBold + "●" + ANSIReset + " ")
		case i < step:
			sb.WriteString(FGGreen + "●" + ANSIReset + " ")
		default:
			sb.WriteString(FGMuted + "○" + ANSIReset + " ")
		}
	}
	fmt.Printf("  %sStep %d of %d%s  %s\n", FGMuted, step, total, ANSIReset, sb.String())
	RenderSectionTitle(title)
	fmt.Println()
}

// RenderSectionTitle prints the amber section heading used throughout the TUI.
func RenderSectionTitle(title string) {
	fmt.Printf("  %s%s%s\n", FGAccent+ANSIBold, strings.TrimSpace(title), ANSIReset)
}

// RenderNote prints a compact dashboard-style note block.
func RenderNote(title, tone string, lines ...string) {
	if strings.TrimSpace(tone) == "" {
		tone = FGAccent
	}
	fmt.Printf("  %s%s%s\n", tone+ANSIBold, strings.TrimSpace(title), ANSIReset)
	if len(lines) == 0 {
		fmt.Printf("  %s│%s\n", FGMuted, ANSIReset)
		return
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			fmt.Println()
			continue
		}
		fmt.Printf("  %s│%s %s\n", FGMuted, ANSIReset, line)
	}
}
