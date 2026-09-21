package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/coolcake/cvkeharness/internal/secrets"
)

// Activity owns its selection and scrolling. Chat scrolling and composer focus
// must not depend on which tool the operator is inspecting.
type chatActivityState struct {
	ready             bool
	focused           bool
	following         bool
	selectedTurn      int
	selectedTool      int // index in chatTab.toolCalls; -1 means no tool
	inspecting        bool
	viewport          viewport.Model
	width             int
	height            int
	header            []string
	followStart       int
	followEnd         int
	backStart         int
	backEnd           int
	rowStarts         map[int]int
	rowEnds           map[int]int
	listOffset        int
	restoreComposer   bool
	mousePressHandled bool
}

func (t *chatTab) initActivity() {
	if t.activity.ready {
		return
	}
	t.activity = chatActivityState{
		ready:        true,
		following:    true,
		selectedTurn: t.activeTurn,
		selectedTool: -1,
		viewport:     viewport.New(40, 12),
		width:        40,
		height:       16,
	}
}

func (t *chatTab) resizeActivity(width, height int) {
	t.initActivity()
	t.activity.width = maxInt(width, 1)
	t.activity.height = maxInt(height, 1)
	t.refreshActivity()
}

func (t *chatTab) refreshActivity() {
	t.initActivity()
	a := &t.activity
	oldTurn, oldOffset := a.selectedTurn, a.viewport.YOffset
	turns := t.activityTurns()
	if a.following && !a.inspecting {
		a.selectedTurn = t.latestActivityTurn(turns)
		indices := t.activityToolIndices(a.selectedTurn)
		a.selectedTool = -1
		if len(indices) > 0 {
			a.selectedTool = indices[len(indices)-1]
		}
	} else if !activityContainsTurn(turns, a.selectedTurn) && len(turns) > 0 {
		a.selectedTurn = turns[0]
		a.selectedTool = -1
		a.inspecting = false
	}
	if !t.activityToolSelected() {
		a.selectedTool = -1
		if indices := t.activityToolIndices(a.selectedTurn); len(indices) > 0 {
			a.selectedTool = indices[0]
		}
	}
	if oldTurn != a.selectedTurn {
		oldOffset = 0
		a.listOffset = 0
	}
	t.refreshActivityHeader(len(turns) > 0)
	a.viewport.Width = a.width
	a.viewport.Height = maxInt(a.height-len(a.header), 1)
	a.rowStarts = make(map[int]int)
	a.rowEnds = make(map[int]int)
	var lines []string
	appendPiece := func(piece string) {
		lines = append(lines, activityPhysicalLines(piece, a.width)...)
	}
	if len(turns) == 0 {
		appendPiece(styleMuted.Render("No activity yet. Send a message to begin."))
	} else {
		if prompt := t.activityPrompt(a.selectedTurn); prompt != "" {
			appendPiece(styleMuted.Render("YOU"))
			promptLines := activityPhysicalLines(activitySafeText(prompt), a.width)
			// The full prompt remains in chat. A bounded excerpt keeps long
			// requests from burying the selected tool and its output here.
			if len(promptLines) > 3 {
				promptLines = promptLines[:3]
				promptLines[2] = ansi.Truncate(promptLines[2], maxInt(a.width-1, 1), "") + "…"
			}
			appendPiece(styleBright.Render(strings.Join(promptLines, "\n")))
			appendPiece("")
		}
		if a.inspecting && t.activityToolSelected() {
			t.appendActivityToolDetail(&lines, t.toolCalls[a.selectedTool])
		} else {
			indices := t.activityToolIndices(a.selectedTurn)
			if len(indices) == 0 {
				appendPiece(styleMuted.Render("No tool calls in this turn."))
			}
			for _, index := range indices {
				tool := t.toolCalls[index]
				a.rowStarts[index] = len(lines)
				marker := "  "
				if index == a.selectedTool {
					marker = styleAccent.Render("› ")
				}
				status := renderNamedStatus(activitySafeText(tool.status))
				duration := ""
				if tool.duration > 0 {
					duration = "  " + tool.duration.Round(time.Millisecond).String()
				}
				appendPiece(marker + status + styleMuted.Render(duration))
				command := firstNonEmptyText(tool.command, tool.name, "Tool call")
				command = strings.Split(activitySafeText(command), "\n")[0]
				appendPiece("  " + styleBase.Render(ansi.Truncate(command, maxInt(a.width-2, 1), "…")))
				a.rowEnds[index] = len(lines) - 1
				appendPiece("")
			}
		}
		// Reuse the complete verification presentation at the activity width,
		// without mutating the transcript viewport or its scroll position.
		var verification []string
		view := *t
		view.viewport.Width = a.width
		view.appendVerificationForTurn(&verification, a.selectedTurn, make(map[int]bool))
		for _, piece := range verification {
			appendPiece(piece)
		}
	}
	a.viewport.SetContent(strings.Join(lines, "\n"))
	if a.following && !a.inspecting {
		a.viewport.GotoBottom()
	} else {
		a.viewport.SetYOffset(oldOffset)
	}
}

