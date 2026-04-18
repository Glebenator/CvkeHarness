package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/coolcake/cvkeharness/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ─── ANSI styling ─────────────────────────────────────────────────────────────

const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"

	fgWhite  = "\033[97m"
	fgGray   = "\033[38;5;246m"
	fgMuted  = "\033[38;5;240m"
	fgAccent = "\033[38;5;45m"  // electric cyan-blue
	fgGreen  = "\033[38;5;82m"
	fgYellow = "\033[38;5;220m"
	fgRed    = "\033[38;5;196m"

	bgSelected = "\033[48;5;237m" // subtle dark highlight for selected row

	hideCursor  = "\033[?25l"
	showCursor  = "\033[?25h"
	clearScreen = "\033[2J\033[H"

	// Shared header separator line (pre-indented)
	hSep = "  ──────────────────────────────────────────────────────"
)

// ─── Key event reading ────────────────────────────────────────────────────────

type keyKind int

const (
	kUnknown keyKind = iota
	kUp
	kDown
	kEnter
	kCtrlC
	kRune
)

type keyEvent struct {
	kind keyKind
	r    rune
}

// nextKey blocks until a key event is available and returns its classification.
// Must be called while the terminal is in raw mode.
func nextKey() keyEvent {
	buf := make([]byte, 4)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return keyEvent{kind: kUnknown}
	}
	switch {
	case n == 1 && (buf[0] == 3 || buf[0] == 4): // Ctrl+C / Ctrl+D
		return keyEvent{kind: kCtrlC}
	case n == 1 && (buf[0] == 13 || buf[0] == 10): // CR / LF
		return keyEvent{kind: kEnter}
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A': // ESC [ A
		return keyEvent{kind: kUp}
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B': // ESC [ B
		return keyEvent{kind: kDown}
	case n == 1 && buf[0] >= 32: // printable ASCII
		return keyEvent{kind: kRune, r: rune(buf[0])}
	}
	return keyEvent{kind: kUnknown}
}

// ─── UI components ────────────────────────────────────────────────────────────

// renderHeader clears the screen and draws the CvkeHarness banner.
func renderHeader() {
	fmt.Print(clearScreen)
	fmt.Println()
	fmt.Printf("%s%s%s\n", fgAccent+ansiBold, hSep, ansiReset)
	fmt.Printf("  %s%s◆  C V K E H A R N E S S%s\n", ansiBold, fgWhite, ansiReset)
	fmt.Printf("  %sAI DevOps Agent  ·  Configuration Wizard%s\n", fgGray, ansiReset)
	fmt.Printf("%s%s%s\n\n", fgAccent+ansiBold, hSep, ansiReset)
}

// renderStep prints the step indicator dots and the step title.
func renderStep(step, total int, title string) {
	var sb strings.Builder
	for i := 1; i <= total; i++ {
		switch {
		case i == step:
			sb.WriteString(fgAccent + ansiBold + "●" + ansiReset + " ")
		case i < step:
			sb.WriteString(fgGreen + "●" + ansiReset + " ")
		default:
			sb.WriteString(fgMuted + "○" + ansiReset + " ")
		}
	}
	fmt.Printf("  %sStep %d of %d%s  %s\n", fgGray, step, total, ansiReset, sb.String())
	fmt.Printf("  %s%s%s%s\n\n", ansiBold, fgWhite, title, ansiReset)
}

// numberedFallback is used when the terminal does not support raw mode.
// It renders a simple numbered menu for selection.
func numberedFallback(items [][2]string, initial int) int {
	for i, it := range items {
		mark := "   "
		if i == initial {
			mark = fgAccent + ansiBold + " ▶ " + ansiReset
		}
		fmt.Printf("  %s%s%s%d%s  %-40s  %s%s%s\n",
			mark,
			ansiBold, fgGray, i+1, ansiReset,
			fgWhite+it[0]+ansiReset,
			ansiDim+fgMuted, it[1], ansiReset)
	}
	fmt.Printf("\n  %sEnter number [1–%d] (↵ for default %d):%s ",
		fgGray, len(items), initial+1, ansiReset)
	fmt.Print(fgWhite + ansiBold)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	fmt.Print(ansiReset)
	line = strings.TrimSpace(line)
	if line == "" {
		return initial
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(items) {
		return initial
	}
	return n - 1
}

// selectList renders an arrow-key navigable list and returns the chosen index.
// Falls back to a numbered menu if raw mode is unavailable.
func selectList(items [][2]string, initial int) int {
	selected := initial

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return numberedFallback(items, initial)
	}
	defer term.Restore(fd, oldState)

	fmt.Print(hideCursor)
	defer fmt.Print(showCursor)

	lineCount := len(items) + 2 // items + blank + hint

	render := func() {
		for i, item := range items {
			fmt.Print("\033[2K\r")
			if i == selected {
				fmt.Printf("  %s%s ▶  %-40s%s  %s%s%s\n",
					bgSelected, fgAccent+ansiBold, item[0], ansiReset,
					bgSelected+ansiDim+fgGray, item[1], ansiReset)
			} else {
				fmt.Printf("     %s%-40s%s  %s%s%s\n",
					fgMuted, item[0], ansiReset,
					ansiDim+fgMuted, item[1], ansiReset)
			}
		}
		fmt.Print("\033[2K\r\n") // blank separator
		fmt.Print("\033[2K\r")
		fmt.Printf("  %s%s↑↓ navigate   Return select   ^C quit%s\n",
			ansiDim, fgMuted, ansiReset)
	}

	render()

	for {
		ev := nextKey()
		switch ev.kind {
		case kUp:
			if selected > 0 {
				selected--
			}
		case kDown:
			if selected < len(items)-1 {
				selected++
			}
		case kEnter:
			return selected
		case kCtrlC:
			fmt.Print(showCursor)
			term.Restore(fd, oldState)
			fmt.Print(clearScreen)
			fmt.Printf("\n  %s%s  Setup cancelled.%s Goodbye!\n\n", ansiBold, fgYellow, ansiReset)
			os.Exit(0)
		}
		// Move cursor back to top of list and redraw.
		fmt.Printf("\033[%dA", lineCount)
		render()
	}
}

