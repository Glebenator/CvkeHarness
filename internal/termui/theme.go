package termui

const (
	ANSIReset = "\033[0m"
	ANSIBold  = "\033[1m"
	ANSIDim   = "\033[2m"

	FGWhite  = "\033[97m"
	FGGray   = "\033[38;5;250m"
	FGMuted  = "\033[38;5;244m"
	FGAccent = "\033[38;5;45m"
	FGGreen  = "\033[38;5;82m"
	FGYellow = "\033[38;5;220m"
	FGRed    = "\033[38;5;196m"

	BGSelected = "\033[48;5;237m"

	HideCursor  = "\033[?25l"
	ShowCursor  = "\033[?25h"
	ClearScreen = "\033[2J\033[H"

	HeaderSeparator = "  ──────────────────────────────────────────────────────"
)
