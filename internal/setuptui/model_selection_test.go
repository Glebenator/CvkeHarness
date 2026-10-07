package setuptui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/glebenator/cvkeharness/config"
	"github.com/glebenator/cvkeharness/internal/modelui"
	"github.com/glebenator/cvkeharness/internal/setupflow"
)

func connectionSetupFixture() setupModel {
	cfg := config.DefaultConfig()
	cfg.Normalize()
	cfg.Connections = map[string]config.Connection{
		"local":  {Name: "Local machine", Provider: "lmstudio", BaseURL: "http://127.0.0.1:1/v1"},
		"review": {Name: "Review server", Provider: "lmstudio", BaseURL: "http://127.0.0.1:2/v1"},
	}
	cfg.Models = config.ModelRoles{
		Primary:     config.ModelBinding{Connection: "local", Model: "org/primary"},
		SafetyJudge: config.ModelBinding{Inherit: config.RolePrimary},
	}
	cfg.EnsureModelBindings()
	return setupModel{cfg: cfg, step: stepProvider, width: 80, height: 24}
}

func finishPicker(m setupModel, role config.ModelRole, binding config.ModelBinding) setupModel {
	next, _ := m.Update(modelui.PickerResultMsg{PickerID: m.modelPicker.ID(), Role: role, Binding: binding})
	return next.(setupModel)
}

func TestSetupSharedRolesPreserveConnectionAndNativeModel(t *testing.T) {
	m := connectionSetupFixture()
	m, _ = m.beginModelPicker(config.RolePrimary, "local")
	m = finishPicker(m, config.RolePrimary, config.ModelBinding{Connection: "local", Model: "publisher/primary/v2"})
	if m.step != stepSafety || m.modelPicker != nil {
		t.Fatal("Primary selection did not advance to Safety")
	}
	m.cursor = m.preferredCursor()
	m = press(m, enterKey)
	if m.step != stepJudge || m.modelPicker == nil {
		t.Fatal("Safety did not open the shared Safety judge picker")
	}
	m = finishPicker(m, config.RoleSafetyJudge, config.ModelBinding{Connection: "review", Model: "publisher/judge/v3"})
	primary, err := m.cfg.ResolveRole(config.RolePrimary)
	if err != nil || primary.ConnectionID != "local" || primary.Model != "publisher/primary/v2" {
		t.Fatalf("Primary binding was changed: %#v, %v", primary, err)
	}
	judge, err := m.cfg.ResolveRole(config.RoleSafetyJudge)
	if err != nil || judge.ConnectionID != "review" || judge.Model != "publisher/judge/v3" || m.step != stepScan {
		t.Fatalf("independent judge was lost: %#v, %v, step=%v", judge, err, m.step)
	}
	m = press(m, enterKey)
	m = press(m, enterKey)
	if m.step != stepReview || !strings.Contains(m.viewReview(), "Review server") || !strings.Contains(m.viewReview(), "publisher/judge/v3") {
		t.Fatal("optional stages or role-aware review were lost")
	}
}

func TestSetupJudgeInheritanceFollowsLaterPrimaryChange(t *testing.T) {
	m := connectionSetupFixture()
	m, _ = m.beginModelPicker(config.RoleSafetyJudge, "")
	m = finishPicker(m, config.RoleSafetyJudge, config.ModelBinding{Inherit: config.RolePrimary})
	m.cfg.SetRoleBinding(config.RolePrimary, config.ModelBinding{Connection: "review", Model: "native/new-primary"})
	judge, err := m.cfg.ResolveRole(config.RoleSafetyJudge)
	if err != nil || judge.ConnectionID != "review" || judge.Model != "native/new-primary" {
		t.Fatalf("inheritance was flattened into a stale copy: %#v, %v", judge, err)
	}
	if !strings.Contains(m.modelRoleSummary(config.RoleSafetyJudge), "Same as primary") {
		t.Fatal("review hides judge inheritance")
	}
}

func TestSetupPickerCancellationAndStaleMessagesDoNotMutateRoles(t *testing.T) {
	m := connectionSetupFixture()
	before := m.cfg.RoleBinding(config.RolePrimary)
	m, _ = m.beginModelPicker(config.RolePrimary, "review")
	oldID := m.modelPicker.ID()
	m, _ = m.beginModelPicker(config.RolePrimary, "local")
	next, _ := m.Update(modelui.PickerResultMsg{PickerID: oldID, Role: config.RolePrimary, Binding: config.ModelBinding{Connection: "review", Model: "wrong"}})
	m = next.(setupModel)
	if m.cfg.RoleBinding(config.RolePrimary) != before || m.modelPicker == nil {
		t.Fatal("stale picker result changed the current role")
	}
	next, _ = m.Update(modelui.PickerResultMsg{PickerID: m.modelPicker.ID(), Role: config.RolePrimary, Cancelled: true})
	m = next.(setupModel)
	if m.step != stepProvider || m.modelPicker != nil || m.cfg.RoleBinding(config.RolePrimary) != before {
		t.Fatal("cancelling a connection/model choice changed Primary")
	}
}

