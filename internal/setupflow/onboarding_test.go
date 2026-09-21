package setupflow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/coolcake/cvkeharness/config"
)

func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestWizardReloadPreservesFullConfiguration(t *testing.T) {
	setupHome(t)
	cfg := config.DefaultConfig()
	cfg.Provider = "lmstudio"
	cfg.DefaultModel = "local"
	cfg.SafetyModel = "judge"
	cfg.PlanningModel = "lmstudio/planner"
	cfg.ExecutionModel = "lmstudio/executor"
	cfg.CurationModel = "lmstudio/curator"
	cfg.PromptDumpRetentionDays = 42
	cfg.Recovery.Roots = []string{filepath.Join(t.TempDir(), "recovery")}
	cfg.Recovery.MaxFiles = 3
	EnsureDefaultApproved(cfg)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWizardConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, loaded) {
		t.Fatal("setup did not preserve the complete saved configuration")
	}
	if err := finalizeWithoutActions(loaded); err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, saved) {
		t.Fatal("rerunning setup lost configuration")
	}
}

func finalizeWithoutActions(cfg *config.Config) error {
	_, err := Finalize(context.Background(), FinalizeOptions{Config: cfg})
	return err
}

func TestProviderSwitchResetsNativeModelsAndKeepsCredentials(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SetAPIKey("openrouter", "retained")
	SelectProvider(cfg, "codex")
	if cfg.PrimaryModel() != "" || cfg.SafetyModel != "" {
		t.Fatal("old models crossed providers")
	}
	SetDefaultModel(cfg, "codex/test-primary")
	if cfg.PrimaryModel() != "test-primary" || cfg.SafetyModel != "test-primary" {
		t.Fatal("judge did not follow selected primary")
	}
	cfg.SafetyModel = "custom-judge"
	SetDefaultModel(cfg, "another-primary")
	if cfg.SafetyModel != "custom-judge" || cfg.GetAPIKey("openrouter") != "retained" {
		t.Fatal("explicit settings lost")
	}
}

func TestFinalizeCannotPublishIncompleteSetup(t *testing.T) {
	home := setupHome(t)
	cfg := config.DefaultConfig()
	if _, err := Finalize(context.Background(), FinalizeOptions{Config: cfg}); err == nil {
		t.Fatal("missing API key accepted")
	}
	if _, err := os.Stat(filepath.Join(home, ".cvkeharness")); !os.IsNotExist(err) {
		t.Fatal("incomplete setup wrote state")
	}
	cfg.Provider = "lmstudio"
	cfg.MemoryDir = filepath.Join(home, "not-a-directory", "memory")
	if err := os.WriteFile(filepath.Join(home, "not-a-directory"), []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Finalize(context.Background(), FinalizeOptions{Config: cfg}); err == nil {
		t.Fatal("expected artifact write failure")
	}
	path, _ := config.ConfigPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed setup published config")
	}
}

func TestSkippedScanDoesNotInventHostFacts(t *testing.T) {
	home := setupHome(t)
	cfg := config.DefaultConfig()
	cfg.Provider = "lmstudio"
	result, err := Finalize(context.Background(), FinalizeOptions{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if result.HostProfilePath != "" || result.HostNotesWritten {
		t.Fatalf("unscanned host was persisted: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(home, ".cvkeharness", HostProfileFile)); !os.IsNotExist(err) {
		t.Fatal("scan artifact exists")
	}
}

func TestSetupRepairsEmptyConfigWithUsableRuntimeLimits(t *testing.T) {
	setupHome(t)
	path, _ := config.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWizardConfig()
	if err != nil {
		t.Fatal(err)
	}
	SelectProvider(cfg, "lmstudio")
	SetDefaultModel(cfg, "local-model")
	if err := finalizeWithoutActions(cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if saved.MaxTokens <= 0 || saved.MaxIterations <= 0 {
		t.Fatal("repaired setup saved unusable runtime limits")
	}
}
