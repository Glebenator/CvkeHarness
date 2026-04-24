package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/coolcake/cvkeharness/internal/httputil"
)

const openAIResponsesBaseURL = "https://api.openai.com/v1"

// OpenAI implements the Provider interface for the OpenAI Responses API.
type OpenAI struct {
	client      *httputil.Client
	baseURL     string
	token       string
	credential  func() (openAICredential, error)
	extraHeader map[string]string
	backend     openAIBackend
}

type openAIBackend int

const (
	openAIBackendResponses openAIBackend = iota
	openAIBackendCodex
)

type openAICredential struct {
	Token            string
	ChatGPTAccountID string
}

// NewOpenAI creates a new OpenAI API client using a bearer credential.
func NewOpenAI(token string) *OpenAI {
	return NewOpenAIWithBaseURL(token, openAIResponsesBaseURL)
}

// NewOpenAIWithBaseURL creates an OpenAI client pointed at a custom base URL.
func NewOpenAIWithBaseURL(token, baseURL string) *OpenAI {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = openAIResponsesBaseURL
	}
	return &OpenAI{
		client:  httputil.NewDefaultClient(),
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		backend: openAIBackendResponses,
	}
}

func newOpenAIWithCredential(baseURL string, credential func() (openAICredential, error), headers map[string]string, backend openAIBackend) *OpenAI {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = openAIResponsesBaseURL
	}
	return &OpenAI{
		client:      httputil.NewDefaultClient(),
		baseURL:     strings.TrimRight(baseURL, "/"),
		credential:  credential,
		extraHeader: headers,
		backend:     backend,
	}
}

type openAIRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions"`
	Input           []json.RawMessage `json:"input"`
	Tools           []openAITool      `json:"tools,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Include         []string          `json:"include,omitempty"`
	Store           *bool             `json:"store,omitempty"`
	Stream          bool              `json:"stream"`
}

type openAITool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIOutputItem struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Status    string `json:"status,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content,omitempty"`
}

type openAIResponse struct {
	ID         string            `json:"id"`
	Model      string            `json:"model"`
	Status     string            `json:"status"`
	Output     []json.RawMessage `json:"output"`
	OutputText string            `json:"output_text"`
	Usage      openAIUsage       `json:"usage"`
	Error      *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

type openAIUsage struct {
	InputTokens        int                 `json:"input_tokens"`
	OutputTokens       int                 `json:"output_tokens"`
	TotalTokens        int                 `json:"total_tokens"`
	PromptTokens       int                 `json:"prompt_tokens"`
	CompletionTokens   int                 `json:"completion_tokens"`
	InputTokenDetails  *openAITokenDetails `json:"input_tokens_details,omitempty"`
	PromptTokenDetails *PromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

type openAITokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

func (u openAIUsage) usage() Usage {
	promptTokens := u.InputTokens
	if promptTokens == 0 {
		promptTokens = u.PromptTokens
	}
	completionTokens := u.OutputTokens
	if completionTokens == 0 {
		completionTokens = u.CompletionTokens
	}
	totalTokens := u.TotalTokens
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}

	var details *PromptTokenDetails
	switch {
	case u.InputTokenDetails != nil:
		details = &PromptTokenDetails{CachedTokens: u.InputTokenDetails.CachedTokens}
	case u.PromptTokenDetails != nil:
		details = u.PromptTokenDetails
	}

	return Usage{
		PromptTokens:       promptTokens,
		CompletionTokens:   completionTokens,
		TotalTokens:        totalTokens,
		PromptTokenDetails: details,
	}
}

// ChatCompletion executes a Responses API request and returns the completed
// response, even when the provider streams internally.
func (o *OpenAI) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	instructions, input, err := openAIInstructionsAndInputItems(req.Messages)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := o.marshalRequest(req, instructions, input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/responses", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	credential, err := o.getCredential()
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+credential.Token)
	if credential.ChatGPTAccountID != "" {
		httpReq.Header.Set("ChatGPT-Account-ID", credential.ChatGPTAccountID)
	}
	for key, value := range o.extraHeader {
		httpReq.Header.Set(key, value)
	}

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to OpenAI failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		var apiResp openAIResponse
		_ = json.Unmarshal(respBody, &apiResp)
		if apiResp.Error != nil && apiResp.Error.Message != "" {
			return nil, fmt.Errorf("OpenAI API error (status %d): %s", httpResp.StatusCode, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", httpResp.StatusCode, string(respBody))
	}

	apiResp, err := parseOpenAIResponseBody(httpResp.Header.Get("Content-Type"), respBody)
	if err != nil {
		return nil, err
	}

	if apiResp.Error != nil && apiResp.Error.Message != "" {
		return nil, fmt.Errorf("OpenAI returned error: %s (type: %s)", apiResp.Error.Message, apiResp.Error.Type)
	}
	if len(apiResp.Output) == 0 && strings.TrimSpace(apiResp.OutputText) == "" {
		return nil, fmt.Errorf("OpenAI returned no output")
	}

	message, err := openAIMessageFromResponse(apiResp)
	if err != nil {
		return nil, err
	}

	return &ChatResponse{
		Message:      message,
		FinishReason: apiResp.Status,
		Usage:        apiResp.Usage.usage(),
		Model:        apiResp.Model,
	}, nil
}

func (o *OpenAI) marshalRequest(req *ChatRequest, instructions string, input []json.RawMessage) ([]byte, error) {
	store := false
	request := openAIRequest{
		Model:        req.Model,
		Instructions: instructions,
		Input:        input,
		Tools:        openAITools(req.Tools),
		Store:        &store,
		Stream:       true,
	}
	if o.backend == openAIBackendResponses {
		request.MaxOutputTokens = req.MaxTokens
		request.Include = []string{"reasoning.encrypted_content"}
	}
	return json.Marshal(request)
}

func parseOpenAIResponseBody(contentType string, body []byte) (openAIResponse, error) {
	if looksLikeEventStream(contentType, body) {
		return parseOpenAIStreamResponse(body)
	}

	var apiResp openAIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return openAIResponse{}, fmt.Errorf("failed to unmarshal JSON response: %w\nResponse was: %s", err, string(body))
	}
	return apiResp, nil
}

func looksLikeEventStream(contentType string, body []byte) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream") {
		return true
	}
	trimmed := bytes.TrimSpace(body)
	return bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:"))
}

func parseOpenAIStreamResponse(body []byte) (openAIResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var dataLines []string
	var eventName string
	var completed *openAIResponse
	var streamErr error
	var streamedOutput []json.RawMessage
	var outputTextParts []string

	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]

		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			return nil
		}

		var event struct {
			Type     string         `json:"type"`
			Response openAIResponse `json:"response"`
			Error    *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("failed to unmarshal streamed event: %w\nEvent was: %s", err, payload)
		}
		if strings.TrimSpace(event.Type) == "" {
			event.Type = eventName
		}
		eventName = ""

		switch event.Type {
		case "response.output_item.done":
			var outputItemEvent struct {
				Item json.RawMessage `json:"item"`
			}
			if err := json.Unmarshal([]byte(payload), &outputItemEvent); err != nil {
				return fmt.Errorf("failed to unmarshal output item event: %w\nEvent was: %s", err, payload)
			}
			if len(bytes.TrimSpace(outputItemEvent.Item)) > 0 {
				streamedOutput = append(streamedOutput, append(json.RawMessage(nil), outputItemEvent.Item...))
			}
		case "response.output_text.done":
			var outputTextEvent struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(payload), &outputTextEvent); err != nil {
				return fmt.Errorf("failed to unmarshal output text event: %w\nEvent was: %s", err, payload)
			}
			if strings.TrimSpace(outputTextEvent.Text) != "" {
				outputTextParts = append(outputTextParts, outputTextEvent.Text)
			}
		case "response.function_call_arguments.done":
			var argsDoneEvent struct {
				ItemID    string `json:"item_id"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(payload), &argsDoneEvent); err != nil {
				return fmt.Errorf("failed to unmarshal function call arguments event: %w\nEvent was: %s", err, payload)
			}
			if strings.TrimSpace(argsDoneEvent.CallID) != "" && strings.TrimSpace(argsDoneEvent.Name) != "" {
				raw, err := json.Marshal(map[string]any{
					"id":        firstNonEmpty(argsDoneEvent.ItemID, argsDoneEvent.CallID),
					"type":      "function_call",
					"call_id":   argsDoneEvent.CallID,
					"name":      argsDoneEvent.Name,
					"arguments": argsDoneEvent.Arguments,
					"status":    "completed",
				})
				if err != nil {
					return fmt.Errorf("failed to marshal function call item: %w", err)
				}
				streamedOutput = append(streamedOutput, raw)
			}
		case "response.completed":
			resp := event.Response
			if len(resp.Output) == 0 && len(streamedOutput) > 0 {
				resp.Output = append([]json.RawMessage(nil), streamedOutput...)
			}
			if strings.TrimSpace(resp.OutputText) == "" && len(outputTextParts) > 0 {
				resp.OutputText = strings.Join(outputTextParts, "")
			}
			completed = &resp
		case "response.failed", "error":
			if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
				streamErr = fmt.Errorf("OpenAI stream error: %s", event.Error.Message)
				return nil
			}
			if event.Response.Error != nil && strings.TrimSpace(event.Response.Error.Message) != "" {
				streamErr = fmt.Errorf("OpenAI stream error: %s", event.Response.Error.Message)
				return nil
			}
			streamErr = fmt.Errorf("OpenAI stream error: %s", payload)
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return openAIResponse{}, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return openAIResponse{}, fmt.Errorf("failed to read streamed response: %w", err)
	}
	if err := flush(); err != nil {
		return openAIResponse{}, err
	}
	if streamErr != nil {
		return openAIResponse{}, streamErr
	}
	if completed == nil {
		return openAIResponse{}, fmt.Errorf("OpenAI stream ended without response.completed event")
	}
	return *completed, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (o *OpenAI) getCredential() (openAICredential, error) {
	if o.credential != nil {
		credential, err := o.credential()
		if err != nil {
			return openAICredential{}, err
		}
		if strings.TrimSpace(credential.Token) == "" {
			return openAICredential{}, fmt.Errorf("OpenAI credential is empty")
		}
		credential.Token = strings.TrimSpace(credential.Token)
		credential.ChatGPTAccountID = strings.TrimSpace(credential.ChatGPTAccountID)
		return credential, nil
	}
	if strings.TrimSpace(o.token) == "" {
		return openAICredential{}, fmt.Errorf("OpenAI credential is empty")
	}
	return openAICredential{Token: strings.TrimSpace(o.token)}, nil
}

