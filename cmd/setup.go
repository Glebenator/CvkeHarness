package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/setuptui"
	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/provider"
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
	termui.RenderWizardHeader("CvkeHarness", "AI DevOps agent  ·  configuration wizard")
}

func renderSettingsHeader(dirty bool) {
	termui.RenderWizardHeader("CvkeHarness", "AI DevOps agent  ·  interactive settings")
	fmt.Printf("  %sJump straight to any setting. Each change returns here, so you can update one thing and leave.%s\n", fgGray, ansiReset)
	if dirty {
		termui.RenderNote("Status", fgYellow, fgYellow+ansiBold+"● Unsaved changes"+ansiReset)
		fmt.Println()
		return
	}
	termui.RenderNote("Status", fgMuted, fgMuted+ansiDim+"No unsaved changes yet."+ansiReset)
	fmt.Println()
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
	row := func(label, value string) string {
		return fmt.Sprintf("%s%-16s%s  %s%s%s", fgMuted, label, ansiReset, ansiBold+fgWhite, value, ansiReset)
	}
	lines := []string{
		row("Provider", cfg.Provider),
		row("Default Model", cfg.PrimaryModel()),
	}
	switch cfg.SafetyMode {
	case tools.SafetyModeUserConfirmAll:
		lines = append(lines, row("Command Approval", "User approval before every command"))
	case tools.SafetyModeUserConfirm:
		lines = append(lines, row("Command Approval", "Manual user confirmation"))
	case tools.SafetyModeUnrestricted:
		lines = append(lines, row("Command Approval", "Unrestricted shell execution"))
	default:
		lines = append(lines, row("Command Approval", "LLM judge"))
		lines = append(lines, row("Safety Model", cfg.SafetyModel))
	}
	if cfg.RoutingEnabled {
		lines = append(lines, row("Routing", "Auto within approved models"))
	} else {
		lines = append(lines, row("Routing", "Default model only"))
	}
	lines = append(lines, row("Approved Models", strconv.Itoa(len(cfg.ApprovedModels))))
	lines = append(lines, row("Favorite Models", strconv.Itoa(len(cfg.FavoriteModels))))
	if cfg.BaseURL != "" {
		lines = append(lines, row("Base URL", cfg.BaseURL))
	}
	if key := cfg.GetAPIKey(cfg.Provider); key != "" {
		lines = append(lines, row("API Key", maskKey(key)))
	}
	lines = append(lines,
		row("Max Tokens", strconv.Itoa(cfg.MaxTokens)),
		row("Max Iterations", strconv.Itoa(cfg.MaxIterations)),
		row("Log Level", cfg.LogLevel),
	)
	termui.RenderNote("Configuration", fgAccent, lines...)
}

