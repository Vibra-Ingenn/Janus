package engine

import (
	"context"
	"fmt"

	"janus/internal/bridge"
)

// CPUBackend is a Provider that runs .gguf models entirely on the CPU.
// It uses the same llama.cpp shared library as VulkanBackend but forces
// nGPULayers=0 so no Vulkan device is required.
//
// Use this when:
//   - No compatible GPU is present.
//   - VulkanBackend.LoadModel returns ErrGPUOOM.
//   - INFERENCE_BACKEND=cpu is set in the environment.
type CPUBackend struct {
	inner *VulkanBackend
}

// NewCPUBackend creates a CPUBackend by loading the llama.cpp shared library
// at libPath with GPU layer count forced to 0.
func NewCPUBackend(libPath string) (*CPUBackend, error) {
	inner, err := NewVulkanBackend(libPath, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("cpu_backend: %w", err)
	}
	return &CPUBackend{inner: inner}, nil
}

// LoadModel loads the .gguf model at path into CPU RAM (no GPU offload).
func (c *CPUBackend) LoadModel(path string) error {
	return c.inner.LoadModel(path)
}

// Tokenize converts text into token IDs.
func (c *CPUBackend) Tokenize(text string) ([]int32, error) {
	return c.inner.Tokenize(text)
}

// Generate streams decoded token strings. See VulkanBackend.Generate for
// the full contract.
func (c *CPUBackend) Generate(ctx context.Context, tokens []int32) (<-chan string, error) {
	return c.inner.Generate(ctx, tokens)
}

// Predict is a convenience wrapper: Tokenize → Generate → collect stream.
func (c *CPUBackend) Predict(ctx context.Context, prompt string) (string, error) {
	return c.inner.Predict(ctx, prompt)
}

// PredictConstrained generates grammar-constrained output (delegates to VulkanBackend).
func (c *CPUBackend) PredictConstrained(ctx context.Context, prompt, grammarStr, grammarRoot string) (string, error) {
	return c.inner.PredictConstrained(ctx, prompt, grammarStr, grammarRoot)
}

// Unload releases the model from CPU RAM immediately.
func (c *CPUBackend) Unload() {
	c.inner.Unload()
}

// Backend returns "cpu" for logging and routing.
func (c *CPUBackend) Backend() string {
	return "cpu"
}

// Lib exposes the underlying LlamaLib for callers that need direct DLL access.
func (c *CPUBackend) Lib() *bridge.LlamaLib {
	return c.inner.lib
}

// ensure CPUBackend satisfies Provider at compile time.
var _ Provider = (*CPUBackend)(nil)
