package engine

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// DefaultVRAMCeiling is 11.8 GiB (12083 MiB) — model weights + KV context budget before OOM risk.
// Override with JANUS_VRAM_CEILING_MB.
const DefaultVRAMCeiling int64 = 12083 * 1024 * 1024

// VRAMCeilingEnvVar overrides Janus's internal VRAM budget in MiB.
const VRAMCeilingEnvVar = "JANUS_VRAM_CEILING_MB"

// VRAMBudget tracks estimated VRAM usage across live agents to prevent
// exceeding the configured ceiling.
//
// Because the relay-race pipeline is sequential (one agent at a time),
// the budget is typically: claim → agent runs → release → next claim.
// If two small models could theoretically fit together, the budget
// permits it automatically.
type VRAMBudget struct {
	mu      sync.Mutex
	ceiling int64
	used    map[string]int64 // agentID → estimated bytes reserved
}

// NewVRAMBudget creates a tracker with the given ceiling in bytes.
// Pass 0 to use DefaultVRAMCeiling (11.8 GiB).
func NewVRAMBudget(ceilingBytes int64) *VRAMBudget {
	if ceilingBytes <= 0 {
		ceilingBytes = DefaultVRAMCeiling
	}
	return &VRAMBudget{
		ceiling: ceilingBytes,
		used:    make(map[string]int64),
	}
}

// VRAMCeilingBytesFromEnv returns the configured VRAM budget in bytes.
// Empty, invalid, or non-positive values fall back to DefaultVRAMCeiling.
func VRAMCeilingBytesFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv(VRAMCeilingEnvVar))
	if raw == "" {
		return DefaultVRAMCeiling
	}
	mb, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mb <= 0 {
		return DefaultVRAMCeiling
	}
	return mb * 1024 * 1024
}

// Claim reserves estimatedBytes for agentID.
// Returns an error immediately (does NOT block) if the claim would exceed
// the ceiling. The caller is responsible for deciding whether to wait and
// retry or abort.
func (v *VRAMBudget) Claim(agentID string, estimatedBytes int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	avail := v.ceiling - v.sumLocked()
	if estimatedBytes > avail {
		return fmt.Errorf(
			"janus/vram: %s needs %s but only %s available (ceiling %s)",
			agentID,
			fmtVRAM(estimatedBytes),
			fmtVRAM(avail),
			fmtVRAM(v.ceiling),
		)
	}
	v.used[agentID] = estimatedBytes
	return nil
}

// Release frees the VRAM reservation for agentID.
// Safe to call even if Claim was never called for this agentID.
func (v *VRAMBudget) Release(agentID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.used, agentID)
}

// Available returns the number of bytes currently free for new agents.
func (v *VRAMBudget) Available() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ceiling - v.sumLocked()
}

// Used returns the total bytes currently reserved.
func (v *VRAMBudget) Used() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sumLocked()
}

// EstimateGGUF returns a conservative VRAM estimate for a GGUF model file alone.
// For quantized models, file size ≈ VRAM footprint (within 5%).
// We add 10% for runtime buffers and fragmentation.
func EstimateGGUF(fileSizeBytes int64) int64 {
	return int64(float64(fileSizeBytes) * 1.10)
}

// EstimateModelVRAM returns weights + KV cache estimate for budget tracking.
// MoE models only keep active experts on GPU, so weight estimate uses ~55% of file size.
func EstimateModelVRAM(fileSizeBytes int64, ctxSize uint32) int64 {
	if ctxSize == 0 {
		ctxSize = 8192
	}
	weights := int64(float64(fileSizeBytes) * 0.55)
	kv_cache := int64(ctxSize) * 384 * 1024
	return weights + kv_cache
}

func (v *VRAMBudget) sumLocked() int64 {
	var total int64
	for _, b := range v.used {
		total += b
	}
	return total
}

func fmtVRAM(b int64) string {
	const (
		gb = 1024 * 1024 * 1024
		mb = 1024 * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

