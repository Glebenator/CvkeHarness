package safety

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func writeJSONAndMarkdownReport(outputDir, jsonName, markdownName string, payload any, markdown string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	jsonBytes, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, jsonName), jsonBytes, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputDir, markdownName), []byte(markdown), 0644)
}
