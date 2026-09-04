package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coolcake/cvkeharness/state"
)

type runsDataMsg struct {
	runs    []state.RunSummary
	err     error
	query   string
	status  string
	target  state.RunTargetFilter
	targets []string
	offset  int
}

type runExportMsg struct {
	path string
	err  error
}

type runsTab struct {
	runs      []state.RunSummary
	cursor    int
	expanded  bool
	loaded    bool
	scroll    int // scroll offset for the detail view
	query     string
	status    string
	target    state.RunTargetFilter
	targets   []string
	offset    int
	more      bool
	searching bool
	search    textinput.Model
	err       error
	message   string
	details   bool
	selected  *state.RunSummary
}

func newRunsTab() tabModel {
	input := textinput.New()
	input.Placeholder = "Search task, answer, error or host in commands"
	input.CharLimit = 200
	return &runsTab{status: "all", search: input}
}

func (t *runsTab) Init(svc *Service) tea.Cmd {
	if t.expanded || t.searching {
		return nil
	}
	query, status, offset, target := t.query, t.status, t.offset, t.target
	if status == "" {
		status = "all"
	}
	return func() tea.Msg {
		runs, err := svc.SearchRunsForTarget(context.Background(), 26, offset, query, status, target)
		var targets []string
		if err == nil {
			targets, err = svc.RunTargets(context.Background())
		}
		return runsDataMsg{runs: runs, err: err, query: query, status: status, offset: offset, target: target, targets: targets}
	}
}

func (t *runsTab) Consuming() bool { return t.searching }

func (t *runsTab) StatusHints() []string {
	if t.searching {
		return []string{renderKeyHint("enter", "search"), renderKeyHint("esc", "cancel")}
	}
	if t.expanded {
		return []string{renderKeyHint("d", "diagnostics"), renderKeyHint("e", "export"),
			renderKeyHint("esc", "back"),
			renderKeyHint("↑↓", "scroll"),
		}
	}
	return []string{renderKeyHint("enter", "open"), renderKeyHint("/", "search"), renderKeyHint("f", "status"), renderKeyHint("t", "target"), renderKeyHint("[ ]", "pages"), renderKeyHint("r", "retry")}

}

func (t *runsTab) Update(msg tea.Msg, svc *Service, width, height int) (tabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case runExportMsg:
		if msg.err != nil {
			t.message = "Export failed: " + msg.err.Error()
		} else {
			t.message = "Exported privately: " + msg.path
		}
	case runsDataMsg:
		if msg.target != t.target || msg.query != t.query || msg.offset != t.offset || (msg.status != "" && msg.status != t.status) {
			return t, nil
		}
		t.loaded = true
		t.err = msg.err
		if msg.err != nil {
			return t, nil
		}
		t.targets = msg.targets
		t.more = len(msg.runs) > 25
		if t.more {
			msg.runs = msg.runs[:25]
		}
		t.runs = msg.runs
		t.loaded = true
		if t.cursor >= len(t.runs) && len(t.runs) > 0 {
			t.cursor = len(t.runs) - 1
		}

	case tea.KeyMsg:
		if t.searching {
			switch msg.String() {
			case "esc":
				t.searching = false
				t.search.Blur()
				return t, nil
			case "enter":
				t.query = t.search.Value()
				t.offset = 0
				t.cursor = 0
				t.searching = false
				t.search.Blur()
				return t, t.Init(svc)
			}
			var cmd tea.Cmd
			t.search, cmd = t.search.Update(msg)
			return t, cmd
		}
		if !t.expanded {
			switch msg.String() {
			case "/":
				t.search.SetValue(t.query)
				t.searching = true
				return t, t.search.Focus()
			case "t":
				options := []state.RunTargetFilter{{}, {Unknown: true}}
				for _, id := range t.targets {
					options = append(options, state.RunTargetFilter{ID: id})
				}
				next := 0
				for i, option := range options {
					if option == t.target {
						next = (i + 1) % len(options)
						break
					}
				}
				t.target = options[next]
				t.offset, t.cursor = 0, 0
				return t, t.Init(svc)
			case "f":
				t.status = nextOption([]string{"all", "success", "failed"}, t.status)
				t.offset = 0
				t.cursor = 0
				return t, t.Init(svc)
			case "]":
				if t.more {
					t.offset += 25
					t.cursor = 0
					return t, t.Init(svc)
				}
			case "[":
				t.offset = maxInt(0, t.offset-25)
				t.cursor = 0
				return t, t.Init(svc)
			case "r":
				return t, t.Init(svc)
			}
		}
		if t.expanded && msg.String() == "e" && t.selected != nil {
			run := *t.selected
			return t, func() tea.Msg { path, err := svc.ExportRun(run); return runExportMsg{path: path, err: err} }
		}
		if t.expanded {
			return t.updateDetail(msg, height)
		}
		return t.updateList(msg)
	}
	return t, nil
}

