package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

type endpointProbe struct {
	t       *testing.T
	manager *memory.Manager
	calls   int
}

func (p *endpointProbe) Name() string                { return "shell_execute" }
func (p *endpointProbe) Description() string         { return "controlled failing disk probe" }
func (p *endpointProbe) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (p *endpointProbe) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	p.calls++
	saved, err := p.manager.UserEndpoints(ctx)
	if err != nil || len(saved) != 1 || saved[0].Endpoint != "192.168.50.69" {
		p.t.Fatalf("probe preceded durable save: %#v %v", saved, err)
	}
	return "", tools.PrerequisiteError{Reason: "SSH authentication denied; credentials required"}
}

func TestEndpointTwoTurnSaveSurvivesFailedCheckAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	store := state.Open(path)
	defer store.Close()
	mgr := memory.NewManager(dir, store)
	registry := tools.NewRegistry()
	registry.Register(tools.NewMemoryRememberTargetTool(mgr))
	probe := &endpointProbe{t: t, manager: mgr}
	registry.Register(probe)
	original := "how is the disk usage on my home server looking like"
	p := newScriptedProvider(t,
		scriptedProviderStep{name: "continue pending check", expect: func(req *provider.ChatRequest) error {
			body, _ := json.Marshal(req)
			if !strings.Contains(string(body), original) || !strings.Contains(string(body), "Runtime memory result") {
				return fmt.Errorf("lost pending request or capture result")
			}
			for _, tool := range req.Tools {
				if tool.Function.Name == "memory_remember_target" {
					return fmt.Errorf("advertised redundant memory write")
				}
			}
			return nil
		}, resp: assistantToolCall("disk", "shell_execute", `{"command":"ssh 192.168.50.69 df -h"}`)},
		scriptedProviderStep{resp: assistantText("SSH access was denied; disk usage is unavailable.")},
		scriptedProviderStep{name: "verify original task", expect: func(req *provider.ChatRequest) error {
			body := req.Messages[len(req.Messages)-1].Content
			if !strings.Contains(body, `"user_request": "`+original+`"`) || !strings.Contains(body, `"status": "saved"`) || !strings.Contains(body, "current_user_message") {
				return fmt.Errorf("wrong verifier context: %s", body)
			}
			return nil
		}, resp: verifierJSON(verificationUnsatisfied, "Disk usage remains unknown.", []string{"shell_execute requires working SSH credentials"}, "Run shell_execute after credentials are supplied")},
	)
	a := New(Options{Provider: p, ProviderName: "test", DefaultModel: "test", ToolRegistry: registry, MemoryRetriever: mgr, MaxIterations: 6, MaxTokens: 1024})
	chat, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, err := chat.Turn(context.Background(), original)
	if err != nil || first.TaskState != state.TaskStateBlockedWaitingUser || len(p.Requests()) != 0 || !first.Target.Ambiguous {
		t.Fatalf("unresolved remote: %#v %v", first, err)
	}
	second, err := chat.Turn(context.Background(), "my home server is 192.168.50.69")
	if err == nil || second.MemoryCapture == nil || !second.MemoryCapture.Saved() || !strings.Contains(second.Output, "Remembered:") || second.Verification.StopReason != tools.VerificationStopCapabilityUnavailable || second.Verification.RepairTriggered || probe.calls != 1 {
		t.Fatalf("wrong independent outcomes: %#v %v", second, err)
	}
	p.AssertComplete(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := state.Open(path)
	defer reopened.Close()
	freshMgr := memory.NewManager(dir, reopened)
	freshProvider := newScriptedProvider(t, scriptedProviderStep{expect: func(req *provider.ChatRequest) error {
		body, _ := json.Marshal(req)
		if !strings.Contains(string(body), "192.168.50.69") {
			return fmt.Errorf("fresh chat lost endpoint")
		}
		return nil
	}, resp: assistantText("Your home server address is 192.168.50.69.")})
	b := New(Options{Provider: freshProvider, ProviderName: "test", DefaultModel: "test", MemoryRetriever: freshMgr, ToolRegistry: tools.NewRegistry(), MaxIterations: 1, DisableCompletionVerification: true})
	fresh, _, _ := b.StartChat(context.Background())
	if len(fresh.History()) != 0 {
		t.Fatal("history leaked")
	}
	result, err := fresh.Turn(context.Background(), "what is the address of my home server")
	if err != nil || result.Target.PrimaryName != "192.168.50.69" {
		t.Fatalf("fresh resolution: %#v %v", result, err)
	}
}

