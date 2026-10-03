//go:build !windows

package bridge

import "fmt"

// llama_load_model_from_file, llama_new_context_with_model and llama_decode
// take their struct parameters BY VALUE. On the System V ABI (Linux, macOS)
// purego copies a Go struct argument onto the C stack, so we register the
// by-value signatures and wrap them behind the pointer-taking fields of
// LlamaLib (the Windows x64 ABI passes such structs by hidden reference, which
// is why abi_windows.go can bind a pointer directly).
//
// Limitation: only linux/amd64 is supported. llama_*_default_params() return
// a large struct through a hidden pointer that arm64 passes in x8 (not the
// first argument register as ModelDefaultParams/ContextDefaultParams assume),
// so arm64 (and macOS on Apple Silicon) is untested and unsupported.
//
// Requirements: purego >= v0.11 (stack-argument limit raised to 26 words) and
// struct buffers at least as large as the llama.h structs of the pinned
// llama.cpp release (see LlamaModelParamsSize / LlamaContextParamsSize).
func (lib *LlamaLib) registerStructFuncs() error {
	var (
		loadModel func(path string, params LlamaModelParams) uintptr
		newCtx    func(model uintptr, params LlamaContextParams) uintptr
		decode    func(ctx uintptr, batch LlamaBatch) int32
	)
	for _, s := range []struct {
		ptr  any
		name string
	}{
		{&loadModel, "llama_load_model_from_file"},
		{&newCtx, "llama_new_context_with_model"},
		{&decode, "llama_decode"},
	} {
		if err := registerSym(s.ptr, lib.handle, s.name); err != nil {
			return fmt.Errorf("bridge: register %q: %w", s.name, err)
		}
	}
	lib.LoadModelFromFile = func(path string, p *LlamaModelParams) uintptr { return loadModel(path, *p) }
	lib.NewContextWithModel = func(model uintptr, p *LlamaContextParams) uintptr { return newCtx(model, *p) }
	lib.Decode = func(ctx uintptr, b *LlamaBatch) int32 { return decode(ctx, *b) }
	return nil
}