func (t *runsTab) updateList(msg tea.KeyMsg) (tabModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Down):
		if t.cursor < len(t.runs)-1 {
			t.cursor++
		}
	case key.Matches(msg, keys.Up):
		if t.cursor > 0 {
			t.cursor--
		}
	case key.Matches(msg, keys.Enter):
		if len(t.runs) > 0 {
			t.expanded = true
			selected := t.runs[t.cursor]
			t.selected = &selected
			t.message = ""
			t.scroll = 0
		}
	}
	return t, nil
}

func (t *runsTab) updateDetail(msg tea.KeyMsg, height int) (tabModel, tea.Cmd) {
	switch {
	case msg.String() == "d":
		t.details = !t.details
		t.scroll = 0
	case msg.String() == "pgdown":
		t.scroll += maxInt(height-2, 1)
	case msg.String() == "pgup":
		t.scroll = maxInt(0, t.scroll-height+2)
	case msg.String() == "home":
		t.scroll = 0
	case msg.String() == "end":
		t.scroll = 1 << 20
	case key.Matches(msg, keys.Back):
		t.expanded = false
		t.selected = nil
	case key.Matches(msg, keys.Down):
		t.scroll++
	case key.Matches(msg, keys.Up):
		if t.scroll > 0 {
			t.scroll--
		}
	}
	return t, nil
}

func (t *runsTab) View(width, height int) string {
	if !t.loaded {
		return styleMuted.Render("  Loading…")
	}

	if t.expanded {
		return t.viewDetail(width, height)
	}
	return t.viewList(width, height)
}

func (t *runsTab) viewList(width, height int) string {
	header := renderPageHeader("Runs", "outcomes and execution history", width)
	header += "  " + fmt.Sprintf("%s · Page %d · / search · f status · t target · [ ] pages", firstNonEmptyText(t.status, "all"), t.offset/25+1) + "\n"
	targetLabel := "all"
	if t.target.Unknown {
		targetLabel = "unknown (not recorded)"
	} else if t.target.ID != "" {
		targetLabel = t.target.ID
	}
	header += "  " + wrapDisplay("Last target: "+targetLabel, width-4) + "\n"
	if t.query != "" {
		header += "  Search: " + t.query + "\n"
	}
	if t.searching {
		t.search.Width = maxInt(width-8, 20)
		header += "  " + t.search.View() + "\n"
	}
	if t.err != nil {
		header += "  " + wrapDisplay("History unavailable: "+t.err.Error()+". Press r to retry.", width-4) + "\n"
		return header
	}
	if len(t.runs) == 0 {
		return header + "\n  No matching runs. / changes search, f status, t target."
	}
	available := maxInt((height-strings.Count(header, "\n")-2)/2, 1)
	start, end := listWindow(t.cursor, len(t.runs), available)
	for i := start; i < end; i++ {
		run := t.runs[i]
		label := runStateLabel(run)
		row := fmt.Sprintf("#%d %s  %s", run.ID, label, run.Task)
		header += "  " + renderSelectableRow(truncate(row, width-6), i == t.cursor) + "\n"
		header += "    " + truncate(fmt.Sprintf("%s · verification: %s · %s", timeAgo(run.StartedAt), firstNonEmptyText(run.VerificationStatus, "not run"), run.Provider), width-6) + "\n"
	}
	header += "  " + scrollHints(start, end, len(t.runs))
	return header
}

