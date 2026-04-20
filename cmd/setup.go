package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

// ─── ANSI styling ─────────────────────────────────────────────────────────────

const (
	ansiReset = termui.ANSIReset
	ansiBold  = termui.ANSIBold
	ansiDim   = termui.ANSIDim

	fgWhite  = termui.FGWhite
	fgGray   = termui.FGGray
	fgMuted  = termui.FGMuted
	fgAccent = termui.FGAccent
	fgGreen  = termui.FGGreen
	fgYellow = termui.FGYellow
	fgRed    = termui.FGRed

	clearScreen = termui.ClearScreen
	hSep        = termui.HeaderSeparator
)

// goBack is returned by selectList when the user presses Backspace.
const goBack = termui.GoBack

// ─── UI components ────────────────────────────────────────────────────────────

func renderHeader() {
	termui.RenderWizardHeader("C V K E H A R N E S S", "AI DevOps Agent  ·  Configuration Wizard")
}

func renderStep(step, total int, title string) {
	termui.RenderWizardStep(step, total, title)
}

// selectList renders an arrow-key navigable list and returns the chosen index,
// or goBack (-1) when the user presses Backspace and canGoBack is true.
func selectList(items [][2]string, initial int, canGoBack bool) int {
	listItems := make([]termui.ListItem, 0, len(items))
	for _, item := range items {
		listItems = append(listItems, termui.ListItem{Label: item[0], Description: item[1]})
	}
	idx, err := termui.SelectList(listItems, initial, canGoBack)
	if err == termui.ErrInterrupted {
		fmt.Print(clearScreen)
		fmt.Printf("\n  %s%s  Setup cancelled.%s Goodbye!\n\n", ansiBold, fgYellow, ansiReset)
		os.Exit(0)
	}
	if err != nil {
		return initial
	}
	return idx
}

// promptText renders a styled text-input prompt in cooked mode.
//
// Back navigation rules (when canGoBack is true):
//   - Typing ":back" always goes back (works regardless of defaultVal).
//   - Pressing Enter with an empty field and no default goes back.
//   - Pressing Enter with an empty field that has a default uses the default.
func promptText(label, defaultVal string, canGoBack bool) (string, bool) {
	val, back, err := termui.PromptText(label, defaultVal, canGoBack)
	if err != nil {
		return defaultVal, false
	}
	return val, back
}

func optionIndexByValue(items [][2]string, value string) int {
	for i, item := range items {
		if item[0] == value {
			return i
		}
	}
	return 0
}

func promptCustomValue(step int, title, label, defaultValue string, intro ...string) (string, bool) {
	renderHeader()
	renderStep(step, totalSteps, title)
	for _, line := range intro {
		if strings.TrimSpace(line) == "" {
			fmt.Println()
			continue
		}
		fmt.Println(line)
	}
	if len(intro) > 0 {
		fmt.Println()
	}
	return promptText(label, defaultValue, true)
}

func chooseNumericOption(step int, title, customTitle, promptLabel string, options [][2]string, current int) (int, bool) {
	renderHeader()
	renderStep(step, totalSteps, title)

	idx := selectList(options, optionIndexByValue(options, strconv.Itoa(current)), true)
	if idx == goBack {
		return 0, true
	}

	if options[idx][0] != "[ custom ]" {
		val, _ := strconv.Atoi(options[idx][0])
		return val, false
	}

	raw, back := promptCustomValue(
		step,
		customTitle,
		promptLabel,
		strconv.Itoa(current),
		fmt.Sprintf("  %sEnter any positive integer.%s", fgGray, ansiReset),
	)
	if back {
		return 0, true
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return current, false
	}
	return n, false
}

