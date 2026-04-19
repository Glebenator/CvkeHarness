package provider

import (
	"context"
	"encoding/json"
)

// Provider abstracts the underlying LLM API.
type Provider interface {
	// ChatCompletion sends a request and gets a full structured response.
	ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}

// ChatRequest is the structured request to the model.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolDef
	Temperature float64
	MaxTokens   int
}

// Message represents a single turn in the conversation.
type Message struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // populated when model returns tools
	ToolCallID string     `json:"tool_call_id,omitempty"` // used when role is "tool"
}

// ToolCall represents a specific tool invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // usually "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction holds the parsed name and raw JSON arguments of a requested tool.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef defines a tool available to the model.
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function ToolFuncDef `json:"function"`
}

// ToolFuncDef describes a specific function's schema.
type ToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
}

// ChatResponse is the response from the provider.
type ChatResponse struct {
	Message      Message
	FinishReason string
	Usage        Usage
	Model        string
}

// Usage tracking for tokens.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