func (t *chatTab) refreshActivityHeader(hasTurn bool) {
	a := &t.activity
	a.header = nil
	appendHeader := func(piece string) { a.header = append(a.header, activityPhysicalLines(piece, a.width)...) }
	heading := "ACTIVITY"
	if hasTurn {
		heading += fmt.Sprintf(" / TURN %d", a.selectedTurn)
	}
	if a.focused {
		appendHeader(styleAccent.Render("▸ " + heading))
	} else {
		appendHeader(styleSectionTitle.Render(heading))
	}
	a.followStart = len(a.header)
	if a.following {
		appendHeader(styleMuted.Render("FOLLOWING LATEST"))
	} else {
		follow := "PINNED · Follow latest"
		if a.focused {
			follow = "PINNED · f Follow latest"
		}
		appendHeader(styleMuted.Render(follow))
	}
	a.followEnd = len(a.header) - 1
	a.backStart = len(a.header)
	back := "Click to focus activity"
	if a.focused {
		back = "Esc Back to chat"
		if a.inspecting {
			back = "Esc Back to tools"
		}
	} else if a.inspecting {
		back = "Back to tools"
	}
	appendHeader(styleMuted.Render(back))
	a.backEnd = len(a.header) - 1
	appendHeader(horizontalRule(a.width))
}

func (t *chatTab) activityView() string {
	t.initActivity()
	if len(t.activity.header) == 0 {
		t.refreshActivity()
	}
	lines := append([]string(nil), t.activity.header...)
	lines = append(lines, strings.Split(t.activity.viewport.View(), "\n")...)
	if len(lines) > t.activity.height {
		lines = lines[:t.activity.height]
	}
	return strings.Join(lines, "\n")
}

func (t *chatTab) openActivity(turn int) {
	t.initActivity()
	a := &t.activity
	if a.selectedTurn != turn {
		a.selectedTurn = turn
		a.selectedTool = -1
		a.inspecting = false
		a.listOffset = 0
		a.viewport.GotoTop()
	}
	a.following = false
	t.focusActivity()
	t.refreshActivity()
	if !a.inspecting {
		t.revealActivityTool()
	}
}

func (t *chatTab) focusActivity() {
	if !t.activity.focused {
		t.activity.restoreComposer = t.composerFocused
	}
	t.activity.focused = true
	t.composerFocused = false
	t.composer.Blur()
	t.closeCommandMenu()
}

func (t *chatTab) closeActivity() {
	t.initActivity()
	a := &t.activity
	if !a.focused {
		return
	}
	a.focused = false
	t.composerFocused = a.restoreComposer
	if t.composerFocused {
		t.composer.Focus()
	} else {
		t.composer.Blur()
	}
	a.restoreComposer = false
	t.refreshActivity()
}

func (t *chatTab) followLatestActivity() {
	t.initActivity()
	t.activity.following = true
	t.activity.inspecting = false
	t.activity.listOffset = 0
	t.activity.viewport.GotoTop()
	t.refreshActivity()
}

func (t *chatTab) handleActivityKey(msg tea.KeyMsg) bool {
	t.initActivity()
	if !t.activity.focused {
		return false
	}
	switch msg.String() {
	case "up", "down", "pgup", "pgdown", "home", "end":
		t.scrollActivity(msg.String())
	case "n", "p":
		direction := 1
		if msg.String() == "p" {
			direction = -1
		}
		t.selectActivityTool(direction)
	case "[", "]":
		direction := 1
		if msg.String() == "[" {
			direction = -1
		}
		t.selectActivityTurn(direction)
	case "enter":
		t.inspectActivityTool()
	case "esc":
		t.backFromActivity()
	case "f":
		t.followLatestActivity()
	default:
		// Activity owns keyboard focus. Unrecognized keys must not reach
		// chat actions, especially approval of a different, hidden turn.
		return true
	}
	return true
}

