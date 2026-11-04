package engine

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"janus/pkg/bridge"
)

// ErrGrammarInvalid is returned when llama_sampler_init_grammar rejects the GBNF text.
var ErrGrammarInvalid = errors.New("janus/grammar: the format could not be read")

// grammarHost is implemented by local GGUF backends that can run GBNF constraints.
type grammarHost interface {
	grammarVocab() (*bridge.LlamaLib, uintptr)
}

// ValidateOutputGrammar checks that grammar parses in llama.cpp before generation starts.
func ValidateOutputGrammar(prov Provider, grammar string) error {
	if grammar == "" {
		return nil
	}
	host, ok := prov.(grammarHost)
	if !ok {
		return fmt.Errorf("janus/grammar: local model required for grammar constraints")
	}
	lib, vocab := host.grammarVocab()
	if lib == nil || vocab == 0 {
		return fmt.Errorf("janus/grammar: model not loaded")
	}
	return tryGrammarSampler(lib, vocab, grammar)
}

func tryGrammarSampler(lib *bridge.LlamaLib, vocab uintptr, grammar string) error {
	if lib.SamplerInitGrammar == nil {
		return fmt.Errorf("janus/grammar: grammar sampler not available in llama.dll")
	}
	gbs := append([]byte(grammar), 0)
	root := append([]byte("root"), 0)
	handle := lib.SamplerInitGrammar(
		vocab,
		uintptr(unsafe.Pointer(&gbs[0])),
		uintptr(unsafe.Pointer(&root[0])),
	)
	runtime.KeepAlive(gbs)
	runtime.KeepAlive(root)
	if handle == 0 {
		return ErrGrammarInvalid
	}
	lib.SamplerFree(handle)
	return nil
}

func newGrammarSamplerChain(lib *bridge.LlamaLib, vocab uintptr, sp SamplerParams) (uintptr, error) {
	if sp.Grammar == "" {
		return 0, nil
	}
	if lib.SamplerChainInit == nil || lib.SamplerChainAdd == nil {
		return 0, fmt.Errorf("janus/grammar: sampler chain API unavailable")
	}
	if err := tryGrammarSampler(lib, vocab, sp.Grammar); err != nil {
		return 0, err
	}

	chain := lib.SamplerChainInit(1)
	if chain == 0 {
		return 0, fmt.Errorf("janus/grammar: sampler chain init failed")
	}

	gbs := append([]byte(sp.Grammar), 0)
	root := append([]byte("root"), 0)
	grammarSam := lib.SamplerInitGrammar(
		vocab,
		uintptr(unsafe.Pointer(&gbs[0])),
		uintptr(unsafe.Pointer(&root[0])),
	)
	runtime.KeepAlive(gbs)
	runtime.KeepAlive(root)
	if grammarSam == 0 {
		lib.SamplerFree(chain)
		return 0, ErrGrammarInvalid
	}
	lib.SamplerChainAdd(chain, grammarSam)

	if lib.SamplerInitTopK != nil {
		lib.SamplerChainAdd(chain, lib.SamplerInitTopK(80))
	}
	topP := float32(sp.TopP)
	if topP <= 0 || topP >= 1 {
		topP = 0.9
	}
	if lib.SamplerInitTopP != nil {
		lib.SamplerChainAdd(chain, lib.SamplerInitTopP(topP, 1))
	}
	if sp.Temp <= 0.05 {
		if lib.SamplerInitGreedy != nil {
			lib.SamplerChainAdd(chain, lib.SamplerInitGreedy())
		}
	} else {
		if lib.SamplerInitTemp != nil {
			lib.SamplerChainAdd(chain, lib.SamplerInitTemp(float32(sp.Temp)))
		}
		if lib.SamplerInitDist != nil {
			lib.SamplerChainAdd(chain, lib.SamplerInitDist(0xffffffff))
		} else if lib.SamplerInitGreedy != nil {
			lib.SamplerChainAdd(chain, lib.SamplerInitGreedy())
		}
	}
	return chain, nil
}