func runStateLabel(run state.RunSummary) string {
	if run.TaskState != "" {
		return strings.ToUpper(string(run.TaskState))
	}
	if run.Success {
		return "SUCCEEDED"
	}
	return "FAILED"
}

func (t *runsTab) viewDetail(width, height int) string {
	if t.selected == nil {
		if t.cursor >= len(t.runs) {
			return ""
		}
		selected := t.runs[t.cursor]
		t.selected = &selected
	}
	run := *t.selected

	// Build all the detail lines, then apply scroll window.
	var lines []string

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s  %s",
		styleSectionTitle.Render(fmt.Sprintf("Run #%d", run.ID)),
		runStateLabel(run)))
	lines = append(lines, "")
	lines = append(lines, "  "+renderKeyValue("Task", run.Task))
	if run.TaskClass != "" {
		lines = append(lines, "  "+renderKeyValue("Task Class", string(run.TaskClass)))
	}
	lines = append(lines, "  "+renderKeyValue("Provider", run.Provider))
	targetLabel := firstNonEmptyText(run.TargetID, "unknown (not recorded)")
	if run.TargetAmbiguous {
		targetLabel += " (ambiguous)"
	}
	lines = append(lines, "  "+renderKeyValue("Last target", targetLabel))
	if run.TargetEnvironment != "" {
		lines = append(lines, "  "+renderKeyValue("Environment", run.TargetEnvironment))
	}
	if !run.StartedAt.IsZero() {
		lines = append(lines, "  "+renderKeyValue("Started", fmtTime(run.StartedAt)))
	}
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		lines = append(lines, "  "+renderKeyValue("Duration", fmtDuration(run.FinishedAt.Sub(run.StartedAt))))
	}
	if run.RoutingEnabled {
		lines = append(lines, "  "+renderKeyValue("Routing", styleSuccess.Render("enabled")))
	}

	// Verification section
	if run.VerificationStatus != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+styleSectionTitle.Render("Verification"))
		lines = append(lines, "")
		verStyle := styleSuccess
		if run.VerificationStatus != "satisfied" && run.VerificationStatus != "pass" && run.VerificationStatus != "ok" {
			verStyle = styleWarning
		}
		lines = append(lines, "  "+renderKeyValue("Status", verStyle.Render(run.VerificationStatus)))
		if run.VerificationReason != "" {
			// Wrap long reason text
			for _, line := range wrapText(run.VerificationReason, width-22) {
				lines = append(lines, "  "+renderKeyValue("Reason", styleMuted.Render(line)))
			}
		}
		if run.VerificationMissingActions != "" {
			lines = append(lines, "  "+renderKeyValue("Missing", styleWarning.Render(run.VerificationMissingActions)))
		}
		if run.VerificationRepairTriggered {
			lines = append(lines, "  "+renderKeyValue("Auto-Repair", styleWarning.Render("triggered")))
		}
	}

	if run.ErrorMessage != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+renderKeyValue("Error", styleError.Render(run.ErrorMessage)))
	}

	// The complete answer comes before optional diagnostics.
	if output := strings.TrimSpace(run.FinalOutput); output != "" {
		lines = append(lines, "", "  "+styleSectionTitle.Render("Agent Output"), "")
		for _, line := range strings.Split(renderMarkdown(output, width-6), "\n") {
			lines = append(lines, "  "+line)
		}
	}
	lines = append(lines, "", "  "+renderKeyHint("d", "show / hide diagnostics"))
	if t.message != "" {
		lines = append(lines, "  "+t.message)
	}
	if t.details {
		// Phases section with token breakdown
		if len(run.Phases) > 0 {
			lines = append(lines, "")
			lines = append(lines, "  "+styleSectionTitle.Render("Phases"))
			lines = append(lines, "")
			for _, phase := range run.Phases {
				icon := statusIcon(phase.Success)
				model := phase.ActualModel
				if model == "" {
					model = phase.RequestedModel
				}
				lines = append(lines, fmt.Sprintf("  %s  %s  %s  %s  %s tokens",
					icon,
					styleMuted.Render(padRight(string(phase.Phase), 12)),
					styleBase.Render(padRight(truncate(model, 30), 30)),
					styleMuted.Render(fmtDurationMs(phase.LatencyMs)),
					styleMuted.Render(formatTokens(phase.TotalTokens)),
				))
				// Token breakdown sub-line
				var tokenParts []string
				if phase.PromptTokens > 0 {
					tokenParts = append(tokenParts, fmt.Sprintf("prompt %s", formatTokens(phase.PromptTokens)))
				}
				if phase.CompletionTokens > 0 {
					tokenParts = append(tokenParts, fmt.Sprintf("completion %s", formatTokens(phase.CompletionTokens)))
				}
				if phase.CachedTokensKnown && phase.CachedTokens > 0 {
					tokenParts = append(tokenParts, fmt.Sprintf("cached %s", formatTokens(phase.CachedTokens)))
				}
				if len(tokenParts) > 0 {
					lines = append(lines, "       "+styleSubtle.Render(strings.Join(tokenParts, "  ·  ")))
				}
				if phase.Confidence > 0 {
					lines = append(lines, "       "+styleSubtle.Render(fmt.Sprintf("confidence %.0f%%", phase.Confidence*100)))
				}
				if phase.Explanation != "" {
					lines = append(lines, "       "+styleSubtle.Render(phase.Explanation))
				}
			}
		}

		// Tool outcomes with more detail
		if len(run.Tools) > 0 {
			lines = append(lines, "")
			lines = append(lines, "  "+styleSectionTitle.Render("Tool Outcomes"))
			lines = append(lines, "")
			for _, tool := range run.Tools {
				icon := statusIcon(tool.Success)
				denied := ""
				if tool.PolicyDenied {
					denied = styleWarning.Render(" denied")
					if tool.DenialClass != "" {
						denied += styleMuted.Render(" (" + tool.DenialClass + ")")
					}
				}
				phaseBadge := ""
				if tool.Phase != "" {
					phaseBadge = styleSubtle.Render("[" + string(tool.Phase) + "] ")
				}
				lines = append(lines, fmt.Sprintf("  %s  %s%s  %s%s",
					icon,
					phaseBadge,
					styleBase.Render(padRight(tool.ToolName, 20)),
					styleMuted.Render(fmtDurationMs(tool.DurationMs)),
					denied,
				))
				if tool.ErrorMessage != "" {
					lines = append(lines, fmt.Sprintf("     %s", styleError.Render(tool.ErrorMessage)))
				}
				if tool.Command != "" {
					lines = append(lines, fmt.Sprintf("     %s %s", styleSubtle.Render("command"), styleBase.Render(tool.Command)))
				} else if args := formatToolArguments(tool.Arguments); args != "" {
					lines = append(lines, fmt.Sprintf("     %s %s", styleSubtle.Render("args"), styleBase.Render(args)))
				}
			}
		}

	}
	lines = strings.Split(wrapDisplay(strings.Join(lines, "\n"), width-2), "\n")

	// Apply scroll offset
	maxScroll := maxInt(len(lines)-height+2, 0)
	t.scroll = clamp(t.scroll, 0, maxScroll)

	var b strings.Builder
	end := minInt(t.scroll+height-1, len(lines))
	for i := t.scroll; i < end; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}

	// Scroll indicator
	if maxScroll > 0 {
		b.WriteString("  ")
		b.WriteString(scrollHints(t.scroll, end, len(lines)))
	}

	return b.String()
}

func formatToolArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	var compacted any
	if err := json.Unmarshal([]byte(arguments), &compacted); err != nil {
		return arguments
	}
	b, err := json.Marshal(compacted)
	if err != nil {
		return arguments
	}
	return string(b)
}
