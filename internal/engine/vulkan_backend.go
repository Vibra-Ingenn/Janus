package engine

import (
	"math"
	"math/rand"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"janus/internal/bridge"
)

// VulkanBackend is the local inference Provider that runs .gguf models on the
// GPU via the llama.cpp Vulkan backend.
//
// Memory ownership contract:
//   - rawModel and rawCtx are C pointers — never touched by the Go GC.
//   - They are freed ONLY inside Unload() via bridge.LlamaLib.FreeContext /
//     FreeModel.  Never free them in a finalizer.
//   - Token slices and batchState memory are pinned with runtime.KeepAlive
//     for the duration of any DLL call that receives their address.
//
// Concurrency:
//   - stateMu (RWMutex): RLock for inference reads, Lock for Load/Unload.
//   - genMu (Mutex): serialises concurrent Generate calls — llama_context
//     is NOT thread-safe for concurrent decode calls.
type VulkanBackend struct {
	temp         float64
	topP         float64

	lib          *bridge.LlamaLib
	libPath      string
	nGPULayers   int
	maxNewTokens int    // 0 = unlimited (bounded only by context window)
	nCtxSize     uint32 // context window size; 0 = use default (16384)

	stateMu    sync.RWMutex
	genMu      sync.Mutex
	errMu      sync.Mutex
	lastGenErr error
	rawModel   uintptr
	rawVocab   uintptr // llama_vocab* — needed by newer llama.cpp for tokenize
	rawCtx     uintptr
	loaded     bool
	modelPath  string
}

func (v *VulkanBackend) setLastGenErr(err error) {
	v.errMu.Lock()
	defer v.errMu.Unlock()
	v.lastGenErr = err
}

func (v *VulkanBackend) LastGenErr() error {
	v.errMu.Lock()
	defer v.errMu.Unlock()
	return v.lastGenErr
}

func (v *VulkanBackend) SetLoadParams(ctxSize uint32, gpuLayers int) {
	v.stateMu.Lock()
	defer v.stateMu.Unlock()
	if ctxSize > 0 {
		v.nCtxSize = ctxSize
	}
	v.nGPULayers = gpuLayers
}

func (v *VulkanBackend) SetSampler(temp float64, topP float64) {
	v.stateMu.Lock()
	defer v.stateMu.Unlock()
	v.temp = temp
	v.topP = topP
}


// NewVulkanBackend creates a VulkanBackend by loading the llama.cpp shared
// library at libPath.
//
//	nGPULayers   — GPU layers to offload: -1 = all, 0 = CPU only, N = first N.
//	maxNewTokens — maximum tokens to generate per call: 0 = full context window.
func NewVulkanBackend(libPath string, nGPULayers, maxNewTokens int) (*VulkanBackend, error) {
	if libPath == "" {
		libPath = defaultLibPath()
	}

	// On newer llama.cpp builds the backends (ggml-vulkan.dll etc.) are
	// separate plugins that must be loaded before the first model load.
	// ggml_backend_load_all() lives in ggml.dll, not llama.dll.
	// We try to call it here; silence any error so older builds still work.
	bridge.TryLoadAllBackends(libPath)

	lib, err := bridge.Open(libPath)
	if err != nil {
		return nil, fmt.Errorf("vulkan_backend: open library: %w", err)
	}

	lib.BackendInit()
	if lib.BackendLoadAll != nil {
		lib.BackendLoadAll()
	}

	nCtxSize := uint32(16384)
	if s := os.Getenv("JANUS_CTX_SIZE"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			nCtxSize = uint32(n)
		}
	}

		return &VulkanBackend{
		lib:          lib,
		libPath:      libPath,
		nGPULayers:   nGPULayers,
		maxNewTokens: maxNewTokens,
		nCtxSize:     nCtxSize,
		temp:         0.7,
		topP:         0.9,
	}, nil
}

// LoadModel loads the .gguf model at path into GPU VRAM.
// On GPU OOM, automatically retries with nGPULayers=0 (CPU fallback).
// Safe to call multiple times — previous model is unloaded first.
func (v *VulkanBackend) ActiveModelPath() string {
	v.stateMu.RLock()
	defer v.stateMu.RUnlock()
	return v.modelPath
}

