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

// PredictWithLimit is like Predict, but honors a per-request token cap.
func (c *CPUBackend) PredictWithLimit(ctx context.Context, prompt string, maxNewTokens int) (string, error) {
	return c.inner.PredictWithLimit(ctx, prompt, maxNewTokens)
}

// GenerateWith streams with a per-request token cap and sampler settings.
func (c *CPUBackend) GenerateWith(ctx context.Context, tokens []int32, maxNewTokens int, sp SamplerParams) (<-chan string, error) {
	return c.inner.GenerateWith(ctx, tokens, maxNewTokens, sp)
}

// GenerateWithInfo is GenerateWith that also reports usage and finish reason.
func (c *CPUBackend) GenerateWithInfo(ctx context.Context, tokens []int32, maxNewTokens int, sp SamplerParams, info *GenInfo) (<-chan string, error) {
	return c.inner.GenerateWithInfo(ctx, tokens, maxNewTokens, sp, info)
}

// LastGenErr returns the error (if any) from the most recent generation.
func (c *CPUBackend) LastGenErr() error {
	return c.inner.LastGenErr()
}

// IsThinkingModel reports whether the loaded model emits <think> blocks.
func (c *CPUBackend) IsThinkingModel() bool {
	return c.inner.IsThinkingModel()
}

// PredictWith is Predict with a per-request token cap and sampler settings.
func (c *CPUBackend) PredictWith(ctx context.Context, prompt string, maxNewTokens int, sp SamplerParams) (string, error) {
	return c.inner.PredictWith(ctx, prompt, maxNewTokens, sp)
}

// FormatChat renders a conversation with the model's embedded chat template.
func (c *CPUBackend) FormatChat(msgs []bridge.ChatMessage) (string, bool) {
	return c.inner.FormatChat(msgs)
}

// Unload releases the model from CPU RAM immediately.
func (c *CPUBackend) Unload() {
	c.inner.Unload()
}

// Backend returns "cpu" for logging and routing.

// SetSampler forwards temperature and top-p values to the inner VulkanBackend.
// SetLoadParams forwards load parameters to the inner VulkanBackend.
func (c *CPUBackend) SetLoadParams(ctxSize uint32, gpuLayers int) {
	c.inner.SetLoadParams(ctxSize, 0) // force 0 GPU layers for CPU backend
}

func (c *CPUBackend) SetSampler(temp float64, topP float64) {
	c.inner.SetSampler(temp, topP)
}

func (c *CPUBackend) Backend() string {
	return "cpu"
}

// Lib exposes the underlying LlamaLib for callers that need direct DLL access.
func (c *CPUBackend) Lib() *bridge.LlamaLib {
	return c.inner.lib
}

// ensure CPUBackend satisfies Provider at compile time.
var _ Provider = (*CPUBackend)(nil)

