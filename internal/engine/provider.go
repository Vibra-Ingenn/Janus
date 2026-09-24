package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// GlobalBudget is the package-level VRAM tracker shared by all backends.
// Initialised by InitFromEnv.
var GlobalBudget *VRAMBudget

func init() {
	GlobalBudget = NewVRAMBudget(VRAMCeilingBytesFromEnv())
}

// EngineStatus holds a snapshot of backend state for the /engine/status endpoint.
type EngineStatus struct {
	Backend     string `json:"backend"`
	ModelLoaded bool   `json:"model_loaded"`
	ModelPath   string `json:"model_path"`
	CtxSize     uint32 `json:"ctx_size,omitempty"`
	GpuLayers   int    `json:"gpu_layers,omitempty"`
	VRAMUsedMB  int64  `json:"vram_used_mb"`
	VRAMFreeMB  int64  `json:"vram_free_mb"`
	VRAMCeilMB  int64  `json:"vram_ceil_mb"`
}

type activeModelProvider interface {
	ActiveModelPath() string
	ActiveModelLoaded() bool
	ActiveCtxSize() uint32
	ActiveGPULayers() int
}

// Provider is the inference backend contract.
// VulkanBackend and CPUBackend both satisfy this interface.
// The Ollama path uses the existing ollama_client in cmd/janus and does
// not implement Provider — it stays on its own HTTP path.
type Provider interface {
	// LoadModel loads a .gguf model from disk into the backend.
	// On GPU OOM it automatically falls back to CPU (0 GPU layers).
	LoadModel(path string) error

	// Tokenize converts a text string into token IDs.
	// The returned slice is owned by Go; safe to pass to Generate.
	Tokenize(text string) ([]int32, error)

	// Generate streams decoded tokens into the returned channel.
	// The caller must drain the channel. Cancel ctx to stop early.
	// Returns an error immediately if the model is not loaded.
	Generate(ctx context.Context, tokens []int32) (<-chan string, error)

	// Predict is a convenience wrapper: Tokenize → Generate → collect stream.
	Predict(ctx context.Context, prompt string) (string, error)

	// Unload releases all GPU/CPU memory for this model IMMEDIATELY.
	// It does not wait for the GC. Must be called when the owner is done.
	Unload()

	// Backend returns a human-readable name for logging/routing.
	// Values: "vulkan", "cpu"
	Backend() string
}

// ErrLibNotFound is returned when the llama.cpp shared library cannot be located.
var ErrLibNotFound = errors.New("janus/engine: llama.cpp library not found")

// ErrModelNotLoaded is returned when Generate or Tokenize is called before LoadModel.
var ErrModelNotLoaded = errors.New("janus/engine: model not loaded — call LoadModel first")

// ErrGPUOOM is returned on a Vulkan/GPU out-of-memory condition.
var ErrGPUOOM = errors.New("janus/engine: GPU out of memory")

// GetEngineStatus returns a live snapshot of VRAM usage.
func GetEngineStatus(p Provider) EngineStatus {
	const mb = 1024 * 1024
	s := EngineStatus{
		VRAMCeilMB: GlobalBudget.ceiling / mb,
		VRAMUsedMB: GlobalBudget.Used() / mb,
		VRAMFreeMB: GlobalBudget.Available() / mb,
	}
	if p != nil {
		s.Backend = p.Backend()
		if amp, ok := p.(activeModelProvider); ok {
			s.ModelLoaded = amp.ActiveModelLoaded()
			if path := amp.ActiveModelPath(); path != "" {
				s.ModelPath = DisplayModelPath(path)
			}
			s.CtxSize = amp.ActiveCtxSize()
			s.GpuLayers = amp.ActiveGPULayers()
		}
		if s.ModelPath == "" {
			s.ModelPath = DisplayModelPath(strings.TrimSpace(os.Getenv("JANUS_MODEL_PATH")))
		}
		if !s.ModelLoaded {
			s.ModelLoaded = s.ModelPath != ""
		}
	}
	return s
}

func InitFromEnv() (Provider, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("INFERENCE_BACKEND")))
	libPath := strings.TrimSpace(os.Getenv("JANUS_LIB_PATH"))
	modelPath := strings.TrimSpace(os.Getenv("JANUS_MODEL_PATH"))
	gpuLayersStr := strings.TrimSpace(os.Getenv("JANUS_GPU_LAYERS"))
	maxTokensStr := strings.TrimSpace(os.Getenv("JANUS_MAX_TOKENS"))
	GlobalBudget = NewVRAMBudget(VRAMCeilingBytesFromEnv())

	if libPath == "" {
		libPath = defaultLibPath()
	}

	gpuLayers := -1 // default: let DLL decide (its default is -1 = all layers on GPU)
	if gpuLayersStr != "" {
		if n, err := strconv.Atoi(gpuLayersStr); err == nil {
			gpuLayers = n
		}
	}

	maxTokens := 0
	if maxTokensStr != "" {
		if n, err := strconv.Atoi(maxTokensStr); err == nil && n > 0 {
			maxTokens = n
		}
	}

	var p Provider
	var err error

	switch backend {
	case "cpu":
		p, err = NewCPUBackend(libPath)
	case "openrouter":
		p, err = NewOpenRouterBackend()
	case "ollama":
		// Ollama backend is handled at the HTTP layer, not here
		return nil, nil
	default:
		p, err = NewVulkanBackend(libPath, gpuLayers, maxTokens)
	}

	if err != nil {
		return nil, fmt.Errorf("janus/engine: init %s backend: %w", backend, err)
	}

	if modelPath != "" {
		if loadErr := p.LoadModel(modelPath); loadErr != nil {
			p.Unload()
			return nil, fmt.Errorf("janus/engine: load model %q: %w", modelPath, loadErr)
		}
	}

	return p, nil
}

// defaultLibPath returns the platform-specific default path to the
// llama.cpp shared library relative to the binary location.
func defaultLibPath() string {
	// Prefer the DLL next to the running executable (e.g. dist/llama.dll).
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		switch runtime.GOOS {
		case "windows":
			candidate := filepath.Join(dir, "llama.dll")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		case "darwin":
			candidate := filepath.Join(dir, "libllama.dylib")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		default:
			candidate := filepath.Join(dir, "libllama.so")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	switch runtime.GOOS {
	case "windows":
		return "llama.dll"
	case "darwin":
		return "libllama.dylib"
	default:
		return "libllama.so"
	}
}

