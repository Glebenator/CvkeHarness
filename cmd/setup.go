package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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
	fgGray   = "\033[38;5;250m" // visible on both light and dark terminals
	fgMuted  = "\033[38;5;244m" // secondary text — legible but not dominant
	fgAccent = "\033[38;5;45m"  // electric cyan-blue
	fgGreen  = "\033[38;5;82m"
	fgYellow = "\033[38;5;220m"
	fgRed    = "\033[38;5;196m"

	bgSelected = "\033[48;5;237m" // subtle dark highlight for selected row

	hideCursor  = "\033[?25l"
	showCursor  = "\033[?25h"
	clearScreen = "\033[2J\033[H"

	hSep = "  ──────────────────────────────────────────────────────"
)

// goBack is returned by selectList when the user presses Backspace.
const goBack = -1

// ─── Key events ───────────────────────────────────────────────────────────────

type keyKind int

const (
	kUnknown keyKind = iota
	kUp
	kDown
	kEnter
	kCtrlC
	kBackspace
	kRune
)

type keyEvent struct {
	kind keyKind
	r    rune
}

// nextKey blocks until a key is available. Must be called while in raw mode.
func nextKey() keyEvent {
	buf := make([]byte, 4)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return keyEvent{kind: kUnknown}
	}
	switch {
	case n == 1 && (buf[0] == 3 || buf[0] == 4):
		return keyEvent{kind: kCtrlC}
	case n == 1 && (buf[0] == 13 || buf[0] == 10):
		return keyEvent{kind: kEnter}
	case n == 1 && (buf[0] == 127 || buf[0] == 8):
		return keyEvent{kind: kBackspace}
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A':
		return keyEvent{kind: kUp}
	case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B':
		return keyEvent{kind: kDown}
	case n == 1 && buf[0] >= 32:
		return keyEvent{kind: kRune, r: rune(buf[0])}
	}
	return keyEvent{kind: kUnknown}
}

// ─── UI components ────────────────────────────────────────────────────────────

func renderHeader() {
	fmt.Print(clearScreen)
	fmt.Println()
	fmt.Printf("%s%s%s\n", fgAccent+ansiBold, hSep, ansiReset)
	fmt.Printf("  %s%s◆  C V K E H A R N E S S%s\n", ansiBold, fgWhite, ansiReset)
	fmt.Printf("  %sAI DevOps Agent  ·  Configuration Wizard%s\n", fgMuted, ansiReset)
	fmt.Printf("%s%s%s\n\n", fgAccent+ansiBold, hSep, ansiReset)
}

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
func numberedFallback(items [][2]string, initial int, canGoBack bool) int {
	for i, it := range items {
		mark := "   "
		if i == initial {
			mark = fgAccent + ansiBold + " ▶ " + ansiReset
		}
		fmt.Printf("  %s%s%s%d%s  %-42s  %s%s%s\n",
			mark, ansiBold, fgGray, i+1, ansiReset,
			fgWhite+it[0]+ansiReset,
			ansiDim+fgMuted, it[1], ansiReset)
	}
	hint := fmt.Sprintf("\n  %sEnter number [1–%d] (↵ default %d)", fgGray, len(items), initial+1)
	if canGoBack {
		hint += "  ·  0 to go back"
	}
	fmt.Printf("%s:%s ", hint, ansiReset)
	fmt.Print(fgWhite + ansiBold)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	fmt.Print(ansiReset)
	line = strings.TrimSpace(line)
	if line == "" {
		return initial
	}
	if line == "0" && canGoBack {
		return goBack
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(items) {
		return initial
	}
	return n - 1
}

// selectList renders an arrow-key navigable list and returns the chosen index,
// or goBack (-1) when the user presses Backspace and canGoBack is true.
func selectList(items [][2]string, initial int, canGoBack bool) int {
	selected := initial
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return numberedFallback(items, initial, canGoBack)
	}
	defer term.Restore(fd, oldState)

	fmt.Print(hideCursor)
	defer fmt.Print(showCursor)

	lineCount := len(items) + 2

	buildHint := func() string {
		parts := []string{"↑↓ navigate", "Return select", "^C quit"}
		if canGoBack {
			parts = append([]string{"← Backspace: back"}, parts...)
		}
		return strings.Join(parts, "   ")
	}

	render := func() {
		for i, item := range items {
			fmt.Print("\033[2K\r")
			if i == selected {
				fmt.Printf("  %s%s ▶  %-42s%s  %s%s%s\n",
					bgSelected, fgAccent+ansiBold, item[0], ansiReset,
					bgSelected+fgGray, item[1], ansiReset)
			} else {
				fmt.Printf("     %s%-42s%s  %s%s%s\n",
					fgMuted, item[0], ansiReset,
					ansiDim+fgMuted, item[1], ansiReset)
			}
		}
		fmt.Print("\033[2K\r\n")
		fmt.Print("\033[2K\r")
		fmt.Printf("  %s%s%s\n", fgGray, buildHint(), ansiReset)
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
		case kBackspace:
			if canGoBack {
				return goBack
			}
		case kCtrlC:
			fmt.Print(showCursor)
			term.Restore(fd, oldState)
			fmt.Print(clearScreen)
			fmt.Printf("\n  %s%s  Setup cancelled.%s Goodbye!\n\n", ansiBold, fgYellow, ansiReset)
			os.Exit(0)
		}
		fmt.Printf("\033[%dA", lineCount)
		render()
	}
}

