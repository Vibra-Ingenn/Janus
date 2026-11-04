package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"janus/pkg/auth"
	"janus/pkg/config"
	"janus/pkg/discover"
	"janus/pkg/engine"
	"janus/pkg/ratelimit"
	"janus/pkg/registry"
	"janus/pkg/reqlog"
	"janus/pkg/settings"
	"janus/pkg/singleton"

	"janus/addons"

	"github.com/joho/godotenv"
)

func init() {
	if p := findEnvFile(); p != "" {
		// Load fills gaps only — process env (e.g. JANUS_LISTEN_ADDR) wins over .env.
		if err := godotenv.Load(p); err != nil {
			log.Printf("failed to load %s: %v", p, err)
		}
	} else {
		log.Printf(".env not found (searched up to filesystem root)")
	}
}

// findEnvFile walks from CWD upward looking for ".env".
// Returns the path if found, or "" if not.
func findEnvFile() string {
	dir, err := os.Getwd()
	if err == nil {
		for {
			candidate := filepath.Join(dir, ".env")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// Fallback: Check the directory containing the executable and its parent
	// (e.g., if running from dist/janus.exe, it checks dist/ and the project root)
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidate := filepath.Join(exeDir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parentDir := filepath.Dir(exeDir)
		candidate = filepath.Join(parentDir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

type router struct {
	provider engine.Provider
	prompts  *config.PromptsStore

	mu           sync.RWMutex
	loadMu       sync.Mutex
	defaultModel string
}

func new_router(provider engine.Provider, defaultModel string, prompts *config.PromptsStore) *router {
	return &router{
		provider:     provider,
		prompts:      prompts,
		defaultModel: defaultModel,
	}
}

func write_json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handle_config_prompts handles GET/POST for /config/prompts.
// GET  → returns current role prompts as JSON.
// POST → accepts full or partial RolePrompts JSON, merges, saves to disk.
func (r *router) handle_config_prompts(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		write_json(w, http.StatusOK, r.prompts.Get())

	case http.MethodPost:
		var incoming config.RolePrompts
		if err := json.NewDecoder(req.Body).Decode(&incoming); err != nil {
			write_json(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		// Merge: only overwrite fields that were explicitly provided (non-empty).
		current := r.prompts.Get()
		if incoming.DefaultChat != "" {
			current.DefaultChat = incoming.DefaultChat
		}
		if incoming.Planner != "" {
			current.Planner = incoming.Planner
		}
		if incoming.Worker != "" {
			current.Worker = incoming.Worker
		}
		if incoming.Judge != "" {
			current.Judge = incoming.Judge
		}
		if incoming.Pruner != "" {
			current.Pruner = incoming.Pruner
		}
		if err := r.prompts.Set(current); err != nil {
			write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		write_json(w, http.StatusOK, map[string]any{"ok": true, "prompts": current})

	default:
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

// handle_models_load hot-swaps the resident model without restarting.
// POST /models/load  {"model_path": "./models/foo.gguf"}
func (r *router) handle_models_load(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if r.provider == nil {
		write_json(w, http.StatusServiceUnavailable, map[string]any{"error": "no local engine — restart with INFERENCE_BACKEND=vulkan"})
		return
	}
	var body struct {
		ModelPath string `json:"model_path"`
		CtxSize   uint32 `json:"ctx_size,omitempty"`
		GPULayers *int   `json:"gpu_layers,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		write_json(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	path := strings.TrimSpace(body.ModelPath)
	if path == "" {
		write_json(w, http.StatusBadRequest, map[string]any{"error": "model_path required"})
		return
	}

	gpuL := -1
	if body.GPULayers != nil {
		gpuL = *body.GPULayers
	}
	opts := modelSwapOptions{
		CtxSize:   body.CtxSize,
		GpuLayers: &gpuL,
	}
	displayPath, err := r.hotSwapLocalModel(path, opts)
	if err != nil {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_ = os.Setenv("JANUS_MODEL_PATH", displayPath)

	settings := config.ModelSettings{
		ModelPath: displayPath,
		CtxSize:   body.CtxSize,
		GpuLayers: gpuL,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if saveErr := config.SaveModelSettings(settings); saveErr != nil {
		log.Printf("janus: could not persist model settings: %v", saveErr)
	}

	write_json(w, http.StatusOK, map[string]any{
		"ok":         true,
		"model_path": displayPath,
		"ctx_size":   body.CtxSize,
		"gpu_layers": gpuL,
	})
}

// handle_models_unload frees GPU/CPU memory for the resident local model.
// POST /models/unload
func (r *router) handle_models_unload(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if r.provider == nil {
		write_json(w, http.StatusServiceUnavailable, map[string]any{"error": "no local engine — restart with INFERENCE_BACKEND=vulkan"})
		return
	}
	r.loadMu.Lock()
	r.provider.Unload()
	r.loadMu.Unlock()
	write_json(w, http.StatusOK, map[string]any{"ok": true})
}

func (r *router) handle_models_default(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.mu.RLock()
		m := r.defaultModel
		r.mu.RUnlock()
		write_json(w, http.StatusOK, map[string]any{"model_path": m})

	case http.MethodPost:
		var body struct {
			ModelPath string `json:"model_path"`
			CtxSize   uint32 `json:"ctx_size,omitempty"`
			GpuLayers *int   `json:"gpu_layers,omitempty"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			write_json(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		displayPath := engine.DisplayModelPath(strings.TrimSpace(body.ModelPath))
		r.mu.Lock()
		r.defaultModel = displayPath
		r.mu.Unlock()
		_ = os.Setenv("JANUS_MODEL_PATH", displayPath)

		gpuL := -1
		if body.GpuLayers != nil {
			gpuL = *body.GpuLayers
		}
		_ = config.SaveModelSettings(config.ModelSettings{
			ModelPath: displayPath,
			CtxSize:   body.CtxSize,
			GpuLayers: gpuL,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		write_json(w, http.StatusOK, map[string]any{"ok": true, "model_path": displayPath})

	default:
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (r *router) handle_webui(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/console.js" {
		http.ServeFile(w, req, singleton.ConsoleScriptPath())
		return
	}
	if strings.HasPrefix(req.URL.Path, "/static/") {
		rel := strings.TrimPrefix(req.URL.Path, "/static/")
		rel = filepath.Clean(strings.ReplaceAll(rel, "\\", "/"))
		if rel == "." || strings.HasPrefix(rel, "..") {
			http.NotFound(w, req)
			return
		}
		root := singleton.InstallRoot()
		full := filepath.Join(root, "static", rel)
		if !singleton.PathUnderRoot(full, filepath.Join(root, "static")) {
			http.NotFound(w, req)
			return
		}
		if _, err := os.Stat(full); err != nil {
			http.NotFound(w, req)
			return
		}
		http.ServeFile(w, req, full)
		return
	}
	if req.URL.Path != "/" && req.URL.Path != "/webui.html" {
		http.NotFound(w, req)
		return
	}
	http.ServeFile(w, req, singleton.WebUIPath())
}

func (r *router) handle_health(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	// Quick check: ?deep=true returns component-level status
	if req.URL.Query().Get("deep") != "true" {
		write_json(w, http.StatusOK, map[string]any{
			"ok":      true,
			"id":      "janus",
			"version": AppVersion,
		})
		return
	}

	checks := map[string]any{}
	allOK := true

	// Inference engine
	if r.provider != nil {
		checks["inference"] = map[string]any{"status": "ok"}
	} else {
		checks["inference"] = map[string]any{"status": "unavailable"}
		allOK = false
	}

	// Memory DB removed from free Janus (archived).

	// Disk: check logs dir is writable
	if fi, err := os.Stat("logs"); err != nil || !fi.IsDir() {
		checks["disk"] = map[string]any{"status": "warning", "detail": "logs directory missing"}
	} else {
		checks["disk"] = map[string]any{"status": "ok"}
	}

	// Model loaded
	_, _, authed := auth.CurrentUser(req)
	if r.defaultModel != "" {
		if _, err := os.Stat(r.defaultModel); err != nil {
			if authed {
				checks["model_file"] = map[string]any{"status": "missing", "path": r.defaultModel}
			} else {
				checks["model_file"] = map[string]any{"status": "missing"}
			}
			allOK = false
		} else {
			if authed {
				checks["model_file"] = map[string]any{"status": "ok", "path": r.defaultModel}
			} else {
				checks["model_file"] = map[string]any{"status": "ok"}
			}
		}
	} else {
		checks["model_file"] = map[string]any{"status": "not_configured"}
	}

	status := http.StatusOK
	if !allOK {
		status = http.StatusServiceUnavailable
	}
	write_json(w, status, map[string]any{"ok": allOK, "checks": checks})
}

func (r *router) handle_engine_status(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	write_json(w, http.StatusOK, engine.GetEngineStatus(r.provider))
}

// setupLogFile tees all log output to both stdout and logs/janus.log.
// The log file is appended to on each run so history is preserved.
func setupLogFile() {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Printf("janus: cannot create logs/ dir: %v", err)
		return
	}
	f, err := os.OpenFile(filepath.Join("logs", "janus.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("janus: cannot open logs/janus.log: %v", err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.Printf("janus: logging to logs/janus.log")
}

// openBrowser opens url after the server is ready (see main).
func openBrowserAfterBind(listenAddr string) {
	if !shouldOpenBrowser() {
		return
	}
	url := consoleURL(listenAddr)
	if !singleton.WaitUntilJanusHealth(singleton.BrowserBaseURL(listenAddr), 15*time.Second) {
		log.Printf("janus: server did not become ready in time; still opening browser at %s", url)
	}
	openBrowserAt(url)
}

func main() {
	setupLogFile()
	if sf, err := settings.Load(); err == nil {
		sf.Apply()
	} else {
		log.Printf("janus: settings: %v", err)
	}
	if created := discover.EnsureBackendConfigFile(); created != "" {
		log.Printf("janus: created %s from backend_config.example.json — add backends / routing there", created)
	}
	init_backend_config()
	auth.Init("data")

	// Port conflict: open console to existing Janus or explain blocker (no bind error).
	listenAddr := janus_listen_addr()
	if !handlePortBeforeStart(listenAddr) {
		os.Exit(0)
	}

	// Requested inference backend. Empty defaults to the local Vulkan engine.
	persisted := config.LoadModelSettings()
	if persisted.ModelPath != "" {
		_ = os.Setenv("JANUS_MODEL_PATH", persisted.ModelPath)
		log.Printf("janus: persisted model override: %q", persisted.ModelPath)
	}
	if persisted.CtxSize > 0 {
		_ = os.Setenv("JANUS_CTX_SIZE", strconv.FormatUint(uint64(persisted.CtxSize), 10))
	}
	if persisted.ModelPath != "" {
		_ = os.Setenv("JANUS_GPU_LAYERS", strconv.Itoa(persisted.GpuLayers))
	}

	backend, err := startInferenceEngine()
	reqBackend := normalizedInferenceBackend()
	logInferenceStartup(reqBackend, backend, err)
	if backend != nil {
		defer backend.Unload()
	}

	prompts, _ := config.OpenPrompts("")
	defaultModel := strings.TrimSpace(os.Getenv("JANUS_MODEL_PATH"))
	if persisted.ModelPath != "" {
		defaultModel = persisted.ModelPath
	}

	r := new_router(backend, defaultModel, prompts)

	chatRL := ratelimit.New(10, 20) // 10 req/s, burst 20

	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handle_webui)
	mux.HandleFunc("/health", r.handle_health)
	mux.HandleFunc("/tool.json", r.handle_tool_json)
	mux.HandleFunc("/config/addons/sidebar", auth.Middleware(auth.RoleReadonly, r.handle_addons_sidebar))
	addons.MountAll(mux)
	mux.HandleFunc("/engine/status", auth.Middleware(auth.RoleReadonly, r.handle_engine_status))
	mux.HandleFunc("/version", r.handle_version)
	mux.HandleFunc("/instance", singleton.HandleInstance)
	mux.HandleFunc("/instance/shutdown", singleton.HandleShutdown)
	mux.HandleFunc("/v1/models", auth.Middleware(auth.RoleReadonly, r.handle_v1_models))
	mux.HandleFunc("/v1/chat/completions", chatRL.Middleware(r.handle_v1_chat_completions))

	mux.HandleFunc("/models/list", auth.Middleware(auth.RoleReadonly, r.handle_models_list))
	mux.HandleFunc("/models/default", auth.Middleware(auth.RoleAdmin, r.handle_models_default))
	mux.HandleFunc("/models/load", auth.Middleware(auth.RoleAdmin, r.handle_models_load))
	mux.HandleFunc("/models/unload", auth.Middleware(auth.RoleAdmin, r.handle_models_unload))
	mux.HandleFunc("/models/search", auth.Middleware(auth.RoleReadonly, r.handle_models_search))
	mux.HandleFunc("/models/files", auth.Middleware(auth.RoleReadonly, r.handle_models_files))
	mux.HandleFunc("/models/download", auth.Middleware(auth.RoleAdmin, r.handle_models_download))
	mux.HandleFunc("/models/downloads", auth.Middleware(auth.RoleAdmin, r.handle_models_downloads))
	mux.HandleFunc("/config/prompts", auth.Middleware(auth.RoleAdmin, r.handle_config_prompts))
	mux.HandleFunc("/config/mode", auth.Middleware(auth.RoleAdmin, r.handle_config_mode))
	mux.HandleFunc("/config/features", auth.Middleware(auth.RoleReadonly, r.handle_config_features))
	mux.HandleFunc("/config/connectors", r.handle_config_connectors)
	mux.HandleFunc("/config/settings", auth.Middleware(auth.RoleAdmin, r.handle_config_settings))
	mux.HandleFunc("/config/backends/reload", auth.Middleware(auth.RoleAdmin, r.handle_config_backends_reload))
	mux.HandleFunc("/config/peers", auth.Middleware(auth.RoleReadonly, r.handle_config_peers))
	mux.HandleFunc("/config/request-log", auth.Middleware(auth.RoleReadonly, r.handle_request_log))
	mux.HandleFunc("/config/model-folders", auth.Middleware(auth.RoleAdmin, r.handle_config_model_folders))
	mux.HandleFunc("/models/scan-pc", auth.Middleware(auth.RoleAdmin, r.handle_models_scan_pc))

	mux.HandleFunc("/auth/me", auth.HandleMe)

	addr := listenAddr
	srv := &http.Server{
		Addr:              addr,
		Handler:           reqlog.Middleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("janus: listen %s: %v", addr, err)
	}
	go func() {
		log.Printf("janus v%s listening on %s", AppVersion, addr)
		register_tool_registry(addr)
		go checkForUpdates()
		firstRunPCScan()
		go openBrowserAfterBind(addr)
		errc <- srv.Serve(ln)
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigc:
		log.Printf("janus: received %s, draining…", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		registry.Unregister("janus")
		log.Printf("janus: shutdown complete")
		os.Exit(0)
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("janus server error: %v", err)
		}
		os.Exit(0)
	}
}

func bind_lan_enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_BIND_LAN")))
	return v == "1" || v == "true" || v == "yes"
}

func janus_listen_addr() string {
	addr := strings.TrimSpace(os.Getenv("JANUS_LISTEN_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8990"
	}
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "127.0.0.1:8990"
	}
	if bind_lan_enabled() {
		host = "0.0.0.0"
	} else if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
