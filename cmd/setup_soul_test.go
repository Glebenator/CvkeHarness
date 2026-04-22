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