func loadedModelCount(items [][2]string) int {
	count := 0
	for _, item := range items {
		if strings.Contains(item[1], "Loaded model") {
			count++
		}
	}
	return count
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
	row("Default Model", cfg.PrimaryModel())
	switch cfg.SafetyMode {
	case tools.SafetyModeUserConfirm:
		row("Command Approval", "Manual user confirmation")
	default:
		row("Command Approval", "LLM judge")
		row("Safety Model", cfg.SafetyModel)
	}
	if cfg.RoutingEnabled {
		row("Routing", "Auto within approved models")
	} else {
		row("Routing", "Default model only")
	}
	row("Approved Models", strconv.Itoa(len(cfg.ApprovedModels)))
	if cfg.BaseURL != "" {
		row("Base URL", cfg.BaseURL)
	}
	if key := cfg.GetAPIKey(cfg.Provider); key != "" {
		row("API Key", maskKey(key))
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
	{"openrouter/auto", "Auto-selected best model         auto"},
	{"openrouter/free", "Auto-selected free model         free"},
	{"anthropic/claude-sonnet-4.6", "Anthropic Claude Sonnet 4.6      in $3.00/M  out $15.00/M"},
	{"anthropic/claude-opus-4.6", "Anthropic Claude Opus 4.6        in $5.00/M  out $25.00/M"},
	{"openai/gpt-5.4", "OpenAI GPT-5.4                   in $2.50/M  out $15.00/M"},
	{"google/gemini-3.1-pro-preview", "Google Gemini 3.1 Pro            in $2.00/M  out $12.00/M"},
	{"deepseek/deepseek-v3.2", "DeepSeek V3.2                    in $0.26/M  out $0.42/M"},
	{"x-ai/grok-4.1-fast", "xAI Grok 4.1 Fast                in $0.20/M  out $0.50/M"},
	{"openai/gpt-5.4-nano", "OpenAI GPT-5.4 Nano              in $0.20/M  out $1.25/M"},
	{"mistralai/mistral-large-2512", "Mistral Large 3 2512             in $0.50/M  out $1.50/M"},
	{"qwen/qwen3.5-plus-02-15", "Qwen 3.5 Plus                    in $0.26/M  out $1.56/M"},
	{"nvidia/nemotron-3-super-120b-a12b:free", "NVIDIA Nemotron 3 Super          free"},
	{"[ custom model ]", "Enter your own model ID →"},
}

var lmStudioModels = [][2]string{
	{"local-model", "Use currently loaded model  ★"},
	{"[ custom model ]", "Enter your model identifier →"},
}

var maxTokenOptions = [][2]string{
	{"1024", "Short · fast responses"},
	{"2048", "Compact responses"},
	{"4096", "Standard · recommended  ★"},
	{"8192", "Extended responses"},
	{"16384", "Maximum context window"},
	{"[ custom ]", "Enter a specific token count →"},
}

var maxIterationOptions = [][2]string{
	{"10", "Short runs · tight loops"},
	{"25", "Standard · recommended  ★"},
	{"40", "Longer investigations"},
	{"60", "Deep multi-step runs"},
	{"[ custom ]", "Enter a specific iteration limit →"},
}

var logLevelOptions = [][2]string{
	{"off", "Silent · only agent output shown  ★"},
	{"error", "Critical errors only"},
	{"warn", "Warnings and errors"},
	{"info", "Standard structured logging"},
	{"debug", "Verbose · all internal events"},
}

// safetyModelOptions are the recommended judge/safety models presented in the wizard.
var safetyModelOptions = [][2]string{
	{"x-ai/grok-4.1-fast", "Grok 4.1 Fast  ·  default  ★            xAI"},
	{"anthropic/claude-sonnet-4.6", "Claude Sonnet 4.6  ·  balanced          Anthropic"},
	{"openai/gpt-5.4-nano", "GPT-5.4 Nano  ·  fast & cheap           OpenAI"},
	{"google/gemini-3.1-flash-lite-preview", "Gemini 3.1 Flash Lite  ·  fast           Google"},
	{"deepseek/deepseek-v3.2", "DeepSeek V3.2  ·  capable               DeepSeek"},
	{"mistralai/mistral-small-2603", "Mistral Small 4  ·  balanced            Mistral"},
	{"[ custom model ]", "Enter your own model ID →"},
}

var safetyModeOptions = [][2]string{
	{tools.SafetyModeLLMJudge, "LLM judge  ·  secondary model reviews commands  ★"},
	{tools.SafetyModeUserConfirm, "Manual confirm  ·  wait for terminal user approval"},
}

var routingOptions = [][2]string{
	{"auto_within_policy", "Auto route within approved models  ★"},
	{"disabled", "Always use the default model"},
}

const totalSteps = 10

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
		{"lmstudio", "Local inference  ·  no key needed  ·  offline-capable"},
	}
	idx := selectList(providers, initial, false) // step 1 has no back
	if idx == goBack {
		return false
	}
	cfg.Provider = providers[idx][0]
	return true
}

