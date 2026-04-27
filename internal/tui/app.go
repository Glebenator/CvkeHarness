package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
)

type section int

const (
	sectionJobs section = iota
	sectionCron
	sectionModels
	sectionCommands
	sectionMemory
	sectionHistory
	sectionSettings
)

var sections = []string{"Jobs", "Cron", "Models", "Commands", "Memory", "History", "Settings"}

type mode int

const (
	modeNormal mode = iota
	modeForm
	modeConfirm
	modeText
)

type formAction string

const (
	actionJobCreate       formAction = "job_create"
	actionJobEdit         formAction = "job_edit"
	actionCronAdd         formAction = "cron_add"
	actionCronUpdate      formAction = "cron_update"
	actionModelFavorite   formAction = "model_favorite"
	actionModelUnfavorite formAction = "model_unfavorite"
	actionModelApprove    formAction = "model_approve"
	actionCommandApprove  formAction = "command_approve"
	actionMemoryRollback  formAction = "memory_rollback"
)

type confirmAction string

const (
	confirmJobDelete confirmAction = "job_delete"
	confirmCronApply confirmAction = "cron_apply"
)

type formField struct {
	Label string
	Value string
}

type formState struct {
	Title  string
	Action formAction
	Fields []formField
	Index  int
	Meta   map[string]string
}

type confirmState struct {
	Title    string
	Body     string
	Action   confirmAction
	JobID    string
	Mutation systemcron.Mutation
}

type textState struct {
	Title string
	Body  string
}

// Model is the root Bubble Tea dashboard.
type Model struct {
	service     *Service
	settingsBin string

	width   int
	height  int
	section section
	mode    mode

	selected map[section]int
	snap     Snapshot
	err      error
	status   string

	input   textinput.Model
	form    *formState
	confirm *confirmState
	text    *textState
}

type snapshotMsg struct {
	snap Snapshot
	err  error
}

type opMsg struct {
	status string
	err    error
	reload bool
}

type settingsMsg struct {
	err error
}

// NewModel creates a dashboard model.
func NewModel(service *Service, settingsBin string) Model {
	input := textinput.New()
	input.Prompt = "› "
	input.CharLimit = 4096
	input.Width = 72
	return Model{
		service:     service,
		settingsBin: settingsBin,
		selected:    make(map[section]int),
		input:       input,
		status:      "Loading dashboard...",
	}
}

// Run starts the TUI.
func Run(service *Service, settingsBin string) error {
	p := tea.NewProgram(NewModel(service, settingsBin), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return m.loadCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = clamp(msg.Width-28, 30, 92)
		return m, nil
	case snapshotMsg:
		m.snap = msg.snap
		m.err = msg.err
		if msg.err != nil {
			m.status = "Loaded with warnings: " + compact(msg.err.Error(), 120)
		} else {
			m.status = "Loaded " + msg.snap.LoadedAt.Format("15:04:05")
		}
		m.clampSelection()
		return m, nil
	case opMsg:
		if msg.err != nil {
			m.status = "Error: " + compact(msg.err.Error(), 140)
			return m, nil
		}
		m.status = msg.status
		if msg.reload {
			return m, m.loadCmd()
		}
		return m, nil
	case settingsMsg:
		if msg.err != nil {
			m.status = "Settings exited with error: " + msg.err.Error()
		} else {
			m.status = "Settings closed"
		}
		return m, m.loadCmd()
	case tea.KeyMsg:
		switch m.mode {
		case modeForm:
			return m.updateForm(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		case modeText:
			if msg.String() == "esc" || msg.String() == "enter" || msg.String() == "q" {
				m.mode = modeNormal
				m.text = nil
			}
			return m, nil
		default:
			return m.updateNormal(msg)
		}
	}
	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab", "right", "l":
		m.section = (m.section + 1) % section(len(sections))
	case "shift+tab", "left", "h":
		m.section = (m.section + section(len(sections)) - 1) % section(len(sections))
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "r":
		m.status = "Refreshing..."
		return m, m.loadCmd()
	case "n":
		m.openCreateForm()
	case "e":
		m.openEditForm()
	case "p":
		return m.togglePauseCmd()
	case "R":
		return m.runJobNowCmd()
	case "d":
		m.openDeleteConfirm()
	case "enter":
		return m.enterSelected()
	case "a":
		m.openApproveForm()
	case "f":
		m.openFavoriteForm(true)
	case "u":
		if m.section == sectionMemory {
			m.openForm("Rollback memory snapshot", actionMemoryRollback, nil, []formField{{"Snapshot ID", ""}})
		} else {
			m.openFavoriteForm(false)
		}
	case "i":
		if m.section == sectionMemory {
			return m, m.opCmd("Reindexed memory", true, func(ctx context.Context) error {
				return m.service.ReindexMemory(ctx)
			})
		}
	case "x":
		return m.toggleCronCmd()
	}
	m.clampSelection()
	return m, nil
}

