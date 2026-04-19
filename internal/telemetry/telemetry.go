package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	mu sync.Mutex
)

// TelemetryData maps model name to a map of base shell commands and their counts.
type TelemetryData map[string]map[string]int

// RecordCommand updates the telemetry file for a specific model zero-shot shell execution attempt.
func RecordCommand(model, fullCommand string) error {
	mu.Lock()
	defer mu.Unlock()

	// Extract base command (e.g., 'docker' from 'docker ps -a')
	baseCmd := "unknown"
	fields := strings.Fields(strings.TrimSpace(fullCommand))
	if len(fields) > 0 {
		baseCmd = fields[0]
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".cvkeharness", "telemetry.json")

	data := make(TelemetryData)

	// Read existing data if possible
	contents, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(contents, &data)
	}

	if data[model] == nil {
		data[model] = make(map[string]int)
	}

	data[model][baseCmd]++

	newContents, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	// Ensure dir exists
	_ = os.MkdirAll(filepath.Dir(path), 0755)

	return os.WriteFile(path, newContents, 0644)
}