func (t *chatTab) handleActivityMouse(msg tea.MouseMsg) {
	t.initActivity()
	a := &t.activity
	if msg.X < 0 || msg.X >= a.width || msg.Y < 0 || msg.Y >= a.height {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		t.scrollActivity("wheelup")
		return
	case tea.MouseButtonWheelDown:
		t.scrollActivity("wheeldown")
		return
	}
	if msg.Button != tea.MouseButtonLeft || (msg.Action != tea.MouseActionPress && msg.Action != tea.MouseActionRelease) {
		return
	}
	// Some terminals report releases only, while others report both. A
	// physical click must execute Back only once across those variants.
	if msg.Action == tea.MouseActionRelease && a.mousePressHandled {
		a.mousePressHandled = false
		return
	}
	a.mousePressHandled = msg.Action == tea.MouseActionPress
	if msg.Y >= a.followStart && msg.Y <= a.followEnd {
		t.focusActivity()
		t.followLatestActivity()
		return
	}
	if msg.Y >= a.backStart && msg.Y <= a.backEnd {
		if !a.focused && !a.inspecting {
			t.focusActivity()
			t.refreshActivity()
		} else {
			t.backFromActivity()
		}
		return
	}
	if msg.Y < len(a.header) {
		return
	}
	if a.inspecting {
		t.focusActivity()
		t.refreshActivity()
		return
	}
	line := msg.Y - len(a.header) + a.viewport.YOffset
	for index, start := range a.rowStarts {
		if line >= start && line <= a.rowEnds[index] {
			t.focusActivity()
			a.selectedTool = index
			t.inspectActivityTool()
			return
		}
	}
}

func (t *chatTab) scrollActivity(key string) {
	a := &t.activity
	a.following = false
	// Only the header changes. Keeping content untouched avoids shifting the
	// viewport while the operator scrolls through output arriving concurrently.
	t.refreshActivityHeader(len(t.activityTurns()) > 0)
	a.viewport.Height = maxInt(a.height-len(a.header), 1)
	switch key {
	case "up":
		a.viewport.ScrollUp(1)
	case "down":
		a.viewport.ScrollDown(1)
	case "pgup":
		a.viewport.ScrollUp(maxInt(a.viewport.Height-1, 1))
	case "pgdown":
		a.viewport.ScrollDown(maxInt(a.viewport.Height-1, 1))
	case "wheelup":
		a.viewport.ScrollUp(3)
	case "wheeldown":
		a.viewport.ScrollDown(3)
	case "home":
		a.viewport.GotoTop()
	case "end":
		a.viewport.GotoBottom()
	}
}

func (t *chatTab) selectActivityTool(direction int) {
	a := &t.activity
	indices := t.activityToolIndices(a.selectedTurn)
	if len(indices) == 0 {
		return
	}
	position := 0
	for i, index := range indices {
		if index == a.selectedTool {
			position = i
			break
		}
	}
	a.selectedTool = indices[clamp(position+direction, 0, len(indices)-1)]
	a.following = false
	if a.inspecting {
		a.viewport.GotoTop()
	}
	t.refreshActivity()
	if !a.inspecting {
		t.revealActivityTool()
	}
}

func (t *chatTab) selectActivityTurn(direction int) {
	a := &t.activity
	turns := t.activityTurns()
	if len(turns) == 0 {
		return
	}
	position := 0
	for i, turn := range turns {
		if turn == a.selectedTurn {
			position = i
			break
		}
	}
	a.selectedTurn = turns[clamp(position+direction, 0, len(turns)-1)]
	a.selectedTool = -1
	a.following = false
	a.inspecting = false
	a.listOffset = 0
	a.viewport.GotoTop()
	t.refreshActivity()
}

func (t *chatTab) inspectActivityTool() {
	if !t.activityToolSelected() || t.activity.inspecting {
		return
	}
	t.activity.listOffset = t.activity.viewport.YOffset
	t.activity.following = false
	t.activity.inspecting = true
	t.activity.viewport.GotoTop()
	t.refreshActivity()
}

func (t *chatTab) backFromActivity() {
	if t.activity.inspecting {
		t.activity.inspecting = false
		t.refreshActivity()
		t.activity.viewport.SetYOffset(t.activity.listOffset)
		return
	}
	t.closeActivity()
}

