package tui

import (
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/state"
)

func TestJobsDetailKeepsPageHeader(t *testing.T) {
	t.Parallel()

	tab := &jobsTab{
		jobs: []state.ScheduledJob{{
			ID:           "job_123",
			Name:         "Speedtest every 2 hours to speedtest.md",
			ScheduleKind: "every",
			ScheduleSpec: "2h",
			Prompt:       "Run a network speed test and append a markdown entry.",
			Enabled:      true,
		}},
		cursor: 0,
		mode:   jobsModeDetail,
		loaded: true,
	}

	view := tab.View(100, 30)
	for _, want := range []string{
		"Jobs",
		"scheduled agent work and run history",
		"Speedtest every 2 hours to speedtest.md",
		"Run History",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected jobs detail to contain %q, got:\n%s", want, view)
		}
	}
}