func TestEndpointCaptureApprovalResumesWithoutModelOrDuplicateWrite(t *testing.T) {
	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()
	mgr := memory.NewManager(dir, store)
	selection := securitypolicy.DefaultSelection()
	if err := selection.SetOverride(securitypolicy.SettingFileAppend, "ask"); err != nil {
		t.Fatal(err)
	}
	policy, err := securitypolicy.Resolve(selection)
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.ConfigureSecurityWithStore(policy, tools.NewBlockingApprover(), nil, store)
	registry.Register(tools.NewMemoryRememberTargetTool(mgr))
	p := newScriptedProvider(t)
	events := make(chan tools.Event, 32)
	a := New(Options{Provider: p, ProviderName: "test", DefaultModel: "test", ToolRegistry: registry, MemoryRetriever: mgr, BlockedWorkStore: store, AwaitManualApprovals: true, EventObserver: approvalEventObserver{events: events}, MaxIterations: 1})
	chat, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan ChatTurnResult, 1)
	go func() {
		result, _ := chat.Turn(ctx, "my home server is 192.168.50.69")
		results <- result
	}()
	for {
		select {
		case event := <-events:
			if event.Type != tools.EventApprovalRequired {
				continue
			}
			saved, err := mgr.UserEndpoints(ctx)
			if err != nil || len(saved) != 0 {
				t.Fatalf("capture happened before approval: %#v %v", saved, err)
			}
			if _, err := tools.ApproveBlockedWork(ctx, store, policy, event.BlockedWorkID, time.Minute, "test"); err != nil {
				t.Fatal(err)
			}
			if err := chat.ResumeApproval(event.BlockedWorkID); err != nil {
				t.Fatal(err)
			}
		case result := <-results:
			if result.ExecutionErr != nil || result.MemoryCapture == nil || result.MemoryCapture.Status != "saved" {
				t.Fatalf("resume lost capture: %#v", result)
			}
			grants, err := store.ListSecurityActionGrants(ctx)
			if err != nil || len(grants) != 1 || grants[0].RemainingUses != 0 {
				t.Fatalf("approval not consumed exactly once: %#v %v", grants, err)
			}
			p.AssertComplete(t)
			return
		case <-ctx.Done():
			t.Fatal("capture approval did not finish")
		}
	}
}

func TestRuntimeMemoryCannotBeOverturnedByVerifier(t *testing.T) {
	r := &memory.EndpointCapture{Status: "saved", Name: "home server", Endpoint: "192.168.50.69"}
	v := enforceMemoryVerification(CompletionVerification{Status: verificationUnsatisfied, MissingActions: []string{"memory_remember_target must save the address"}, RepairInstruction: "retry the save"}, r)
	if r.Status != "saved" || v.StopReason != tools.VerificationStopNoProgress || v.RepairInstruction != "" || v.satisfied() {
		t.Fatalf("memory-only verifier judgment altered independent outcomes: %#v %#v", r, v)
	}
	v = enforceMemoryVerification(CompletionVerification{Status: verificationUnsatisfied, MissingActions: []string{"save the address", "shell_execute must check disk usage"}}, r)
	if len(v.MissingActions) != 1 || v.MissingActions[0] != "shell_execute must check disk usage" || v.satisfied() {
		t.Fatalf("lost execution obligation: %#v", v)
	}
}

