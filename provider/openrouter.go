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

// OpenRouter implements the Provider interface for the OpenRouter API.
type OpenRouter struct {
	client  *httputil.Client
	baseURL string
	apiKey  string
}

// NewOpenRouter creates a new OpenRouter API client.
func NewOpenRouter(apiKey string) *OpenRouter {
	return &OpenRouter{
		client:  httputil.NewDefaultClient(),
		baseURL: "https://openrouter.ai/api/v1",
		apiKey:  apiKey,
	}
}

// openRouterRequest represents the JSON payload to the OpenRouter API.
type openRouterRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// openRouterResponse represents the JSON response from OpenRouter API.
type openRouterResponse struct {
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

// ChatCompletion executes a non-streaming chat completion request.
func (o *OpenRouter) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Map internal request types to OpenRouter structure
	apiReq := openRouterRequest{
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/coolcake/cvkeharness") // OpenRouter requests this
	httpReq.Header.Set("X-Title", "CvkeHarness")                                  // Optional, but good practice for OpenRouter

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to OpenRouter failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("OpenRouter API error (status %d): %s", httpResp.StatusCode, string(respBody))
	}

	var apiResp openRouterResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w\nResponse was: %s", err, string(respBody))
	}

	if apiResp.Error.Message != "" {
		return nil, fmt.Errorf("OpenRouter returned error: %s (type: %s)", apiResp.Error.Message, apiResp.Error.Type)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("OpenRouter returned no choices")
	}

	choice := apiResp.Choices[0]
	return &ChatResponse{
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
		Usage:        apiResp.Usage,
		Model:        apiResp.Model,
	}, nil
}