// wizardAPIKey is step 2 — dispatches to the correct credential flow.
func wizardAPIKey(cfg *config.Config) bool {
	if cfg.Provider == "openrouter" {
		return wizardOpenRouterKey(cfg)
	}
	return wizardLMStudioURL(cfg)
}

func wizardOpenRouterKey(cfg *config.Config) bool {
	// The key for this provider lives in the map; nothing is lost when the
	// user switches to another provider and comes back.
	keyToOffer := cfg.GetAPIKey("openrouter")

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
				// Key is already in the map; nothing to update.
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
		cfg.SetAPIKey("openrouter", key)
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
			// Leave cfg.APIKey untouched so the OpenRouter key is preserved in
			// the saved YAML — the user can reuse it if they switch back later.
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
	// Leave cfg.APIKey untouched so the OpenRouter key is preserved in
	// the saved YAML — the user can reuse it if they switch back later.
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
			loadedCount := loadedModelCount(items)
			fmt.Printf("  %s✔  Found %d loaded / %d total models on local server%s\n\n", fgGreen+ansiBold, loadedCount, len(items)-1, ansiReset)
		} else {
			fmt.Printf("  %s⊙  Could not verify loaded models on local server%s\n\n", fgYellow+ansiDim, ansiReset)
			cfg.DefaultModel = "local-model"
		}
	} else {
		items = result.items
		renderModelStatus(result)
	}

	idx := selectList(items, optionIndexByValue(items, cfg.PrimaryModel()), true)
	if idx == goBack {
		return false
	}

	if items[idx][0] == "[ custom model ]" {
		val, back := promptCustomValue(
			3,
			"Custom Model Identifier",
			"Model ID:",
			cfg.PrimaryModel(),
			fmt.Sprintf("  %sEnter the exact model ID used by your provider.%s", fgGray, ansiReset),
			fmt.Sprintf("  %sExample: %santhropic/claude-sonnet-4.6%s", fgGray, fgAccent+ansiBold, ansiReset),
		)
		if back {
			return false
		}
		setDefaultModel(cfg, val)
	} else {
		setDefaultModel(cfg, items[idx][0])
	}
	return true
}

func wizardTokens(cfg *config.Config) bool {
	value, back := chooseNumericOption(6, "Max Response Length  (Tokens)", "Custom Token Limit", "Max tokens:", maxTokenOptions, cfg.MaxTokens)
	if back {
		return false
	}
	cfg.MaxTokens = value
	return true
}

