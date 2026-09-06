// Package bridge provides pure-Go bindings to a pre-compiled llama.cpp
// shared library (llama.dll on Windows, libllama.so on Linux) via purego.
//
// No C compiler or CGO is required at build time.
// The shared library is loaded at runtime with purego.Dlopen.
//
// ABI reference: see internal/bridge/headers/llama.h
// Target: llama.cpp b1870 or later (b5000+ recommended for Vulkan stability)
package bridge

import (
	"encoding/binary"
	"fmt"
	"log"
	"unsafe"

	"github.com/ebitengine/purego"
)

// openLib and closeLib are implemented in open_windows.go (Windows) and
// open_unix.go (Linux/macOS) using platform-specific DLL loading APIs.

// LlamaLib holds all loaded symbols from the llama.cpp shared library.
// Obtain one with Open(); close with Close() or defer lib.Close().
type LlamaLib struct {
	handle uintptr

	// LoadModelFromFile loads a GGUF model file and returns an opaque handle.
	// paramsPtr must be unsafe.Pointer to a LlamaModelParams struct.
	// Returns 0 on failure (check logs from the library).
	LoadModelFromFile func(path string, paramsPtr uintptr) uintptr

	// FreeModel releases the model and its GPU/CPU memory.
	// Must be called exactly once per successful LoadModelFromFile.
	FreeModel func(model uintptr)

	// NewContextWithModel creates an inference context for a loaded model.
	// paramsPtr must be unsafe.Pointer to a LlamaContextParams struct.
	// Returns 0 on failure (e.g. GPU OOM).
	NewContextWithModel func(model uintptr, paramsPtr uintptr) uintptr

	// FreeContext releases the inference context and its KV cache.
	// Must be called exactly once per successful NewContextWithModel.
	FreeContext func(ctx uintptr)

	// ModelGetVocab returns the vocab handle for a loaded model.
	// Required by newer llama.cpp (b5000+) where tokenize/vocab
	// functions take llama_vocab* instead of llama_model*.
	ModelGetVocab func(model uintptr) uintptr

	// Tokenize converts text into token IDs.
	// vocabOrModel is llama_vocab* on newer builds (b5000+), llama_model* on older.
	// tokensPtr is unsafe.Pointer to a []int32 backing array.
	// Returns the number of tokens written, or a negative error code.
	Tokenize func(vocabOrModel uintptr, text string, textLen int32, tokensPtr uintptr, nMax int32, addSpecial uint8, parseSpecial uint8) int32

	// Decode runs a forward pass on the provided batch.
	// batchPtr is unsafe.Pointer to a LlamaBatch struct.
	// Returns 0 on success, 1 on OOM, -1 on other error.
	Decode func(ctx uintptr, batchPtr uintptr) int32

	// BackendInit initialises all ggml backends (call once at startup).
	BackendInit func()

	// BackendFree releases all ggml backends (call once at shutdown).
	BackendFree func()

	// GetLogitsIth returns a pointer to the float32 logits array for the
	// i-th token in the last decoded batch (i=0 for single-token decode).
	// Slice it with unsafe.Slice((*float32)(unsafe.Pointer(ptr)), nVocab).
	GetLogitsIth func(ctx uintptr, i int32) uintptr

	// NVocab returns the vocabulary size of the model.
	NVocab func(model uintptr) int32

	// NCtx returns the context window size of the loaded context.
	NCtx func(ctx uintptr) uint32

	// TokenToPiece converts a single token ID to its UTF-8 string fragment.
	// buf is a pre-allocated byte slice; length is its size.
	// Returns bytes written, or negative required size on buffer-too-small.
	TokenToPiece func(model uintptr, token int32, buf uintptr, length int32, lstrip int32, special uint8) int32

	// TokenEOS returns the end-of-sequence token ID for the model.
	TokenEOS func(model uintptr) int32

	// TokenEOT returns the end-of-turn token ID (e.g. <|im_end|> in ChatML).
	// May be nil on older builds — check before calling.
	TokenEOT func(model uintptr) int32

	// KVCacheClear clears the entire KV cache of the context.
	// Call this before each new generation to prevent stale context.
	KVCacheClear func(ctx uintptr)

	// GetMemory returns the llama_memory* handle from a context.
	// Required before calling MemoryClear on newer builds.
	GetMemory func(ctx uintptr) uintptr

	// MemoryClear clears the memory module (recurrent state / KV cache).
	// New API (llama.cpp b5000+) that replaces llama_kv_cache_clear for
	// recurrent models (Gated Delta Net, Mamba, RWKV, etc.).
	// Takes llama_memory* (from GetMemory), and a bool (clear data).
	MemoryClear func(memory uintptr, clearData uint8)

	// BackendLoadAll loads all available ggml backends (new API, llama.cpp b4000+).
	// Call this before LoadModelFromFile on newer builds.
	BackendLoadAll func()

	// --- Sampler API (llama.cpp b3000+) ---

	// SamplerChainInit creates a sampler chain.
	// The uint8 arg is llama_sampler_chain_params { bool no_perf }.
	SamplerChainInit func(noPerf uint8) uintptr

	// SamplerChainAdd appends a sampler to the chain.
	SamplerChainAdd func(chain uintptr, sampler uintptr)

	// SamplerSample runs the chain and returns the selected token.
	// idx is the logits index (typically -1 for last output).
	SamplerSample func(chain uintptr, ctx uintptr, idx int32) int32

	// SamplerFree destroys a sampler or sampler chain.
	SamplerFree func(sampler uintptr)

	// SamplerInitGreedy creates a greedy (argmax) sampler.
	SamplerInitGreedy func() uintptr

	// SamplerInitTemp creates a temperature sampler.
	SamplerInitTemp func(temp float32) uintptr

	// SamplerInitTopK creates a top-k sampler.
	SamplerInitTopK func(k int32) uintptr

	// SamplerInitTopP creates a nucleus (top-p) sampler.
	SamplerInitTopP func(p float32, minKeep uint64) uintptr

	// SamplerInitMinP creates a min-p sampler.
	SamplerInitMinP func(p float32, minKeep uint64) uintptr

	// SamplerInitGrammar creates a GBNF grammar-constrained sampler.
	// vocab is llama_vocab*, grammarStr is the GBNF grammar text,
	// grammarRoot is the root rule name (e.g. "root").
	SamplerInitGrammar func(vocab uintptr, grammarStr string, grammarRoot string) uintptr

	// llamaModelDefaultParamsRaw is the raw binding for llama_model_default_params.
	// On Windows/Linux x64, structs > 8 bytes are returned via a hidden first
	// argument (caller allocates, passes pointer, callee writes and returns it).
	// We register it as func(uintptr) uintptr and pass our buffer pointer.
	llamaModelDefaultParamsRaw func(uintptr) uintptr

	// llamaContextDefaultParamsRaw is the raw binding for llama_context_default_params.
	// Same hidden-pointer ABI as llama_model_default_params.
	llamaContextDefaultParamsRaw func(uintptr) uintptr
}