func (v *VulkanBackend) ActiveModelLoaded() bool {
	v.stateMu.RLock()
	defer v.stateMu.RUnlock()
	return v.loaded
}

func (v *VulkanBackend) ActiveCtxSize() uint32 {
	v.stateMu.RLock()
	defer v.stateMu.RUnlock()
	if v.nCtxSize == 0 {
		return 8192
	}
	return v.nCtxSize
}

func (v *VulkanBackend) ActiveGPULayers() int {
	v.stateMu.RLock()
	defer v.stateMu.RUnlock()
	return v.nGPULayers
}

func (v *VulkanBackend) LoadModel(path string) error {
	resolved, err := ResolveModelPath(path)
	if err != nil {
		return err
	}

	v.stateMu.Lock()
	defer v.stateMu.Unlock()

	if v.loaded && v.modelPath == resolved {
		log.Printf("engine: model %q is already loaded, skipping load", DisplayModelPath(resolved))
		return nil
	}

	v.unloadLocked()

	err = v.tryLoadLocked(resolved, v.nGPULayers)
	if err != nil {
		if errors.Is(err, ErrGPUOOM) {
			log.Printf("janus/engine: GPU OOM loading %q — retrying on CPU", path)
			err = v.tryLoadLocked(path, 0)
		}
	}
	return err
}

func (v *VulkanBackend) tryLoadLocked(path string, nLayers int) error {
	if v.lib == nil {
		return ErrLibNotFound
	}

	params := v.lib.ModelDefaultParams(nLayers)
	paramsPtr := bridge.UnsafePtr(&params)

	log.Printf("engine: loading model %q with nGPULayers=%d", path, nLayers)
	model := v.lib.LoadModelFromFile(path, paramsPtr)
	runtime.KeepAlive(params)

	if model == 0 {
		return fmt.Errorf("llama_load_model_from_file returned NULL for %q — check model path and VRAM", path)
	}
	log.Printf("engine: model loaded OK (ptr=%x)", model)

	nCtx := v.nCtxSize
	if nCtx == 0 {
		nCtx = 8192
	}
	ctxParams := v.lib.ContextDefaultParams(nCtx, runtime.NumCPU())
	log.Printf("engine: creating context: n_ctx=%d n_threads=%d n_batch=%d", ctxParams.NCtx, ctxParams.NThreads, ctxParams.NBatch)
	ctxParamsPtr := bridge.UnsafePtr(&ctxParams)

	ctx := v.lib.NewContextWithModel(model, ctxParamsPtr)
	runtime.KeepAlive(ctxParams)

	if ctx == 0 {
		v.lib.FreeModel(model)
		if nLayers > 0 {
			log.Printf("engine: llama_new_context_with_model returned NULL with nGPULayers=%d — treating as GPU OOM, will retry on CPU", nLayers)
			return ErrGPUOOM
		}
		return errors.New("janus/engine: llama_new_context_with_model returned NULL even on CPU (nGPULayers=0)")
	}
	log.Printf("engine: context created OK (ptr=%x)", ctx)

	v.rawModel = model
	v.rawCtx = ctx
	v.modelPath = path

	// Newer llama.cpp (b5000+): tokenize takes llama_vocab* not llama_model*.
	if v.lib.ModelGetVocab != nil {
		v.rawVocab = v.lib.ModelGetVocab(model)
		log.Printf("engine: model vocab handle = %x", v.rawVocab)
	}

	v.loaded = true

	// Track VRAM usage in the global budget (weights + KV context estimate).
	if fi, err := os.Stat(path); err == nil {
		nCtx := v.nCtxSize
		if nCtx == 0 {
			nCtx = 8192
		}
		estimate := EstimateModelVRAM(fi.Size(), nCtx)
		if claimErr := GlobalBudget.Claim("vulkan-primary", estimate); claimErr != nil {
			log.Printf("engine: VRAM budget warning: %v (model loaded anyway)", claimErr)
		}
	}

	return nil
}

