package agent

import (
	"context"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/tools"
)

type memoryEventObserver struct {
	events []tools.Event
}

func (o *memoryEventObserver) Observe(event tools.Event) {
	o.events = append(o.events, event)
}

func TestEmitMemoryInjectionIncludesBoundedSourceDetails(t *testing.T) {
	t.Parallel()

	observer := &memoryEventObserver{}
	ctx := tools.WithEventObserver(context.Background(), observer)
	emitMemoryInjection(ctx, core.PhaseChat, memory.RetrievalResult{
		Sources: []memory.InjectionSource{{
			Name:    memory.TargetsFile,
			Origin:  "target summary",
			Chars:   42,
			Preview: "Target web-01 (ssh)",
		}},
	})

	if len(observer.events) != 1 {
		t.Fatalf("event count = %d, want 1", len(observer.events))
	}
	event := observer.events[0]
	if event.Type != tools.EventMemoryInjected {
		t.Fatalf("event type = %q, want %q", event.Type, tools.EventMemoryInjected)
	}
	if len(event.MemorySources) != 1 {
		t.Fatalf("memory source count = %d, want 1", len(event.MemorySources))
	}
	source := event.MemorySources[0]
	if source.Name != memory.TargetsFile || source.Origin != "target summary" || source.Chars != 42 || source.Preview != "Target web-01 (ssh)" {
		t.Fatalf("unexpected memory source: %#v", source)
	}
}
