package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/memory"
)

func TestWriteSetupSoulCreatesGeneratedGuidanceFromStub(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, memory.GuidanceFile), []byte("# Guidance\n\n"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	wrote, err := writeSetupSoul(dir, soulProfileByID("mentor"))
	if err != nil {
		t.Fatalf("writeSetupSoul returned error: %v", err)
	}
	if !wrote {
		t.Fatal("expected setup to replace the empty guidance stub")
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.GuidanceFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "- Tone: explanatory") {
		t.Fatalf("expected mentor tone in generated guidance, got %q", content)
	}
	if !strings.Contains(content, "## Purpose") {
		t.Fatalf("expected generated guidance template, got %q", content)
	}
}

func TestWriteSetupSoulPreservesExistingUserGuidance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := "# Guidance\n\nCustom user-owned guidance.\n"
	if err := os.WriteFile(filepath.Join(dir, memory.GuidanceFile), []byte(want), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	wrote, err := writeSetupSoul(dir, soulProfileByID("concise"))
	if err != nil {
		t.Fatalf("writeSetupSoul returned error: %v", err)
	}
	if wrote {
		t.Fatal("expected setup to preserve existing guidance")
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.GuidanceFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != want {
		t.Fatalf("expected user guidance to remain unchanged, got %q", string(data))
	}
}

func TestWriteSetupSoulEnsuresOtherMemoryFilesExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, memory.PlaybooksFile)); !os.IsNotExist(err) {
		t.Fatalf("expected playbooks.md to start missing, stat err=%v", err)
	}

	if _, err := writeSetupSoul(dir, defaultSoulProfile()); err != nil {
		t.Fatalf("writeSetupSoul returned error: %v", err)
	}

	for _, name := range []string{
		memory.GuidanceFile,
		memory.TargetsFile,
		memory.PlaybooksFile,
		memory.FindingsFile,
		memory.CautionsFile,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist after setup, got %v", name, err)
		}
	}
}

func TestWriteSetupHostNotesSeedsGuidanceNotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	status, err := writeSetupHostNotes(dir, []string{
		"Docker requires sudo",
		"Homebrew lives in /opt/homebrew",
	})
	if err != nil {
		t.Fatalf("writeSetupHostNotes returned error: %v", err)
	}
	if status != setupHostNotesWritten {
		t.Fatalf("expected setupHostNotesWritten, got %v", status)
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.GuidanceFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## Runtime Host Notes") {
		t.Fatalf("expected guidance.md to include runtime host notes, got %q", content)
	}
	if !strings.Contains(content, "- Docker requires sudo") {
		t.Fatalf("expected Docker note in guidance.md, got %q", content)
	}
	if !strings.Contains(content, "- Homebrew lives in /opt/homebrew") {
		t.Fatalf("expected Homebrew note in guidance.md, got %q", content)
	}
}

func TestWriteSetupHostNotesPreservesExistingGuidanceNotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := writeSetupHostNotes(dir, []string{"Docker requires sudo"}); err != nil {
		t.Fatalf("first writeSetupHostNotes returned error: %v", err)
	}

	status, err := writeSetupHostNotes(dir, []string{"Corporate VPN rewrites DNS"})
	if err != nil {
		t.Fatalf("second writeSetupHostNotes returned error: %v", err)
	}
	if status != setupHostNotesPreserved {
		t.Fatalf("expected setupHostNotesPreserved, got %v", status)
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.GuidanceFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "- Docker requires sudo") {
		t.Fatalf("expected original note to remain in guidance.md, got %q", content)
	}
	if strings.Contains(content, "Corporate VPN rewrites DNS") {
		t.Fatalf("expected existing host notes to be preserved without overwrite, got %q", content)
	}
}