// promptText renders a styled text-input prompt in cooked mode.
//
// Back navigation rules (when canGoBack is true):
//   - Typing ":back" always goes back (works regardless of defaultVal).
//   - Pressing Enter with an empty field and no default goes back.
//   - Pressing Enter with an empty field that has a default uses the default.
func promptText(label, defaultVal string, canGoBack bool) (string, bool) {
	fmt.Printf("  %s%s%s\n", fgGray, label, ansiReset)
	if defaultVal != "" {
		fmt.Printf("  %s(default: %s%s%s%s)%s\n",
			fgMuted, fgAccent+ansiBold, defaultVal, ansiReset, fgMuted, ansiReset)
		if canGoBack {
			fmt.Printf("  %stype :back to return to the previous step%s\n", fgMuted+ansiDim, ansiReset)
		}
	} else if canGoBack {
		fmt.Printf("  %sleave blank to go back%s\n", fgMuted+ansiDim, ansiReset)
	}
	fmt.Printf("\n  %s%s╰▶%s  ", fgAccent, ansiBold, ansiReset)
	fmt.Print(fgWhite + ansiBold)
	reader := bufio.NewReader(os.Stdin)
	val, _ := reader.ReadString('\n')
	fmt.Print(ansiReset)
	val = strings.TrimSpace(val)

	if canGoBack && val == ":back" {
		return "", true
	}
	if canGoBack && val == "" && defaultVal == "" {
		return "", true
	}
	if val == "" {
		return defaultVal, false
	}
	return val, false
}

// maskKey returns a partially masked representation of an API key.
func maskKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	return key[:4] + strings.Repeat("•", 8) + key[len(key)-4:]
}

// renderReview prints a structured summary of cfg.
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
		row("API Key", maskKey(cfg.APIKey))
	}
	row("Max Tokens", strconv.Itoa(cfg.MaxTokens))
	row("Max Iterations", strconv.Itoa(cfg.MaxIterations))
	row("Log Level", cfg.LogLevel)
	fmt.Println(botSep)
}

// ─── OpenRouter model fetching ────────────────────────────────────────────────

type orPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

type orModel struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ContextLength int       `json:"context_length"`
	Pricing       orPricing `json:"pricing"`
}

type orModelsResponse struct {
	Data []orModel `json:"data"`
}

// modelsResult carries the list alongside live/fallback metadata for the status indicator.
type modelsResult struct {
	items     [][2]string
	isLive    bool
	timestamp time.Time
}

// fmtPrice converts a per-token USD string from the API to a $/M display string.
func fmtPrice(s string) string {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return ""
	}
	if v == 0 {
		return "free"
	}
	perM := v * 1_000_000
	if perM < 1 {
		return fmt.Sprintf("$%.3f/M", perM)
	}
	return fmt.Sprintf("$%.2f/M", perM)
}