func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeForm()
		return m, nil
	case "tab", "down":
		m.commitInput()
		if m.form != nil && len(m.form.Fields) > 0 {
			m.form.Index = (m.form.Index + 1) % len(m.form.Fields)
			m.loadInput()
		}
		return m, nil
	case "shift+tab", "up":
		m.commitInput()
		if m.form != nil && len(m.form.Fields) > 0 {
			m.form.Index = (m.form.Index + len(m.form.Fields) - 1) % len(m.form.Fields)
			m.loadInput()
		}
		return m, nil
	case "enter":
		m.commitInput()
		return m.submitForm()
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "esc", "n":
		m.mode = modeNormal
		m.confirm = nil
		m.status = "Cancelled"
	case "y", "enter":
		c := m.confirm
		m.mode = modeNormal
		m.confirm = nil
		if c == nil {
			return m, nil
		}
		switch c.Action {
		case confirmJobDelete:
			return m, m.opCmd("Deleted job "+c.JobID, true, func(ctx context.Context) error {
				return m.service.DeleteJob(ctx, c.JobID)
			})
		case confirmCronApply:
			return m, m.opCmd("Applied crontab "+c.Mutation.Action, true, func(ctx context.Context) error {
				return m.service.ApplyCronMutation(ctx, c.Mutation)
			})
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		m.width = 100
	}
	header := titleStyle.Render("CvkeHarness") + " " + mutedStyle.Render("interactive operations hub")
	nav := m.renderNav()
	body := m.renderBody()
	footer := m.renderFooter()
	if m.mode == modeForm {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", m.renderForm())
	}
	if m.mode == modeConfirm {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", m.renderConfirm())
	}
	if m.mode == modeText {
		body = m.renderText()
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, nav, "", body, "", footer)
}

