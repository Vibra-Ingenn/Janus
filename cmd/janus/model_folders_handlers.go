package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"janus/pkg/discover"
	"janus/pkg/settings"
)

func (r *router) handle_config_model_folders(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		write_json(w, http.StatusOK, map[string]any{
			"folders": discover.ListModelFolders(),
		})
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
			Path   string `json:"path"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			write_json(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		action := strings.ToLower(strings.TrimSpace(body.Action))
		path := strings.TrimSpace(body.Path)
		switch action {
		case "add":
			if path == "" {
				write_json(w, http.StatusBadRequest, map[string]any{"error": "path required"})
				return
			}
			clean := filepath.Clean(path)
			st, err := os.Stat(clean)
			if err != nil || !st.IsDir() {
				write_json(w, http.StatusBadRequest, map[string]any{"error": "folder not found or not a directory"})
				return
			}
			extras := discover.MergeExtraSearchPath(discover.ExtraModelSearchPaths(), clean)
			if _, err := settings.MergePatch(map[string]string{
				"JANUS_MODEL_SEARCH_PATHS": strings.Join(extras, ","),
			}); err != nil {
				write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		case "remove":
			if path == "" {
				write_json(w, http.StatusBadRequest, map[string]any{"error": "path required"})
				return
			}
			extras := discover.RemoveExtraSearchPath(discover.ExtraModelSearchPaths(), path)
			val := strings.Join(extras, ",")
			patch := map[string]string{"JANUS_MODEL_SEARCH_PATHS": val}
			if val == "" {
				patch["JANUS_MODEL_SEARCH_PATHS"] = ""
			}
			if _, err := settings.MergePatch(patch); err != nil {
				write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		default:
			write_json(w, http.StatusBadRequest, map[string]any{"error": "action must be add or remove"})
			return
		}
		write_json(w, http.StatusOK, map[string]any{
			"ok":      true,
			"folders": discover.ListModelFolders(),
		})
	default:
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

// handle_models_scan_pc runs the whole-PC search for model files.
// GET = progress; POST = start (folders it finds are added like "Add a folder").
func (r *router) handle_models_scan_pc(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		write_json(w, http.StatusOK, discover.PCScan())
	case http.MethodPost:
		started := startPCScan()
		write_json(w, http.StatusOK, map[string]any{"started": started, "scan": discover.PCScan()})
	default:
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

// startPCScan starts the background scan and saves the folders it finds when it's done.
func startPCScan() bool {
	return discover.StartPCScan(func(found []string) {
		extras := discover.ExtraModelSearchPaths()
		for _, f := range found {
			extras = discover.MergeExtraSearchPath(extras, f)
		}
		if _, err := settings.MergePatch(map[string]string{
			"JANUS_MODEL_SEARCH_PATHS": strings.Join(extras, ","),
			"JANUS_PC_SCANNED_AT":      time.Now().Format(time.RFC3339),
		}); err != nil {
			log.Printf("janus: saving scanned model folders: %v", err)
			return
		}
		log.Printf("janus: PC scan done, %d new model folders: %v", len(found), found)
	})
}

// firstRunPCScan scans the whole PC once, the first time Janus runs (JANUS_PC_SCAN=0 turns it off).
func firstRunPCScan() {
	if strings.TrimSpace(os.Getenv("JANUS_PC_SCAN")) == "0" || strings.TrimSpace(os.Getenv("JANUS_PC_SCANNED_AT")) != "" {
		return
	}
	log.Print("janus: first run, searching this PC for model files in the background")
	startPCScan()
}
