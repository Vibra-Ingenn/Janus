package main

import (
	"net/http"
	"os"

	"janus/pkg/features"
)

// handle_config_mode returns the current mode gating setting.
// GET /config/mode
// If JANUS_LOCAL_ONLY is empty, "true", or "1" → {"local_only": true}
// If JANUS_LOCAL_ONLY is "false" or "0"         → {"local_only": false}
func (r *router) handle_config_mode(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	val := os.Getenv("JANUS_LOCAL_ONLY")
	localOnly := true
	if val == "false" || val == "0" {
		localOnly = false
	}
	write_json(w, http.StatusOK, map[string]any{"local_only": localOnly})
}

// handle_config_features exposes optional modules and load policy for the web UI.
// GET /config/features
func (r *router) handle_config_features(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	write_json(w, http.StatusOK, features.Snapshot())
}

// GET /config/connectors — how separate programs attach to Janus (this process is the host).
func (r *router) handle_config_connectors(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	write_json(w, http.StatusOK, map[string]any{
		"role":          "host",
		"listen_hint":   "http://127.0.0.1:8990",
		"docs":          "docs/CONNECTORS.md",
		"openai_compat": true,
		"endpoints": []string{
			"/health",
			"/v1/chat/completions",
			"/v1/models",
			"/models/list",
			"/models/load",
			"/models/unload",
			"/engine/status",
		},
	})
}

// handle_config_backends_reload re-reads backend_config.json without a restart.
// POST /config/backends/reload
func (r *router) handle_config_backends_reload(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	reload_backend_config(true)
	cfg := get_backend_config()
	backendConfigMu.RLock()
	path := backendConfigPath
	backendConfigMu.RUnlock()
	count := 0
	if cfg != nil {
		count = len(cfg.Backends)
	}
	write_json(w, http.StatusOK, map[string]any{"ok": true, "path": path, "backends": count})
}
