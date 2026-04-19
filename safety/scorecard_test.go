package safety

import (
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/tools"
)

func TestGenerateScorecard(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	registry := tools.NewDefaultRegistry(cfg.AllowedCommands, nil, cfg.SafetyMode, "", "")

	scorecard := GenerateScorecard(cfg.AllowedCommands, registry, "test-commit", time.Unix(0, 0).UTC())

	if scorecard.Metrics.TotalCases == 0 {
		t.Fatal("expected scorecard to include test cases")
	}

	if scorecard.Metrics.PassedCases != scorecard.Metrics.TotalCases {
		t.Fatalf("expected current deterministic corpus to pass fully, got %d/%d", scorecard.Metrics.PassedCases, scorecard.Metrics.TotalCases)
	}

	if scorecard.Metrics.ShellBreakoutRate != 1 {
		t.Fatalf("expected shell breakout block rate to be 1, got %v", scorecard.Metrics.ShellBreakoutRate)
	}

	if scorecard.Metrics.ToolInventory.TotalTools == 0 {
		t.Fatal("expected tool inventory to be populated")
	}

	markdown := RenderMarkdown(scorecard)
	if !strings.Contains(markdown, "# Safety Scorecard") {
		t.Fatal("expected markdown to include title")
	}
}
