package termui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// PromptText renders a styled text prompt in cooked mode.
//
// Back navigation rules when canGoBack is true:
// - Typing ":back" always goes back.
// - Pressing Enter with an empty field and no default goes back.
// - Pressing Enter with an empty field that has a default uses the default.
func PromptText(label, defaultVal string, canGoBack bool) (string, bool, error) {
	fmt.Printf("  %s%s%s\n", FGGray, label, ANSIReset)
	if defaultVal != "" {
		fmt.Printf("  %s(default: %s%s%s%s)%s\n",
			FGMuted, FGAccent+ANSIBold, defaultVal, ANSIReset, FGMuted, ANSIReset)
		if canGoBack {
			fmt.Printf("  %stype :back to return to the previous step%s\n", FGMuted+ANSIDim, ANSIReset)
		}
	} else if canGoBack {
		fmt.Printf("  %sleave blank to go back%s\n", FGMuted+ANSIDim, ANSIReset)
	}

	fmt.Printf("\n  %s%s›%s  ", FGAccent, ANSIBold, ANSIReset)
	fmt.Print(FGWhite + ANSIBold)
	NotifyInputRequested(os.Stdout, label, "Response needed in the terminal.")

	reader := bufio.NewReader(os.Stdin)
	val, err := reader.ReadString('\n')
	fmt.Print(ANSIReset)
	if err != nil {
		return "", false, err
	}
	val = strings.TrimSpace(val)

	if canGoBack && val == ":back" {
		return "", true, nil
	}
	if canGoBack && val == "" && defaultVal == "" {
		return "", true, nil
	}
	if val == "" {
		return defaultVal, false, nil
	}
	return val, false, nil
}
