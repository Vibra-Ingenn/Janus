package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AgentConfig describes everything the planner decided for a single task step.
// The AI planner populates this — it picks the role, model, and backend.
type AgentConfig struct {
	ID        string // unique ID per task-step, e.g. "task-abc-step-1"
	Role      string // "worker" | "reviewer" | "pruner"
	ModelPath string // absolute or relative path to the .gguf file
	Backend   string // "vulkan" | "cpu"
	GPULayers int    // how many transformer layers to offload; -1 = all
}

// AgentHandle represents a live ephemeral agent.
// It holds one loaded model in GPU/CPU memory.
// Always call Destroy() when finished — never rely on the GC.
type AgentHandle struct {
	Config   AgentConfig
	provider Provider

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// Spawn creates an ephemeral agent:
//  1. Constructs the correct Provider (Vulkan or CPU) from cfg.
//  2. Calls LoadModel — GPU VRAM is reserved here.
//  3. Launches a goroutine that runs task(ctx, provider).
//  4. When task returns OR ctx is cancelled → Unload() is called immediately.
//
// GPU memory is freed the moment task() exits, not when the GC runs.
// The caller must call handle.Destroy() to confirm cleanup and release the goroutine.
func Spawn(
	parentCtx context.Context,
	cfg AgentConfig,
	task func(context.Context, Provider) error,
) (*AgentHandle, error) {
	libPath := strings.TrimSpace(os.Getenv("JANUS_LIB_PATH"))
	if libPath == "" {
		libPath = defaultLibPath()
	}

	var p Provider
	var err error

	switch cfg.Backend {
	case "cpu":
		p, err = NewCPUBackend(libPath)
	default:
		p, err = NewVulkanBackend(libPath, cfg.GPULayers, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("janus/engine: spawn %s: create backend: %w", cfg.ID, err)
	}

	if err := p.LoadModel(cfg.ModelPath); err != nil {
		p.Unload()
		return nil, fmt.Errorf("janus/engine: spawn %s: load model: %w", cfg.ID, err)
	}

	ctx, cancel := context.WithCancel(parentCtx)

	h := &AgentHandle{
		Config:   cfg,
		provider: p,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	go func() {
		defer func() {
			p.Unload()
			close(h.done)
		}()

		if err := task(ctx, p); err != nil {
			log.Printf("janus/engine: agent %s [%s] error: %v", cfg.ID, cfg.Role, err)
		}
	}()

	return h, nil
}

// Destroy cancels the agent's context and blocks until GPU memory is confirmed
// freed. Always call this — do not rely on the GC to release VRAM.
func (h *AgentHandle) Destroy() {
	h.once.Do(func() {
		h.cancel()
		<-h.done
		h.cleanScratch()
	})
}

// ScratchDir returns a temporary directory scoped to this agent.
// The directory is created on first call and removed by Destroy.
func (h *AgentHandle) ScratchDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "janus-agent-"+h.Config.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// cleanScratch removes the agent's temp directory.
// Called internally by Destroy after the goroutine exits.
func (h *AgentHandle) cleanScratch() {
	dir := filepath.Join(os.TempDir(), "janus-agent-"+h.Config.ID)
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("janus/engine: cleanup scratch %s: %v", h.Config.ID, err)
	}
}
