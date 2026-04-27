package memory

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

const (
	OperatorFile     = "operator.md"
	SoulFile         = "soul.md"
	TargetsFile      = "targets.md"
	HostFile         = "host.md"
	PlaybooksFile    = "playbooks.md"
	FindingsFile     = "findings.md"
	CautionsFile     = "cautions.md"
	LegacyMemoryFile = "memory.md"
)

const (
	TargetKindRuntime        = "runtime"
	TargetKindSSH            = "ssh"
	TargetKindLocalContainer = "local_container"
	TargetKindUnknown        = "unknown"
)

const (
	IntentInspectLogs          = "inspect_logs"
	IntentInspectServiceStatus = "inspect_service_status"
	IntentRestartService       = "restart_service"
	IntentInstallDependency    = "install_dependency"
	IntentDockerRecovery       = "docker_recovery"
	IntentPortConflict         = "port_conflict"
	IntentNetworkDebug         = "network_debug"
	IntentBuildFix             = "build_fix"
	IntentTestFix              = "test_fix"
	IntentConfigEdit           = "config_edit"
	IntentSSHConnectivity      = "ssh_connectivity"
	IntentGeneral              = "general"
)

// RetrievalResult contains the compact prompt-ready memory brief.
type RetrievalResult struct {
	BuiltInRules       string
	Operator           string
	Soul               string
	RuntimeHostSummary string
	TargetSummary      string
	PlaybookBrief      string
	CautionBrief       string
	FallbackBrief      string
	Sources            []InjectionSource
}

// InjectionSource describes a memory section that was prepared for prompt
// injection. It is safe for logs and UI summaries because Preview is bounded.
type InjectionSource struct {
	Name    string
	Origin  string
	Chars   int
	Preview string
}

// Lesson keeps backward-compatible ad hoc finding writes available to tools.
type Lesson struct {
	Body       string
	Scope      string
	Provider   string
	Model      string
	ToolName   string
	TaskClass  core.TaskClass
	Phase      core.Phase
	Confidence float64
}

// TargetResolutionInput carries deterministic clues for resolving a target.
type TargetResolutionInput struct {
	Task    string
	Command string
}

// TargetResolution is the resolved runtime and active target identity.
type TargetResolution struct {
	RuntimeHostID string
	TargetID      string
	TargetKind    string
	PrimaryName   string
}

// ObservedToolCall captures a tool invocation for post-run curation.
type ObservedToolCall struct {
	ToolName     string
	Command      string
	Result       string
	Success      bool
	PolicyDenied bool
	DenialClass  string
	DurationMs   int64
}

// RunOutcome is the structured input to deterministic memory curation.
type RunOutcome struct {
	Task           string
	TaskClass      core.TaskClass
	Intent         string
	Target         TargetResolution
	Output         string
	ExecutionError string
	ToolCalls      []ObservedToolCall
}

type fileState struct {
	Targets          []targetRecord
	RuntimeHostID    string
	RuntimeHostFacts []state.HostFact
	RuntimeHostNotes []string
	Playbooks        []state.Playbook
	Findings         []state.Finding
	Cautions         []state.Caution
}

type targetRecord struct {
	Target    state.Target
	Aliases   []string
	Hostnames []string
	IPs       []string
	Facts     []state.HostFact
}

// Manager handles target-aware readable memory files plus structured state.
type Manager struct {
	dir         string
	store       *state.Store
	maxSnippets int
	now         func() time.Time
	hostname    func() string
}

// NewManager creates a new memory manager.
func NewManager(dir string, store *state.Store, maxSnippets int) *Manager {
	if maxSnippets <= 0 {
		maxSnippets = 3
	}
	return &Manager{
		dir:         dir,
		store:       store,
		maxSnippets: maxSnippets,
		now: func() time.Time {
			return time.Now().UTC()
		},
		hostname: func() string {
			name, err := os.Hostname()
			if err != nil || name == "" {
				return "localhost"
			}
			return name
		},
	}
}

// Dir returns the memory directory.
func (m *Manager) Dir() string {
	return m.dir
}

func (m *Manager) managedPath(name string) string {
	return filepath.Join(m.dir, name)
}

func structuredManagedFiles() []string {
	return []string{TargetsFile, HostFile, PlaybooksFile, FindingsFile, CautionsFile}
}

func allManagedFiles() []string {
	return []string{OperatorFile, SoulFile, TargetsFile, HostFile, PlaybooksFile, FindingsFile, CautionsFile}
}

func (m *Manager) loadState(ctx context.Context) (fileState, error) {
	if m.store != nil && m.store.Available() {
		mem, err := m.store.LoadOperationalMemory(ctx)
		if err == nil && (len(mem.Targets) > 0 || len(mem.Playbooks) > 0 || len(mem.Findings) > 0 || len(mem.Cautions) > 0 || len(mem.HostFacts) > 0) {
			return stateFromOperationalMemory(mem), nil
		}
	}
	return m.parseManagedFiles()
}
