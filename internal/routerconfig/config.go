package routerconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type BackendEntry struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	ModelPath     string  `json:"model_path"`
	DefaultModel  string  `json:"default_model"`
	Temperature   float64 `json:"temperature"`
	SystemPrompt  string  `json:"system_prompt_file"`
	UseCase       string  `json:"use_case"`
}

type Config struct {
	Version       string            `json:"version"`
	Backends      []BackendEntry    `json:"backends"`
	RoutingRules  map[string]string `json:"routing_rules"`
	FallbackChain []string          `json:"fallback_chain"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = "backend_config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func FindConfigFile() string {
	candidates := []string{"backend_config.json"}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "backend_config.json"),
			filepath.Join(filepath.Dir(dir), "backend_config.json"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "backend_config.json"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (c *Config) BackendByID(id string) *BackendEntry {
	if c == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	for i := range c.Backends {
		if c.Backends[i].ID == id {
			return &c.Backends[i]
		}
	}
	return nil
}

func (c *Config) ResolveTaskBackend(task string) string {
	if c == nil || c.RoutingRules == nil {
		return ""
	}
	return strings.TrimSpace(c.RoutingRules[task])
}
