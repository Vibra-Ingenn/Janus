package main

import (
	"encoding/json"
	"net/http"

	"janus/pkg/registry"
	"janus/pkg/reqlog"
	"janus/pkg/settings"
)

func (r *router) handle_config_settings(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		write_json(w, http.StatusOK, map[string]any{
			"path":     settings.Path(),
			"values":   settings.Snapshot(),
			"keys":     settings.KnownKeys,
			"listen":   janus_listen_addr(),
			"api_base": "http://" + janus_listen_addr() + "/v1",
		})
	case http.MethodPost:
		var body struct {
			Values map[string]string `json:"values"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			write_json(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if _, err := settings.MergePatch(body.Values); err != nil {
			write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		write_json(w, http.StatusOK, map[string]any{"ok": true, "values": settings.Snapshot()})
	default:
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (r *router) handle_request_log(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		write_json(w, http.StatusOK, map[string]any{
			"entries": reqlog.List(),
			"paused":  reqlog.Paused(),
		})
	case http.MethodDelete:
		reqlog.Clear()
		write_json(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodPost:
		var body struct {
			Paused *bool `json:"paused"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body.Paused != nil {
			reqlog.SetPaused(*body.Paused)
		}
		write_json(w, http.StatusOK, map[string]any{"ok": true, "paused": reqlog.Paused()})
	default:
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (r *router) handle_config_peers(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	write_json(w, http.StatusOK, map[string]any{"peers": registry.ListPeers("janus")})
}