func (t *chatTab) revealActivityTool() {
	a := &t.activity
	start, ok := a.rowStarts[a.selectedTool]
	if !ok {
		return
	}
	end := a.rowEnds[a.selectedTool]
	if start < a.viewport.YOffset {
		a.viewport.SetYOffset(start)
	} else if end >= a.viewport.YOffset+a.viewport.Height {
		a.viewport.SetYOffset(maxInt(start, end-a.viewport.Height+1))
	}
}

func (t *chatTab) appendActivityToolDetail(lines *[]string, tool liveToolCall) {
	width := t.activity.width
	appendPiece := func(piece string) { *lines = append(*lines, activityPhysicalLines(piece, width)...) }
	appendPiece(renderNamedStatus(activitySafeText(tool.status)))
	appendPiece(styleMuted.Render(activitySafeText(firstNonEmptyText(tool.name, "Tool call"))))
	appendPiece("")
	if tool.command != "" {
		appendPiece(styleMuted.Render("COMMAND"))
		appendPiece(styleBright.Render(activitySafeText(tool.command)))
		appendPiece("")
	}
	if tool.approvalReason != "" {
		appendPiece(styleWarning.Render("POLICY REASON"))
		appendPiece(styleBase.Render(activitySafeText(tool.approvalReason)))
		appendPiece("")
	}
	appendPiece(styleMuted.Render("RAW OUTPUT  stdout + stderr"))
	output := strings.TrimSuffix(activitySafeText(tool.output), "\n")
	if output == "" {
		placeholder := "No output emitted."
		if tool.status == "RUNNING" || tool.status == "APPROVAL CHECK" {
			placeholder = "Waiting for output…"
		} else if tool.status == "APPROVAL REQUIRED" {
			placeholder = "Not run. Waiting for operator approval."
		}
		appendPiece(styleMuted.Render(placeholder))
	} else {
		appendPiece(styleBase.Render(output))
	}
	if tool.err != "" {
		appendPiece("")
		appendPiece(styleError.Render("ERROR"))
		appendPiece(styleError.Render(activitySafeText(tool.err)))
	}
	if tool.duration > 0 {
		appendPiece("")
		appendPiece(styleMuted.Render("DURATION  " + tool.duration.Round(time.Millisecond).String()))
	}
}

func (t *chatTab) activityToolSelected() bool {
	i := t.activity.selectedTool
	return i >= 0 && i < len(t.toolCalls) && t.toolCalls[i].turn == t.activity.selectedTurn
}

func (t *chatTab) activityToolIndices(turn int) []int {
	var indices []int
	for i, tool := range t.toolCalls {
		if tool.turn == turn {
			indices = append(indices, i)
		}
	}
	return indices
}

func (t *chatTab) activityPrompt(turn int) string {
	for _, message := range t.messages {
		if message.turn == turn && message.role == "user" {
			return message.content
		}
	}
	if turn == t.activeTurn {
		return t.pendingPrompt
	}
	return ""
}

func (t *chatTab) activityTurns() []int {
	seen := make(map[int]bool)
	if t.activeTurn > 0 {
		seen[t.activeTurn] = true
	}
	for _, message := range t.messages {
		if message.role == "user" {
			seen[message.turn] = true
		}
	}
	for _, tool := range t.toolCalls {
		seen[tool.turn] = true
	}
	for turn := range t.verifierActivity {
		seen[turn] = true
	}
	turns := make([]int, 0, len(seen))
	for turn := range seen {
		turns = append(turns, turn)
	}
	sort.Ints(turns)
	return turns
}

func (t *chatTab) latestActivityTurn(turns []int) int {
	if activityContainsTurn(turns, t.activeTurn) {
		return t.activeTurn
	}
	if len(turns) > 0 {
		return turns[len(turns)-1]
	}
	return 0
}

func activityContainsTurn(turns []int, wanted int) bool {
	for _, turn := range turns {
		if turn == wanted {
			return true
		}
	}
	return false
}

func activitySafeText(text string) string {
	return sanitizeToolOutput(secrets.Mask(text))
}

// Every entry is one terminal row, including multiline and wide-character
// content. These same rows define mouse hit ranges and viewport offsets.
func activityPhysicalLines(piece string, width int) []string {
	return strings.Split(wrapDisplay(piece, maxInt(width, 1)), "\n")
}