// promptText renders a styled single-line text input.
// If defaultVal is non-empty, pressing Enter without typing returns it.
func promptText(label, defaultVal string) string {
	fmt.Printf("  %s%s%s\n", fgGray, label, ansiReset)
	if defaultVal != "" {
		fmt.Printf("  %s(default: %s%s%s%s)%s\n",
			fgMuted, fgAccent+ansiBold, defaultVal, ansiReset, fgMuted, ansiReset)
	}
	fmt.Printf("\n  %s%s╰▶%s  ", fgAccent, ansiBold, ansiReset)
	fmt.Print(fgWhite + ansiBold)
	reader := bufio.NewReader(os.Stdin)
	val, _ := reader.ReadString('\n')
	fmt.Print(ansiReset)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	return val
}

// renderReview prints a structured summary of the current configuration.
func renderReview(cfg *config.Config) {
	topSep := "  " + fgGray + ansiBold + "┌" + strings.Repeat("─", 52) + ansiReset
	botSep := "  " + fgGray + ansiBold + "└" + strings.Repeat("─", 52) + ansiReset
	row := func(label, value string) {
		fmt.Printf("  %s%s│%s  %s%-16s%s  %s%s%s\n",
			ansiBold, fgGray, ansiReset,
			fgMuted, label, ansiReset,
			ansiBold, fgWhite, value+ansiReset)
	}
	fmt.Println(topSep)
	row("Provider", cfg.Provider)
	row("Model", cfg.Model)
	if cfg.BaseURL != "" {
		row("Base URL", cfg.BaseURL)
	}
	if cfg.APIKey != "" {
		// Show first 4 + masked + last 4 for a quick sanity check.
		masked := cfg.APIKey
		if len(masked) > 8 {
			masked = masked[:4] + strings.Repeat("•", 8) + masked[len(masked)-4:]
		}
		row("API Key", masked)
	}
	row("Max Tokens", strconv.Itoa(cfg.MaxTokens))
	row("Max Iterations", strconv.Itoa(cfg.MaxIterations))
	row("Log Level", cfg.LogLevel)
	fmt.Println(botSep)
}

// ─── Wizard data ──────────────────────────────────────────────────────────────

var providerOptions = [][2]string{
	{"openrouter", "Cloud API · access to many models · requires API key"},
	{"lmstudio",   "Local inference · no key needed · offline-capable"},
}

var openRouterModels = [][2]string{
	{"anthropic/claude-3.5-sonnet",          "Best reasoning & tool use  ★"},
	{"anthropic/claude-3-haiku",              "Fast & cost-efficient"},
	{"google/gemini-flash-1.5",               "Google · very fast"},
	{"google/gemini-pro-1.5",                 "Google · high quality"},
	{"openai/gpt-4o",                         "OpenAI flagship"},
	{"openai/gpt-4o-mini",                    "OpenAI · efficient"},
	{"meta-llama/llama-3.1-8b-instruct:free", "Free tier · no cost"},
	{"[ custom model ]",                      "Enter your own model ID →"},
}

var lmStudioModels = [][2]string{
	{"local-model",     "Use currently loaded model  ★"},
	{"[ custom model ]", "Enter your model identifier →"},
}

var maxTokenOptions = [][2]string{
	{"1024",  "Short · fast responses"},
	{"2048",  "Compact responses"},
	{"4096",  "Standard · recommended  ★"},
	{"8192",  "Extended responses"},
	{"16384", "Maximum context window"},
}

const totalSteps = 5

// ─── Wizard steps ─────────────────────────────────────────────────────────────

func wizardProvider(cfg *config.Config) {
	renderHeader()
	renderStep(1, totalSteps, "Choose your LLM Provider")

	initial := 0
	if cfg.Provider == "lmstudio" {
		initial = 1
	}
	idx := selectList(providerOptions, initial)
	cfg.Provider = providerOptions[idx][0]
}

