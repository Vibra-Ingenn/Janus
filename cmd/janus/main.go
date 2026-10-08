package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"janus/internal/engine"
	"janus/internal/singleton"

	"github.com/joho/godotenv"
)

func init() {
	if p := findEnvFile(); p != "" {
		if err := godotenv.Overload(p); err != nil {
			log.Printf("failed to load %s: %v", p, err)
		}
	} else {
		log.Printf(".env not found (searched up to filesystem root)")
	}
}

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

type server struct {
	provider     engine.Provider
	mu           sync.RWMutex
	defaultModel string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) handleHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if req.URL.Query().Get("deep") != "true" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	checks := map[string]any{}
	allOK := true
	if s.provider != nil {
		checks["inference"] = map[string]any{"status": "ok"}
	} else {
		checks["inference"] = map[string]any{"status": "unavailable"}
		allOK = false
	}
	s.mu.RLock()
	model := s.defaultModel
	s.mu.RUnlock()
	if model != "" {
		checks["model"] = map[string]any{"status": "ok", "path": engine.DisplayModelPath(model)}
	} else {
		checks["model"] = map[string]any{"status": "not_configured"}
	}
	status := http.StatusOK
	if !allOK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"ok": allOK, "checks": checks})
}

func (s *server) handleEngineStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, engine.GetEngineStatus(s.provider))
}

func (s *server) handleModelsLoad(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if s.provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no local engine"})
		return
	}
	var body struct {
		ModelPath string `json:"model_path"`
		CtxSize   uint32 `json:"ctx_size,omitempty"`
		GPULayers *int   `json:"gpu_layers,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	path := strings.TrimSpace(body.ModelPath)
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "model_path required"})
		return
	}

	gpuL := -1
	if body.GPULayers != nil {
		gpuL = *body.GPULayers
	}
	type loadParamConfigurer interface {
		SetLoadParams(ctxSize uint32, gpuLayers int)
	}
	if configurer, ok := s.provider.(loadParamConfigurer); ok {
		configurer.SetLoadParams(body.CtxSize, gpuL)
	}

	displayPath := engine.DisplayModelPath(path)
	if resolved, err := engine.ResolveModelPath(path); err == nil {
		displayPath = engine.DisplayModelPath(resolved)
	}

	log.Printf("janus: hot-swapping model to %q", displayPath)
	if err := s.provider.LoadModel(path); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.mu.Lock()
	s.defaultModel = displayPath
	s.mu.Unlock()
	_ = os.Setenv("JANUS_MODEL_PATH", displayPath)

	log.Printf("janus: model loaded — %q", displayPath)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"model_path": displayPath,
		"ctx_size":   body.CtxSize,
		"gpu_layers": gpuL,
	})
}

func (s *server) handleModelsList(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	now := time.Now().Unix()

	var models []string
	modelsDir := "models"
	if entries, err := os.ReadDir(modelsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
				models = append(models, filepath.Join(modelsDir, e.Name()))
			}
		}
	}

	s.mu.RLock()
	current := s.defaultModel
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"current": engine.DisplayModelPath(current),
		"models":  models,
		"time":    now,
	})
}

func (s *server) handleRoot(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" {
		http.NotFound(w, req)
		return
	}
	s.mu.RLock()
	model := engine.DisplayModelPath(s.defaultModel)
	s.mu.RUnlock()
	if model == "" {
		model = "(none loaded)"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Janus</title><meta name="viewport" content="width=device-width,initial-scale=1">
<link rel="icon" href="/favicon.ico">
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;background:#0f172a;color:#e2e8f0;display:flex;justify-content:center;padding:40px 16px}
.container{max-width:640px;width:100%}
h1{font-size:2rem;margin-bottom:8px;display:flex;align-items:center;gap:12px}
h1 img{width:88px;height:88px;margin:-20px -14px -20px -18px}
.sub{color:#94a3b8;margin-bottom:32px}
.card{background:#1e293b;border-radius:12px;padding:20px;margin-bottom:16px}
.card h2{font-size:1rem;color:#38bdf8;margin-bottom:8px}
code{background:#0f172a;padding:2px 6px;border-radius:4px;font-size:0.85rem}
pre{background:#0f172a;padding:12px;border-radius:8px;overflow-x:auto;font-size:0.85rem;margin-top:8px}
a{color:#38bdf8}
.status{display:inline-block;padding:4px 10px;border-radius:99px;font-size:0.8rem;background:#065f46;color:#6ee7b7}
</style></head><body>
<div class="container">
<h1><img src="/favicon.ico" alt="">Janus</h1>
<p class="sub">Local LLM server &amp; OpenAI-compatible API</p>
<div class="card">
<h2>Status</h2>
<p><span class="status">Running</span> &nbsp; Model: <code>` + model + `</code></p>
</div>
<div class="card">
<h2>API</h2>
<p>OpenAI-compatible endpoint:</p>
<pre>curl http://127.0.0.1:8990/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"local","messages":[{"role":"user","content":"Hello!"}]}'</pre>
<p style="margin-top:12px">Base URL: <code>http://127.0.0.1:8990/v1</code></p>
</div>
<div class="card">
<h2>Endpoints</h2>
<p><code>GET /health</code> — liveness check</p>
<p><code>GET /v1/models</code> — model list</p>
<p><code>POST /v1/chat/completions</code> — chat (streaming supported)</p>
<p><code>POST /models/load</code> — hot-swap model</p>
<p><code>GET /engine/status</code> — VRAM and backend info</p>
<p><code>GET /models/list</code> — available .gguf files</p>
</div>
<div class="card">
<h2>Connect</h2>
<p>Use with Cursor, Cline, or any OpenAI client:</p>
<pre>Base URL: http://127.0.0.1:8990/v1
API Key:  (leave blank)</pre>
</div>
</div></body></html>`))
}

