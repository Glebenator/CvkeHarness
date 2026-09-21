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
	"time"
)

const antigravityEndpoint = "https://daily-cloudcode-pa.sandbox.googleapis.com"

// Antigravity uses a personal Google OAuth session. This is an unofficial
// integration; Google may change the protocol or deny third-party access.
// It only requests model completions; tools remain owned by CvkeHarness.
type Antigravity struct {
	client   *http.Client
	endpoint string
	authPath string
}

func NewAntigravity() *Antigravity {
	return &Antigravity{client: antigravityHTTPClient(), endpoint: antigravityEndpoint, authPath: AntigravityAuthPath()}
}

func antigravityHTTPClient() *http.Client {
	return &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type agPart struct {
	Text             string    `json:"text,omitempty"`
	Thought          bool      `json:"thought,omitempty"`
	ThoughtSignature string    `json:"thoughtSignature,omitempty"`
	FunctionCall     *agCall   `json:"functionCall,omitempty"`
	FunctionResponse *agResult `json:"functionResponse,omitempty"`
}
type agCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
	ID   string          `json:"id,omitempty"`
}
type agResult struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
	ID       string `json:"id,omitempty"`
}
type agContent struct {
	Role  string            `json:"role"`
	Parts []json.RawMessage `json:"parts"`
}
type agSavedPart struct {
	Part json.RawMessage `json:"cvke_antigravity_part"`
}

func agRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func antigravityRequest(req *ChatRequest, project string) ([]byte, error) {
	if req == nil || !strings.HasPrefix(req.Model, "gemini-") {
		return nil, fmt.Errorf("Antigravity requires a Gemini model ID")
	}
	var contents []agContent
	var system []string
	calls := map[string]agCall{}
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		c := agContent{Role: "user"}
		switch m.Role {
		case "assistant":
			c.Role = "model"
			for _, tc := range m.ToolCalls {
				calls[tc.ID] = agCall{Name: tc.Function.Name, Args: json.RawMessage(tc.Function.Arguments), ID: tc.ID}
			}
			for _, saved := range m.ResponseItems {
				var item agSavedPart
				if json.Unmarshal(saved, &item) == nil && len(item.Part) > 0 {
					c.Parts = append(c.Parts, item.Part)
					var part agPart
					if json.Unmarshal(item.Part, &part) == nil && part.FunctionCall != nil {
						for _, tc := range m.ToolCalls {
							if tc.Function.Name == part.FunctionCall.Name && (part.FunctionCall.ID == "" || part.FunctionCall.ID == tc.ID) {
								calls[tc.ID] = *part.FunctionCall
							}
						}
					}
				}
			}
			if len(c.Parts) == 0 {
				if len(m.ToolCalls) > 0 {
					return nil, fmt.Errorf("Antigravity tool history is missing native thought signatures; start a new task after switching providers")
				}
				if m.Content != "" {
					c.Parts = append(c.Parts, agRaw(agPart{Text: m.Content}))
				}
			}
		case "tool":
			call, ok := calls[m.ToolCallID]
			if !ok {
				return nil, fmt.Errorf("Antigravity tool result has no matching call")
			}
			c.Parts = append(c.Parts, agRaw(agPart{FunctionResponse: &agResult{Name: call.Name, ID: call.ID, Response: map[string]string{"result": m.Content}}}))
		case "user":
			c.Parts = append(c.Parts, agRaw(agPart{Text: m.Content}))
		default:
			return nil, fmt.Errorf("unsupported Antigravity message role %q", m.Role)
		}
		if len(c.Parts) > 0 {
			if len(contents) > 0 && contents[len(contents)-1].Role == c.Role {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, c.Parts...)
			} else {
				contents = append(contents, c)
			}
		}
	}
	generation := map[string]any{"temperature": req.Temperature}
	if req.MaxTokens > 0 {
		generation["maxOutputTokens"] = req.MaxTokens
	}
	request := map[string]any{"contents": contents, "generationConfig": generation}
	if len(system) > 0 {
		request["systemInstruction"] = map[string]any{"parts": []agPart{{Text: strings.Join(system, "\n\n")}}}
	}
	if len(req.Tools) > 0 {
		declarations := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			if !json.Valid(t.Function.Parameters) {
				return nil, fmt.Errorf("invalid schema for tool %s", t.Function.Name)
			}
			declarations = append(declarations, map[string]any{"name": t.Function.Name, "description": t.Function.Description, "parametersJsonSchema": t.Function.Parameters})
		}
		request["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	return json.Marshal(map[string]any{"project": project, "model": req.Model, "request": request, "requestType": "agent", "userAgent": "cvkeharness", "requestId": "agent-" + randomAGString()})
}