func wizardModel(cfg *config.Config) {
	renderHeader()
	renderStep(2, totalSteps, "Select a Model")

	var items [][2]string
	if cfg.Provider == "lmstudio" {
		items = lmStudioModels
		cfg.Model = "local-model"
	} else {
		items = openRouterModels
	}

	// Pre-select the current model if it exists in the list.
	initial := 0
	for i, it := range items {
		if it[0] == cfg.Model {
			initial = i
			break
		}
	}

	idx := selectList(items, initial)

	if items[idx][0] == "[ custom model ]" {
		renderHeader()
		renderStep(2, totalSteps, "Custom Model Identifier")
		fmt.Printf("  %sEnter the exact model ID used by your provider.%s\n", fgGray, ansiReset)
		fmt.Printf("  %sExample: %santhropic/claude-3.5-sonnet%s\n", fgGray, fgAccent+ansiBold, ansiReset)
		fmt.Println()
		cfg.Model = promptText("Model ID:", cfg.Model)
	} else {
		cfg.Model = items[idx][0]
	}
}

func wizardCredentials(cfg *config.Config) {
	renderHeader()

	if cfg.Provider == "openrouter" {
		renderStep(3, totalSteps, "OpenRouter API Key")
		fmt.Printf("  %sStored locally in %s~/.cvkeharness/config.yaml%s  (never sent elsewhere)\n",
			fgGray, fgAccent+ansiBold, ansiReset)
		fmt.Printf("  %sGet a key at: %shttps://openrouter.ai/keys%s\n",
			fgGray, fgAccent, ansiReset)
		fmt.Println()

		key := promptText("Paste your API key:", "")
		if strings.TrimSpace(key) == "" {
			fmt.Printf("\n  %s%s✗  An API key is required for OpenRouter.%s\n\n", ansiBold, fgRed, ansiReset)
			os.Exit(1)
		}
		cfg.APIKey = key
		cfg.BaseURL = "" // clear any stale LM Studio URL
	} else {
		renderStep(3, totalSteps, "LM Studio Connection")
		fmt.Printf("  %sEnsure LM Studio is running and the local server is started.%s\n",
			fgGray, ansiReset)
		fmt.Printf("  %sDefault port is %s1234%s — change only if you modified LM Studio settings.%s\n",
			fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
		fmt.Println()

		cfg.BaseURL = promptText("LM Studio base URL:", "http://localhost:1234/v1")
		if cfg.BaseURL == "" {
			cfg.BaseURL = "http://localhost:1234/v1"
		}
		cfg.APIKey = "" // no key required for local inference
	}
}

func wizardTokens(cfg *config.Config) {
	renderHeader()
	renderStep(4, totalSteps, "Max Response Length  (Tokens)")

	initial := 2 // points to 4096 by default
	for i, it := range maxTokenOptions {
		if it[0] == strconv.Itoa(cfg.MaxTokens) {
			initial = i
			break
		}
	}

	idx := selectList(maxTokenOptions, initial)
	val, _ := strconv.Atoi(maxTokenOptions[idx][0])
	cfg.MaxTokens = val
}

func wizardConfirm(cfg *config.Config) {
	renderHeader()
	renderStep(5, totalSteps, "Review & Confirm")

	renderReview(cfg)
	fmt.Println()

	choices := [][2]string{
		{"✔  Save and finish", "Write config · ready to run"},
		{"✗  Cancel",          "Exit without saving changes"},
	}
	idx := selectList(choices, 0)
	if idx == 1 {
		fmt.Print(clearScreen)
		fmt.Printf("\n  %s%s  Setup cancelled.%s No changes were saved.\n\n",
			ansiBold, fgYellow, ansiReset)
		os.Exit(0)
	}
}

// ─── Command ──────────────────────────────────────────────────────────────────

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive onboarding wizard to configure the agent",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.DefaultConfig()

		wizardProvider(cfg)
		wizardModel(cfg)
		wizardCredentials(cfg)
		wizardTokens(cfg)
		wizardConfirm(cfg)

		if err := cfg.Save(); err != nil {
			fmt.Printf("\n  %s%s✗  Failed to save configuration: %v%s\n\n",
				ansiBold, fgRed, err, ansiReset)
			os.Exit(1)
		}

		// ─── Success banner ───────────────────────────────────────────────
		fmt.Print(clearScreen)
		fmt.Println()
		fmt.Printf("%s%s%s\n", fgGreen+ansiBold, hSep, ansiReset)
		fmt.Printf("  %s%s✔  Setup complete!%s Configuration saved.\n", ansiBold, fgGreen, ansiReset)
		fmt.Printf("%s%s%s\n\n", fgGreen+ansiBold, hSep, ansiReset)

		renderReview(cfg)

		fmt.Println()
		fmt.Printf("  %sYou're all set. Try running:%s\n\n", fgGray, ansiReset)
		fmt.Printf("  %s%s  cvkeharness run \"list all running docker containers\"%s\n\n",
			ansiBold, fgAccent, ansiReset)
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
