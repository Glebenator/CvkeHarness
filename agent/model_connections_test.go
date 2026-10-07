package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/glebenator/cvkeharness/core"
	"github.com/glebenator/cvkeharness/provider"
	"github.com/glebenator/cvkeharness/tools"
)

type connectionResolverStub struct {
	client provider.Provider
	refs   []core.ModelRef
}

func (r *connectionResolverStub) Resolve(string) (provider.Provider, error) {
	return nil, fmt.Errorf("provider-only resolution loses the connection")
}
func (r *connectionResolverStub) ResolveModel(ref core.ModelRef) (provider.Provider, error) {
	r.refs = append(r.refs, ref)
	return r.client, nil
}

func TestVerifierResolvesNamedConnectionAndDefaultsToActualExecution(t *testing.T) {
	actual := core.ModelRef{Connection: "execution-server", Provider: "lmstudio", Model: "actual-executor"}
	independent := core.ModelRef{Connection: "review-server", Provider: "lmstudio", Model: "vendor/judge"}
	for _, test := range []struct {
		name                 string
		configured, expected core.ModelRef
	}{
		{"inherit routed executor", core.ModelRef{}, actual},
		{"independent verifier", independent, independent},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newScriptedProvider(t, scriptedProviderStep{expect: expectModel(test.expected.Model), resp: &provider.ChatResponse{Message: provider.Message{Content: `{"status":"satisfied","reason":"complete"}`}}})
			resolver := &connectionResolverStub{client: client}
			a := New(Options{ProviderName: "lmstudio", DefaultModel: "default-executor", ProviderResolver: resolver, VerifierModel: test.configured, MaxTokens: 1024})
			decision, record, err := a.verifyCompletion(context.Background(), core.RoutingSelection{Requested: actual}, core.TaskClassGeneral, "reply", "reply", nil, nil)
			if err != nil || !decision.satisfied() {
				t.Fatalf("verify: %+v %v", decision, err)
			}
			if len(resolver.refs) != 1 || !resolver.refs[0].Equal(test.expected) || record.RequestedModel != test.expected.Model {
				t.Fatalf("wrong verifier selection: %+v %+v", resolver.refs, record)
			}
			client.AssertComplete(t)
		})
	}
}

func TestClassifierUsesItsExplicitModelInsteadOfSafetyJudgeModel(t *testing.T) {
	classifier := &classifierProviderStub{content: `{"task_class":"general","actionable":false}`}
	a := New(Options{SafetyMode: tools.SafetyModeLLMJudge, SafetyModel: "safety-native", ClassifierModel: "classifier-native", ClassifierProvider: classifier})
	a.classifyTask(context.Background(), "hello", classificationContext{})
	if classifier.req == nil || classifier.req.Model != "classifier-native" {
		t.Fatalf("classifier reused safety model: %+v", classifier.req)
	}
}

func TestNamedConnectionNeverFallsBackToPrimaryProvider(t *testing.T) {
	a := New(Options{ProviderName: "lmstudio", Provider: &classifierProviderStub{}})
	if _, err := a.resolveModelProvider(core.ModelRef{Connection: "other-server", Provider: "lmstudio", Model: "same-model"}); err == nil {
		t.Fatal("named connection silently reused primary provider")
	}
}
