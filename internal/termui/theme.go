package termui

const (
	ANSIReset = "\033[0m"
	ANSIBold  = "\033[1m"
	ANSIDim   = "\033[2m"

	FGWhite  = "\033[38;5;252m" // bright stone
	FGGray   = "\033[38;5;248m" // warm body text
	FGMuted  = "\033[38;5;240m" // subdued stone
	FGSubtle = "\033[38;5;238m" // faint rules
	FGAccent = "\033[38;5;180m" // dashboard amber
	FGGreen  = "\033[38;5;108m" // sage
	FGYellow = "\033[38;5;179m" // dusty gold
	FGRed    = "\033[38;5;173m" // terracotta

	BGSelected = "\033[48;5;236m"

	HideCursor  = "\033[?25l"
	ShowCursor  = "\033[?25h"
	ClearScreen = "\033[2J\033[H"

	HeaderSeparator = "────────────────────────────────────────────────"
)