// TryLoadAllBackends attempts to load ggml.dll from the same directory as
// llamaLibPath and call ggml_backend_load_all() on it.  This registers the
// separate backend plugins (ggml-vulkan.dll, ggml-cpu-*.dll, …) that newer
// llama.cpp builds require before any model can be loaded.
func TryLoadAllBackends(llamaLibPath string) {
	dir := llamaLibPath
	// Strip filename to get directory
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' || dir[i] == '\\' {
			dir = dir[:i]
			break
		}
	}
	if dir == llamaLibPath {
		dir = "." // no separator found — same directory
	}

	candidates := []string{
		dir + "/ggml.dll",
		dir + "\\ggml.dll",
		"ggml.dll",
	}

	for _, path := range candidates {
		handle, err := openLib(path)
		if err != nil {
			log.Printf("bridge: TryLoadAllBackends: cannot open %q: %v", path, err)
			continue
		}
		log.Printf("bridge: TryLoadAllBackends: loaded %q", path)
		var fn func()
		if err := registerSym(&fn, handle, "ggml_backend_load_all"); err != nil {
			log.Printf("bridge: TryLoadAllBackends: ggml_backend_load_all NOT found in %q: %v — Vulkan backend will NOT be registered", path, err)
		} else if fn != nil {
			log.Printf("bridge: TryLoadAllBackends: calling ggml_backend_load_all()")
			fn()
			log.Printf("bridge: TryLoadAllBackends: ggml_backend_load_all() returned — backends registered")
		} else {
			log.Printf("bridge: TryLoadAllBackends: ggml_backend_load_all symbol is nil — skipping")
		}
		// Leave the handle open — the DLL must stay loaded.
		return
	}
	log.Printf("bridge: TryLoadAllBackends: ggml.dll NOT found in any candidate path — Vulkan may fail to initialise")
}

// Open loads the llama.cpp shared library at libPath and registers all
// required function symbols. Returns an error if the file cannot be opened
// or a required symbol is missing.
// The caller must call lib.Close() when done.
func Open(libPath string) (*LlamaLib, error) {
	handle, err := openLib(libPath)
	if err != nil {
		return nil, err
	}

	lib := &LlamaLib{handle: handle}

	if err := lib.registerSymbols(); err != nil {
		_ = closeLib(handle)
		return nil, err
	}

	return lib, nil
}