// Tokenize converts text into a slice of llama token IDs.
// The returned slice is Go-owned and safe to pass to Generate.
func (v *VulkanBackend) Tokenize(text string) ([]int32, error) {
	v.stateMu.RLock()
	defer v.stateMu.RUnlock()

	if !v.loaded {
		return nil, ErrModelNotLoaded
	}
	if len(text) == 0 {
		return []int32{}, nil
	}

	maxTokens := int32(len(text) + 16)
	buf := make([]int32, maxTokens)

	tokTarget := v.rawVocab
	if tokTarget == 0 {
		tokTarget = v.rawModel
	}
	n := v.lib.Tokenize(
		tokTarget,
		text,
		int32(len(text)),
		uintptr(unsafe.Pointer(&buf[0])),
		maxTokens,
		1,
		0,
	)
	runtime.KeepAlive(buf)

	if n < 0 {
		return nil, fmt.Errorf("janus/engine: tokenize error code %d", n)
	}

	return buf[:n], nil
}

// Generate streams decoded token strings into the returned channel.
// The caller must drain the channel.  Cancel ctx to stop generation early.
// Returns an error immediately if the model is not loaded.
//
// Generation uses greedy sampling (argmax over logits).
// The KV cache is cleared before each call so contexts do not bleed.
func (v *VulkanBackend) Generate(ctx context.Context, tokens []int32) (<-chan string, error) {
	return v.generateWithLimit(ctx, tokens, v.maxNewTokens)
}

func (v *VulkanBackend) generateWithLimit(ctx context.Context, tokens []int32, maxNewTokens int) (<-chan string, error) {
	v.stateMu.RLock()
	if !v.loaded {
		v.stateMu.RUnlock()
		return nil, ErrModelNotLoaded
	}

	if len(tokens) == 0 {
		v.stateMu.RUnlock()
		return nil, fmt.Errorf("janus/engine: Generate called with empty token slice")
	}

	ch := make(chan string, 64)
	v.setLastGenErr(nil)

	go func() {
		defer v.stateMu.RUnlock()
		defer close(ch)

		v.genMu.Lock()
		defer v.genMu.Unlock()

		// Clear memory (KV cache / recurrent state) so previous requests don't bleed.
		v.clearMemory()

		nCtx := int(v.lib.NCtx(v.rawCtx))

		// NVocab now takes vocab* in newer llama.cpp (b5000+)
		vocabTarget := v.rawVocab
		if vocabTarget == 0 {
			vocabTarget = v.rawModel
		}
		nVocab := int(v.lib.NVocab(vocabTarget))
		eosToken := v.lib.TokenEOS(vocabTarget)

		// End-of-turn token (ChatML <|im_end|>) — additional stop condition.
		eotToken := int32(-1)
		if v.lib.TokenEOT != nil {
			eotToken = v.lib.TokenEOT(vocabTarget)
		}

		// Compute hard stop position (context window or user-supplied limit).
		stopPos := nCtx
		if maxNewTokens > 0 {
			if lim := len(tokens) + maxNewTokens; lim < stopPos {
				stopPos = lim
			}
		}

		// --- Prefill pass ---
		// Decode input tokens in n_batch-sized chunks to avoid
		// GGML_ASSERT(n_tokens_all <= cparams.n_batch) on large prompts.
		rc, lastChunk := v.chunkedPrefill(tokens)
		if rc != 0 {
			if bridge.IsOOMResult(rc) {
				log.Printf("janus/engine: GPU OOM during prefill (rc=%d)", rc)
				v.setLastGenErr(ErrGPUOOM)
			} else {
				v.setLastGenErr(fmt.Errorf("janus/engine: prefill failed rc=%d", rc))
			}
			return
		}

		// --- Generation loop ---
		pos := len(tokens)
		pieceBuf := make([]byte, 32)
		// After chunked prefill, logits are at (lastChunkSize - 1) in the last batch.
		// After single-token decode, logits are at batch index 0.
		logitsIdx := int32(lastChunk - 1)

		// String-based stop sequences (ChatML end-of-turn markers).
		stopSeqs := []string{"<|im_end|>", "<|end|>", "<|eot_id|>"}
		var recentBuf strings.Builder // tracks recent output for stop detection
		const recentBufCap = 32       // only need enough chars to match longest stop seq

		log.Printf("engine: generate: prefill ok, nTokens=%d nVocab=%d stopPos=%d", len(tokens), nVocab, stopPos)

	genLoop:
		for pos < stopPos {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Get logits for the token that had logits enabled in the last batch.
			logitsPtr := v.lib.GetLogitsIth(v.rawCtx, logitsIdx)
			if logitsPtr == nil {
				break
			}
			logits := unsafe.Slice((*float32)(logitsPtr), nVocab)

			// Temperature and Top-P Sampler
			best := sampleLogits(logits, v.temp, v.topP)
			runtime.KeepAlive(logits)

			// Universal end-of-generation check: is_eog catches EVERY stop token
			// for this model (EOS, EOT, <|eot_id|>, <end_of_turn>, harmony end
			// tokens, etc.). Relying only on TokenEOS/TokenEOT misses model-
			// specific end tokens and causes runaway/looping generation. Fall
			// back to the explicit EOS/EOT comparison on older builds.
			if (v.lib.TokenIsEOG != nil && v.lib.TokenIsEOG(vocabTarget, best)) ||
				best == eosToken || (eotToken >= 0 && best == eotToken) {
				log.Printf("engine: generate: stop token at pos=%d (generated %d tokens)", pos, pos-len(tokens))
				break
			}

			// Detokenize the selected token to a UTF-8 string piece.
			n := v.lib.TokenToPiece(
				vocabTarget, best,
				uintptr(unsafe.Pointer(&pieceBuf[0])),
				int32(len(pieceBuf)),
				0, 0,
			)
			if n < 0 {
				// Buffer was too small — grow and retry once.
				pieceBuf = make([]byte, -n+1)
				n = v.lib.TokenToPiece(
					vocabTarget, best,
					uintptr(unsafe.Pointer(&pieceBuf[0])),
					int32(len(pieceBuf)),
					0, 0,
				)
			}
			runtime.KeepAlive(pieceBuf)

			if n > 0 {
				piece := string(pieceBuf[:n])

				// Check string-based stop sequences.
				recentBuf.WriteString(piece)
				if recentBuf.Len() > recentBufCap {
					s := recentBuf.String()
					recentBuf.Reset()
					recentBuf.WriteString(s[len(s)-recentBufCap:])
				}
				recent := recentBuf.String()
				for _, ss := range stopSeqs {
					if strings.Contains(recent, ss) {
						log.Printf("engine: generate: stop %q at pos=%d (generated %d tokens)", ss, pos, pos-len(tokens))
						break genLoop
					}
				}

				select {
				case ch <- piece:
				case <-ctx.Done():
					return
				}
			}

			// Decode the next single token to advance the KV cache.
			next := newBatch([]int32{best}, int32(pos), true)
			rc = v.lib.Decode(v.rawCtx, next.ptr())
			runtime.KeepAlive(next)
			if rc != 0 {
				if bridge.IsOOMResult(rc) {
					v.setLastGenErr(ErrGPUOOM)
				} else {
					v.setLastGenErr(fmt.Errorf("janus/engine: decode failed rc=%d", rc))
				}
				break
			}
			logitsIdx = 0 // single-token batch: logits at index 0
			pos++
		}
	}()

	return ch, nil
}

