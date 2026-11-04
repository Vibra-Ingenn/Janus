package main

import (
	"net/http"
	"strings"

	"janus/pkg/routerconfig"
)

func resolveLocalGGUF(modelRef, task string) (ggufPath string, backendID string) {
	cfg := get_backend_config()
	if cfg == nil {
		ref := strings.TrimSpace(modelRef)
		if strings.HasSuffix(strings.ToLower(ref), ".gguf") {
			return ref, ""
		}
		return "", ""
	}
	return cfg.ResolveLocalGGUF(modelRef, task)
}

func (r *router) loadLocalGGUF(ggufPath string, backendID string) error {
	ggufPath = strings.TrimSpace(ggufPath)
	if ggufPath == "" {
		return nil
	}
	_, err := r.hotSwapLocalModel(ggufPath, modelSwapOptions{Label: backendID})
	return err
}

func (r *router) ensureLocalModelForChat(modelName, task string) error {
	path, backendID := resolveLocalGGUF(modelName, task)
	if path == "" && backendID == "" && !routerconfig.IsGenericLocalRef(modelName) {
		path, backendID = resolveLocalGGUF(strings.TrimSpace(modelName), task)
	}
	return r.loadLocalGGUF(path, backendID)
}

func writeLocalModelLoadError(w http.ResponseWriter, err error) {
	write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}
