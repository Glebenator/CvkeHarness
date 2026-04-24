package termui

const (
	ANSIReset = "\033[0m"
	ANSIBold  = "\033[1m"
	ANSIDim   = "\033[2m"

	FGWhite  = "\033[38;5;252m"
	FGGray   = "\033[38;5;247m"
	FGMuted  = "\033[38;5;240m"
	FGAccent = "\033[38;5;250m"
	FGGreen  = "\033[38;5;108m"
	FGYellow = "\033[38;5;180m"
	FGRed    = "\033[38;5;167m"

	BGSelected = "\033[48;5;236m"

	HideCursor  = "\033[?25l"
	ShowCursor  = "\033[?25h"
	ClearScreen = "\033[2J\033[H"

	HeaderSeparator = "  ───────────────────────────────────────────────"
)