// Predict is a convenience wrapper: Tokenize → Generate → collect stream.
// The output is post-processed to strip stop sequences and thinking blocks.
func (v *VulkanBackend) Predict(ctx context.Context, prompt string) (string, error) {
	tokens, err := v.Tokenize(prompt)
	if err != nil {
		return "", err
	}

	stream, err := v.Generate(ctx, tokens)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for tok := range stream {
		sb.WriteString(tok)
	}
	if genErr := v.LastGenErr(); genErr != nil {
		return "", genErr
	}
	return cleanOutput(sb.String()), ctx.Err()
}

// PredictWithLimit is like Predict, but lets HTTP/API callers honor a
// per-request max_tokens value without mutating the backend-wide default used
// by kernel and tool loops.
func (v *VulkanBackend) PredictWithLimit(ctx context.Context, prompt string, maxNewTokens int) (string, error) {
	tokens, err := v.Tokenize(prompt)
	if err != nil {
		return "", err
	}

	stream, err := v.generateWithLimit(ctx, tokens, maxNewTokens)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for tok := range stream {
		sb.WriteString(tok)
	}
	if genErr := v.LastGenErr(); genErr != nil {
		return "", genErr
	}
	return cleanOutput(sb.String()), ctx.Err()
}

// cleanOutput strips ChatML stop sequences and <think>...</think> reasoning blocks.
func cleanOutput(s string) string {
	// Strip stop sequences that may have partially leaked.
	for _, ss := range []string{"<|im_end|>", "<|im_end|", "<|im_end", "<|end|>", "<|eot_id|>"} {
		s = strings.ReplaceAll(s, ss, "")
	}
	// Remove <think>...</think> reasoning blocks (non-greedy).
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "</think>")
		if end == -1 {
			// Unterminated think block — strip from <think> to end.
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

// Unload releases the llama_context and llama_model from GPU/CPU memory
// immediately.  After Unload, LoadModel must be called again before generating.
// Blocks until any in-progress Generate call completes.
// Safe to call multiple times.
func (v *VulkanBackend) Unload() {
	v.stateMu.Lock()
	defer v.stateMu.Unlock()
	v.unloadLocked()
}

// clearMemory resets the model's memory module between requests.
// Prefers llama_memory_clear (new API, works with recurrent models)
// and falls back to llama_kv_cache_clear (legacy).
func (v *VulkanBackend) clearMemory() {
	if v.lib.GetMemory != nil && v.lib.MemoryClear != nil {
		mem := v.lib.GetMemory(v.rawCtx)
		if mem != 0 {
			v.lib.MemoryClear(mem, 1) // clearData = true
		}
	} else if v.lib.KVCacheClear != nil {
		v.lib.KVCacheClear(v.rawCtx)
	}
}

func (v *VulkanBackend) unloadLocked() {
	if v.rawCtx != 0 {
		v.clearMemory()
		v.lib.FreeContext(v.rawCtx)
		v.rawCtx = 0
	}
	if v.rawModel != 0 {
		v.lib.FreeModel(v.rawModel)
		v.rawModel = 0
	}
	v.rawVocab = 0
	v.loaded = false
	v.modelPath = ""
	GlobalBudget.Release("vulkan-primary")
}

// Backend returns "vulkan" for logging and routing.
func (v *VulkanBackend) Backend() string {
	return "vulkan"
}

// ensure VulkanBackend satisfies Provider at compile time.
var _ Provider = (*VulkanBackend)(nil)

// chunkedPrefill decodes tokens in n_batch-sized chunks so the prompt can
// exceed the DLL's batch limit without hitting the
// GGML_ASSERT(n_tokens_all <= cparams.n_batch) assertion.
// Only the *last* chunk requests logits (computeLast=true).
// Returns (decode return code, size of the last chunk).
// The logits index after prefill is lastChunkSize-1, NOT len(tokens)-1.
func (v *VulkanBackend) chunkedPrefill(tokens []int32) (int32, int) {
	nBatch := int(v.lib.ContextDefaultParams(0, 1).NBatch)
	if nBatch <= 0 {
		nBatch = 512
	}

	lastChunkSize := 0
	for off := 0; off < len(tokens); off += nBatch {
		end := off + nBatch
		isLast := false
		if end >= len(tokens) {
			end = len(tokens)
			isLast = true
		}
		chunk := tokens[off:end]
		lastChunkSize = len(chunk)
		b := newBatch(chunk, int32(off), isLast)
		rc := v.lib.Decode(v.rawCtx, b.ptr())
		runtime.KeepAlive(b)
		if rc != 0 {
			return rc, lastChunkSize
		}
	}
	return 0, lastChunkSize
}

// ---------------------------------------------------------------------------
// batchState — Go-owned memory backing a bridge.LlamaBatch
//
// llama_batch is passed to llama_decode by value, but on both Windows x64
// (MS ABI) and Linux x64 (SysV ABI) structs > 8 bytes are passed via a
// hidden pointer in the second parameter register.  Passing
// uintptr(unsafe.Pointer(&bs.batch)) as the second arg is ABI-correct.
//
// All slice backing arrays must remain live until after the Decode call;
// use runtime.KeepAlive(bs) to guarantee this.
// ---------------------------------------------------------------------------

type batchState struct {
	batch   bridge.LlamaBatch
	tokens  []int32
	pos     []int32
	nSeqID  []int32
	seqIDs  []uintptr // per-token pointers into seqID0
	seqID0  []int32   // always {0} — every token belongs to sequence 0
	logitsB []int8    // 1 = compute logits for this position, 0 = skip
}

// newBatch builds a batchState for the given tokens starting at startPos.
// If computeLast is true, logits are requested only for the final token
// (standard for prefill and single-token generation steps).
func newBatch(tokens []int32, startPos int32, computeLast bool) *batchState {
	n := len(tokens)
	bs := &batchState{
		tokens:  make([]int32, n),
		pos:     make([]int32, n),
		nSeqID:  make([]int32, n),
		seqIDs:  make([]uintptr, n),
		seqID0:  []int32{0},
		logitsB: make([]int8, n),
	}
	copy(bs.tokens, tokens)
	seqID0Ptr := uintptr(unsafe.Pointer(&bs.seqID0[0]))
	for i := 0; i < n; i++ {
		bs.pos[i] = startPos + int32(i)
		bs.nSeqID[i] = 1
		bs.seqIDs[i] = seqID0Ptr
	}
	if computeLast && n > 0 {
		bs.logitsB[n-1] = 1
	}
	bs.batch = bridge.LlamaBatch{
		NTokens: int32(n),
		Token:   uintptr(unsafe.Pointer(&bs.tokens[0])),
		Embd:    0,
		Pos:     uintptr(unsafe.Pointer(&bs.pos[0])),
		NSeqID:  uintptr(unsafe.Pointer(&bs.nSeqID[0])),
		SeqID:   uintptr(unsafe.Pointer(&bs.seqIDs[0])),
		Logits:  uintptr(unsafe.Pointer(&bs.logitsB[0])),
	}
	return bs
}

// ptr returns the uintptr of bs.batch for passing to lib.Decode.
func (bs *batchState) ptr() uintptr {
	return uintptr(unsafe.Pointer(&bs.batch))
}

// sampleLogits performs temperature scaling, Top-K=80 filtering, and Top-P (nucleus) sampling.
func sampleLogits(logits []float32, temp float64, topP float64) int32 {
	nVocab := len(logits)
	if temp <= 0.05 {
		best := int32(0)
		bestVal := logits[0]
		for i := 1; i < nVocab; i++ {
			if logits[i] > bestVal {
				bestVal = logits[i]
				best = int32(i)
			}
		}
		return best
	}

	type logitEntry struct {
		id  int32
		val float32
	}
	topK := 80
	entries := make([]logitEntry, nVocab)
	for i := 0; i < nVocab; i++ {
		entries[i] = logitEntry{id: int32(i), val: logits[i]}
	}

	importSortNeeded := true
	if importSortNeeded {
		// Use a local sort slice of topK entries to stay extremely fast.
		// Sort entries descending
		importSortNeeded = false
	}
	
	// We import "sort" locally or use basic bubble/insertion sort for K=80 to avoid import issues.
	// Actually, sorting the whole slice using Go's sort package is fine. Let's make sure "sort" is imported!
	// We'll add the sort package to the imports in the script below.
	
	// Bubble sort top K elements to keep it self-contained without needing Go import rewrites.
	// This is extremely simple and fast for topK=80:
	for i := 0; i < topK && i < nVocab; i++ {
		maxIdx := i
		for j := i + 1; j < nVocab; j++ {
			if entries[j].val > entries[maxIdx].val {
				maxIdx = j
			}
		}
		entries[i], entries[maxIdx] = entries[maxIdx], entries[i]
	}
	
	if len(entries) > topK {
		entries = entries[:topK]
	}

	var maxLogit float64 = float64(entries[0].val)
	var sum float64 = 0.0
	type tokenProb struct {
		id   int32
		prob float64
	}
	probs := make([]tokenProb, len(entries))
	for i, entry := range entries {
		val := math.Exp((float64(entry.val) - maxLogit) / temp)
		probs[i] = tokenProb{id: entry.id, prob: val}
		sum += val
	}

	for i := range probs {
		probs[i].prob /= sum
	}

	if topP > 0.0 && topP < 1.0 {
		var cumSum float64 = 0.0
		cutoff := len(probs)
		for i, p := range probs {
			cumSum += p.prob
			if cumSum >= topP {
				cutoff = i + 1
				break
			}
		}
		probs = probs[:cutoff]
		sum = 0.0
		for _, p := range probs {
			sum += p.prob
		}
		for i := range probs {
			probs[i].prob /= sum
		}
	}

	rVal := rand.Float64()
	var cum float64 = 0.0
	for _, p := range probs {
		cum += p.prob
		if rVal <= cum {
			return p.id
		}
	}
	return probs[0].id
}

