package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ModelSettings is persisted when the user hot-swaps models in the UI.
type ModelSettings struct {
	ModelPath  string `json:"model_path"`
	CtxSize    uint32 `json:"ctx_size,omitempty"`
	GpuLayers  int    `json:"gpu_layers,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

const modelSettingsFile = "data/model-settings.json"

var modelSettingsMu sync.Mutex

func modelSettingsPath() string {
	return filepath.Join(".", modelSettingsFile)
}

// LoadModelSettings reads the last UI-selected model (empty struct if missing).
func LoadModelSettings() ModelSettings {
	modelSettingsMu.Lock()
	defer modelSettingsMu.Unlock()

	data, err := os.ReadFile(modelSettingsPath())
	if err != nil {
		return ModelSettings{}
	}
	var s ModelSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return ModelSettings{}
	}
	return s
}

// SaveModelSettings writes the active model choice so restarts and the UI agree.
func SaveModelSettings(s ModelSettings) error {
	modelSettingsMu.Lock()
	defer modelSettingsMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(modelSettingsPath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(modelSettingsPath(), data, 0o644)
}