func TestSetupSharedPickerConsumesSearchInsteadOfWizardNextShortcut(t *testing.T) {
	m := connectionSetupFixture()
	m, _ = m.beginModelPicker(config.RolePrimary, "local")
	for _, r := range "reason" {
		m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.step != stepModel || m.modelPicker == nil {
		t.Fatal("typing a search triggered the wizard's N shortcut")
	}
	if !strings.Contains(ansi.Strip(m.View()), "reason") {
		t.Fatal("typed search was not passed to the shared picker")
	}
}

func TestSetupConnectionEditsAreStagedWithoutChangingRoles(t *testing.T) {
	m := connectionSetupFixture()
	before := m.cfg.RoleBinding(config.RolePrimary)
	m, _ = m.beginConnectionEditor("")
	newConnection := config.Connection{Name: "Second local server", Provider: "lmstudio", BaseURL: "http://127.0.0.1:3/v1"}
	next, _ := m.Update(modelui.ConnectionResultMsg{EditorID: m.connectionEditor.ID(), ID: "second", Connection: newConnection})
	m = next.(setupModel)
	if m.cfg.Connections["second"] != newConnection || m.cfg.RoleBinding(config.RolePrimary) != before || m.modelPicker == nil {
		t.Fatal("connection editing changed a role before model selection")
	}
	next, _ = m.Update(modelui.PickerResultMsg{PickerID: m.modelPicker.ID(), Role: config.RolePrimary, Cancelled: true})
	m = next.(setupModel)
	m, _ = m.beginConnectionEditor("second")
	next, _ = m.Update(modelui.ConnectionResultMsg{EditorID: m.connectionEditor.ID(), OriginalID: "second", ID: "second", Cancelled: true})
	m = next.(setupModel)
	if m.step != stepProvider || m.cfg.Connections["second"] != newConnection || m.cfg.RoleBinding(config.RolePrimary) != before {
		t.Fatal("cancelled connection editor altered the staged configuration")
	}
}

func TestSetupReviewBlocksMissingNamedCodexLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	m := connectionSetupFixture()
	auth := filepath.Join(home, "codex", "auth.json")
	m.cfg.Connections["account"] = config.Connection{Name: "Account", Provider: "codex", AuthFile: auth}
	m.cfg.SetRoleBinding(config.RolePrimary, config.ModelBinding{Connection: "account", Model: "codex-primary"})
	m.step = stepReview
	m = press(m, enterKey)
	if m.saving || m.errMessage == "" {
		t.Fatal("Review accepted a missing named Codex login")
	}
	if err := os.MkdirAll(filepath.Dir(auth), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte(`{"tokens":{"access_token":"synthetic-test-token","account_id":"test-account"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m = press(m, enterKey)
	if !m.saving {
		t.Fatalf("synthetic named login did not pass local readiness: %s", m.errMessage)
	}
}

func TestSetupSharedModelViewsFitRepresentativeTerminals(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 28}, {120, 32}} {
		for _, role := range []config.ModelRole{config.RolePrimary, config.RoleSafetyJudge} {
			m := connectionSetupFixture()
			m.width, m.height = size[0], size[1]
			m, _ = m.beginModelPicker(role, "")
			view := m.View()
			if got := len(strings.Split(view, "\n")); got > size[1] {
				t.Fatalf("%s view height %d > %d", role, got, size[1])
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("%s view exceeds %d columns: %s", role, size[0], line)
				}
			}
		}
	}
}

func TestSetupSafetyProfileSelectionKeepsHeaderVisibleAt24Rows(t *testing.T) {
	for _, width := range []int{80, 100, 120} {
		m := connectionSetupFixture()
		m.width, m.height, m.step = width, 24, stepSafety
		for cursor := 0; cursor < m.itemCount(); cursor++ {
			m.cursor = cursor
			view := ansi.Strip(m.View())
			if got := len(strings.Split(view, "\n")); got > 24 {
				t.Fatalf("Safety profile %d at %d columns exceeds 24 rows: %d", cursor, width, got)
			}
			if !strings.Contains(view, "GUIDED SETUP") || !strings.Contains(view, "▸ "+modelRows(setupflow.SafetyOptions())[cursor].label) {
				t.Fatal("Safety header or selected profile is missing")
			}
		}
	}
}