// Close unloads the shared library. After Close, all function fields are invalid.
func (lib *LlamaLib) Close() error {
	if lib.handle == 0 {
		return nil
	}
	err := closeLib(lib.handle)
	lib.handle = 0
	return err
}

// registerSymbols binds all DLL function symbols via purego.RegisterLibFunc.
// RegisterLibFunc panics on a missing symbol; we recover and return an error.
func (lib *LlamaLib) registerSymbols() (retErr error) {
	type sym struct {
		ptr  any
		name string
	}
	symbols := []sym{
		{&lib.LoadModelFromFile, "llama_load_model_from_file"},
		{&lib.FreeModel, "llama_free_model"},
		{&lib.NewContextWithModel, "llama_new_context_with_model"},
		{&lib.FreeContext, "llama_free"},
		{&lib.ModelGetVocab, "llama_model_get_vocab"},
		{&lib.Tokenize, "llama_tokenize"},
		{&lib.Decode, "llama_decode"},
		{&lib.BackendInit, "llama_backend_init"},
		{&lib.BackendFree, "llama_backend_free"},
		{&lib.GetLogitsIth, "llama_get_logits_ith"},
		{&lib.NVocab, "llama_n_vocab"},
		{&lib.NCtx, "llama_n_ctx"},
		{&lib.TokenToPiece, "llama_token_to_piece"},
		{&lib.TokenEOS, "llama_token_eos"},
	}

	for _, s := range symbols {
		if err := registerSym(s.ptr, lib.handle, s.name); err != nil {
			return fmt.Errorf("bridge: register %q: %w", s.name, err)
		}
	}

	// Optional symbols - don't fail if missing
	optionalSymbols := []sym{
		{&lib.KVCacheClear, "llama_kv_cache_clear"},
		{&lib.GetMemory, "llama_get_memory"},
		{&lib.MemoryClear, "llama_memory_clear"},
		{&lib.TokenEOT, "llama_token_eot"},
		{&lib.BackendLoadAll, "ggml_backend_load_all"},
		{&lib.llamaModelDefaultParamsRaw, "llama_model_default_params"},
		{&lib.llamaContextDefaultParamsRaw, "llama_context_default_params"},
		// Sampler API
		{&lib.SamplerChainInit, "llama_sampler_chain_init"},
		{&lib.SamplerChainAdd, "llama_sampler_chain_add"},
		{&lib.SamplerSample, "llama_sampler_sample"},
		{&lib.SamplerFree, "llama_sampler_free"},
		{&lib.SamplerInitGreedy, "llama_sampler_init_greedy"},
		{&lib.SamplerInitTemp, "llama_sampler_init_temp"},
		{&lib.SamplerInitTopK, "llama_sampler_init_top_k"},
		{&lib.SamplerInitTopP, "llama_sampler_init_top_p"},
		{&lib.SamplerInitMinP, "llama_sampler_init_min_p"},
		{&lib.SamplerInitGrammar, "llama_sampler_init_grammar"},
	}
	for _, s := range optionalSymbols {
		_ = registerSym(s.ptr, lib.handle, s.name) // ignore error
	}

	return nil
}

// registerSym wraps purego.RegisterLibFunc with panic recovery.
// RegisterLibFunc is void and panics if the symbol is not found.
func registerSym(fptr any, handle uintptr, name string) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("symbol not found: %v", r)
		}
	}()
	purego.RegisterLibFunc(fptr, handle, name)
	return nil
}

// ---------------------------------------------------------------------------
// Param structs — Go mirrors of the C structs in llama.h
//
// Layout is for llama.cpp b5000+ on 64-bit (Windows and Linux, same ABI).
// If you are on an older build, compare against internal/bridge/headers/llama.h
// and adjust field offsets accordingly.
//
// IMPORTANT: These structs are passed to C code via unsafe.Pointer.
// The Go compiler will NOT reorder fields (they are in declaration order).
// Do NOT add methods, embed other structs, or use Go interface fields here.
// ---------------------------------------------------------------------------

// SplitMode matches enum llama_split_mode in llama.h.
type SplitMode int32

const (
	SplitModeNone  SplitMode = 0
	SplitModeLayer SplitMode = 1
	SplitModeRow   SplitMode = 2
)

// LlamaModelParamsSize is the expected size of the C struct on 64-bit systems.
// Confirmed via llama_model_default_params() byte dump on the actual DLL.
const LlamaModelParamsSize = 72