// fetchOpenRouterModels queries the programming category endpoint sorted by weekly
// usage. Returns a modelsResult with isLive=true if the request succeeded.
func fetchOpenRouterModels() modelsResult {
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get(
		"https://openrouter.ai/api/v1/models?category=programming&order=top-weekly")
	if err != nil {
		return modelsResult{items: openRouterFallbackModels, isLive: false, timestamp: time.Now()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return modelsResult{items: openRouterFallbackModels, isLive: false, timestamp: time.Now()}
	}

	var data orModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return modelsResult{items: openRouterFallbackModels, isLive: false, timestamp: time.Now()}
	}

	seen := map[string]bool{
		"openrouter/auto": true,
		"openrouter/free": true,
	}
	var items [][2]string
	items = append(items, [2]string{"openrouter/auto", "Auto-selected best model        auto"})
	items = append(items, [2]string{"openrouter/free", "Auto-selected free model        free"})

	for _, m := range data.Data {
		if seen[m.ID] || m.ID == "" {
			continue
		}
		seen[m.ID] = true

		in := fmtPrice(m.Pricing.Prompt)
		out := fmtPrice(m.Pricing.Completion)

		var desc string
		switch {
		case in == "free" && out == "free":
			desc = fmt.Sprintf("%-30s  free", m.Name)
		case in != "" && out != "":
			desc = fmt.Sprintf("%-30s  in %s  out %s", m.Name, in, out)
		default:
			desc = m.Name
		}
		items = append(items, [2]string{m.ID, desc})
		if len(items) >= 22 {
			break
		}
	}

	if len(items) == 0 {
		return modelsResult{items: openRouterFallbackModels, isLive: false, timestamp: time.Now()}
	}
	items = append(items, [2]string{"[ custom model ]", "Enter your own model ID →"})
	return modelsResult{items: items, isLive: true, timestamp: time.Now()}
}

type lmModel struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type lmModelsResponse struct {
	Data []lmModel `json:"data"`
}

func fetchLMStudioModels(baseURL string) modelsResult {
	if baseURL == "" {
		baseURL = "http://localhost:1234/v1"
	}

	fetchURL := baseURL + "/models"
	if strings.HasSuffix(baseURL, "/v1") {
		fetchURL = strings.TrimSuffix(baseURL, "/v1") + "/api/v0/models"
	}

	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(fetchURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		// Fallback to /v1/models if /api/v0/models fails
		if fetchURL != baseURL+"/models" {
			resp, err = client.Get(baseURL + "/models")
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			return modelsResult{items: lmStudioModels, isLive: false, timestamp: time.Now()}
		}
	}
	defer resp.Body.Close()

	var data lmModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return modelsResult{items: lmStudioModels, isLive: false, timestamp: time.Now()}
	}

	var loadedItems [][2]string
	var availableItems [][2]string

	for _, m := range data.Data {
		if m.ID == "" {
			continue
		}
		if m.State == "loaded" {
			loadedItems = append(loadedItems, [2]string{m.ID, "Loaded model  ★"})
		} else {
			availableItems = append(availableItems, [2]string{m.ID, "Available (not loaded)"})
		}
	}

	var items [][2]string
	items = append(items, loadedItems...)
	items = append(items, availableItems...)

	if len(items) == 0 {
		return modelsResult{items: lmStudioModels, isLive: false, timestamp: time.Now()}
	}
	items = append(items, [2]string{"[ custom model ]", "Enter your own model ID →"})
	return modelsResult{items: items, isLive: true, timestamp: time.Now()}
}

// renderModelStatus prints the live/fallback indicator for the model step.
func renderModelStatus(r modelsResult) {
	if r.isLive {
		fmt.Printf("  %s⬤ LIVE%s  top-weekly · programming  ·  retrieved %s%s\n\n",
			fgGreen+ansiBold, ansiReset+fgGray, r.timestamp.Format("15:04 UTC")+ansiReset, ansiReset)
	} else {
		fmt.Printf("  %s⊙ offline fallback%s  ·  snapshot accurate as of 2026-04-18%s\n\n",
			fgYellow+ansiDim, ansiReset+fgMuted, ansiReset)
	}
}