//go:embed favicon.ico
var faviconICO []byte

func handleFavicon(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(faviconICO)
}

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

func openBrowser(url string) {
	time.Sleep(600 * time.Millisecond)
	if err := exec.Command("cmd", "/c", "start", url).Start(); err != nil {
		log.Printf("janus: could not open browser: %v", err)
	}
}

func listenAddr() string {
	addr := strings.TrimSpace(os.Getenv("JANUS_LISTEN_ADDR"))
	if addr == "" {
		return "127.0.0.1:8990"
	}
	if !strings.Contains(addr, ":") {
		return "127.0.0.1:" + addr
	}
	return addr
}

func main() {
	setupLogFile()

	addr := listenAddr()
	if err := singleton.Ensure(addr); err != nil {
		log.Fatalf("janus: refusing to start — %v", err)
	}

	reqBackend := strings.ToLower(strings.TrimSpace(os.Getenv("INFERENCE_BACKEND")))
	wantsLocal := reqBackend == "" || reqBackend == "vulkan" || reqBackend == "cpu"

	modelPath := strings.TrimSpace(os.Getenv("JANUS_MODEL_PATH"))
	if gpuStr := strings.TrimSpace(os.Getenv("JANUS_GPU_LAYERS")); gpuStr != "" {
		if _, err := strconv.Atoi(gpuStr); err != nil {
			log.Printf("janus: invalid JANUS_GPU_LAYERS=%q, using default", gpuStr)
		}
	}

	backend, err := engine.InitFromEnv()
	if err != nil {
		if wantsLocal {
			log.Fatalf("janus: local engine (%s) failed to start: %v\n"+
				"    Fix the model path / VRAM / GPU layers, or set INFERENCE_BACKEND=ollama to use Ollama.", reqBackend, err)
		}
		log.Printf("janus: local engine unavailable (%v)", err)
	} else if backend != nil {
		log.Printf("janus: local engine ready [%s]", backend.Backend())
		defer backend.Unload()
	} else if wantsLocal {
		log.Fatalf("janus: INFERENCE_BACKEND=%s requested but no local engine was created — check JANUS_MODEL_PATH", reqBackend)
	}

	defaultModel := modelPath
	s := &server{
		provider:     backend,
		defaultModel: defaultModel,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/engine/status", s.handleEngineStatus)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/v1/models", s.handleV1Models)
	mux.HandleFunc("/v1/chat/completions", s.handleV1ChatCompletions)
	mux.HandleFunc("/models/list", s.handleModelsList)
	mux.HandleFunc("/models/load", s.handleModelsLoad)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Printf("janus v%s listening on %s", AppVersion, addr)
		go checkForUpdates()
		if strings.TrimSpace(os.Getenv("JANUS_NO_BROWSER")) == "" {
			go openBrowser("http://" + addr)
		}
		errc <- srv.ListenAndServe()
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigc:
		log.Printf("janus: received %s, draining…", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		log.Printf("janus: shutdown complete")
		os.Exit(0)
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("janus server error: %v", err)
		}
		os.Exit(0)
	}
}
