package discover

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"janus/pkg/routerconfig"
	"janus/pkg/singleton"
)

// LocalModel is a .gguf file found on disk.
type LocalModel struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Size    int64    `json:"size_bytes"`
	Source  string   `json:"source,omitempty"`  // "ollama" for Ollama's store, else empty
	Kind    string   `json:"kind,omitempty"`    // "embedding" for search-only models
	Aliases []string `json:"aliases,omitempty"` // other names for the same file (Ollama tags, links)

	info os.FileInfo // for spotting hard links to the same file
}

// Snapshot is what Janus found on this machine (for logs and GET /config/features).
type Snapshot struct {
	AutoEnabled      bool         `json:"auto_enabled"`
	AppliedBackend   string       `json:"applied_backend,omitempty"`
	AppliedModelPath string       `json:"applied_model_path,omitempty"`
	LocalGGUF        []LocalModel `json:"local_gguf"`
	OllamaReachable  bool         `json:"ollama_reachable"`
	OllamaURL        string       `json:"ollama_url,omitempty"`
	OllamaModelCount int          `json:"ollama_model_count"`
	OpenRouterKey    bool         `json:"openrouter_key"`
	GeminiKey        bool         `json:"gemini_key"`
	DeferModelLoad   bool         `json:"defer_model_load"`
	SearchDirs       []string     `json:"search_dirs"`
}

var lastSnapshot Snapshot

// LastSnapshot returns the result of the most recent ApplyStartupDefaults call.
func LastSnapshot() Snapshot {
	return lastSnapshot
}

func autoEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_AUTO")))
	if v == "0" || v == "false" || v == "no" {
		return false
	}
	return true
}

func deferLoadEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_DEFER_MODEL_LOAD")))
	return v == "1" || v == "true" || v == "yes"
}

func modelSearchDirs() []string {
	root := singleton.InstallRoot()
	var dirs []string
	if root != "" {
		dirs = append(dirs, filepath.Join(root, "models"), filepath.Join(root, "dist", "models"))
	}
	dirs = append(dirs, "models", "dist/models")
	if strings.TrimSpace(os.Getenv("JANUS_SCAN_USER_PATHS")) != "0" {
		dirs = append(dirs, defaultUserModelSearchDirs()...)
	}
	for _, p := range parseExtraSearchPaths() {
		dirs = append(dirs, p)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		clean := filepath.Clean(d)
		key := strings.ToLower(clean)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, clean)
	}
	return out
}

// defaultUserModelSearchDirs adds common third-party model locations (non-recursive roots only).
func defaultUserModelSearchDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "Downloads"),
		filepath.Join(home, ".ollama", "models"),
		filepath.Join(home, ".cache"), // Hugging Face hub and other apps' model caches
		filepath.Join(home, ".lmstudio", "models"),
		filepath.Join(home, "AppData", "Local", "lm-studio", "models"),
		filepath.Join(home, ".unsloth", "studio", "exports"),
		filepath.Join(home, "AppData", "Local", "nomic.ai", "gpt4all"),
		filepath.Join(home, ".jan", "models"),
	}
	var out []string
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

const ggufWalkMaxDepth = 6

// addGGUFFile keeps the first copy of each real file (a link and its target are one model).
func addGGUFFile(byKey map[string]LocalModel, path string, size int64) {
	addModel(byKey, newLocalModel(path, filepath.Base(path), size, ""))
}

func scanDirForGGUF(dir string, byPath map[string]LocalModel) {
	dir = filepath.Clean(dir)
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) > ggufWalkMaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".gguf") {
			return nil
		}
		// Follows links (d.Info() would report a link's 0 bytes); skips broken links and tokenizer files.
		if size, ok := ggufFileSize(path); ok {
			addGGUFFile(byPath, path, size)
		}
		return nil
	})
}

// PickLocalGGUF chooses the largest model that fits the configured VRAM/RAM budget.
func PickLocalGGUF(models []LocalModel) (LocalModel, bool) {
	if len(models) == 0 {
		return LocalModel{}, false
	}
	ceiling := vramCeilingBytesFromEnv()
	const defaultCtx = uint32(8192)
	for _, m := range models {
		if m.Kind == "embedding" {
			continue
		}
		need := estimateModelVRAMBytes(m.Size, defaultCtx)
		if need <= ceiling {
			return m, true
		}
	}
	return models[len(models)-1], true
}

func vramCeilingBytesFromEnv() int64 {
	const defaultMiB = 12083
	raw := strings.TrimSpace(os.Getenv("JANUS_VRAM_CEILING_MB"))
	if raw == "" {
		return int64(defaultMiB) * 1024 * 1024
	}
	mb, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mb <= 0 {
		return int64(defaultMiB) * 1024 * 1024
	}
	return mb * 1024 * 1024
}

func estimateModelVRAMBytes(fileSize int64, ctxSize uint32) int64 {
	if ctxSize == 0 {
		ctxSize = 8192
	}
	weights := int64(float64(fileSize) * 0.55)
	kvCache := int64(ctxSize) * 384 * 1024
	return weights + kvCache
}