func summarizeReviewNotes(notes []string, maxLen int) string {
	text := strings.Join(notes, "; ")
	if maxLen > 0 && len(text) > maxLen {
		return strings.TrimSpace(text[:maxLen-3]) + "..."
	}
	return text
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
	source    string
	message   string
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

type openAIModel struct {
	ID string `json:"id"`
}

type openAIModelsResponse struct {
	Data []openAIModel `json:"data"`
}

const codexModelsFreshDuration = 6 * time.Hour

type codexModelsCache struct {
	FetchedAt     time.Time         `json:"fetched_at"`
	ClientVersion string            `json:"client_version"`
	Models        []codexCacheModel `json:"models"`
}

type codexCacheModel struct {
	Slug           string `json:"slug"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	Visibility     string `json:"visibility"`
	Priority       int    `json:"priority"`
	SupportedInAPI bool   `json:"supported_in_api"`
}

func codexModelsCachePath() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "models_cache.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "models_cache.json")
}

func fetchCodexModels(now time.Time) modelsResult {
	path := codexModelsCachePath()
	if strings.TrimSpace(path) == "" {
		return modelsResult{
			items:     codexUnavailableModels,
			timestamp: now,
			source:    "fallback",
			message:   "Codex models cache path could not be resolved",
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return modelsResult{
			items:     codexUnavailableModels,
			timestamp: now,
			source:    "fallback",
			message:   fmt.Sprintf("Codex models cache unavailable at %s", path),
		}
	}

	var cache codexModelsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return modelsResult{
			items:     codexUnavailableModels,
			timestamp: now,
			source:    "fallback",
			message:   fmt.Sprintf("Could not parse Codex models cache at %s", path),
		}
	}

	items := codexCacheItems(cache.Models)
	if len(items) == 0 {
		return modelsResult{
			items:     codexUnavailableModels,
			timestamp: cache.FetchedAt,
			source:    "fallback",
			message:   "Codex models cache did not contain listable models",
		}
	}

	age := now.Sub(cache.FetchedAt)
	if age < 0 {
		age = 0
	}
	if cache.FetchedAt.IsZero() || age > codexModelsFreshDuration {
		return modelsResult{
			items:     codexUnavailableModels,
			timestamp: cache.FetchedAt,
			source:    "codex-cache-stale",
			message:   "Codex models cache is stale; run Codex once to refresh account-scoped models",
		}
	}

	items = append(items, [2]string{"[ custom model ]", "Enter your own model ID →"})
	return modelsResult{
		items:     items,
		isLive:    true,
		timestamp: cache.FetchedAt,
		source:    "codex-cache",
		message:   cache.ClientVersion,
	}
}

func codexCacheItems(models []codexCacheModel) [][2]string {
	list := make([]codexCacheModel, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model.Slug) == "" {
			continue
		}
		if model.Visibility != "" && model.Visibility != "list" {
			continue
		}
		if !model.SupportedInAPI {
			continue
		}
		list = append(list, model)
	}

	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Priority != list[j].Priority {
			return list[i].Priority < list[j].Priority
		}
		return list[i].Slug < list[j].Slug
	})

	items := make([][2]string, 0, len(list))
	for _, model := range list {
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = model.Slug
		}
		desc := strings.TrimSpace(model.Description)
		if desc == "" {
			desc = "Available from Codex models cache"
		}
		items = append(items, [2]string{model.Slug, fmt.Sprintf("%s  ·  %s", name, desc)})
		if len(items) >= 22 {
			break
		}
	}
	return items
}

func fetchOpenAIModels(token string) modelsResult {
	client := &http.Client{Timeout: 6 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return modelsResult{items: openAIModels, isLive: false, timestamp: time.Now()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return modelsResult{items: openAIModels, isLive: false, timestamp: time.Now()}
	}

	var data openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return modelsResult{items: openAIModels, isLive: false, timestamp: time.Now()}
	}

	available := make(map[string]bool, len(data.Data))
	for _, model := range data.Data {
		if model.ID != "" {
			available[model.ID] = true
		}
	}

	var items [][2]string
	seen := make(map[string]bool)
	for _, item := range openAIModels {
		if item[0] == "[ custom model ]" {
			continue
		}
		if available[item[0]] {
			items = append(items, item)
			seen[item[0]] = true
		}
	}
	for _, model := range data.Data {
		if !openAIModelVisible(model.ID) || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		items = append(items, [2]string{model.ID, "Available in your OpenAI account"})
		if len(items) >= 22 {
			break
		}
	}
	if len(items) == 0 {
		return modelsResult{items: openAIModels, isLive: false, timestamp: time.Now()}
	}
	items = append(items, [2]string{"[ custom model ]", "Enter your own model ID →"})
	return modelsResult{items: items, isLive: true, timestamp: time.Now()}
}

func openAIModelVisible(id string) bool {
	return strings.Contains(id, "codex") || strings.HasPrefix(id, "gpt-5")
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

func renderOpenAIModelStatus(r modelsResult) {
	if r.isLive {
		fmt.Printf("  %s⬤ LIVE%s  OpenAI models available to your credential  ·  retrieved %s%s\n\n",
			fgGreen+ansiBold, ansiReset+fgGray, r.timestamp.Format("15:04 UTC")+ansiReset, ansiReset)
	} else {
		fmt.Printf("  %s⊙ offline fallback%s  ·  Codex-capable Responses API models%s\n\n",
			fgYellow+ansiDim, ansiReset+fgMuted, ansiReset)
	}
}

func renderCodexModelStatus(r modelsResult, now time.Time) {
	if r.isLive {
		fmt.Printf("  %s⬤ LIVE%s  Codex model cache refreshed %s  ·  %s%s\n\n",
			fgGreen+ansiBold, ansiReset+fgGray, relativeAge(now, r.timestamp), r.timestamp.Format("15:04 UTC")+ansiReset, ansiReset)
		return
	}

	if r.source == "codex-cache" && !r.timestamp.IsZero() {
		fmt.Printf("  %s⊙ cached%s  ·  Codex model cache last refreshed %s%s\n\n",
			fgYellow+ansiDim, ansiReset+fgMuted, r.timestamp.Format(time.RFC1123), ansiReset)
		return
	}

	if r.source == "codex-cache-stale" && !r.timestamp.IsZero() {
		fmt.Printf("  %s⊙ stale cache%s  ·  Codex model cache last refreshed %s; refresh Codex before choosing%s\n\n",
			fgYellow+ansiDim, ansiReset+fgMuted, r.timestamp.Format(time.RFC1123), ansiReset)
		return
	}

	fmt.Printf("  %s⊙ account models unavailable%s  ·  %s%s\n\n",
		fgYellow+ansiDim, ansiReset+fgMuted, fallbackMessage(r.message), ansiReset)
}

func relativeAge(now, then time.Time) string {
	if then.IsZero() {
		return "at an unknown time"
	}
	d := now.Sub(then)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func fallbackMessage(message string) string {
	if strings.TrimSpace(message) == "" {
		return "using built-in Codex model fallback"
	}
	return message
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

// ─── OpenAI token validation ─────────────────────────────────────────────────

type openAIValidationResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// validateOpenAIKey calls the models endpoint to confirm the bearer credential.
func validateOpenAIKey(key string) error {
	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %v", err)
	}
	defer resp.Body.Close()

	var out openAIValidationResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)

	if resp.StatusCode == http.StatusUnauthorized {
		msg := out.Error.Message
		if msg == "" {
			msg = "unauthorized"
		}
		return fmt.Errorf("invalid credential — %s", msg)
	}
	if resp.StatusCode != http.StatusOK {
		msg := out.Error.Message
		if msg != "" {
			return fmt.Errorf("unexpected status %d from validation endpoint: %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("unexpected status %d from validation endpoint", resp.StatusCode)
	}
	return nil
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

var openAIModels = [][2]string{
	{"gpt-5.2-codex", "GPT-5.2 Codex  ·  strongest agentic coding model  ★"},
	{"gpt-5.1-codex", "GPT-5.1 Codex  ·  agentic coding"},
	{"gpt-5.1-codex-max", "GPT-5.1 Codex Max  ·  long-running coding tasks"},
	{"gpt-5.1-codex-mini", "GPT-5.1 Codex Mini  ·  smaller coding model"},
	{"gpt-5-codex", "GPT-5 Codex  ·  agentic coding"},
	{"gpt-5.2", "GPT-5.2  ·  general coding and agentic tasks"},
	{"gpt-5.2-pro", "GPT-5.2 Pro  ·  deeper reasoning"},
	{"gpt-5.1", "GPT-5.1  ·  coding and agentic tasks"},
	{"gpt-5-mini", "GPT-5 Mini  ·  faster and lower cost"},
	{"gpt-5-nano", "GPT-5 Nano  ·  fastest and lowest cost"},
	{"[ custom model ]", "Enter your own model ID →"},
}

var codexUnavailableModels = [][2]string{
	{"[ custom model ]", "Enter a Codex model ID manually →"},
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

var openAISafetyModelOptions = [][2]string{
	{"gpt-5-nano", "GPT-5 Nano  ·  fast & cheap  ★"},
	{"gpt-5-mini", "GPT-5 Mini  ·  balanced"},
	{"gpt-5.1", "GPT-5.1  ·  stronger judge"},
	{"[ custom model ]", "Enter your own OpenAI model ID →"},
}

var safetyModeOptions = [][2]string{
	{tools.SafetyModeLLMJudge, "LLM judge  ·  secondary model reviews commands  ★"},
	{tools.SafetyModeUserConfirmAll, "User approval  ·  ask before every shell command"},
	{tools.SafetyModeUnrestricted, "Unrestricted  ·  risky, no command approval gate"},
}

func safetyModelsForProvider(cfg *config.Config) [][2]string {
	switch cfg.Provider {
	case "codex":
		return fetchCodexModels(time.Now()).items
	case "openai":
		return openAISafetyModelOptions
	default:
		return safetyModelOptions
	}
}

var routingOptions = [][2]string{
	{"auto_within_policy", "Auto route within approved models  ★"},
	{"disabled", "Always use the default model"},
}

const totalSteps = 11

// ─── Wizard steps ─────────────────────────────────────────────────────────────
// Each function returns true to advance, false to go back.

func wizardProvider(cfg *config.Config) bool {
	renderHeader()
	renderStep(1, totalSteps, "Choose your LLM Provider")

	initial := 0
	providers := [][2]string{
		{"codex", "ChatGPT subscription  ·  reuses Codex CLI login"},
		{"openrouter", "Cloud API  ·  many models  ·  requires API key"},
		{"openai", "OpenAI API  ·  Codex models  ·  usage-based API key"},
		{"lmstudio", "Local inference  ·  no key needed  ·  offline-capable"},
	}
	for i, item := range providers {
		if item[0] == cfg.Provider {
			initial = i
			break
		}
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
	if cfg.Provider == "codex" {
		return wizardCodexCLIAuth(cfg)
	}
	if cfg.Provider == "openrouter" {
		return wizardOpenRouterKey(cfg)
	}
	if cfg.Provider == "openai" {
		return wizardOpenAIKey(cfg)
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

func wizardCodexCLIAuth(cfg *config.Config) bool {
	for {
		renderHeader()
		renderStep(2, totalSteps, "Codex ChatGPT Login")

		auth, err := provider.LoadCodexCLIAuth(provider.CodexAuthPath())
		if err == nil {
			fmt.Printf("  %s✔ Found Codex CLI ChatGPT login%s\n", fgGreen+ansiBold, ansiReset)
			fmt.Printf("  %sAuth cache:%s %s%s%s\n", fgMuted, ansiReset, fgAccent, auth.AuthPath, ansiReset)
			if auth.AccountID != "" {
				fmt.Printf("  %sWorkspace/account:%s %s%s%s\n", fgMuted, ansiReset, fgWhite+ansiBold, auth.AccountID, ansiReset)
			}
			fmt.Printf("\n  %sCvkeHarness will reuse the current access token and leave token refresh to the official Codex CLI.%s\n\n",
				fgGray, ansiReset)

			choices := [][2]string{
				{"Use this Codex login", "ChatGPT subscription limits apply"},
				{"I changed accounts; check again", "re-read the Codex CLI auth cache"},
				{"← Return to provider selection", ""},
			}
			switch selectList(choices, 0, true) {
			case goBack, 2:
				return false
			case 1:
				continue
			default:
				return true
			}
		}

		fmt.Printf("  %sNo usable Codex CLI ChatGPT login was found.%s\n\n", fgYellow+ansiBold, ansiReset)
		fmt.Printf("  %sTo use ChatGPT Plus/Pro/Business/Edu/Enterprise Codex access, run:%s\n\n", fgGray, ansiReset)
		fmt.Printf("  %s%s  codex login%s\n\n", ansiBold, fgAccent, ansiReset)
		fmt.Printf("  %sChoose %sSign in with ChatGPT%s, then return here.%s\n\n",
			fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
		fmt.Printf("  %sCurrent lookup path:%s %s%s%s\n\n", fgMuted, ansiReset, fgAccent, provider.CodexAuthPath(), ansiReset)
		fmt.Printf("  %sDetails: %v%s\n\n", fgMuted, err, ansiReset)

		choices := [][2]string{
			{"Check again", "I completed `codex login`"},
			{"← Return to provider selection", ""},
		}
		if selectList(choices, 0, true) != 0 {
			return false
		}
	}
}

func wizardOpenAIKey(cfg *config.Config) bool {
	// Store OpenAI credentials independently so switching providers does not
	// overwrite a previously validated OpenRouter key.
	keyToOffer := cfg.GetAPIKey("openai")
	showReuseMenu := keyToOffer != ""

	for {
		renderHeader()
		renderStep(2, totalSteps, "OpenAI API Key")

		if showReuseMenu {
			fmt.Printf("  %sAn OpenAI API key from a previous setup was found.%s\n\n", fgGray, ansiReset)
			choices := [][2]string{
				{"Reuse existing key", maskKey(keyToOffer)},
				{"Enter a different key", "paste a usage-based OpenAI API key"},
			}
			idx := selectList(choices, 0, true)
			switch idx {
			case goBack:
				return false
			case 0:
				return true
			}
			showReuseMenu = false
		}

		fmt.Printf("  %sStored locally in %s~/.cvkeharness/config.yaml%s\n",
			fgGray, fgAccent+ansiBold, ansiReset)
		fmt.Printf("  %sUse this for usage-based OpenAI API access. For ChatGPT subscription Codex access, choose the Codex provider instead.%s\n",
			fgGray, ansiReset)
		fmt.Printf("  %sOpenAI API keys are managed at: %shttps://platform.openai.com/api-keys%s\n\n",
			fgGray, fgAccent, ansiReset)

		key, back := promptText("Paste your OpenAI API key:", "", true)
		if back {
			if keyToOffer != "" {
				showReuseMenu = true
				continue
			}
			return false
		}

		renderHeader()
		renderStep(2, totalSteps, "OpenAI API Key")
		fmt.Printf("\n  %s%s⟳  Validating key …%s\n", ansiDim, fgGray, ansiReset)

		if err := validateOpenAIKey(key); err != nil {
			renderHeader()
			renderStep(2, totalSteps, "OpenAI API Key")
			fmt.Printf("\n  %s%s✗  %v%s\n\n", ansiBold, fgRed, err, ansiReset)

			retry := [][2]string{
				{"Try a different key", "paste or type another API key"},
				{"← Return to provider selection", ""},
			}
			if selectList(retry, 0, false) == 1 {
				return false
			}
			continue
		}

		cfg.SetAPIKey("openai", key)
		renderHeader()
		renderStep(2, totalSteps, "OpenAI API Key")
		fmt.Printf("\n  %s%s✔  Key validated successfully%s\n",
			ansiBold, fgGreen, ansiReset)
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
	} else if cfg.Provider == "codex" {
		res := fetchCodexModels(time.Now())
		items = res.items
		renderCodexModelStatus(res, time.Now())
	} else if cfg.Provider == "openai" {
		res := fetchOpenAIModels(cfg.GetAPIKey("openai"))
		items = res.items
		renderOpenAIModelStatus(res)
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
			modelExample(cfg),
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

func modelExample(cfg *config.Config) string {
	example := "anthropic/claude-sonnet-4.6"
	if cfg.Provider == "codex" {
		example = "gpt-5.2-codex"
	}
	if cfg.Provider == "openai" {
		example = "gpt-5.2-codex"
	}
	if cfg.Provider == "lmstudio" {
		example = "local-model"
	}
	return fmt.Sprintf("  %sExample: %s%s%s", fgGray, fgAccent+ansiBold, example, ansiReset)
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
		if cfg.SafetyMode == tools.SafetyModeUserConfirmAll || cfg.SafetyMode == tools.SafetyModeUserConfirm {
			initialMode = 1
		} else if cfg.SafetyMode == tools.SafetyModeUnrestricted {
			initialMode = 2
		}

		modeIdx := selectList(safetyModeOptions, initialMode, true)
		if modeIdx == goBack {
			return false
		}
		cfg.SafetyMode = safetyModeOptions[modeIdx][0]
		if cfg.SafetyMode == tools.SafetyModeUserConfirmAll || cfg.SafetyMode == tools.SafetyModeUserConfirm || cfg.SafetyMode == tools.SafetyModeUnrestricted {
			return true
		}

		renderHeader()
		renderStep(4, totalSteps, "Select a Safety Model  (LLM-as-a-Judge)")
		fmt.Printf("  %sThe safety model reviews shell commands before execution.%s\n", fgGray, ansiReset)
		fmt.Printf("  %sIt should be a capable, instruction-following model from your provider.%s\n\n", fgMuted+ansiDim, ansiReset)

		var options [][2]string
		if cfg.Provider == "codex" {
			now := time.Now()
			res := fetchCodexModels(now)
			options = res.items
			renderCodexModelStatus(res, now)
		} else {
			options = safetyModelsForProvider(cfg)
		}
		idx := selectList(options, optionIndexByValue(options, cfg.SafetyModel), true)
		if idx == goBack {
			continue
		}

		if options[idx][0] == "[ custom model ]" {
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
			cfg.SafetyModel = config.NormalizeProviderModelID(cfg.Provider, val)
		} else {
			cfg.SafetyModel = config.NormalizeProviderModelID(cfg.Provider, options[idx][0])
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

func wizardMachineNotes(cfg *config.Config, notes *[]string) bool {
	renderHeader()
	renderStep(10, totalSteps, "Machine Notes")

	existingNotes, err := loadSetupHostNotes(cfg.MemoryDir)
	if err == nil && len(existingNotes) > 0 {
		*notes = existingNotes
		fmt.Printf("  %sExisting runtime-host notes were found in %s and will be preserved.%s\n", fgGray, fgAccent+ansiBold+"host.md"+ansiReset+fgGray, ansiReset)
		fmt.Printf("  %sThese notes already participate in the runtime-host summary, so setup will leave them alone.%s\n\n", fgMuted+ansiDim, ansiReset)

		choices := [][2]string{
			{"Keep existing host.md notes", "Preserve the current machine guidance"},
		}
		return selectList(choices, 0, true) != goBack
	}

	fmt.Printf("  %sOptional: capture machine-specific quirks CvkeHarness should keep in mind on this runtime host.%s\n", fgGray, ansiReset)
	fmt.Printf("  %sExamples: Docker requires sudo, Homebrew lives in /opt/homebrew, VPN/DNS rewrites hostnames, or services are managed in a nonstandard way.%s\n\n", fgMuted+ansiDim, ansiReset)

	choices := [][2]string{
		{"Add machine notes", "Write operator-authored quirks into host.md and include them in runtime-host retrieval"},
		{"Skip for now", "Leave host.md facts-only; you can edit it later"},
	}
	initial := 1
	if len(*notes) > 0 {
		initial = 0
	}
	idx := selectList(choices, initial, true)
	if idx == goBack {
		return false
	}
	if idx == 1 {
		*notes = nil
		return true
	}

	defaultVal := strings.Join(*notes, "; ")
	raw, back := promptCustomValue(
		10,
		"Machine Notes",
		"Notes (single line; separate multiple notes with ';'):",
		defaultVal,
		fmt.Sprintf("  %sKeep these short and durable. They should be things the harness should remember across tasks on this machine.%s", fgGray, ansiReset),
	)
	if back {
		return false
	}

	*notes = splitSetupNotes(raw)
	return true
}

func wizardConfirm(cfg *config.Config, profile soulProfile, hostNotes []string) bool {
	renderHeader()
	renderStep(11, totalSteps, "Review & Confirm")

	renderReview(cfg)
	fmt.Println()
	fmt.Printf("  %sSoul profile:%s  %s%s%s\n\n", fgMuted, ansiReset, ansiBold, fgWhite, profile.Label+ansiReset)
	if len(hostNotes) > 0 {
		fmt.Printf("  %sRuntime host notes:%s  %s%s%s\n\n", fgMuted, ansiReset, ansiBold, fgWhite, summarizeReviewNotes(hostNotes, 92)+ansiReset)
	}

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
	model = config.NormalizeProviderModelID(cfg.Provider, model)
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

func cloneConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}

	clone := *cfg
	if len(cfg.APIKeys) > 0 {
		clone.APIKeys = make(map[string]string, len(cfg.APIKeys))
		for key, value := range cfg.APIKeys {
			clone.APIKeys[key] = value
		}
	}
	if len(cfg.AllowedCommands) > 0 {
		clone.AllowedCommands = append([]string(nil), cfg.AllowedCommands...)
	}
	if len(cfg.ApprovedModels) > 0 {
		clone.ApprovedModels = append([]string(nil), cfg.ApprovedModels...)
	}
	if len(cfg.FavoriteModels) > 0 {
		clone.FavoriteModels = append([]string(nil), cfg.FavoriteModels...)
	}
	return &clone
}

func configChanged(before, after *config.Config) bool {
	return !reflect.DeepEqual(before, after)
}

func ensureFetchedModels(modelsCh <-chan modelsResult, fetched *modelsResult) modelsResult {
	if fetched.items != nil {
		return *fetched
	}

	select {
	case result := <-modelsCh:
		*fetched = result
	case <-time.After(100 * time.Millisecond):
		renderHeader()
		renderStep(3, totalSteps, "Select a Model")
		fmt.Printf("  %s%s⟳  Fetching latest models …%s\n", ansiDim, fgGray, ansiReset)
		*fetched = <-modelsCh
	}

	return *fetched
}

func modelsForProvider(cfg *config.Config, modelsCh <-chan modelsResult, fetched *modelsResult) modelsResult {
	if cfg.Provider != "openrouter" {
		return modelsResult{}
	}
	return ensureFetchedModels(modelsCh, fetched)
}

type settingsMenuAction int

const (
	settingsEditProvider settingsMenuAction = iota
	settingsEditConnection
	settingsEditModel
	settingsEditSafety
	settingsEditRouting
	settingsEditTokens
	settingsEditIterations
	settingsEditLogLevel
	settingsEditSoul
	settingsReviewAndSave
	settingsDiscardAndExit
)

type settingsMenuEntry struct {
	Label       string
	Description string
	Action      settingsMenuAction
}

func providerSummary(cfg *config.Config) string {
	switch cfg.Provider {
	case "codex":
		return "Codex via ChatGPT"
	case "lmstudio":
		return "LM Studio"
	case "openai":
		return "OpenAI"
	default:
		return "OpenRouter"
	}
}

func connectionLabel(cfg *config.Config) string {
	if cfg.Provider == "codex" {
		return "Codex Login"
	}
	if cfg.Provider == "lmstudio" {
		return "Connection"
	}
	if cfg.Provider == "openai" {
		return "API Key"
	}
	return "API Key"
}

func connectionSummary(cfg *config.Config) string {
	if cfg.Provider == "codex" {
		if auth, err := provider.LoadCodexCLIAuth(provider.CodexAuthPath()); err == nil {
			if auth.AccountID != "" {
				return "Found ChatGPT login"
			}
			return "Found ChatGPT login"
		}
		return "Run codex login"
	}
	if cfg.Provider == "lmstudio" {
		if strings.TrimSpace(cfg.BaseURL) == "" {
			return "Not configured"
		}
		return cfg.BaseURL
	}

	providerName := "openrouter"
	if cfg.Provider == "openai" {
		providerName = "openai"
	}
	key := cfg.GetAPIKey(providerName)
	if strings.TrimSpace(key) == "" {
		return "Not configured"
	}
	return maskKey(key)
}

func approvalSummary(cfg *config.Config) string {
	if cfg.SafetyMode == tools.SafetyModeUserConfirmAll {
		return "User approval before every command"
	}
	if cfg.SafetyMode == tools.SafetyModeUserConfirm {
		return "Manual user confirmation"
	}
	if cfg.SafetyMode == tools.SafetyModeUnrestricted {
		return "Unrestricted shell execution"
	}
	if strings.TrimSpace(cfg.SafetyModel) == "" {
		return "LLM judge"
	}
	return "LLM judge via " + cfg.SafetyModel
}

func routingSummary(cfg *config.Config) string {
	if !cfg.RoutingEnabled || cfg.RoutingMode == "disabled" {
		return "Default model only"
	}
	return "Auto within approved models"
}

func soulSummary(cfg *config.Config, profile soulProfile) (string, bool) {
	needsBootstrap, err := soulBootstrapRequired(cfg.MemoryDir)
	if err != nil {
		return "Soul file needs review", true
	}
	if !needsBootstrap {
		return "Existing soul.md is preserved", false
	}
	return "Bootstrap with " + profile.Label + " profile", true
}

func settingsMenuEntries(cfg *config.Config, profile soulProfile) []settingsMenuEntry {
	entries := []settingsMenuEntry{
		{
			Label:       "Provider",
			Description: providerSummary(cfg),
			Action:      settingsEditProvider,
		},
		{
			Label:       connectionLabel(cfg),
			Description: connectionSummary(cfg),
			Action:      settingsEditConnection,
		},
		{
			Label:       "Default Model",
			Description: cfg.PrimaryModel(),
			Action:      settingsEditModel,
		},
		{
			Label:       "Command Approval",
			Description: approvalSummary(cfg),
			Action:      settingsEditSafety,
		},
		{
			Label:       "Routing",
			Description: routingSummary(cfg),
			Action:      settingsEditRouting,
		},
		{
			Label:       "Max Tokens",
			Description: strconv.Itoa(cfg.MaxTokens),
			Action:      settingsEditTokens,
		},
		{
			Label:       "Max Iterations",
			Description: strconv.Itoa(cfg.MaxIterations),
			Action:      settingsEditIterations,
		},
		{
			Label:       "Log Level",
			Description: cfg.LogLevel,
			Action:      settingsEditLogLevel,
		},
	}

	if summary, editable := soulSummary(cfg, profile); editable {
		entries = append(entries, settingsMenuEntry{
			Label:       "Agent Soul",
			Description: summary,
			Action:      settingsEditSoul,
		})
	}

	entries = append(entries,
		settingsMenuEntry{
			Label:       "Review & Save",
			Description: "Write changes and exit",
			Action:      settingsReviewAndSave,
		},
		settingsMenuEntry{
			Label:       "Cancel",
			Description: "Exit without saving",
			Action:      settingsDiscardAndExit,
		},
	)

	return entries
}

func selectSettingsAction(cfg *config.Config, profile soulProfile, initial int, dirty bool) (settingsMenuAction, int) {
	renderSettingsHeader(dirty)

	entries := settingsMenuEntries(cfg, profile)
	items := make([][2]string, 0, len(entries))
	for _, entry := range entries {
		items = append(items, [2]string{entry.Label, entry.Description})
	}

	idx := selectList(items, initial, false)
	if idx < 0 || idx >= len(entries) {
		return settingsDiscardAndExit, len(entries) - 1
	}
	return entries[idx].Action, idx
}

func runProviderEditor(cfg *config.Config, modelsCh <-chan modelsResult, fetched *modelsResult) bool {
	before := cloneConfig(cfg)
	previousProvider := cfg.Provider

	if !wizardProvider(cfg) {
		return false
	}
	if cfg.Provider == previousProvider {
		return configChanged(before, cfg)
	}
	if !wizardAPIKey(cfg) {
		*cfg = *before
		return false
	}
	if !wizardModel(cfg, modelsForProvider(cfg, modelsCh, fetched)) {
		*cfg = *before
		return false
	}
	return configChanged(before, cfg)
}

func runConnectionEditor(cfg *config.Config) bool {
	before := cloneConfig(cfg)
	if !wizardAPIKey(cfg) {
		return false
	}
	return configChanged(before, cfg)
}

func runModelEditor(cfg *config.Config, modelsCh <-chan modelsResult, fetched *modelsResult) bool {
	before := cloneConfig(cfg)
	if !wizardModel(cfg, modelsForProvider(cfg, modelsCh, fetched)) {
		return false
	}
	return configChanged(before, cfg)
}

func runSafetyEditor(cfg *config.Config) bool {
	before := cloneConfig(cfg)
	if !wizardSafetyModel(cfg) {
		return false
	}
	return configChanged(before, cfg)
}

func runRoutingEditor(cfg *config.Config) bool {
	before := cloneConfig(cfg)
	if !wizardRouting(cfg) {
		return false
	}
	return configChanged(before, cfg)
}

func runTokensEditor(cfg *config.Config) bool {
	before := cloneConfig(cfg)
	if !wizardTokens(cfg) {
		return false
	}
	return configChanged(before, cfg)
}

func runIterationsEditor(cfg *config.Config) bool {
	before := cloneConfig(cfg)
	if !wizardIterations(cfg) {
		return false
	}
	return configChanged(before, cfg)
}

func runLogLevelEditor(cfg *config.Config) bool {
	before := cloneConfig(cfg)
	if !wizardLogLevel(cfg) {
		return false
	}
	return configChanged(before, cfg)
}

func runSoulEditor(cfg *config.Config, profile *soulProfile) bool {
	beforeProfile := *profile
	if !wizardSoulProfile(cfg, profile) {
		return false
	}
	return beforeProfile.ID != profile.ID
}

func reviewSettingsChanges(cfg *config.Config, profile soulProfile, dirty bool) bool {
	renderHeader()
	title := "Review Current Settings"
	if dirty {
		title = "Review & Save"
	}
	renderStep(11, totalSteps, title)
	renderReview(cfg)
	fmt.Println()

	if summary, editable := soulSummary(cfg, profile); editable {
		fmt.Printf("  %sSoul:%s  %s%s%s\n\n", fgMuted, ansiReset, ansiBold, fgWhite, summary+ansiReset)
	}

	choices := [][2]string{
		{"Save and exit", "Write configuration to disk"},
		{"Keep editing", "Return to the settings menu"},
	}
	idx := selectList(choices, 0, false)
	return idx == 0
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
	if len(existingCfg.FavoriteModels) > 0 {
		cfg.FavoriteModels = existingCfg.FavoriteModels
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
	var selectedHostNotes []string

	// ── Step loop with Backspace back-navigation ──────────────────────
	step := 1
	for step >= 1 && step <= totalSteps {
		var advanced bool
		switch step {
		case 1:
			advanced = wizardProvider(cfg)
		case 2:
			// Step 2: provider credential or connection details.
			advanced = wizardAPIKey(cfg)
		case 3:
			advanced = wizardModel(cfg, modelsForProvider(cfg, modelsCh, &fetchedModels))
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
			advanced = wizardMachineNotes(cfg, &selectedHostNotes)
		case 11:
			advanced = wizardConfirm(cfg, selectedSoulProfile, selectedHostNotes)
		}

		if advanced {
			step++
		} else if step > 1 {
			step--
		}
		// step == 1 and back pressed → stays at 1 (no previous step)
	}

	finalizeSetup(mode, cfg, selectedSoulProfile, selectedHostNotes)
}

func finalizeSetup(mode string, cfg *config.Config, selectedSoulProfile soulProfile, hostNotes []string) {
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
	hostNotesStatus, err := writeSetupHostNotes(cfg.MemoryDir, cfg.MemoryMaxSnippets, hostNotes)
	if err != nil {
		fmt.Printf("\n  %s%s✗  Failed to update runtime host notes: %v%s\n\n",
			ansiBold, fgRed, err, ansiReset)
		os.Exit(1)
	}

	successTitle := "Setup complete!"
	actionLabel := "Setup"
	if mode == "settings" {
		successTitle = "Settings updated!"
		actionLabel = "Settings"
	}

	fmt.Print(clearScreen)
	fmt.Println()
	fmt.Printf("  %s%s%s\n", fgGreen+ansiBold, hSep, ansiReset)
	fmt.Printf("  %s%s✔  %s%s Configuration saved.\n", ansiBold, fgGreen, successTitle, ansiReset)
	fmt.Printf("  %s%s%s\n\n", fgGreen+ansiBold, hSep, ansiReset)

	renderReview(cfg)

	fmt.Println()
	fmt.Printf("  %sTry running:%s\n\n", fgGray, ansiReset)
	fmt.Printf("  %s%s  cvkeharness run \"list all running docker containers\"%s\n\n",
		ansiBold, fgAccent, ansiReset)
	if wroteSoul {
		fmt.Printf("  %s%s generated %s~/.cvkeharness/soul.md%s and prepared the structured memory files in %s~/.cvkeharness/%s: operator.md, targets.md, host.md, playbooks.md, findings.md, cautions.md.%s\n",
			fgGray, actionLabel,
			fgAccent+ansiBold, ansiReset+fgGray,
			fgAccent+ansiBold, ansiReset+fgGray,
			ansiReset)
	} else {
		fmt.Printf("  %s%s preserved your existing %s~/.cvkeharness/soul.md%s and confirmed the structured memory files in %s~/.cvkeharness/%s: operator.md, targets.md, host.md, playbooks.md, findings.md, cautions.md.%s\n",
			fgGray, actionLabel,
			fgAccent+ansiBold, ansiReset+fgGray,
			fgAccent+ansiBold, ansiReset+fgGray,
			ansiReset)
	}
	fmt.Printf("  %sThe SQLite state file %s~/.cvkeharness/state.db%s will still be created on first run as needed.%s\n\n",
		fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
	switch hostNotesStatus {
	case setupHostNotesWritten:
		fmt.Printf("  %sSaved your runtime-host notes into %s~/.cvkeharness/host.md%s so machine quirks are part of retrieval from the first run.%s\n\n",
			fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
	case setupHostNotesPreserved:
		fmt.Printf("  %sPreserved the existing runtime-host notes in %s~/.cvkeharness/host.md%s.%s\n\n",
			fgGray, fgAccent+ansiBold, ansiReset+fgGray, ansiReset)
	}
}

func runSettingsMenu() {
	cfg := loadWizardConfig()

	modelsCh := make(chan modelsResult, 1)
	go func() { modelsCh <- fetchOpenRouterModels() }()

	var fetchedModels modelsResult
	selectedSoulProfile := defaultSoulProfile()
	dirty := false
	selectedIndex := 0

	for {
		action, idx := selectSettingsAction(cfg, selectedSoulProfile, selectedIndex, dirty)
		selectedIndex = idx

		switch action {
		case settingsEditProvider:
			dirty = runProviderEditor(cfg, modelsCh, &fetchedModels) || dirty
		case settingsEditConnection:
			dirty = runConnectionEditor(cfg) || dirty
		case settingsEditModel:
			dirty = runModelEditor(cfg, modelsCh, &fetchedModels) || dirty
		case settingsEditSafety:
			dirty = runSafetyEditor(cfg) || dirty
		case settingsEditRouting:
			dirty = runRoutingEditor(cfg) || dirty
		case settingsEditTokens:
			dirty = runTokensEditor(cfg) || dirty
		case settingsEditIterations:
			dirty = runIterationsEditor(cfg) || dirty
		case settingsEditLogLevel:
			dirty = runLogLevelEditor(cfg) || dirty
		case settingsEditSoul:
			dirty = runSoulEditor(cfg, &selectedSoulProfile) || dirty
		case settingsReviewAndSave:
			if reviewSettingsChanges(cfg, selectedSoulProfile, dirty) {
				finalizeSetup("settings", cfg, selectedSoulProfile, nil)
				return
			}
		case settingsDiscardAndExit:
			fmt.Print(clearScreen)
			fmt.Printf("\n  %s%s  Settings closed.%s No changes were saved.\n\n",
				ansiBold, fgYellow, ansiReset)
			return
		}
	}
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive onboarding wizard to configure the agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		return setuptui.Run()
	},
}

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Interactive settings editor for the agent",
	Run: func(cmd *cobra.Command, args []string) {
		runSettingsMenu()
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(settingsCmd)
}