// LlamaModelParams mirrors llama_model_params in llama.h (b5500+, 64-bit).
// Layout confirmed by calling llama_model_default_params() and inspecting
// the raw bytes: two pointer fields precede n_gpu_layers.
//
//	Offset  Size  Field
//	0       8     devices       (*backend_dev[], NULL = all)
//	8       8     _ptr2         (second pointer, NULL by default — reserved)
//	16      4     n_gpu_layers  (int32, default -1 = all layers on GPU)
//	20      4     split_mode    (int32 enum, default LAYER=1)
//	24      4     main_gpu      (int32, default 0)
//	28      4     _pad          (alignment to 8 bytes)
//	32      8     tensor_split  (*float32, NULL = uniform)
//	40      8     progress_cb   (func ptr, NULL)
//	48      8     progress_data (void*, NULL)
//	56      8     kv_overrides  (*override, NULL)
//	64      1     vocab_only    (bool)
//	65      1     use_mmap      (bool, default true)
//	66      1     use_mlock     (bool)
//	67      1     check_tensors (bool)
//	68      4     _trailing_pad
//	Total: 72 bytes
type LlamaModelParams struct {
	Devices      uintptr // NULL = use all available backends
	_ptr2        uintptr //nolint:unused — second pointer field (NULL by default)
	NGPULayers   int32   // default -1 = all layers on GPU
	SplitMode    SplitMode
	MainGPU      int32
	_pad0        int32 //nolint:unused
	TensorSplit  uintptr
	ProgressCB   uintptr
	ProgressData uintptr
	KVOverrides  uintptr
	VocabOnly    uint8
	UseMmap      uint8
	UseMlock     uint8
	CheckTensors uint8
	_pad1        [4]byte //nolint:unused
}

// DefaultModelParams returns a LlamaModelParams initialised with the same
// defaults as llama_model_default_params() in llama.h.
// nGPULayers: -1 = offload all layers, 0 = CPU only, N = offload N layers.
func DefaultModelParams(nGPULayers int) LlamaModelParams {
	return LlamaModelParams{
		NGPULayers: int32(nGPULayers),
		SplitMode:  SplitModeLayer,
		UseMmap:    1,
	}
}

// ModelDefaultParams calls llama_model_default_params() from the loaded DLL
// so the struct layout is always correct regardless of llama.cpp version.
// On x64 (Windows and Linux) large structs are returned via a hidden first
// argument — we pass a pointer to a 128-byte buffer so we have room even if
// the C struct grows beyond our LlamaModelParams mirror.
// Falls back to the hand-crafted DefaultModelParams if the symbol is absent.
func (lib *LlamaLib) ModelDefaultParams(nGPULayers int) LlamaModelParams {
	if lib.llamaModelDefaultParamsRaw != nil {
		var buf [128]byte // oversized so any future growth is safe
		lib.llamaModelDefaultParamsRaw(uintptr(unsafe.Pointer(&buf[0])))

		// Dump raw bytes so we can confirm the actual C struct layout.
		// Look for the n_gpu_layers field offset in these logs.
		log.Printf("bridge: llama_model_default_params raw bytes (first 64):")
		for i := 0; i < 64; i += 8 {
			log.Printf("  [%02d-%02d]: %02x %02x %02x %02x  %02x %02x %02x %02x  | int32@%d=%d  int32@%d=%d",
				i, i+7,
				buf[i], buf[i+1], buf[i+2], buf[i+3],
				buf[i+4], buf[i+5], buf[i+6], buf[i+7],
				i, int32(binary.LittleEndian.Uint32(buf[i:])),
				i+4, int32(binary.LittleEndian.Uint32(buf[i+4:])),
			)
		}

		p := *(*LlamaModelParams)(unsafe.Pointer(&buf[0]))
		// NGPULayers is confirmed at offset 16. Override with caller's value.
		// -1 (from env) is remapped to 9999 upstream, but DLL default is -1
		// which means all layers. Only override if caller explicitly set one.
		if nGPULayers >= 0 {
			p.NGPULayers = int32(nGPULayers)
		}
		return p
	}
	log.Printf("bridge: llama_model_default_params not found in DLL — using hand-crafted defaults")
	return DefaultModelParams(nGPULayers)
}

// LlamaContextParamsSize is set large enough to cover any foreseeable
// llama.cpp version. All bytes beyond the named fields are zero-initialized,
// ensuring pointer fields like `smpl` (sampler chain) are NULL by default.
// The DLL must not read garbage past the struct boundary.
const LlamaContextParamsSize = 512

