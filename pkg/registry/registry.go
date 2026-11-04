package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Entry is written to AdeptusTools registry when Janus starts.
type Entry struct {
	ID        string          `json:"id"`
	Address   string          `json:"address"`
	PID       int             `json:"pid"`
	StartedAt string          `json:"started_at"`
	Card      json.RawMessage `json:"card,omitempty"`
}

func Dir() string {
	if v := os.Getenv("ADEPTUS_TOOLS_REGISTRY"); v != "" {
		return v
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "AdeptusTools", "registry")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "adeptus-tools", "registry")
}

// Register writes this process to the registry folder.
func Register(id, listenAddr string, card []byte) error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	e := Entry{
		ID:        id,
		Address:   listenAddr,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Card:      card,
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".json"), b, 0o644)
}

// Unregister removes the registry entry on clean shutdown.
func Unregister(id string) {
	_ = os.Remove(filepath.Join(Dir(), id+".json"))
}
