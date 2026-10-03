package bridge

import (
	"testing"
	"unsafe"
)

// Pins the Go mirrors to llama.h of llama.cpp b11146. Re-check llama.h and
// update these numbers when bumping the version in build.sh.
func TestParamLayoutsMatchPinnedHeader(t *testing.T) {
	var cp LlamaContextParams
	var mp LlamaModelParams
	checks := []struct {
		name      string
		got, want uintptr
	}{
		{"ctx.NSeqMax", unsafe.Offsetof(cp.NSeqMax), 12},
		{"ctx.NRsSeq", unsafe.Offsetof(cp.NRsSeq), 16},
		{"ctx.NThreads", unsafe.Offsetof(cp.NThreads), 28},
		{"ctx.NThreadsBatch", unsafe.Offsetof(cp.NThreadsBatch), 32},
		{"ctx.size", unsafe.Sizeof(cp), LlamaContextParamsSize},
		{"model.NGPULayers", unsafe.Offsetof(mp.NGPULayers), 16},
		{"model.SplitMode", unsafe.Offsetof(mp.SplitMode), 20},
		{"model.size", unsafe.Sizeof(mp), LlamaModelParamsSize},
		{"batch.Token", unsafe.Offsetof(LlamaBatch{}.Token), 8},
		{"batch.Logits", unsafe.Offsetof(LlamaBatch{}.Logits), 48},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}
