package main

import (
	"log"
	"net/http"
	"os"

	"janus/addons"
	"janus/pkg/registry"
)

func (r *router) handle_tool_json(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	path := "tool.json"
	if _, err := os.Stat(path); err != nil {
		write_json(w, http.StatusNotFound, map[string]any{"error": "tool.json missing"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	http.ServeFile(w, req, path)
}

func (r *router) handle_addons_sidebar(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	write_json(w, http.StatusOK, map[string]any{"items": addons.SidebarItems()})
}

func load_tool_card_bytes() []byte {
	b, err := os.ReadFile("tool.json")
	if err != nil {
		return nil
	}
	return b
}

func register_tool_registry(listenAddr string) {
	card := load_tool_card_bytes()
	if err := registry.Register("janus", "http://"+listenAddr+"/v1", card); err != nil {
		log.Printf("janus: registry: %v", err)
	}
}