// LlamaContextParams mirrors llama_context_params in llama.h.
// Named fields cover the stable head of the struct; the rest is a zeroed
// byte array so the DLL never reads garbage for newer optional fields.
//
//	Offset  Size  Field
//	0       4     n_ctx           (uint32, 0 = from model)
//	4       4     n_batch         (uint32, default 512)
//	8       4     n_ubatch        (uint32, default 512)
//	12      4     n_seq_max       (uint32, default 1)
//	16      4     n_threads       (int32)
//	20      4     n_threads_batch (int32)
//	24+     ...   all other fields zeroed — NULL pointers, 0 enums
//	Total: 512 bytes (over-allocated; safe)
type LlamaContextParams struct {
	NCtx          uint32
	NBatch        uint32
	NUbatch       uint32
	NSeqMax       uint32
	NThreads      int32
	NThreadsBatch int32
	_rest         [LlamaContextParamsSize - 24]byte //nolint:unused
}

// DefaultContextParams returns a LlamaContextParams with sensible defaults.
func DefaultContextParams(nCtx uint32, nThreads int) LlamaContextParams {
	if nCtx == 0 {
		nCtx = 2048
	}
	if nThreads <= 0 {
		nThreads = 4
	}
	return LlamaContextParams{
		NCtx:          nCtx,
		NBatch:        512,
		NUbatch:       512,
		NSeqMax:       1,
		NThreads:      int32(nThreads),
		NThreadsBatch: int32(nThreads),
	}
}

// ContextDefaultParams calls llama_context_default_params() from the loaded DLL
// using the same hidden-pointer ABI as ModelDefaultParams.
// Only n_ctx and n_threads are overridden; all other fields (pooling_type,
// sampler chain, etc.) come from the DLL so the layout is always correct.
// Falls back to hand-crafted defaults if the symbol is absent.
func (lib *LlamaLib) ContextDefaultParams(nCtx uint32, nThreads int) LlamaContextParams {
	if lib.llamaContextDefaultParamsRaw != nil {
		log.Printf("bridge: using DLL llama_context_default_params (struct size %d bytes)", LlamaContextParamsSize)
		var buf [LlamaContextParamsSize]byte
		lib.llamaContextDefaultParamsRaw(uintptr(unsafe.Pointer(&buf[0])))
		p := *(*LlamaContextParams)(unsafe.Pointer(&buf[0]))
		if nCtx > 0 {
			p.NCtx = nCtx
		}
		if nThreads > 0 {
			p.NThreads = int32(nThreads)
			p.NThreadsBatch = int32(nThreads)
		}
		return p
	}
	log.Printf("bridge: llama_context_default_params NOT found — using hand-crafted %d-byte zeroed defaults", LlamaContextParamsSize)
	return DefaultContextParams(nCtx, nThreads)
}

// LlamaBatch mirrors llama_batch in llama.h.
// Used for single-sequence decode calls in Generate.
//
//	Offset  Size  Field
//	0       4     n_tokens      (int32)
//	4       4     _pad
//	8       8     token         (*llama_token / *int32)
//	16      8     embd          (*float, NULL for token mode)
//	24      8     pos           (*llama_pos / *int32)
//	32      8     n_seq_id      (*int32)
//	40      8     seq_id        (**int32)
//	48      8     logits        (*int8)
//	56      4     all_pos_0     (llama_pos / int32)
//	60      4     all_pos_1     (llama_pos / int32)
//	64      4     all_seq_id    (llama_seq_id / int32)
//	68      4     _trailing_pad
//	Total: 72 bytes
type LlamaBatch struct {
	NTokens  int32
	_pad     int32 //nolint:unused
	Token    uintptr
	Embd     uintptr
	Pos      uintptr
	NSeqID   uintptr
	SeqID    uintptr
	Logits   uintptr
	AllPos0  int32
	AllPos1  int32
	AllSeqID int32
	_tpad    int32 //nolint:unused
}

// UnsafePtr returns an unsafe.Pointer to p, which can be cast to uintptr for
// passing to LlamaLib function calls.
// The caller must call runtime.KeepAlive(p) after any DLL call that uses the pointer.
func UnsafePtr[T any](p *T) uintptr {
	return uintptr(unsafe.Pointer(p))
}

// IsOOMResult returns true if the llama_decode return code indicates OOM.
// llama_decode returns 1 specifically for OOM (vs -1 for other errors).
func IsOOMResult(code int32) bool {
	return code == 1
}

// WriteLittleEndianInt32 writes v at byte offset off in buf.
// Useful for manually patching struct fields when exact layout is uncertain.
func WriteLittleEndianInt32(buf []byte, off int, v int32) {
	binary.LittleEndian.PutUint32(buf[off:], uint32(v))
}
