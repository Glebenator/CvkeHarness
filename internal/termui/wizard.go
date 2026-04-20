package termui

import (
	"fmt"
	"strings"
)

// RenderWizardHeader draws a shared full-screen header for setup-style flows.
func RenderWizardHeader(appName, subtitle string) {
	fmt.Print(ClearScreen)
	fmt.Println()
	fmt.Printf("%s%s%s\n", FGAccent+ANSIBold, HeaderSeparator, ANSIReset)
	fmt.Printf("  %s%s◆  %s%s\n", ANSIBold, FGWhite, appName, ANSIReset)
	fmt.Printf("  %s%s%s\n", FGMuted, subtitle, ANSIReset)
	fmt.Printf("%s%s%s\n\n", FGAccent+ANSIBold, HeaderSeparator, ANSIReset)
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
	fmt.Printf("  %sStep %d of %d%s  %s\n", FGGray, step, total, ANSIReset, sb.String())
	fmt.Printf("  %s%s%s%s\n\n", ANSIBold, FGWhite, title, ANSIReset)
}
