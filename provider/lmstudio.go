package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/coolcake/cvkeharness/internal/httputil"
)

// LMStudio implements the Provider interface for a local LM Studio instance.
type LMStudio struct {
	client  *httputil.Client
	baseURL string
}

// NewLMStudio creates a new LM Studio API client.
func NewLMStudio(baseURL string) *LMStudio {
	if baseURL == "" {
		baseURL = "http://localhost:1234/v1"
	}
	return &LMStudio{
		client:  httputil.NewDefaultClient(),
		baseURL: baseURL,
	}
}

// lmStudioRequest represents the JSON payload to the LM Studio API.
// It uses the standard OpenAI-compatible schema.
type lmStudioRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// lmStudioResponse represents the JSON response from LM Studio.
type lmStudioResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ChatCompletion executes a non-streaming chat completion request against LM Studio.
func (l *LMStudio) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	apiReq := lmStudioRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Tools:       req.Tools,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	bodyBytes, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	// LM Studio typically doesn't strictly require a token, but OpenAI clients usually send one
	httpReq.Header.Set("Authorization", "Bearer lm-studio")

	httpResp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to LM Studio failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("LM Studio API error (status %d): %s", httpResp.StatusCode, string(respBody))
	}

	var apiResp lmStudioResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w\nResponse was: %s", err, string(respBody))
	}

	if apiResp.Error.Message != "" {
		return nil, fmt.Errorf("LM Studio returned error: %s (type: %s)", apiResp.Error.Message, apiResp.Error.Type)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("LM Studio returned no choices")
	}

	choice := apiResp.Choices[0]
	return &ChatResponse{
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
		Usage:        apiResp.Usage,
		Model:        apiResp.Model,
	}, nil
}
