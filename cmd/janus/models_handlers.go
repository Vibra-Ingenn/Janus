package main

import (
	"net/http"
	"os"
	"strings"

	"janus/pkg/discover"
)

func (r *router) handle_models_list(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	type modelEntry struct {
		Name      string   `json:"name"`
		Path      string   `json:"path"`
		Size      int64    `json:"size_bytes"`
		Backend   string   `json:"backend,omitempty"`
		Label     string   `json:"label,omitempty"`
		UseCase   string   `json:"use_case,omitempty"`
		HotSwap   bool     `json:"hot_swap"`
		LocalFile bool     `json:"local_file"`
		Source    string   `json:"source,omitempty"`
		Kind      string   `json:"kind,omitempty"`
		Aliases   []string `json:"aliases,omitempty"`
	}
	var models []modelEntry

	seen := map[string]bool{}

	for _, m := range discover.ScanLocalGGUF() {
		if seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		models = append(models, modelEntry{
			Name:      m.Name,
			Path:      m.Path,
			Size:      m.Size,
			Backend:   "vulkan",
			Label:     m.Name,
			HotSwap:   true,
			LocalFile: true,
			Source:    m.Source,
			Kind:      m.Kind,
			Aliases:   m.Aliases,
		})
	}

	for _, entry := range list_configured_model_entries() {
		name, _ := entry["name"].(string)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		path, _ := entry["path"].(string)
		backend, _ := entry["backend"].(string)
		if path == "" && (strings.EqualFold(backend, "vulkan") || strings.EqualFold(backend, "cpu")) {
			continue
		}
		label, _ := entry["label"].(string)
		useCase, _ := entry["use_case"].(string)
		localFile := strings.EqualFold(backend, "vulkan") || strings.EqualFold(backend, "cpu")
		models = append(models, modelEntry{
			Name:      name,
			Path:      path,
			Size:      0,
			Backend:   backend,
			Label:     label,
			UseCase:   useCase,
			HotSwap:   true,
			LocalFile: localFile,
		})
	}

	backend := strings.TrimSpace(os.Getenv("INFERENCE_BACKEND"))
	if backend == "openrouter" && len(models) == 0 {
		modelName := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
		if modelName == "" {
			modelName = "openai/gpt-4o-mini"
		}
		models = append(models, modelEntry{
			Name:    "openrouter",
			Path:    modelName,
			Backend: "openrouter",
			HotSwap: true,
		})
		write_json(w, http.StatusOK, map[string]any{"models": models})
		return
	}

	if r.provider == nil || backend == "ollama" {
		ollamaURL := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL"))
		if ollamaURL == "" {
			ollamaURL = "http://127.0.0.1:11434"
		}
		client := new_ollama_client(ollamaURL)
		if tags, err := client.tags(req.Context()); err == nil {
			for _, m := range tags {
				if seen[m] {
					continue
				}
				seen[m] = true
				models = append(models, modelEntry{
					Name:    m,
					Path:    m,
					HotSwap: true,
				})
			}
			write_json(w, http.StatusOK, map[string]any{"models": models})
			return
		}
	}

	write_json(w, http.StatusOK, map[string]any{"models": models})
}
