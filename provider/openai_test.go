package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIChatCompletionMapsResponsesFunctionCall(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("expected /responses path, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected auth header %q", got)
		}

		var body struct {
			Model        string `json:"model"`
			Instructions string `json:"instructions"`
			Stream       bool   `json:"stream"`
			Input        []struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"input"`
			Tools []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tools"`
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Model != "gpt-5.2-codex" {
			t.Fatalf("unexpected model %q", body.Model)
		}
		if body.Instructions == "" {
			t.Fatal("expected instructions to always be sent")
		}
		if !body.Stream {
			t.Fatal("expected stream=true")
		}
		if len(body.Input) != 1 || body.Input[0].Role != "user" || body.Input[0].Content != "list files" {
			t.Fatalf("unexpected input %#v", body.Input)
		}
		if len(body.Tools) != 1 || body.Tools[0].Type != "function" || body.Tools[0].Name != "shell" {
			t.Fatalf("unexpected tools %#v", body.Tools)
		}
		if body.MaxOutputTokens != 512 {
			t.Fatalf("unexpected max output tokens %d", body.MaxOutputTokens)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.2-codex\",\"status\":\"completed\",\"output\":[{\"id\":\"rs_123\",\"type\":\"reasoning\",\"summary\":[]},{\"id\":\"fc_123\",\"type\":\"function_call\",\"call_id\":\"call_123\",\"name\":\"shell\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\",\"status\":\"completed\"}],\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n"))
	}))
	defer server.Close()

	client := NewOpenAIWithBaseURL("test-token", server.URL)
	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Model:     "gpt-5.2-codex",
		Messages:  []Message{{Role: "user", Content: "list files"}},
		MaxTokens: 512,
		Tools: []ToolDef{{
			Type: "function",
			Function: ToolFuncDef{
				Name:        "shell",
				Description: "Run a shell command",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if resp.Model != "gpt-5.2-codex" {
		t.Fatalf("unexpected response model %q", resp.Model)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_123" || call.Function.Name != "shell" || call.Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("unexpected tool call %#v", call)
	}
	if len(resp.Message.ResponseItems) != 2 {
		t.Fatalf("expected raw response items to be preserved, got %d", len(resp.Message.ResponseItems))
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Fatalf("unexpected usage %#v", resp.Usage)
	}
	if cached, ok := resp.Usage.CachedTokens(); !ok || cached != 2 {
		t.Fatalf("unexpected cached token details cached=%d ok=%v", cached, ok)
	}
}

func TestParseOpenAIStreamResponseUsesCompletedEvent(t *testing.T) {
	t.Parallel()

	resp, err := parseOpenAIResponseBody("text/event-stream", []byte(
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.2-codex\",\"status\":\"in_progress\"}}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.2-codex\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n"+
			"data: [DONE]\n\n",
	))
	if err != nil {
		t.Fatalf("parseOpenAIResponseBody returned error: %v", err)
	}
	if resp.Status != "completed" || resp.Model != "gpt-5.2-codex" {
		t.Fatalf("unexpected response %#v", resp)
	}
	if resp.OutputText != "" && resp.OutputText != "ok" {
		t.Fatalf("unexpected output text %q", resp.OutputText)
	}
}

func TestParseOpenAIStreamResponseHandlesExplicitEventLines(t *testing.T) {
	t.Parallel()

	resp, err := parseOpenAIResponseBody("", []byte(
		"event: response.created\n"+
			"data: {\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.2-codex\",\"status\":\"in_progress\"}}\n\n"+
			"event: response.completed\n"+
			"data: {\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.2-codex\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n",
	))
	if err != nil {
		t.Fatalf("parseOpenAIResponseBody returned error: %v", err)
	}
	if resp.Status != "completed" || resp.Model != "gpt-5.2-codex" {
		t.Fatalf("unexpected response %#v", resp)
	}
}

func TestParseOpenAIStreamResponseBuildsOutputFromItemDoneEvents(t *testing.T) {
	t.Parallel()

	resp, err := parseOpenAIResponseBody("", []byte(
		"event: response.output_item.done\n"+
			"data: {\"item\":{\"id\":\"msg_123\",\"type\":\"message\",\"status\":\"completed\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\",\"annotations\":[]}]}}\n\n"+
			"event: response.completed\n"+
			"data: {\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.2-codex\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n",
	))
	if err != nil {
		t.Fatalf("parseOpenAIResponseBody returned error: %v", err)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("expected one synthesized output item, got %d", len(resp.Output))
	}
	message, err := openAIMessageFromResponse(resp)
	if err != nil {
		t.Fatalf("openAIMessageFromResponse returned error: %v", err)
	}
	if message.Content != "ok" {
		t.Fatalf("unexpected message content %q", message.Content)
	}
}

func TestParseOpenAIStreamResponseBuildsFunctionCallFromArgumentsDone(t *testing.T) {
	t.Parallel()

	resp, err := parseOpenAIResponseBody("", []byte(
		"event: response.function_call_arguments.done\n"+
			"data: {\"item_id\":\"fc_123\",\"call_id\":\"call_123\",\"name\":\"shell\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}\n\n"+
			"event: response.completed\n"+
			"data: {\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5.2-codex\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n",
	))
	if err != nil {
		t.Fatalf("parseOpenAIResponseBody returned error: %v", err)
	}
	message, err := openAIMessageFromResponse(resp)
	if err != nil {
		t.Fatalf("openAIMessageFromResponse returned error: %v", err)
	}
	if len(message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", message.ToolCalls)
	}
	if message.ToolCalls[0].ID != "call_123" || message.ToolCalls[0].Function.Name != "shell" {
		t.Fatalf("unexpected tool call %#v", message.ToolCalls[0])
	}
}

func TestParseOpenAIStreamResponseReturnsErrorEvent(t *testing.T) {
	t.Parallel()

	_, err := parseOpenAIResponseBody("text/event-stream", []byte(
		"data: {\"type\":\"error\",\"error\":{\"message\":\"boom\",\"type\":\"invalid_request_error\"}}\n\n",
	))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stream error, got %v", err)
	}
}

func TestMarshalRequestUsesResponsesSchemaForOpenAI(t *testing.T) {
	t.Parallel()

	client := NewOpenAIWithBaseURL("test-token", "https://api.openai.com/v1")
	body, err := client.marshalRequest(&ChatRequest{
		Model:     "gpt-5.4",
		MaxTokens: 321,
	}, "system rules", []json.RawMessage{json.RawMessage(`{"type":"message","role":"user","content":"hi"}`)})
	if err != nil {
		t.Fatalf("marshalRequest returned error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if parsed["instructions"] != "system rules" {
		t.Fatalf("unexpected instructions %#v", parsed["instructions"])
	}
	if parsed["max_output_tokens"] != float64(321) {
		t.Fatalf("unexpected max_output_tokens %#v", parsed["max_output_tokens"])
	}
}

func TestOpenAIInstructionsAndInputItemsMovesSystemMessagesToInstructions(t *testing.T) {
	t.Parallel()

	instructions, items, err := openAIInstructionsAndInputItems([]Message{
		{Role: "system", Content: "system rules"},
		{Role: "developer", Content: "developer rules"},
		{Role: "user", Content: "list files"},
	})
	if err != nil {
		t.Fatalf("openAIInstructionsAndInputItems returned error: %v", err)
	}
	if instructions != "system rules\n\ndeveloper rules" {
		t.Fatalf("unexpected instructions %q", instructions)
	}
	if len(items) != 1 {
		t.Fatalf("expected one input item, got %d", len(items))
	}

	var item map[string]any
	if err := json.Unmarshal(items[0], &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	if item["role"] != "user" || item["content"] != "list files" {
		t.Fatalf("unexpected input item %#v", item)
	}
}

func TestOpenAIInstructionsAndInputItemsPreserveResponseItemsBeforeToolOutput(t *testing.T) {
	t.Parallel()

	rawCall := json.RawMessage(`{"id":"fc_123","type":"function_call","call_id":"call_123","name":"shell","arguments":"{\"command\":\"ls\"}"}`)
	instructions, items, err := openAIInstructionsAndInputItems([]Message{
		{
			Role:          "assistant",
			ResponseItems: []json.RawMessage{rawCall},
		},
		{
			Role:       "tool",
			ToolCallID: "call_123",
			Content:    "README.md",
		},
	})
	if err != nil {
		t.Fatalf("openAIInstructionsAndInputItems returned error: %v", err)
	}
	if instructions == "" {
		t.Fatal("expected default instructions")
	}
	if len(items) != 2 {
		t.Fatalf("expected two items, got %d", len(items))
	}

	var first map[string]any
	if err := json.Unmarshal(items[0], &first); err != nil {
		t.Fatalf("unmarshal first item: %v", err)
	}
	if first["type"] != "function_call" || first["call_id"] != "call_123" {
		t.Fatalf("unexpected first item %#v", first)
	}

	var second map[string]any
	if err := json.Unmarshal(items[1], &second); err != nil {
		t.Fatalf("unmarshal second item: %v", err)
	}
	if second["type"] != "function_call_output" || second["call_id"] != "call_123" || second["output"] != "README.md" {
		t.Fatalf("unexpected second item %#v", second)
	}
}
