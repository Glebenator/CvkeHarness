package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

func TestChatRememberEndpointThenFreshConversationAndFollowUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	store := state.Open(path)
	ctx := context.Background()
	mgr := memory.NewManager(dir, store)
	old, err := mgr.ResolveTarget(ctx, memory.TargetResolutionInput{Task: "inspect coolcake@192.168.50.69"})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewMemoryRememberTargetTool(mgr))
	registry.Register(tools.NewMemoryRecordFindingTool(mgr))
	p := newScriptedProvider(t) // Saving a declaration does not require a model call.

	a := New(Options{Provider: p, ProviderName: "test", DefaultModel: "test-model", ToolRegistry: registry, MemoryRetriever: mgr, MemoryCurator: mgr, MaxIterations: 3, MaxTokens: 512})
	chat, _, err := a.StartChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chat.Turn(ctx, "remember that my servers address is 192.168.50.69")
	if err != nil || result.TaskState != state.TaskStateCompleted || result.Verification.RepairTriggered {
		t.Fatalf("remember failed: %#v %v", result, err)
	}
	p.AssertComplete(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = state.Open(path)
	defer store.Close()
	mgr = memory.NewManager(dir, store)
	// A fresh agent and empty conversation must recall from SQLite, not from
	// the first session's messages or an LLM provider's conversation cache.
	q := &sequenceProvider{fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		found := false
		for _, message := range req.Messages {
			if message.Role == "system" && strings.Contains(message.Content, "Target summary:") && strings.Contains(message.Content, "192.168.50.69") {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("new conversation/follow-up missing server endpoint on call %d", call)
		}
		return assistantText("The selected server is 192.168.50.69."), nil
	}}
	b := New(Options{Provider: q, ProviderName: "test", DefaultModel: "test-model", ToolRegistry: tools.NewRegistry(), MemoryRetriever: mgr, MaxIterations: 2, MaxTokens: 512, DisableCompletionVerification: true})
	fresh, _, err := b.StartChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.History()) != 0 {
		t.Fatal("new chat inherited conversation")
	}
	for _, prompt := range []string{"check the disk usage on my server", "check its disk again", "is that server still online"} {
		turn, err := fresh.Turn(ctx, prompt)
		if err != nil || turn.Target.TargetID != old.TargetID {
			t.Fatalf("target lost for %q: %#v %v", prompt, turn.Target, err)
		}
	}
}

func TestBackgroundRunCannotInventDirectUserEndpointDeclaration(t *testing.T) {
	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()
	mgr := memory.NewManager(dir, store)
	registry := tools.NewRegistry()
	registry.Register(tools.NewMemoryRememberTargetTool(mgr))
	p := newScriptedProvider(t,
		scriptedProviderStep{resp: assistantToolCall("forged-save", "memory_remember_target", `{"name":"server","endpoint":"192.168.50.99"}`)},
		scriptedProviderStep{expect: expectLastMessage("tool", "no direct endpoint declaration"), resp: assistantText("No user declaration was saved.")},
	)
	a := New(Options{Provider: p, ProviderName: "test", DefaultModel: "test-model", ToolRegistry: registry, MemoryRetriever: mgr, MaxIterations: 2, MaxTokens: 512, DisableCompletionVerification: true})
	if _, err := a.Run(context.Background(), "remember my server address is 192.168.50.99"); err != nil {
		t.Fatal(err)
	}
	items, err := mgr.UserEndpoints(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("background text became operator memory: %#v %v", items, err)
	}
}