// ─── OpenRouter key validation ────────────────────────────────────────────────

type orKeyResponse struct {
	Data struct {
		Label string `json:"label"`
	} `json:"data"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// validateOpenRouterKey calls the /auth/key endpoint and returns the key label,
// or a descriptive error if the key is invalid or the request fails.
func validateOpenRouterKey(key string) (label string, err error) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "https://openrouter.ai/api/v1/auth/key", nil)
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connection failed: %v", err)
	}
	defer resp.Body.Close()

	var out orKeyResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)

	if resp.StatusCode == 401 {
		msg := out.Error.Message
		if msg == "" {
			msg = "unauthorized"
		}
		return "", fmt.Errorf("invalid key — %s", msg)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("unexpected status %d from validation endpoint", resp.StatusCode)
	}
	return out.Data.Label, nil
}

// ─── Wizard data ──────────────────────────────────────────────────────────────

// openRouterFallbackModels mirrors the top-weekly programming ranking as of 2026-04-18.
var openRouterFallbackModels = [][2]string{
	{"openrouter/auto",                        "Auto-selected best model         auto"},
	{"openrouter/free",                        "Auto-selected free model         free"},
	{"minimax/minimax-m2.7",                   "MiniMax M2.7                     in $0.00/M  out $0.00/M"},
	{"anthropic/claude-opus-4.6",              "Anthropic Claude Opus 4.6        in $5.00/M  out $25.00/M"},
	{"anthropic/claude-sonnet-4.6",            "Anthropic Claude Sonnet 4.6      in $3.00/M  out $15.00/M"},
	{"google/gemini-3.1-pro-preview",          "Google Gemini 3.1 Pro            in $2.00/M  out $12.00/M"},
	{"openai/gpt-5.4",                         "OpenAI GPT-5.4                   in $2.50/M  out $15.00/M"},
	{"openai/gpt-5.3-codex",                   "OpenAI GPT-5.3-Codex             in $1.75/M  out $14.00/M"},
	{"google/gemini-3-flash-preview",          "Google Gemini 3 Flash            in $0.50/M  out $3.00/M"},
	{"deepseek/deepseek-v3.2",                 "DeepSeek V3.2                    in $0.26/M  out $0.42/M"},
	{"google/gemini-2.5-flash",                "Google Gemini 2.5 Flash          in $0.30/M  out $2.50/M"},
	{"anthropic/claude-haiku-4.5",             "Anthropic Claude Haiku 4.5       in $1.00/M  out $5.00/M"},
	{"openai/gpt-5.4-nano",                    "OpenAI GPT-5.4 Nano              in $0.20/M  out $1.25/M"},
	{"nvidia/nemotron-3-super-120b-a12b:free", "NVIDIA Nemotron 3 Super          free"},
	{"[ custom model ]",                       "Enter your own model ID →"},
}

var lmStudioModels = [][2]string{
	{"local-model",      "Use currently loaded model  ★"},
	{"[ custom model ]", "Enter your model identifier →"},
}

var maxTokenOptions = [][2]string{
	{"1024",       "Short · fast responses"},
	{"2048",       "Compact responses"},
	{"4096",       "Standard · recommended  ★"},
	{"8192",       "Extended responses"},
	{"16384",      "Maximum context window"},
	{"[ custom ]", "Enter a specific token count →"},
}

var logLevelOptions = [][2]string{
	{"off",   "Silent · only agent output shown  ★"},
	{"error", "Critical errors only"},
	{"warn",  "Warnings and errors"},
	{"info",  "Standard structured logging"},
	{"debug", "Verbose · all internal events"},
}

const totalSteps = 6

// ─── Wizard steps ─────────────────────────────────────────────────────────────
// Each function returns true to advance, false to go back.

func wizardProvider(cfg *config.Config) bool {
	renderHeader()
	renderStep(1, totalSteps, "Choose your LLM Provider")

	initial := 0
	if cfg.Provider == "lmstudio" {
		initial = 1
	}
	providers := [][2]string{
		{"openrouter", "Cloud API  ·  many models  ·  requires API key"},
		{"lmstudio",   "Local inference  ·  no key needed  ·  offline-capable"},
	}
	idx := selectList(providers, initial, false) // step 1 has no back
	if idx == goBack {
		return false
	}
	cfg.Provider = providers[idx][0]
	return true
}

// wizardAPIKey is step 2 — dispatches to the correct credential flow.
// savedKey is the key that was saved in the config before this wizard run
// (used as a fallback for the reuse offer if cfg.APIKey was cleared by a
// previous provider switch and the user now switches back to openrouter).
func wizardAPIKey(cfg *config.Config, savedKey string) bool {
	if cfg.Provider == "openrouter" {
		return wizardOpenRouterKey(cfg, savedKey)
	}
	return wizardLMStudioURL(cfg)
}

func wizardOpenRouterKey(cfg *config.Config, savedKey string) bool {
	// Determine which key (if any) to offer for reuse.
	// Prefer the key the wizard already has in cfg (may be a newly validated one
	// from an earlier pass through this step); fall back to the originally saved key.
	keyToOffer := cfg.APIKey
	if keyToOffer == "" {
		keyToOffer = savedKey
	}

	showReuseMenu := keyToOffer != "" // show the "reuse / enter new" select on first draw

	for {
		renderHeader()
		renderStep(2, totalSteps, "OpenRouter API Key")

		if showReuseMenu {
			fmt.Printf("  %sA key from a previous setup was found.%s\n\n", fgGray, ansiReset)
			choices := [][2]string{
				{"Reuse existing key", maskKey(keyToOffer)},
				{"Enter a different key", "generate or paste a new one"},
			}
			idx := selectList(choices, 0, true)
			switch idx {
			case goBack:
				return false
			case 0: // reuse — skip validation (was valid before)
				cfg.APIKey = keyToOffer
				cfg.BaseURL = ""
				return true
			}
			// case 1: user wants to enter a new key — fall through
			showReuseMenu = false
		}

		// ── Text input for a new key ──────────────────────────────────────
		fmt.Printf("  %sStored locally in %s~/.cvkeharness/config.yaml%s\n",
			fgGray, fgAccent+ansiBold, ansiReset)
		fmt.Printf("  %sGet a key at: %shttps://openrouter.ai/keys%s\n\n",
			fgGray, fgAccent, ansiReset)

		key, back := promptText("Paste your API key:", "", true)
		if back {
			// If reuse was available, go back to the reuse menu; otherwise, back to step 1.
			if keyToOffer != "" {
				showReuseMenu = true
				continue
			}
			return false
		}

		// ── Validate ─────────────────────────────────────────────────────
		renderHeader()
		renderStep(2, totalSteps, "OpenRouter API Key")
		fmt.Printf("\n  %s%s⟳  Validating key …%s\n", ansiDim, fgGray, ansiReset)

		label, err := validateOpenRouterKey(key)
		if err != nil {
			renderHeader()
			renderStep(2, totalSteps, "OpenRouter API Key")
			fmt.Printf("\n  %s%s✗  %v%s\n\n", ansiBold, fgRed, err, ansiReset)

			retry := [][2]string{
				{"Try a different key", "paste or type another key"},
				{"← Return to provider selection", ""},
			}
			if selectList(retry, 0, false) == 1 {
				return false
			}
			// Retry — loop back to text input.
			continue
		}

		// ── Success ───────────────────────────────────────────────────────
		cfg.APIKey = key
		cfg.BaseURL = ""
		renderHeader()
		renderStep(2, totalSteps, "OpenRouter API Key")
		if label != "" {
			fmt.Printf("\n  %s%s✔  Key valid%s  ·  %s%s%s\n",
				ansiBold, fgGreen, ansiReset, fgGray, label, ansiReset)
		} else {
			fmt.Printf("\n  %s%s✔  Key validated successfully%s\n",
				ansiBold, fgGreen, ansiReset)
		}
		time.Sleep(900 * time.Millisecond)
		return true
	}
}

func wizardLMStudioURL(cfg *config.Config) bool {
	renderHeader()
	renderStep(2, totalSteps, "LM Studio Connection")
	
	defaultURL := "http://localhost:1234/v1"
	client := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Get(defaultURL + "/models")
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		fmt.Printf("  %sEnsure LM Studio is running and the local server is active.%s\n", fgGray, ansiReset)
		fmt.Printf("  %s✔ Found local server running at %s%s\n\n", fgGreen+ansiBold, defaultURL, ansiReset)

		choices := [][2]string{
			{"Use this local server", defaultURL},
			{"Enter a different URL", ""},
		}
		idx := selectList(choices, 0, true)
		if idx == goBack {
			return false
		}
		if idx == 0 {
			cfg.BaseURL = defaultURL
			cfg.APIKey = ""
			return true
		}
		renderHeader()
		renderStep(2, totalSteps, "LM Studio Connection")
	}

	fmt.Printf("  %sEnsure LM Studio is running and the local server is active.%s\n",
		fgGray, ansiReset)
	fmt.Printf("  %sDefault port is %s1234%s — change only if you modified LM Studio settings.%s\n\n",
		fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)

	url, back := promptText("LM Studio base URL:", defaultURL, true)
	if back {
		return false
	}
	cfg.BaseURL = url
	cfg.APIKey = ""
	return true
}

func wizardModel(cfg *config.Config, result modelsResult) bool {
	renderHeader()
	renderStep(3, totalSteps, "Select a Model")

	var items [][2]string
	if cfg.Provider == "lmstudio" {
		res := fetchLMStudioModels(cfg.BaseURL)
		items = res.items
		if res.isLive {
			loadedCount := 0
			for _, item := range items {
				if strings.Contains(item[1], "Loaded model") {
					loadedCount++
				}
			}
			fmt.Printf("  %s✔  Found %d loaded / %d total models on local server%s\n\n", fgGreen+ansiBold, loadedCount, len(items)-1, ansiReset)
		} else {
			fmt.Printf("  %s⊙  Could not verify loaded models on local server%s\n\n", fgYellow+ansiDim, ansiReset)
			cfg.Model = "local-model"
		}
	} else {
		items = result.items
		renderModelStatus(result)
	}

	initial := 0
	for i, it := range items {
		if it[0] == cfg.Model {
			initial = i
			break
		}
	}

	idx := selectList(items, initial, true)
	if idx == goBack {
		return false
	}

	if items[idx][0] == "[ custom model ]" {
		renderHeader()
		renderStep(3, totalSteps, "Custom Model Identifier")
		fmt.Printf("  %sEnter the exact model ID used by your provider.%s\n", fgGray, ansiReset)
		fmt.Printf("  %sExample: %santhroptic/claude-sonnet-4.6%s\n\n", fgGray, fgAccent+ansiBold, ansiReset)
		val, back := promptText("Model ID:", cfg.Model, true)
		if back {
			return false
		}
		cfg.Model = val
	} else {
		cfg.Model = items[idx][0]
	}
	return true
}

func wizardTokens(cfg *config.Config) bool {
	renderHeader()
	renderStep(4, totalSteps, "Max Response Length  (Tokens)")

	initial := 2 // 4096 by default
	for i, it := range maxTokenOptions {
		if it[0] == strconv.Itoa(cfg.MaxTokens) {
			initial = i
			break
		}
	}

	idx := selectList(maxTokenOptions, initial, true)
	if idx == goBack {
		return false
	}

	if maxTokenOptions[idx][0] == "[ custom ]" {
		renderHeader()
		renderStep(4, totalSteps, "Custom Token Limit")
		fmt.Printf("  %sEnter any positive integer.%s\n\n", fgGray, ansiReset)
		raw, back := promptText("Max tokens:", strconv.Itoa(cfg.MaxTokens), true)
		if back {
			return false
		}
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			cfg.MaxTokens = n
		}
	} else {
		val, _ := strconv.Atoi(maxTokenOptions[idx][0])
		cfg.MaxTokens = val
	}
	return true
}

func wizardLogLevel(cfg *config.Config) bool {
	renderHeader()
	renderStep(5, totalSteps, "Log Verbosity")

	initial := 0
	for i, it := range logLevelOptions {
		if it[0] == cfg.LogLevel {
			initial = i
			break
		}
	}

	idx := selectList(logLevelOptions, initial, true)
	if idx == goBack {
		return false
	}
	cfg.LogLevel = logLevelOptions[idx][0]
	return true
}

func wizardConfirm(cfg *config.Config) bool {
	renderHeader()
	renderStep(6, totalSteps, "Review & Confirm")

	renderReview(cfg)
	fmt.Println()

	choices := [][2]string{
		{"✔  Save and finish", "Write config — ready to run"},
		{"✗  Cancel",          "Exit without saving changes"},
	}
	idx := selectList(choices, 0, true)
	if idx == goBack {
		return false
	}
	if idx == 1 {
		fmt.Print(clearScreen)
		fmt.Printf("\n  %s%s  Setup cancelled.%s No changes were saved.\n\n",
			ansiBold, fgYellow, ansiReset)
		os.Exit(0)
	}
	return true
}

// ─── Command ──────────────────────────────────────────────────────────────────

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive onboarding wizard to configure the agent",
	Run: func(cmd *cobra.Command, args []string) {
		// Load any previously saved configuration so we can:
		//   (a) pre-populate selections with the user's current values, and
		//   (b) offer to reuse their existing API key without re-typing it.
		existingCfg, _ := config.LoadConfig()
		cfg := config.DefaultConfig()
		savedKey := "" // the API key from the saved config — kept separate so a
		// provider switch mid-wizard doesn't permanently lose it.

		if existingCfg != nil {
			if existingCfg.Provider != "" {
				cfg.Provider = existingCfg.Provider
			}
			if existingCfg.Model != "" {
				cfg.Model = existingCfg.Model
			}
			if existingCfg.APIKey != "" {
				cfg.APIKey = existingCfg.APIKey
				savedKey = existingCfg.APIKey
			}
			if existingCfg.BaseURL != "" {
				cfg.BaseURL = existingCfg.BaseURL
			}
			if existingCfg.MaxTokens > 0 {
				cfg.MaxTokens = existingCfg.MaxTokens
			}
			if existingCfg.MaxIterations > 0 {
				cfg.MaxIterations = existingCfg.MaxIterations
			}
			if existingCfg.LogLevel != "" {
				cfg.LogLevel = existingCfg.LogLevel
			}
		}

		// Begin fetching the model list concurrently so it's ready by step 3.
		modelsCh := make(chan modelsResult, 1)
		go func() { modelsCh <- fetchOpenRouterModels() }()

		var fetchedModels modelsResult // populated on first visit to step 3

		// ── Step loop with Backspace back-navigation ──────────────────────
		step := 1
		for step >= 1 && step <= totalSteps {
			var advanced bool
			switch step {
			case 1:
				advanced = wizardProvider(cfg)
			case 2:
				// Step 2: API key (OpenRouter) or base URL (LM Studio).
				advanced = wizardAPIKey(cfg, savedKey)
			case 3:
				// Ensure the model list is ready before rendering.
				if fetchedModels.items == nil {
					select {
					case fetchedModels = <-modelsCh:
					case <-time.After(100 * time.Millisecond):
						renderHeader()
						renderStep(3, totalSteps, "Select a Model")
						fmt.Printf("  %s%s⟳  Fetching latest models …%s\n", ansiDim, fgGray, ansiReset)
						fetchedModels = <-modelsCh
					}
				}
				advanced = wizardModel(cfg, fetchedModels)
			case 4:
				advanced = wizardTokens(cfg)
			case 5:
				advanced = wizardLogLevel(cfg)
			case 6:
				advanced = wizardConfirm(cfg)
			}

			if advanced {
				step++
			} else if step > 1 {
				step--
			}
			// step == 1 and back pressed → stays at 1 (no previous step)
		}

		if err := cfg.Save(); err != nil {
			fmt.Printf("\n  %s%s✗  Failed to save configuration: %v%s\n\n",
				ansiBold, fgRed, err, ansiReset)
			os.Exit(1)
		}

		// ── Success banner ─────────────────────────────────────────────────
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
