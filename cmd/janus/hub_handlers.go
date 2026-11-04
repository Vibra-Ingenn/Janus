package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"janus/pkg/hfhub"
	"janus/pkg/singleton"
)

// Discover screen: search Hugging Face, list a repo's model files, download them into models\.
var hubDownloads = hfhub.NewManager()

// hub_blocked is true only when the user switched on "Use only this PC" (JANUS_LOCAL_ONLY=true/1).
func hub_blocked(w http.ResponseWriter) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_LOCAL_ONLY")))
	if v == "true" || v == "1" {
		write_json(w, http.StatusForbidden, map[string]any{
			"error": `"Use only this PC" is on in Settings, so Janus won't contact Hugging Face.`,
		})
		return true
	}
	return false
}

func hub_models_dir() string {
	if root := singleton.InstallRoot(); root != "" {
		return filepath.Join(root, "models")
	}
	return "models"
}

// GET /models/search?q=&sort=downloads|newest|likes
func (r *router) handle_models_search(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if hub_blocked(w) {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	repos, err := hfhub.Search(ctx, req.URL.Query().Get("q"), req.URL.Query().Get("sort"))
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	write_json(w, http.StatusOK, map[string]any{"repos": repos})
}

// GET /models/files?repo=owner/name
func (r *router) handle_models_files(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if hub_blocked(w) {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	vram := hfhub.VRAMBudgetBytes()
	files, err := hfhub.Files(ctx, req.URL.Query().Get("repo"), vram)
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	write_json(w, http.StatusOK, map[string]any{"files": files, "vram_bytes": vram})
}

// POST /models/download {"repo": "...", "name": "..."} starts or resumes a download.
func (r *router) handle_models_download(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if hub_blocked(w) {
		return
	}
	var body struct {
		Repo string `json:"repo"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Repo == "" || body.Name == "" {
		write_json(w, http.StatusBadRequest, map[string]any{"error": "send repo and name"})
		return
	}
	// Look the file up again rather than trusting part names from the page.
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	files, err := hfhub.Files(ctx, body.Repo, hfhub.VRAMBudgetBytes())
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	for _, f := range files {
		if f.Name == body.Name {
			job, err := hubDownloads.Start(body.Repo, f, hub_models_dir())
			if err != nil {
				write_json(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			write_json(w, http.StatusOK, map[string]any{"id": job.ID})
			return
		}
	}
	write_json(w, http.StatusNotFound, map[string]any{"error": "that file isn't in the repository"})
}

// GET /models/downloads lists downloads; POST /models/downloads {"id": "...", "action": "pause"|"cancel"}.
func (r *router) handle_models_downloads(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		write_json(w, http.StatusOK, map[string]any{"downloads": hubDownloads.Jobs(), "folder": hub_models_dir()})
	case http.MethodPost:
		var body struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			write_json(w, http.StatusBadRequest, map[string]any{"error": "send id and action"})
			return
		}
		ok := false
		switch body.Action {
		case "pause":
			ok = hubDownloads.Pause(body.ID)
		case "cancel":
			ok = hubDownloads.Cancel(body.ID)
		}
		write_json(w, http.StatusOK, map[string]any{"ok": ok})
	default:
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}
