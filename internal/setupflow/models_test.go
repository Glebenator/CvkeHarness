package setupflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexStaleCatalogRetainsChoices(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	data := `{"fetched_at":"2020-01-01T00:00:00Z","models":[{"slug":"model-test","visibility":"list","supported_in_api":true},{"slug":"hidden-test","visibility":"hide","supported_in_api":true}]}`
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	result := fetchCodexModels(time.Now())
	if result.Live || result.Source != "codex-cache" || len(result.Items) != 2 || result.Items[0].ID != "model-test" || result.Message == "" {
		t.Fatalf("stale catalog: %+v", result)
	}
}