// ScanLocalGGUF lists .gguf files under the standard search dirs (deduped by path).
func ScanLocalGGUF() []LocalModel {
	dirs := modelSearchDirs()
	byPath := map[string]LocalModel{}
	// Ollama first, so its models keep Ollama's names (qwen3:8b) when links to the same file exist.
	if strings.TrimSpace(os.Getenv("JANUS_SCAN_USER_PATHS")) != "0" {
		scanOllamaStore(ollamaModelsDir(), byPath)
	}
	for _, dir := range dirs {
		scanDirForGGUF(dir, byPath)
	}
	out := make([]LocalModel, 0, len(byPath))
	for _, m := range byPath {
		out = append(out, m)
	}
	nameCollisions(out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func ollamaBaseURL() string {
	u := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL"))
	if u == "" {
		return "http://127.0.0.1:11434"
	}
	return strings.TrimRight(u, "/")
}

func probeOllama(ctx context.Context) (bool, int) {
	base := ollamaBaseURL()
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tags", nil)
	if err != nil {
		return false, 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, 0
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return true, 0
	}
	return true, len(payload.Models)
}

func envUnset(key string) bool {
	_, ok := os.LookupEnv(key)
	return !ok
}

func envBlank(key string) bool {
	return strings.TrimSpace(os.Getenv(key)) == ""
}

// EnsureBackendConfigFile copies backend_config.example.json → backend_config.json when missing.
// Returns the path written, or "" if unchanged.
func EnsureBackendConfigFile() string {
	if p := routerconfig.FindConfigFile(); p != "" {
		return ""
	}
	root := singleton.InstallRoot()
	if root == "" {
		return ""
	}
	example := filepath.Join(root, "backend_config.example.json")
	target := filepath.Join(root, "backend_config.json")
	if _, err := os.Stat(example); err != nil {
		return ""
	}
	if _, err := os.Stat(target); err == nil {
		return ""
	}
	in, err := os.Open(example)
	if err != nil {
		return ""
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return ""
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return ""
	}
	return target
}

func startupModelFromBackendConfig() string {
	path := routerconfig.FindConfigFile()
	if path == "" {
		return ""
	}
	cfg, err := routerconfig.Load(path)
	if err != nil || cfg == nil {
		return ""
	}
	tryPath := func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" {
			return ""
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
		root := singleton.InstallRoot()
		if root != "" {
			joined := filepath.Join(root, p)
			if _, err := os.Stat(joined); err == nil {
				return joined
			}
		}
		return ""
	}
	if entry := cfg.BackendByID("local"); entry != nil {
		if p := tryPath(entry.ModelPath); p != "" {
			return p
		}
	}
	for _, entry := range cfg.Backends {
		typ := strings.ToLower(strings.TrimSpace(entry.Type))
		if typ != "" && typ != "vulkan" && typ != "cpu" {
			continue
		}
		if p := tryPath(entry.ModelPath); p != "" {
			return p
		}
	}
	return ""
}

// ApplyStartupDefaults fills unset .env-style variables from what is on this PC.
// It never overrides explicit INFERENCE_BACKEND, JANUS_MODEL_PATH, or JANUS_DEFER_MODEL_LOAD.
func ApplyStartupDefaults() Snapshot {
	snap := Snapshot{
		AutoEnabled:    autoEnabled(),
		SearchDirs:     modelSearchDirs(),
		OpenRouterKey:  strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "",
		GeminiKey:      strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "",
		DeferModelLoad: deferLoadEnv(),
		LocalGGUF:      ScanLocalGGUF(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, n := probeOllama(ctx)
	snap.OllamaReachable = ok
	snap.OllamaURL = ollamaBaseURL()
	snap.OllamaModelCount = n

	if !snap.AutoEnabled {
		lastSnapshot = snap
		return snap
	}

	explicitBackend := !envUnset("INFERENCE_BACKEND") && strings.TrimSpace(os.Getenv("INFERENCE_BACKEND")) != ""

	if envBlank("JANUS_MODEL_PATH") {
		if fromCfg := startupModelFromBackendConfig(); fromCfg != "" {
			_ = os.Setenv("JANUS_MODEL_PATH", fromCfg)
			snap.AppliedModelPath = fromCfg
		}
	}

	if envBlank("JANUS_MODEL_PATH") && len(snap.LocalGGUF) > 0 {
		if chosen, ok := PickLocalGGUF(snap.LocalGGUF); ok {
			_ = os.Setenv("JANUS_MODEL_PATH", chosen.Path)
			snap.AppliedModelPath = chosen.Path
		}
	}

	backend := strings.ToLower(strings.TrimSpace(os.Getenv("INFERENCE_BACKEND")))
	if backend == "" {
		switch {
		case len(snap.LocalGGUF) > 0 || snap.AppliedModelPath != "":
			backend = "vulkan"
		case snap.OllamaReachable:
			backend = "ollama"
		case snap.OpenRouterKey:
			backend = "openrouter"
		default:
			backend = "vulkan"
		}
		if !explicitBackend {
			_ = os.Setenv("INFERENCE_BACKEND", backend)
			snap.AppliedBackend = backend
		}
	}

	if envBlank("JANUS_MODEL_PATH") && !deferLoadEnv() {
		hasLocal := len(snap.LocalGGUF) > 0
		if !hasLocal && !snap.OllamaReachable && !snap.OpenRouterKey {
			if envUnset("JANUS_DEFER_MODEL_LOAD") {
				_ = os.Setenv("JANUS_DEFER_MODEL_LOAD", "1")
				snap.DeferModelLoad = true
			}
		}
	}

	snap.DeferModelLoad = deferLoadEnv()
	lastSnapshot = snap
	return snap
}