func wizardSafetyModel(cfg *config.Config) bool {
	for {
		renderHeader()
		renderStep(4, totalSteps, "Command Approval Policy")

		fmt.Printf("  %sCommands outside the auto-approved allowlist need a secondary gate before execution.%s\n", fgGray, ansiReset)
		fmt.Printf("  %sChoose whether that gate is another model or a direct user confirmation prompt.%s\n\n", fgMuted+ansiDim, ansiReset)

		initialMode := 0
		if cfg.SafetyMode == tools.SafetyModeUserConfirm {
			initialMode = 1
		}

		modeIdx := selectList(safetyModeOptions, initialMode, true)
		if modeIdx == goBack {
			return false
		}
		cfg.SafetyMode = safetyModeOptions[modeIdx][0]
		if cfg.SafetyMode == tools.SafetyModeUserConfirm {
			return true
		}

		renderHeader()
		renderStep(4, totalSteps, "Select a Safety Model  (LLM-as-a-Judge)")
		fmt.Printf("  %sThe safety model reviews shell commands before execution.%s\n", fgGray, ansiReset)
		fmt.Printf("  %sIt should be a capable, instruction-following model from your provider.%s\n\n", fgMuted+ansiDim, ansiReset)

		idx := selectList(safetyModelOptions, optionIndexByValue(safetyModelOptions, cfg.SafetyModel), true)
		if idx == goBack {
			continue
		}

		if safetyModelOptions[idx][0] == "[ custom model ]" {
			val, back := promptCustomValue(
				4,
				"Custom Safety Model Identifier",
				"Safety Model ID:",
				cfg.SafetyModel,
				fmt.Sprintf("  %sEnter the exact model ID used by your provider.%s", fgGray, ansiReset),
				fmt.Sprintf("  %sExample: %santhropic/claude-3.5-sonnet%s", fgGray, fgAccent+ansiBold, ansiReset),
			)
			if back {
				continue
			}
			cfg.SafetyModel = val
		} else {
			cfg.SafetyModel = safetyModelOptions[idx][0]
		}
		return true
	}
}

func wizardRouting(cfg *config.Config) bool {
	renderHeader()
	renderStep(5, totalSteps, "Execution Routing")

	fmt.Printf("  %sRouting can use different approved models for planning, execution, and memory curation.%s\n", fgGray, ansiReset)
	fmt.Printf("  %sUnapproved recommendations still require a prompt before use.%s\n\n", fgMuted+ansiDim, ansiReset)

	current := "auto_within_policy"
	if !cfg.RoutingEnabled || cfg.RoutingMode == "disabled" {
		current = "disabled"
	}

	idx := selectList(routingOptions, optionIndexByValue(routingOptions, current), true)
	if idx == goBack {
		return false
	}

	switch routingOptions[idx][0] {
	case "disabled":
		cfg.RoutingEnabled = false
		cfg.RoutingMode = "disabled"
	default:
		cfg.RoutingEnabled = true
		cfg.RoutingMode = "auto_within_policy"
	}
	return true
}

func wizardIterations(cfg *config.Config) bool {
	value, back := chooseNumericOption(7, "Max Iterations", "Custom Iteration Limit", "Max iterations:", maxIterationOptions, cfg.MaxIterations)
	if back {
		return false
	}
	cfg.MaxIterations = value
	return true
}

func wizardLogLevel(cfg *config.Config) bool {
	renderHeader()
	renderStep(8, totalSteps, "Log Verbosity")

	idx := selectList(logLevelOptions, optionIndexByValue(logLevelOptions, cfg.LogLevel), true)
	if idx == goBack {
		return false
	}
	cfg.LogLevel = logLevelOptions[idx][0]
	return true
}

func wizardSoulProfile(cfg *config.Config, profile *soulProfile) bool {
	renderHeader()
	renderStep(9, totalSteps, "Agent Soul")

	needsBootstrap, err := soulBootstrapRequired(cfg.MemoryDir)
	if err != nil {
		fmt.Printf("  %sThe soul file could not be inspected yet, so setup will prepare a fresh one.%s\n\n", fgYellow+ansiBold, ansiReset)
		needsBootstrap = true
	}

	if !needsBootstrap {
		fmt.Printf("  %sAn existing %s will be preserved.%s\n", fgGray, fgAccent+ansiBold+"soul.md"+ansiReset+fgGray, ansiReset)
		fmt.Printf("  %sSetup will still ensure the other memory files exist, but it will not overwrite your current soul.%s\n\n", fgMuted+ansiDim, ansiReset)

		choices := [][2]string{
			{"Keep existing soul.md", "User-owned file preserved as-is"},
		}
		return selectList(choices, 0, true) != goBack
	}

	fmt.Printf("  %sChoose the starting voice and working style for the generated %s.%s\n", fgGray, fgAccent+ansiBold+"soul.md"+ansiReset+fgGray, ansiReset)
	fmt.Printf("  %sThis mainly tunes tone, autonomy, risk posture, and explanation depth.%s\n\n", fgMuted+ansiDim, ansiReset)

	idx := selectList(soulProfileItems(), soulProfileIndexByID(profile.ID), true)
	if idx == goBack {
		return false
	}

	*profile = soulProfiles[idx]
	return true
}

