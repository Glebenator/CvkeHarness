package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/memory"
)

func TestWriteSetupSoulCreatesGeneratedSoulFromStub(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, memory.SoulFile), []byte("# Soul\n\n"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	wrote, err := writeSetupSoul(dir, 3, soulProfileByID("mentor"))
	if err != nil {
		t.Fatalf("writeSetupSoul returned error: %v", err)
	}
	if !wrote {
		t.Fatal("expected setup to replace the empty soul stub")
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.SoulFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "- Tone: explanatory") {
		t.Fatalf("expected mentor tone in generated soul, got %q", content)
	}
	if !strings.Contains(content, "## Purpose") {
		t.Fatalf("expected generated soul template, got %q", content)
	}
}

func TestWriteSetupSoulPreservesExistingUserSoul(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := "# Soul\n\nCustom user-owned guidance.\n"
	if err := os.WriteFile(filepath.Join(dir, memory.SoulFile), []byte(want), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	wrote, err := writeSetupSoul(dir, 3, soulProfileByID("concise"))
	if err != nil {
		t.Fatalf("writeSetupSoul returned error: %v", err)
	}
	if wrote {
		t.Fatal("expected setup to preserve an existing soul")
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.SoulFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != want {
		t.Fatalf("expected user soul to remain unchanged, got %q", string(data))
	}
}

func TestWriteSetupSoulEnsuresOtherMemoryFilesExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, memory.PlaybooksFile)); !os.IsNotExist(err) {
		t.Fatalf("expected playbooks.md to start missing, stat err=%v", err)
	}

	if _, err := writeSetupSoul(dir, 5, defaultSoulProfile()); err != nil {
		t.Fatalf("writeSetupSoul returned error: %v", err)
	}

	for _, name := range []string{
		memory.OperatorFile,
		memory.SoulFile,
		memory.TargetsFile,
		memory.HostFile,
		memory.PlaybooksFile,
		memory.FindingsFile,
		memory.CautionsFile,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist after setup, got %v", name, err)
		}
	}
}

func TestWriteSetupHostNotesSeedsRuntimeHostNotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	status, err := writeSetupHostNotes(dir, 5, []string{
		"Docker requires sudo",
		"Homebrew lives in /opt/homebrew",
	})
	if err != nil {
		t.Fatalf("writeSetupHostNotes returned error: %v", err)
	}
	if status != setupHostNotesWritten {
		t.Fatalf("expected setupHostNotesWritten, got %v", status)
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.HostFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "### Notes") {
		t.Fatalf("expected host.md to include a Notes section, got %q", content)
	}
	if !strings.Contains(content, "- Docker requires sudo") {
		t.Fatalf("expected Docker note in host.md, got %q", content)
	}
	if !strings.Contains(content, "- Homebrew lives in /opt/homebrew") {
		t.Fatalf("expected Homebrew note in host.md, got %q", content)
	}
}

func TestWriteSetupHostNotesPreservesExistingRuntimeHostNotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := writeSetupHostNotes(dir, 5, []string{"Docker requires sudo"}); err != nil {
		t.Fatalf("first writeSetupHostNotes returned error: %v", err)
	}

	status, err := writeSetupHostNotes(dir, 5, []string{"Corporate VPN rewrites DNS"})
	if err != nil {
		t.Fatalf("second writeSetupHostNotes returned error: %v", err)
	}
	if status != setupHostNotesPreserved {
		t.Fatalf("expected setupHostNotesPreserved, got %v", status)
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.HostFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "- Docker requires sudo") {
		t.Fatalf("expected original note to remain in host.md, got %q", content)
	}
	if strings.Contains(content, "Corporate VPN rewrites DNS") {
		t.Fatalf("expected existing host notes to be preserved without overwrite, got %q", content)
	}
}
