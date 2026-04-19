package agent

import (
	"strings"

	"github.com/coolcake/cvkeharness/provider"
)

// ChatState owns the mutable conversation history for one execution phase.
type ChatState struct {
	messages []provider.Message
}

// NewChatState seeds a new conversation with the provided messages.
func NewChatState(seed ...provider.Message) *ChatState {
	state := &ChatState{}
	state.messages = append(state.messages, seed...)
	return state
}

// Messages returns a snapshot of the current chat history.
func (c *ChatState) Messages() []provider.Message {
	return append([]provider.Message(nil), c.messages...)
}

// Add appends a provider message to the chat history.
func (c *ChatState) Add(message provider.Message) {
	c.messages = append(c.messages, message)
}

// AddSystem appends a non-empty system message.
func (c *ChatState) AddSystem(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	c.messages = append(c.messages, provider.Message{
		Role:    "system",
		Content: content,
	})
}

// AddToolResult appends a tool response for a specific tool call.
func (c *ChatState) AddToolResult(callID, content string) {
	c.messages = append(c.messages, provider.Message{
		Role:       "tool",
		ToolCallID: callID,
		Content:    content,
	})
}