func wizardConfirm(cfg *config.Config, profile soulProfile) bool {
	renderHeader()
	renderStep(10, totalSteps, "Review & Confirm")

	renderReview(cfg)
	fmt.Println()
	fmt.Printf("  %sSoul profile:%s  %s%s%s\n\n", fgMuted, ansiReset, ansiBold, fgWhite, profile.Label+ansiReset)

	choices := [][2]string{
		{"✔  Save and finish", "Write config — ready to run"},
		{"✗  Cancel", "Exit without saving changes"},
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

func setDefaultModel(cfg *config.Config, model string) {
	model = strings.TrimSpace(model)
	cfg.DefaultModel = model
	ensureDefaultApproved(cfg)
}

func ensureDefaultApproved(cfg *config.Config) {
	if strings.TrimSpace(cfg.Provider) == "" || strings.TrimSpace(cfg.PrimaryModel()) == "" {
		return
	}

	entry := cfg.Provider + "/" + cfg.PrimaryModel()
	for _, existing := range cfg.ApprovedModels {
		if existing == entry {
			return
		}
	}
	cfg.ApprovedModels = append(cfg.ApprovedModels, entry)
}

// ─── Command ──────────────────────────────────────────────────────────────────

func loadWizardConfig() *config.Config {
	// Load any previously saved configuration so we can:
	//   (a) pre-populate selections with the user's current values, and
	//   (b) offer to reuse stored credentials without re-typing them.
	existingCfg, _ := config.LoadConfig()
	cfg := config.DefaultConfig()

	if existingCfg == nil {
		return cfg
	}

	if existingCfg.Provider != "" {
		cfg.Provider = existingCfg.Provider
	}
	if existingCfg.PrimaryModel() != "" {
		cfg.DefaultModel = existingCfg.PrimaryModel()
	}
	// Carry over the full key map so every provider's credential is
	// available for the reuse prompt regardless of which provider the
	// user starts from or switches to during this wizard run.
	if len(existingCfg.APIKeys) > 0 {
		cfg.APIKeys = existingCfg.APIKeys
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
	if existingCfg.SafetyModel != "" {
		cfg.SafetyModel = existingCfg.SafetyModel
	}
	if existingCfg.SafetyMode != "" {
		cfg.SafetyMode = existingCfg.SafetyMode
	}
	cfg.RoutingEnabled = existingCfg.RoutingEnabled
	if existingCfg.RoutingMode != "" {
		cfg.RoutingMode = existingCfg.RoutingMode
	}
	if len(existingCfg.ApprovedModels) > 0 {
		cfg.ApprovedModels = existingCfg.ApprovedModels
	}
	if existingCfg.MemoryDir != "" {
		cfg.MemoryDir = existingCfg.MemoryDir
	}
	if existingCfg.StateDBPath != "" {
		cfg.StateDBPath = existingCfg.StateDBPath
	}
	if existingCfg.MemoryMaxSnippets > 0 {
		cfg.MemoryMaxSnippets = existingCfg.MemoryMaxSnippets
	}
	if existingCfg.RoutingMinConfidence > 0 {
		cfg.RoutingMinConfidence = existingCfg.RoutingMinConfidence
	}

	return cfg
}

func runSetupWizard(mode string) {
	cfg := loadWizardConfig()

	// Begin fetching the model list concurrently so it's ready by step 3.
	modelsCh := make(chan modelsResult, 1)
	go func() { modelsCh <- fetchOpenRouterModels() }()

	var fetchedModels modelsResult // populated on first visit to step 3
	selectedSoulProfile := defaultSoulProfile()

	// ── Step loop with Backspace back-navigation ──────────────────────
	step := 1
	for step >= 1 && step <= totalSteps {
		var advanced bool
		switch step {
		case 1:
			advanced = wizardProvider(cfg)
		case 2:
			// Step 2: API key (OpenRouter) or base URL (LM Studio).
			advanced = wizardAPIKey(cfg)
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
			advanced = wizardSafetyModel(cfg)
		case 5:
			advanced = wizardRouting(cfg)
		case 6:
			advanced = wizardTokens(cfg)
		case 7:
			advanced = wizardIterations(cfg)
		case 8:
			advanced = wizardLogLevel(cfg)
		case 9:
			advanced = wizardSoulProfile(cfg, &selectedSoulProfile)
		case 10:
			advanced = wizardConfirm(cfg, selectedSoulProfile)
		}

		if advanced {
			step++
		} else if step > 1 {
			step--
		}
		// step == 1 and back pressed → stays at 1 (no previous step)
	}

	cfg.Normalize()
	if cfg.RoutingMode == "" {
		cfg.RoutingMode = "auto_within_policy"
	}
	ensureDefaultApproved(cfg)

	if err := cfg.Save(); err != nil {
		fmt.Printf("\n  %s%s✗  Failed to save configuration: %v%s\n\n",
			ansiBold, fgRed, err, ansiReset)
		os.Exit(1)
	}

	wroteSoul, err := writeSetupSoul(cfg.MemoryDir, cfg.MemoryMaxSnippets, selectedSoulProfile)
	if err != nil {
		fmt.Printf("\n  %s%s✗  Failed to prepare memory files: %v%s\n\n",
			ansiBold, fgRed, err, ansiReset)
		os.Exit(1)
	}

	successTitle := "Setup complete!"
	actionLabel := "Setup"
	if mode == "settings" {
		successTitle = "Settings updated!"
		actionLabel = "Settings"
	}

	// ── Success banner ─────────────────────────────────────────────────
	fmt.Print(clearScreen)
	fmt.Println()
	fmt.Printf("%s%s%s\n", fgGreen+ansiBold, hSep, ansiReset)
	fmt.Printf("  %s%s✔  %s%s Configuration saved.\n", ansiBold, fgGreen, successTitle, ansiReset)
	fmt.Printf("%s%s%s\n\n", fgGreen+ansiBold, hSep, ansiReset)

	renderReview(cfg)

	fmt.Println()
	fmt.Printf("  %sTry running:%s\n\n", fgGray, ansiReset)
	fmt.Printf("  %s%s  cvkeharness run \"list all running docker containers\"%s\n\n",
		ansiBold, fgAccent, ansiReset)
	if wroteSoul {
		fmt.Printf("  %s%s generated %s~/.cvkeharness/soul.md%s and ensured %soperator.md%s, %smemory.md%s, and %sfindings.md%s are ready.%s\n",
			fgGray, actionLabel, fgAccent+ansiBold, ansiReset+fgGray, fgAccent+ansiBold, ansiReset+fgGray, fgAccent+ansiBold, ansiReset+fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
	} else {
		fmt.Printf("  %s%s preserved your existing %s~/.cvkeharness/soul.md%s and ensured %soperator.md%s, %smemory.md%s, and %sfindings.md%s are present.%s\n",
			fgGray, actionLabel, fgAccent+ansiBold, ansiReset+fgGray, fgAccent+ansiBold, ansiReset+fgGray, fgAccent+ansiBold, ansiReset+fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
	}
	fmt.Printf("  %sThe SQLite state file %s~/.cvkeharness/state.db%s will still be created on first run as needed.%s\n\n",
		fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive onboarding wizard to configure the agent",
	Run: func(cmd *cobra.Command, args []string) {
		runSetupWizard("setup")
	},
}

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Interactive settings editor for the agent",
	Run: func(cmd *cobra.Command, args []string) {
		runSetupWizard("settings")
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(settingsCmd)
}
