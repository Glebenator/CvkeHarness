package provider

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const codexResponsesBaseURL = "https://chatgpt.com/backend-api/codex"

const (
	codexOriginatorHeader = "Originator"
	codexOriginator       = "codex_cli_rs"
)

// NewCodexFromCLIAuth creates a provider that reuses the official Codex CLI
// ChatGPT login cache. Token refresh remains owned by Codex CLI.
func NewCodexFromCLIAuth() *OpenAI {
	return NewCodexWithAuthPath(CodexAuthPath())
}

// NewCodexWithAuthPath creates a Codex subscription provider for tests or
// custom auth-cache locations.
func NewCodexWithAuthPath(path string) *OpenAI {
	return newOpenAIWithCredential(codexResponsesBaseURL, func() (openAICredential, error) {
		auth, err := LoadCodexCLIAuth(path)
		if err != nil {
			return openAICredential{}, err
		}
		return openAICredential{
			Token:            auth.AccessToken,
			ChatGPTAccountID: auth.AccountID,
		}, nil
	}, map[string]string{
		codexOriginatorHeader: codexOriginator,
		"User-Agent":          codexUserAgent(),
	}, openAIBackendCodex)
}

// CodexCLIAuth contains the subset of the official Codex auth cache needed to
// call the ChatGPT Codex backend.
type CodexCLIAuth struct {
	AccessToken string
	AccountID   string
	AuthPath    string
}

// CodexAuthPath returns the official Codex CLI auth cache path.
func CodexAuthPath() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

// LoadCodexCLIAuth reads the Codex CLI ChatGPT OAuth auth cache without
// refreshing it. This avoids racing Codex CLI's single-use refresh tokens.
func LoadCodexCLIAuth(path string) (CodexCLIAuth, error) {
	if strings.TrimSpace(path) == "" {
		return CodexCLIAuth{}, fmt.Errorf("Codex auth path could not be resolved")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CodexCLIAuth{}, fmt.Errorf("Codex CLI login not found at %s; run `codex login` and choose Sign in with ChatGPT", path)
		}
		return CodexCLIAuth{}, fmt.Errorf("failed to read Codex CLI auth at %s: %w", path, err)
	}

	var raw struct {
		AuthMode          string `json:"auth_mode"`
		OpenAIAPIKey      string `json:"openai_api_key"`
		UpperOpenAIAPIKey string `json:"OPENAI_API_KEY"`
		Tokens            struct {
			AccessToken string          `json:"access_token"`
			AccountID   string          `json:"account_id"`
			IDToken     json.RawMessage `json:"id_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CodexCLIAuth{}, fmt.Errorf("failed to parse Codex CLI auth at %s: %w", path, err)
	}

	if strings.EqualFold(raw.AuthMode, "apikey") || strings.TrimSpace(raw.OpenAIAPIKey) != "" || strings.TrimSpace(raw.UpperOpenAIAPIKey) != "" {
		return CodexCLIAuth{}, fmt.Errorf("Codex CLI auth at %s is API-key mode; run `codex logout` then `codex login` and choose Sign in with ChatGPT", path)
	}

	accessToken := strings.TrimSpace(raw.Tokens.AccessToken)
	if accessToken == "" {
		return CodexCLIAuth{}, fmt.Errorf("Codex CLI auth at %s does not contain a ChatGPT access token; run `codex login`", path)
	}

	accountID := strings.TrimSpace(raw.Tokens.AccountID)
	if accountID == "" {
		accountID = chatGPTAccountIDFromIDToken(raw.Tokens.IDToken)
	}

	return CodexCLIAuth{
		AccessToken: accessToken,
		AccountID:   accountID,
		AuthPath:    path,
	}, nil
}

func chatGPTAccountIDFromIDToken(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var object struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(raw, &object); err == nil && strings.TrimSpace(object.ChatGPTAccountID) != "" {
		return strings.TrimSpace(object.ChatGPTAccountID)
	}

	var token string
	if err := json.Unmarshal(raw, &token); err != nil {
		return ""
	}
	return chatGPTAccountIDFromJWT(token)
}

func chatGPTAccountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}

	payload, err := decodeJWTPart(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		Auth             struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(claims.ChatGPTAccountID, claims.Auth.ChatGPTAccountID))
}

func decodeJWTPart(part string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(part)
}

func codexUserAgent() string {
	return fmt.Sprintf("%s/0.0.1 (%s; %s) CvkeHarness", codexOriginator, runtime.GOOS, runtime.GOARCH)
}