func (a *Antigravity) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	auth, err := antigravityCredential(ctx, a.client, a.authPath)
	if err != nil {
		return nil, err
	}
	body, err := antigravityRequest(req, auth.ProjectID)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+"/v1internal:streamGenerateContent?alt=sse", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("User-Agent", "antigravity/1.107.0 CvkeHarness")
	res, err := a.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("Antigravity request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, antigravityStatusError(res.StatusCode)
	}
	return parseAntigravityStream(res.Body, req.Model)
}
func antigravityStatusError(status int) error {
	switch status {
	case 401:
		return fmt.Errorf("Antigravity login expired or revoked; run `cvkeharness antigravity login`")
	case 403:
		return fmt.Errorf("Antigravity denied access (403); check account eligibility and third-party access restrictions")
	case 429:
		return fmt.Errorf("Antigravity quota exhausted (429); wait for the quota reset")
	default:
		return fmt.Errorf("Antigravity returned HTTP %d", status)
	}
}
func parseAntigravityStream(r io.Reader, model string) (*ChatResponse, error) {
	out := &ChatResponse{Model: model, Message: Message{Role: "assistant"}}
	scanner := bufio.NewScanner(io.LimitReader(r, 16*1024*1024+1))
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	var data []string
	seen := false
	consume := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		if raw == "[DONE]" {
			return nil
		}
		var chunk struct {
			Error    json.RawMessage `json:"error"`
			Response struct {
				Candidates []struct {
					Content      agContent `json:"content"`
					FinishReason string    `json:"finishReason"`
				} `json:"candidates"`
				Usage struct {
					Prompt     int `json:"promptTokenCount"`
					Completion int `json:"candidatesTokenCount"`
					Thoughts   int `json:"thoughtsTokenCount"`
					Total      int `json:"totalTokenCount"`
					Cached     int `json:"cachedContentTokenCount"`
				} `json:"usageMetadata"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(raw), &chunk) != nil {
			return fmt.Errorf("invalid Antigravity stream JSON")
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return fmt.Errorf("Antigravity returned a streaming error")
		}
		u := chunk.Response.Usage
		if u.Total > 0 {
			out.Usage = Usage{PromptTokens: u.Prompt, CompletionTokens: u.Completion + u.Thoughts, TotalTokens: u.Total, PromptTokenDetails: &PromptTokenDetails{CachedTokens: u.Cached}}
		}
		if len(chunk.Response.Candidates) == 0 {
			return nil
		}
		c := chunk.Response.Candidates[0]
		for _, rawPart := range c.Content.Parts {
			var part agPart
			if json.Unmarshal(rawPart, &part) != nil {
				return fmt.Errorf("invalid Antigravity response part")
			}
			seen = true
			out.Message.ResponseItems = append(out.Message.ResponseItems, agRaw(agSavedPart{Part: rawPart}))
			if !part.Thought {
				out.Message.Content += part.Text
			}
			if call := part.FunctionCall; call != nil {
				if call.Name == "" || !json.Valid(call.Args) {
					return fmt.Errorf("invalid Antigravity tool call")
				}
				id := call.ID
				if id == "" {
					id = "ag_" + randomAGString()
				}
				out.Message.ToolCalls = append(out.Message.ToolCalls, ToolCall{ID: id, Type: "function", Function: ToolFunction{Name: call.Name, Arguments: string(call.Args)}})
			}
		}
		if c.FinishReason != "" {
			out.FinishReason = c.FinishReason
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := consume(); err != nil {
				return nil, err
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("Antigravity stream read failed: %w", err)
	}
	if err := consume(); err != nil {
		return nil, err
	}
	if !seen || out.FinishReason == "" {
		return nil, fmt.Errorf("Antigravity stream ended without a completed response")
	}
	if out.FinishReason != "STOP" && out.FinishReason != "MAX_TOKENS" {
		return nil, fmt.Errorf("Antigravity response blocked or incomplete (%s)", out.FinishReason)
	}
	if len(out.Message.ToolCalls) > 0 && out.FinishReason == "MAX_TOKENS" {
		return nil, fmt.Errorf("Antigravity tool response exceeded the token limit; increase max_tokens")
	}
	if len(out.Message.ToolCalls) > 0 {
		out.FinishReason = "tool_calls"
	} else if out.FinishReason == "MAX_TOKENS" {
		out.FinishReason = "length"
	} else {
		out.FinishReason = "stop"
	}
	return out, nil
}