func openAIInstructionsAndInputItems(messages []Message) (string, []json.RawMessage, error) {
	var instructions []string
	items := make([]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		if role == "system" || role == "developer" {
			if text := strings.TrimSpace(msg.Content); text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		if len(msg.ResponseItems) > 0 {
			items = append(items, msg.ResponseItems...)
			continue
		}

		switch role {
		case "tool":
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			raw, err := json.Marshal(map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
			if err != nil {
				return "", nil, fmt.Errorf("failed to marshal tool result: %w", err)
			}
			items = append(items, raw)
			continue
		case "system", "developer", "user", "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				raw, err := json.Marshal(map[string]any{
					"type":    "message",
					"role":    role,
					"content": msg.Content,
				})
				if err != nil {
					return "", nil, fmt.Errorf("failed to marshal message: %w", err)
				}
				items = append(items, raw)
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					continue
				}
				raw, err := json.Marshal(map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      call.Function.Name,
					"arguments": call.Function.Arguments,
				})
				if err != nil {
					return "", nil, fmt.Errorf("failed to marshal function call: %w", err)
				}
				items = append(items, raw)
			}
		default:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			raw, err := json.Marshal(map[string]any{
				"type":    "message",
				"role":    "user",
				"content": msg.Content,
			})
			if err != nil {
				return "", nil, fmt.Errorf("failed to marshal message: %w", err)
			}
			items = append(items, raw)
		}
	}
	return openAIInstructions(instructions), items, nil
}

func openAIInstructions(parts []string) string {
	if len(parts) == 0 {
		return "Follow the conversation and available tool contracts."
	}
	return strings.Join(parts, "\n\n")
}

func openAITools(tools []ToolDef) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" || strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		out = append(out, openAITool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return out
}

func openAIMessageFromResponse(resp openAIResponse) (Message, error) {
	var contentParts []string
	var toolCalls []ToolCall

	for _, raw := range resp.Output {
		var item openAIOutputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return Message{}, fmt.Errorf("failed to unmarshal OpenAI output item: %w", err)
		}

		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && strings.TrimSpace(part.Text) != "" {
					contentParts = append(contentParts, part.Text)
				}
			}
		case "function_call":
			callID := item.CallID
			if strings.TrimSpace(callID) == "" {
				callID = item.ID
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   callID,
				Type: "function",
				Function: ToolFunction{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}

	content := strings.TrimSpace(strings.Join(contentParts, "\n"))
	if content == "" {
		content = strings.TrimSpace(resp.OutputText)
	}

	return Message{
		Role:          "assistant",
		Content:       content,
		ToolCalls:     toolCalls,
		ResponseItems: append([]json.RawMessage(nil), resp.Output...),
	}, nil
}
