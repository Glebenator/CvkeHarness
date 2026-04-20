package memory

import (
	"path/filepath"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

const (
	// File names exposed to the user.
	OperatorFile = "operator.md"
	SoulFile     = "soul.md"
	MemoryFile   = "memory.md"
	FindingsFile = "findings.md"
)

// RetrievalResult contains the system-facing context injected into a run.
type RetrievalResult struct {
	BuiltInRules string
	Operator     string
	Soul         string
	Learned      string
	Snippets     []state.MemoryEntry
}

// Lesson is a curated memory candidate.
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

// Manager handles readable memory files plus machine metadata.
type Manager struct {
	dir         string
	store       *state.Store
	maxSnippets int
	now         func() time.Time
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
	}
}

// Dir returns the memory directory.
func (m *Manager) Dir() string {
	return m.dir
}

func (m *Manager) managedPath(name string) string {
	return filepath.Join(m.dir, name)
}

func managedSourceFiles() []string {
	return []string{MemoryFile, FindingsFile}
}

func allManagedFiles() []string {
	return []string{OperatorFile, SoulFile, MemoryFile, FindingsFile}
}