func TestEndpointDeclarationDoesNotReviveCancelledOrDifferentTask(t *testing.T) {
	for _, intervening := range []string{"cancel that", "what is 2 plus 2", "my staging server is 192.168.50.70"} {
		t.Run(intervening, func(t *testing.T) {
			dir := t.TempDir()
			store := state.Open(filepath.Join(dir, "state.db"))
			defer store.Close()
			mgr := memory.NewManager(dir, store)
			reg := tools.NewRegistry()
			reg.Register(tools.NewMemoryRememberTargetTool(mgr))
			p := newScriptedProvider(t)
			if !strings.HasPrefix(intervening, "my ") {
				p = newScriptedProvider(t, scriptedProviderStep{resp: assistantText("Okay.")})
			}
			a := New(Options{Provider: p, ProviderName: "test", DefaultModel: "test", ToolRegistry: reg, MemoryRetriever: mgr, MaxIterations: 1, DisableCompletionVerification: true})
			chat, _, _ := a.StartChat(context.Background())
			chat.Turn(context.Background(), "check disk usage on my home server")
			if _, err := chat.Turn(context.Background(), intervening); err != nil {
				t.Fatal(err)
			}
			result, err := chat.Turn(context.Background(), "my home server is 192.168.50.69")
			if err != nil || result.MemoryCapture == nil || !result.MemoryCapture.Saved() {
				t.Fatalf("declaration: %#v %v", result, err)
			}
			p.AssertComplete(t)
		})
	}
}

func TestEndpointCaptureSettingsAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		mode, prompt string
		decision     securitypolicy.Decision
		want         string
	}{
		{"declarations", "my home server is 192.168.50.69", securitypolicy.DecisionAllow, "saved"},
		{"off", "remember my home server is 192.168.50.69", securitypolicy.DecisionAllow, ""},
		{"explicit_only", "my home server is 192.168.50.69", securitypolicy.DecisionAllow, ""},
		{"explicit_only", "remember my home server is 192.168.50.69", securitypolicy.DecisionAllow, "saved"},
		{"declarations", "my home server is 192.168.50.69", securitypolicy.DecisionDeny, "failed"},
		{"declarations", "my home server is 192.168.50.69", securitypolicy.DecisionAsk, "failed"},
	} {
		t.Run(tc.mode+string(tc.decision)+tc.prompt, func(t *testing.T) {
			dir := t.TempDir()
			store := state.Open(filepath.Join(dir, "state.db"))
			defer store.Close()
			mgr := memory.NewManager(dir, store)
			reg := tools.NewRegistry()
			reg.Register(tools.NewMemoryRememberTargetTool(mgr))
			selection := securitypolicy.DefaultSelection()
			selection.SetOverride(securitypolicy.SettingFileAppend, string(tc.decision))
			policy, _ := securitypolicy.Resolve(selection)
			reg.ConfigureSecurity(policy, tools.NewBlockingApprover(), nil)
			p := newScriptedProvider(t)
			a := New(Options{Provider: p, DefaultModel: "test", ProviderName: "test", MemoryCapture: tc.mode, MemoryRetriever: mgr, ToolRegistry: reg, MaxIterations: 1})
			chat, _, _ := a.StartChat(context.Background())
			result, _ := chat.Turn(context.Background(), tc.prompt)
			if tc.want == "" {
				if result.MemoryCapture != nil {
					t.Fatalf("capture disabled: %#v", result.MemoryCapture)
				}
			} else if result.MemoryCapture == nil || result.MemoryCapture.Status != tc.want {
				t.Fatalf("result: %#v", result)
			}
			saved, err := mgr.UserEndpoints(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (len(saved) > 0) != (tc.want == "saved") {
				t.Fatalf("policy/mode bypass: %#v", saved)
			}
			p.AssertComplete(t)
		})
	}
}

func TestRequiredPrerequisiteRejectionAllowsCorrectedArguments(t *testing.T) {
	v := CompletionVerification{MissingActions: []string{"memory_remember_target must save the address"}}
	calls := []memory.ObservedToolCall{{ToolName: "memory_remember_target", PrerequisiteRejected: true}}
	if !requiredPrerequisiteRejected(v, calls) {
		t.Fatal("immutable rejection not detected")
	}
	calls = append(calls, memory.ObservedToolCall{ToolName: "memory_remember_target", Success: true})
	if requiredPrerequisiteRejected(v, calls) {
		t.Fatal("later success ignored")
	}
	if requiredPrerequisiteRejected(v, []memory.ObservedToolCall{{ToolName: "memory_remember_target", Result: "malformed args"}}) {
		t.Fatal("correctable arguments stopped repair")
	}
}
