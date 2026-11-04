package main

import (
	"log"
	"os"
	"strings"

	"janus/pkg/discover"
	"janus/pkg/engine"
	"janus/pkg/features"
	"janus/pkg/singleton"
)

func normalizedInferenceBackend() string {
	b := strings.ToLower(strings.TrimSpace(os.Getenv("INFERENCE_BACKEND")))
	if b == "" {
		return "vulkan"
	}
	return b
}

func wantsLocalInferenceBackend(backend string) bool {
	return backend == "" || backend == "vulkan" || backend == "cpu"
}

func validateInferenceConfig(backend string) {
	switch backend {
	case "openrouter":
		if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) == "" {
			log.Fatalf(`janus: INFERENCE_BACKEND=openrouter but OPENROUTER_API_KEY is empty.
    Add a key to .env, run with Ollama, or drop a .gguf in .\models\ (Janus auto-detects when JANUS_AUTO=1).
    See README "Quick start".`)
		}
	case "ollama":
		return
	case "vulkan", "cpu":
		if strings.TrimSpace(os.Getenv("JANUS_MODEL_PATH")) == "" && !features.DeferModelLoad() {
			log.Fatalf(`janus: INFERENCE_BACKEND=%s but no .gguf was found and defer load is off.
    Put a model in .\models\ or set JANUS_MODEL_PATH.
    Or set INFERENCE_BACKEND=ollama if Ollama is running.
    Set JANUS_AUTO=0 to require explicit .env.`, backend)
		}
	case "cloud", "anthropic", "remote", "none", "off":
		return
	default:
		log.Fatalf("janus: unknown INFERENCE_BACKEND=%q — use vulkan, cpu, ollama, or openrouter", backend)
	}
}

func logDiscoverSummary(cap discover.Snapshot) {
	log.Printf("janus: system scan — %d local .gguf, ollama=%v (%d models), openrouter_key=%v, gemini_key=%v",
		len(cap.LocalGGUF), cap.OllamaReachable, cap.OllamaModelCount, cap.OpenRouterKey, cap.GeminiKey)
	if cap.AppliedModelPath != "" {
		log.Printf("janus: auto-selected model %q", cap.AppliedModelPath)
	}
	if cap.AppliedBackend != "" {
		log.Printf("janus: auto-selected backend %q", cap.AppliedBackend)
	}
	if cap.DeferModelLoad && cap.AppliedModelPath == "" && !cap.OllamaReachable && !cap.OpenRouterKey {
		log.Printf("janus: no inference source yet — UI is up; add .gguf to models\\, start Ollama, or set OPENROUTER_API_KEY")
	}
}

func startInferenceEngine() (engine.Provider, error) {
	cap := discover.ApplyStartupDefaults()
	logDiscoverSummary(cap)
	if lib := strings.TrimSpace(os.Getenv("JANUS_LIB_PATH")); lib != "" {
		if _, err := discover.EnsureSharedInferenceLibrary(lib); err != nil {
			log.Printf("janus: runtime library: %v", err)
		}
	}
	backend := normalizedInferenceBackend()
	validateInferenceConfig(backend)
	return engine.InitFromEnv()
}

func logInferenceStartup(backend string, provider engine.Provider, initErr error) {
	root := singleton.InstallRoot()
	if root != "" {
		log.Printf("janus: install root %s", root)
	}

	if initErr != nil {
		if backend == "ollama" {
			log.Printf("janus: Ollama mode (INFERENCE_BACKEND=ollama) — local GGUF engine not used; ensure Ollama is running")
			return
		}
		if wantsLocalInferenceBackend(backend) {
			log.Fatalf("janus: local engine (%s) failed: %v\n"+
				"    Check JANUS_MODEL_PATH, llama.dll in dist\\, and VRAM/GPU layers.\n"+
				"    Or set INFERENCE_BACKEND=ollama on purpose to use Ollama instead.", backend, initErr)
		}
		log.Fatalf("janus: inference backend %q failed: %v", backend, initErr)
	}

	switch {
	case provider != nil:
		name := provider.Backend()
		if features.DeferModelLoad() && wantsLocalInferenceBackend(backend) {
			log.Printf("janus: local engine ready [%s] — GGUF load deferred (JANUS_DEFER_MODEL_LOAD); use Models → Load Model", name)
			return
		}
		if backend == "openrouter" {
			log.Printf("janus: cloud inference ready [%s] — no local GGUF loaded (OpenRouter handles models)", name)
			return
		}
		log.Printf("janus: local engine ready [%s] — model loaded from JANUS_MODEL_PATH", name)
	case backend == "ollama":
		log.Printf("janus: Ollama mode — route chat/API to Ollama; no local GGUF in Janus")
	case wantsLocalInferenceBackend(backend):
		if features.DeferModelLoad() {
			log.Printf("janus: local engine [%s] — ready to load a model (UI → Models or POST /models/load)", backend)
			return
		}
		log.Fatalf("janus: INFERENCE_BACKEND=%s but no engine started — add a .gguf under models\\ or set JANUS_MODEL_PATH", backend)
	default:
		log.Printf("janus: inference mode %q — no local GGUF provider (API routing only)", backend)
	}
}