func (m Model) renderNav() string {
	items := make([]string, 0, len(sections))
	for i, item := range sections {
		if section(i) == m.section {
			items = append(items, tabActiveStyle.Render(item))
		} else {
			items = append(items, tabStyle.Render(item))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, items...)
}

func (m Model) renderBody() string {
	leftWidth := clamp(m.width/2-3, 36, 72)
	rightWidth := clamp(m.width-leftWidth-8, 36, 96)
	list := panelStyle.Width(leftWidth).Render(m.renderList(leftWidth - 4))
	detail := panelStyle.Width(rightWidth).Render(m.renderDetail(rightWidth - 4))
	return lipgloss.JoinHorizontal(lipgloss.Top, list, "  ", detail)
}

func (m Model) renderList(width int) string {
	var lines []string
	lines = append(lines, sectionTitleStyle.Render(sections[m.section]))
	switch m.section {
	case sectionJobs:
		if len(m.snap.Jobs) == 0 {
			lines = append(lines, mutedStyle.Render("No scheduled jobs"))
		}
		for i, job := range m.snap.Jobs {
			lines = append(lines, m.row(i, jobRow(job), width))
		}
	case sectionCron:
		if len(m.snap.CronEntries) == 0 {
			lines = append(lines, mutedStyle.Render("No crontab entries"))
		}
		for i, entry := range m.snap.CronEntries {
			lines = append(lines, m.row(i, cronRow(entry), width))
		}
	case sectionModels:
		items := m.modelRows()
		for i, item := range items {
			lines = append(lines, m.row(i, item, width))
		}
	case sectionCommands:
		items := append(prefixRows("allow", m.snap.CommandAllow), commandApprovalRows(m.snap.CommandApprovals)...)
		if len(items) == 0 {
			items = append(items, mutedStyle.Render("No command approvals"))
		}
		for i, item := range items {
			lines = append(lines, m.row(i, item, width))
		}
	case sectionMemory:
		items := memoryRows(m.snap.MemoryEntries, m.snap.Snapshots)
		if len(items) == 0 {
			items = append(items, mutedStyle.Render("No indexed memory entries"))
		}
		for i, item := range items {
			lines = append(lines, m.row(i, item, width))
		}
	case sectionHistory:
		items := historyRows(m.snap.Runs, m.snap.Chats)
		if len(items) == 0 {
			items = append(items, mutedStyle.Render("No persisted runs or chat sessions"))
		}
		for i, item := range items {
			lines = append(lines, m.row(i, item, width))
		}
	case sectionSettings:
		lines = append(lines, selectedStyle.Render("›  Open interactive settings"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderDetail(width int) string {
	var lines []string
	lines = append(lines, sectionTitleStyle.Render("Details"))
	switch m.section {
	case sectionJobs:
		lines = append(lines, m.jobDetail(width)...)
	case sectionCron:
		lines = append(lines, m.cronDetail(width)...)
	case sectionModels:
		lines = append(lines, m.modelsDetail(width)...)
	case sectionCommands:
		lines = append(lines, m.commandsDetail(width)...)
	case sectionMemory:
		lines = append(lines, m.memoryDetail(width)...)
	case sectionHistory:
		lines = append(lines, m.historyDetail(width)...)
	case sectionSettings:
		lines = append(lines, wrap("Press enter to launch the existing interactive settings editor. When it exits, the dashboard reloads configuration and state.", width)...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderFooter() string {
	keys := []string{"tab section", "j/k select", "r refresh", "q quit"}
	switch m.section {
	case sectionJobs:
		keys = append(keys, "n new", "e edit", "p pause/resume", "R run now", "d delete", "enter output")
	case sectionCron:
		keys = append(keys, "n add", "e update", "x enable/disable", "d remove", "enter raw")
	case sectionModels:
		keys = append(keys, "a approve", "f favorite", "u unfavorite")
	case sectionCommands:
		keys = append(keys, "a approve")
	case sectionMemory:
		keys = append(keys, "i reindex", "u rollback", "enter full")
	case sectionHistory:
		keys = append(keys, "enter inspect")
	case sectionSettings:
		keys = append(keys, "enter open settings")
	}
	status := m.status
	if m.err != nil {
		status = "Warning: " + m.err.Error()
	}
	return mutedStyle.Render(strings.Join(keys, " · ")) + "\n" + statusStyle.Render(compact(status, clamp(m.width-2, 40, 160)))
}

func (m Model) renderForm() string {
	if m.form == nil {
		return ""
	}
	lines := []string{sectionTitleStyle.Render(m.form.Title)}
	for i, field := range m.form.Fields {
		label := field.Label
		value := field.Value
		if i == m.form.Index {
			lines = append(lines, accentStyle.Render(label))
			lines = append(lines, m.input.View())
		} else {
			if strings.TrimSpace(value) == "" {
				value = mutedStyle.Render("(blank)")
			}
			lines = append(lines, mutedStyle.Render(label)+" "+value)
		}
	}
	lines = append(lines, mutedStyle.Render("enter submit · tab next field · esc cancel"))
	return panelStyle.Width(clamp(m.width-8, 56, 110)).Render(strings.Join(lines, "\n"))
}

func (m Model) renderConfirm() string {
	if m.confirm == nil {
		return ""
	}
	lines := []string{sectionTitleStyle.Render(m.confirm.Title)}
	lines = append(lines, wrap(m.confirm.Body, clamp(m.width-14, 40, 120))...)
	lines = append(lines, mutedStyle.Render("enter/y confirm · n/esc cancel"))
	return dangerPanelStyle.Width(clamp(m.width-8, 56, 120)).Render(strings.Join(lines, "\n"))
}

func (m Model) renderText() string {
	if m.text == nil {
		return ""
	}
	bodyWidth := clamp(m.width-8, 60, 140)
	lines := []string{sectionTitleStyle.Render(m.text.Title)}
	lines = append(lines, truncateLines(m.text.Body, bodyWidth, clamp(m.height-8, 14, 80))...)
	lines = append(lines, mutedStyle.Render("enter/esc/q return"))
	return panelStyle.Width(bodyWidth + 4).Render(strings.Join(lines, "\n"))
}

func (m Model) row(i int, text string, width int) string {
	text = compact(text, width-4)
	if i == m.selected[m.section] {
		return selectedStyle.Width(width).Render("› " + text)
	}
	return "  " + text
}

func (m Model) jobDetail(width int) []string {
	job, ok := m.selectedJob()
	if !ok {
		return []string{mutedStyle.Render("Select a job to inspect it.")}
	}
	lines := []string{
		label("ID", job.ID),
		label("Name", job.Name),
		label("Status", enabledText(job.Enabled)),
		label("Schedule", job.ScheduleKind+" "+job.ScheduleSpec),
		label("Next", timeText(job.NextRunAt)),
		label("Last", timeText(job.LastRunAt)+" "+job.LastRunStatus),
		label("Failures", strconv.Itoa(job.ConsecutiveFail)),
		"",
		accentStyle.Render("Prompt"),
	}
	lines = append(lines, wrap(job.Prompt, width)...)
	runs := m.snap.JobRuns[job.ID]
	lines = append(lines, "", accentStyle.Render("Recent Runs"))
	if len(runs) == 0 {
		lines = append(lines, mutedStyle.Render("No runs recorded"))
	}
	for _, run := range runs {
		line := fmt.Sprintf("#%d %s %s", run.ID, run.Status, run.StartedAt.Format("Jan 02 15:04"))
		if run.Error != "" {
			line += " error=" + compact(run.Error, width-28)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m Model) cronDetail(width int) []string {
	entry, ok := m.selectedCron()
	if !ok {
		return []string{mutedStyle.Render("Select a crontab entry.")}
	}
	id := entry.ID
	if id == "" {
		id = entry.Hash
	}
	lines := []string{
		label("Target", id),
		label("Line", strconv.Itoa(entry.Line+1)),
		label("Status", enabledText(!entry.Disabled)),
		label("Managed", fmt.Sprintf("%v", entry.Managed)),
		label("Schedule", entry.Schedule),
		"",
		accentStyle.Render("Command"),
	}
	lines = append(lines, wrap(entry.Command, width)...)
	if len(m.snap.CronAudits) > 0 {
		lines = append(lines, "", accentStyle.Render("Recent Audit"))
		for _, audit := range m.snap.CronAudits[:min(5, len(m.snap.CronAudits))] {
			lines = append(lines, fmt.Sprintf("%s target=%s success=%v", audit.Action, audit.Target, audit.Success))
		}
	}
	return lines
}

func (m Model) modelsDetail(width int) []string {
	lines := []string{
		label("Favorites", strconv.Itoa(len(m.snap.FavoriteModels))),
		label("Approved", strconv.Itoa(len(m.snap.ApprovedModels))),
		label("Routing candidates", strconv.Itoa(len(m.snap.Routing))),
		label("Recent usage", strconv.Itoa(len(m.snap.RecentModels))),
		"",
		accentStyle.Render("Recent Models"),
	}
	for _, item := range m.snap.RecentModels[:min(8, len(m.snap.RecentModels))] {
		successRate := 0.0
		if item.Uses > 0 {
			successRate = float64(item.Successes) / float64(item.Uses) * 100
		}
		lines = append(lines, compact(fmt.Sprintf("%s/%s uses=%d success=%.0f%%", item.Provider, item.RequestedModel, item.Uses, successRate), width))
	}
	if len(m.snap.RecentModels) == 0 {
		lines = append(lines, mutedStyle.Render("No recent model usage"))
	}
	return lines
}

func (m Model) commandsDetail(width int) []string {
	lines := []string{
		label("Static allowlist", strconv.Itoa(len(m.snap.CommandAllow))),
		label("Learned approvals", strconv.Itoa(len(m.snap.CommandApprovals))),
		"",
		accentStyle.Render("Recent Approvals"),
	}
	for _, item := range m.snap.CommandApprovals[:min(10, len(m.snap.CommandApprovals))] {
		lines = append(lines, compact(item.Command+" · "+item.Source+" · "+item.Status, width))
	}
	if len(m.snap.CommandApprovals) == 0 {
		lines = append(lines, mutedStyle.Render("No learned approvals"))
	}
	return lines
}

func (m Model) memoryDetail(width int) []string {
	lines := []string{
		label("Entries", strconv.Itoa(len(m.snap.MemoryEntries))),
		label("Snapshots", strconv.Itoa(len(m.snap.Snapshots))),
		"",
		accentStyle.Render("Preview"),
	}
	lines = append(lines, truncateLines(m.snap.Memory, width, 18)...)
	return lines
}

func (m Model) historyDetail(width int) []string {
	idx := m.selected[m.section]
	if idx < len(m.snap.Runs) {
		run := m.snap.Runs[idx]
		lines := []string{
			label("Run", strconv.FormatInt(run.ID, 10)),
			label("Task", run.Task),
			label("Provider", run.Provider),
			label("Class", string(run.TaskClass)),
			label("Success", fmt.Sprintf("%v", run.Success)),
			label("Started", timeText(run.StartedAt)),
			"",
			accentStyle.Render("Output"),
		}
		lines = append(lines, truncateLines(run.FinalOutput, width, 18)...)
		return lines
	}
	chatIdx := idx - len(m.snap.Runs)
	if chatIdx >= 0 && chatIdx < len(m.snap.Chats) {
		chat := m.snap.Chats[chatIdx]
		lines := []string{
			label("Chat", strconv.FormatInt(chat.ID, 10)),
			label("Provider", chat.Provider),
			label("Model", chat.PinnedModel),
			label("Turns", strconv.Itoa(chat.TurnCount)),
			label("Exit", chat.ExitReason),
			"",
			accentStyle.Render("Transcript"),
		}
		detail := m.snap.ChatDetails[chat.ID]
		for _, msg := range detail.Messages[:min(8, len(detail.Messages))] {
			lines = append(lines, compact(msg.Role+": "+strings.TrimSpace(msg.Content), width))
		}
		return lines
	}
	return []string{mutedStyle.Render("Select a run or chat session.")}
}

func (m *Model) openCreateForm() {
	switch m.section {
	case sectionJobs:
		m.openForm("Create scheduled job", actionJobCreate, nil, []formField{
			{"Name", "Scheduled job"},
			{"Kind (at/every/cron)", "every"},
			{"Spec", "1h"},
			{"Prompt", ""},
		})
	case sectionCron:
		m.openForm("Add crontab entry", actionCronAdd, nil, []formField{
			{"Name", ""},
			{"Schedule", "*/5 * * * *"},
			{"Command", ""},
		})
	}
}

func (m *Model) openEditForm() {
	switch m.section {
	case sectionJobs:
		job, ok := m.selectedJob()
		if !ok {
			return
		}
		m.openForm("Edit scheduled job", actionJobEdit, map[string]string{"id": job.ID}, []formField{
			{"Name", job.Name},
			{"Kind (at/every/cron)", job.ScheduleKind},
			{"Spec", job.ScheduleSpec},
			{"Prompt", job.Prompt},
		})
	case sectionCron:
		entry, ok := m.selectedCron()
		if !ok {
			return
		}
		m.openForm("Update crontab entry", actionCronUpdate, map[string]string{"target": cronTarget(entry)}, []formField{
			{"Schedule", entry.Schedule},
			{"Command", entry.Command},
		})
	}
}

func (m *Model) openApproveForm() {
	switch m.section {
	case sectionModels:
		m.openForm("Approve model", actionModelApprove, nil, []formField{{"Provider/model", ""}})
	case sectionCommands:
		m.openForm("Approve command", actionCommandApprove, nil, []formField{{"Command", ""}})
	}
}

func (m *Model) openFavoriteForm(favorite bool) {
	if m.section != sectionModels {
		return
	}
	action := actionModelFavorite
	title := "Favorite model"
	if !favorite {
		action = actionModelUnfavorite
		title = "Remove favorite"
	}
	m.openForm(title, action, nil, []formField{{"Provider/model", ""}})
}

func (m *Model) openDeleteConfirm() {
	switch m.section {
	case sectionJobs:
		job, ok := m.selectedJob()
		if !ok {
			return
		}
		m.mode = modeConfirm
		m.confirm = &confirmState{Title: "Delete job", Body: "Delete scheduled job " + job.ID + " and its run history?", Action: confirmJobDelete, JobID: job.ID}
	case sectionCron:
		entry, ok := m.selectedCron()
		if !ok {
			return
		}
		m.previewCron("remove", cronTarget(entry), "", "", "")
	}
}

func (m *Model) openForm(title string, action formAction, meta map[string]string, fields []formField) {
	m.mode = modeForm
	m.form = &formState{Title: title, Action: action, Fields: fields, Meta: meta}
	m.loadInput()
}

func (m *Model) closeForm() {
	m.mode = modeNormal
	m.form = nil
	m.input.Blur()
}

func (m *Model) loadInput() {
	if m.form == nil || len(m.form.Fields) == 0 {
		return
	}
	field := m.form.Fields[m.form.Index]
	m.input.SetValue(field.Value)
	m.input.Placeholder = field.Label
	m.input.Focus()
	m.input.CursorEnd()
}

func (m *Model) commitInput() {
	if m.form == nil || len(m.form.Fields) == 0 {
		return
	}
	m.form.Fields[m.form.Index].Value = m.input.Value()
}

func (m Model) submitForm() (tea.Model, tea.Cmd) {
	if m.form == nil {
		return m, nil
	}
	values := make([]string, len(m.form.Fields))
	for i, field := range m.form.Fields {
		values[i] = strings.TrimSpace(field.Value)
	}
	action := m.form.Action
	meta := m.form.Meta
	m.closeForm()

	switch action {
	case actionJobCreate:
		return m, m.opCmd("Created scheduled job", true, func(ctx context.Context) error {
			_, err := m.service.CreateJob(ctx, values[0], values[1], values[2], values[3])
			return err
		})
	case actionJobEdit:
		return m, m.opCmd("Updated scheduled job", true, func(ctx context.Context) error {
			_, err := m.service.UpdateJob(ctx, meta["id"], values[0], values[1], values[2], values[3])
			return err
		})
	case actionCronAdd:
		m.previewCron("add", "", values[1], values[2], values[0])
		return m, nil
	case actionCronUpdate:
		m.previewCron("update", meta["target"], values[0], values[1], "")
		return m, nil
	case actionModelFavorite:
		return m, m.opCmd("Favorited model", true, func(ctx context.Context) error {
			_, err := m.service.FavoriteModel(values[0], true)
			return err
		})
	case actionModelUnfavorite:
		return m, m.opCmd("Removed favorite", true, func(ctx context.Context) error {
			_, err := m.service.FavoriteModel(values[0], false)
			return err
		})
	case actionModelApprove:
		return m, m.opCmd("Approved model", true, func(ctx context.Context) error {
			_, err := m.service.ApproveModel(ctx, values[0])
			return err
		})
	case actionCommandApprove:
		return m, m.opCmd("Approved command", true, func(ctx context.Context) error {
			_, err := m.service.ApproveCommand(ctx, values[0])
			return err
		})
	case actionMemoryRollback:
		return m, m.opCmd("Rolled back memory", true, func(ctx context.Context) error {
			return m.service.RollbackMemory(ctx, values[0])
		})
	}
	return m, nil
}

func (m *Model) previewCron(action, target, schedule, command, name string) {
	mutation, diff, err := m.service.CronMutation(context.Background(), action, target, schedule, command, name)
	if err != nil {
		m.status = "Error: " + err.Error()
		return
	}
	m.mode = modeConfirm
	m.confirm = &confirmState{
		Title:    "Apply crontab " + action,
		Body:     "Review this diff before applying:\n\n" + diff,
		Action:   confirmCronApply,
		Mutation: mutation,
	}
}

func (m Model) enterSelected() (tea.Model, tea.Cmd) {
	switch m.section {
	case sectionJobs:
		job, ok := m.selectedJob()
		if !ok {
			return m, nil
		}
		runs := m.snap.JobRuns[job.ID]
		if len(runs) == 0 {
			m.status = "No run output for selected job"
			return m, nil
		}
		run := runs[0]
		body := run.Output
		if strings.TrimSpace(body) == "" {
			body = run.Error
		}
		if strings.TrimSpace(body) == "" {
			body = "(empty output)"
		}
		m.mode = modeText
		m.text = &textState{Title: fmt.Sprintf("Job %s run #%d", job.ID, run.ID), Body: body}
	case sectionCron:
		m.mode = modeText
		m.text = &textState{Title: "Raw crontab", Body: m.snap.RawCron}
	case sectionMemory:
		m.mode = modeText
		m.text = &textState{Title: "Memory", Body: m.snap.Memory}
	case sectionHistory:
		m.mode = modeText
		m.text = &textState{Title: "History detail", Body: strings.Join(m.historyDetail(clamp(m.width-14, 60, 140)), "\n")}
	case sectionSettings:
		if strings.TrimSpace(m.settingsBin) == "" {
			m.status = "Settings command unavailable"
			return m, nil
		}
		cmd := exec.Command(m.settingsBin, "settings")
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return settingsMsg{err: err} })
	}
	return m, nil
}

func (m Model) togglePauseCmd() (tea.Model, tea.Cmd) {
	if m.section != sectionJobs {
		return m, nil
	}
	job, ok := m.selectedJob()
	if !ok {
		return m, nil
	}
	enabled := !job.Enabled
	status := "Resumed job " + job.ID
	if !enabled {
		status = "Paused job " + job.ID
	}
	return m, m.opCmd(status, true, func(ctx context.Context) error {
		_, err := m.service.SetJobEnabled(ctx, job.ID, enabled)
		return err
	})
}

func (m Model) runJobNowCmd() (tea.Model, tea.Cmd) {
	if m.section != sectionJobs {
		return m, nil
	}
	job, ok := m.selectedJob()
	if !ok {
		return m, nil
	}
	return m, m.opCmd("Ran job "+job.ID, true, func(ctx context.Context) error {
		_, err := m.service.RunJobNow(ctx, job.ID)
		return err
	})
}

func (m Model) toggleCronCmd() (tea.Model, tea.Cmd) {
	if m.section != sectionCron {
		return m, nil
	}
	entry, ok := m.selectedCron()
	if !ok {
		return m, nil
	}
	action := "disable"
	if entry.Disabled {
		action = "enable"
	}
	m.previewCron(action, cronTarget(entry), "", "", "")
	return m, nil
}

func (m Model) opCmd(status string, reload bool, fn func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		err := fn(context.Background())
		return opMsg{status: status, err: err, reload: reload}
	}
}

func (m Model) loadCmd() tea.Cmd {
	return func() tea.Msg {
		snap, err := m.service.LoadSnapshot(context.Background())
		return snapshotMsg{snap: snap, err: err}
	}
}

func (m *Model) moveSelection(delta int) {
	count := m.sectionCount(m.section)
	if count <= 0 {
		m.selected[m.section] = 0
		return
	}
	next := m.selected[m.section] + delta
	if next < 0 {
		next = count - 1
	}
	if next >= count {
		next = 0
	}
	m.selected[m.section] = next
}

func (m *Model) clampSelection() {
	for i := range sections {
		sec := section(i)
		count := m.sectionCount(sec)
		if count <= 0 || m.selected[sec] < 0 {
			m.selected[sec] = 0
			continue
		}
		if m.selected[sec] >= count {
			m.selected[sec] = count - 1
		}
	}
}

func (m Model) sectionCount(sec section) int {
	switch sec {
	case sectionJobs:
		return len(m.snap.Jobs)
	case sectionCron:
		return len(m.snap.CronEntries)
	case sectionModels:
		return len(m.modelRows())
	case sectionCommands:
		return len(m.snap.CommandAllow) + len(m.snap.CommandApprovals)
	case sectionMemory:
		return len(m.snap.MemoryEntries) + len(m.snap.Snapshots)
	case sectionHistory:
		return len(m.snap.Runs) + len(m.snap.Chats)
	case sectionSettings:
		return 1
	default:
		return 0
	}
}

func (m Model) selectedJob() (state.ScheduledJob, bool) {
	idx := m.selected[sectionJobs]
	if idx < 0 || idx >= len(m.snap.Jobs) {
		return state.ScheduledJob{}, false
	}
	return m.snap.Jobs[idx], true
}

func (m Model) selectedCron() (systemcron.Entry, bool) {
	idx := m.selected[sectionCron]
	if idx < 0 || idx >= len(m.snap.CronEntries) {
		return systemcron.Entry{}, false
	}
	return m.snap.CronEntries[idx], true
}

func (m Model) modelRows() []string {
	var rows []string
	rows = append(rows, prefixRows("favorite", m.snap.FavoriteModels)...)
	rows = append(rows, prefixRows("approved", m.snap.ApprovedModels)...)
	for _, item := range m.snap.Routing {
		rows = append(rows, fmt.Sprintf("route %s/%s %s score=%.2f", item.Provider, item.Model, item.Status, item.Score))
	}
	for _, item := range m.snap.ModelAliases {
		rows = append(rows, fmt.Sprintf("alias %s/%s -> %s", item.Provider, item.RequestedModel, item.ActualModel))
	}
	for _, item := range m.snap.ModelStats {
		rows = append(rows, fmt.Sprintf("stat %s/%s %s runs=%d", item.Provider, item.Model, item.Phase, item.Runs))
	}
	if len(rows) == 0 {
		rows = append(rows, mutedStyle.Render("No model data yet"))
	}
	return rows
}

func jobRow(job state.ScheduledJob) string {
	return fmt.Sprintf("%s %s %s next=%s", enabledGlyph(job.Enabled), job.Name, job.ScheduleKind+" "+job.ScheduleSpec, shortTime(job.NextRunAt))
}

func cronRow(entry systemcron.Entry) string {
	target := cronTarget(entry)
	return fmt.Sprintf("%s %s %s", enabledGlyph(!entry.Disabled), target, entry.Schedule)
}

func cronTarget(entry systemcron.Entry) string {
	if entry.ID != "" {
		return entry.ID
	}
	if entry.Hash != "" {
		return entry.Hash
	}
	return strconv.Itoa(entry.Line + 1)
}

func prefixRows(prefix string, items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, prefix+" "+item)
	}
	return out
}

func commandApprovalRows(items []state.CommandApproval) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, "learned "+item.Command+" · "+item.Status)
	}
	return out
}

func memoryRows(entries []state.MemoryEntry, snapshots []state.Snapshot) []string {
	out := make([]string, 0, len(entries)+len(snapshots))
	for _, item := range entries {
		out = append(out, fmt.Sprintf("memory %s %.2f %s", item.SourceFile, item.Confidence, compact(item.Body, 60)))
	}
	for _, item := range snapshots {
		out = append(out, fmt.Sprintf("snapshot %s %s", item.ID, item.SourceFile))
	}
	return out
}

func historyRows(runs []state.RunSummary, chats []state.ChatSessionSummary) []string {
	out := make([]string, 0, len(runs)+len(chats))
	for _, run := range runs {
		out = append(out, fmt.Sprintf("run #%d %s success=%v", run.ID, compact(run.Task, 60), run.Success))
	}
	for _, chat := range chats {
		out = append(out, fmt.Sprintf("chat #%d %s turns=%d", chat.ID, chat.PinnedModel, chat.TurnCount))
	}
	return out
}

func enabledGlyph(enabled bool) string {
	if enabled {
		return "on "
	}
	return "off"
}

func enabledText(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func label(name, value string) string {
	return mutedStyle.Render(name+": ") + value
}

func timeText(t time.Time) string {
	if t.IsZero() {
		return "(none)"
	}
	return t.Format(time.RFC3339)
}

func shortTime(t time.Time) string {
	if t.IsZero() {
		return "(none)"
	}
	return t.Format("Jan 02 15:04")
}

func compact(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if width <= 0 || len([]rune(text)) <= width {
		return text
	}
	r := []rune(text)
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

func truncateLines(text string, width, limit int) []string {
	if strings.TrimSpace(text) == "" {
		return []string{mutedStyle.Render("(empty)")}
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, wrap(line, width)...)
		if len(out) >= limit {
			out = out[:limit]
			out = append(out, mutedStyle.Render("…"))
			break
		}
	}
	return out
}

func wrap(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	if width <= 10 {
		return []string{text}
	}
	words := strings.Fields(text)
	var lines []string
	var current string
	for _, word := range words {
		if len([]rune(current))+1+len([]rune(word)) > width && current != "" {
			lines = append(lines, current)
			current = word
			continue
		}
		if current == "" {
			current = word
		} else {
			current += " " + word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var (
	titleStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	mutedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	accentStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true)
	statusStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	sectionTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	tabStyle          = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("247"))
	tabActiveStyle    = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236")).Bold(true)
	selectedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236"))
	panelStyle        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(1, 2)
	dangerPanelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("167")).Padding(1, 2)
)
