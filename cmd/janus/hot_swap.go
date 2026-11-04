package main

import (
	"fmt"
	"log"
	"strings"

	"janus/pkg/engine"
)

type loadParamConfigurer interface {
	SetLoadParams(ctxSize uint32, gpuLayers int)
}

type modelSwapOptions struct {
	CtxSize   uint32
	GpuLayers *int
	Label     string
}

func (r *router) hotSwapLocalModel(modelPath string, opts modelSwapOptions) (displayPath string, err error) {
	if r.provider == nil {
		return "", fmt.Errorf("no local engine — restart with INFERENCE_BACKEND=vulkan")
	}
	modelPath = strings.TrimSpace(modelPath)
	if modelPath == "" {
		return "", fmt.Errorf("model_path required")
	}

	resolved, err := engine.ResolveModelPath(modelPath)
	if err != nil {
		return "", err
	}
	displayPath = engine.DisplayModelPath(resolved)

	r.loadMu.Lock()
	defer r.loadMu.Unlock()

	if same, err := engine.SameModelFile(r.provider, resolved); err == nil && same {
		if opts.Label != "" {
			r.mu.Lock()
			r.defaultModel = opts.Label
			r.mu.Unlock()
		}
		return displayPath, nil
	}

	if configurer, ok := r.provider.(loadParamConfigurer); ok && opts.GpuLayers != nil {
		configurer.SetLoadParams(opts.CtxSize, *opts.GpuLayers)
	} else if configurer, ok := r.provider.(loadParamConfigurer); ok && opts.CtxSize > 0 {
		type gpuLayerReader interface {
			ActiveGPULayers() int
		}
		gpuL := -1
		if gl, ok := r.provider.(gpuLayerReader); ok {
			gpuL = gl.ActiveGPULayers()
		}
		configurer.SetLoadParams(opts.CtxSize, gpuL)
	}

	log.Printf("janus: hot-swapping model to %q", displayPath)
	if err := r.provider.LoadModel(modelPath); err != nil {
		return displayPath, err
	}

	label := displayPath
	if opts.Label != "" {
		label = opts.Label
	}
	r.mu.Lock()
	r.defaultModel = label
	r.mu.Unlock()

	log.Printf("janus: model loaded — %q", displayPath)
	return displayPath, nil
}
