package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"janus/pkg/engine"
	"janus/pkg/format"
	"net/http"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// OpenAI-compatible types
// ---------------------------------------------------------------------------

type oai_message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ReasoningContent carries a thinking model's <think> text separately
	// from the answer (same field name as DeepSeek's API and llama-server).
	// Ignored on input: past reasoning is never fed back into the prompt.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type oai_chat_request struct {
	Model     string        `json:"model"`
	Messages  []oai_message `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens"`
	// Pointers so an explicit 0 (e.g. temperature 0 = greedy) is honoured
	// instead of being mistaken for "not set".
	Temperature   *float64 `json:"temperature"`
	TopP          *float64 `json:"top_p"`
	RepeatPenalty *float64 `json:"repeat_penalty"` // Ollama/llama.cpp extension
	// Think turns a thinking model's reasoning on/off (Ollama's "think"
	// field). Omitted = JANUS_THINK env (default on).
	Think *bool `json:"think"`
	// ResponseFormat / Grammar constrain local GGUF output (GBNF via llama.cpp).
	ResponseFormat *format.ResponseFormat `json:"response_format"`
	Grammar        string                 `json:"grammar"`
}

// thinking_enabled resolves the per-request think flag against JANUS_THINK.
func (o oai_chat_request) thinking_enabled() bool {
	if o.Think != nil {
		return *o.Think
	}
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("JANUS_THINK")), "false")
}

// thinking_model is implemented by local backends whose model uses <think> blocks.
type thinking_model interface {
	IsThinkingModel() bool
}

// info_generator is implemented by local backends that report usage and
// finish reason for a generation.
type info_generator interface {
	GenerateWithInfo(ctx context.Context, tokens []int32, maxNewTokens int, sp engine.SamplerParams, info *engine.GenInfo) (<-chan string, error)
}

// gen_err_reporter exposes errors that happened inside a streaming generation.
type gen_err_reporter interface {
	LastGenErr() error
}

// opt_float returns *p, or def when the field was omitted.
func opt_float(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

// sampler_params builds per-request sampler settings for the local engine.
func (o oai_chat_request) sampler_params() engine.SamplerParams {
	penalty, lastN := engine.DefaultRepeatPenalty()
	return engine.SamplerParams{
		Temp:          opt_float(o.Temperature, 0.7),
		TopP:          opt_float(o.TopP, 0.9),
		RepeatPenalty: opt_float(o.RepeatPenalty, penalty),
		RepeatLastN:   lastN,
	}
}

// param_generator is implemented by local GGUF backends; it takes a
// per-request token cap and sampler settings.
type param_generator interface {
	GenerateWith(ctx context.Context, tokens []int32, maxNewTokens int, sp engine.SamplerParams) (<-chan string, error)
}

type oai_usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type oai_choice struct {
	Index        int         `json:"index"`
	Message      oai_message `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type oai_chat_response struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []oai_choice `json:"choices"`
	Usage   oai_usage    `json:"usage"`
}

type oai_stream_delta struct {
	Role             string `json:"role,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type oai_stream_choice struct {
	Index        int              `json:"index"`
	Delta        oai_stream_delta `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
}

type oai_stream_chunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []oai_stream_choice `json:"choices"`
}

type oai_model_entry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type oai_models_response struct {
	Object string            `json:"object"`
	Data   []oai_model_entry `json:"data"`
}

// ---------------------------------------------------------------------------
// /v1/models
// ---------------------------------------------------------------------------

// handle_v1_models returns a minimal models list.
// When local inference is active, lists the configured model path.
// Falls back to the configured Ollama model name.
func (r *router) handle_v1_models(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	now := time.Now().Unix()
	entries := list_configured_models()
	if len(entries) > 0 {
		write_json(w, http.StatusOK, oai_models_response{
			Object: "list",
			Data:   entries,
		})
		return
	}

	entries = []oai_model_entry{}

	if r.provider != nil {
		backend := strings.TrimSpace(os.Getenv("INFERENCE_BACKEND"))
		if backend == "openrouter" {
			modelName := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
			if modelName == "" {
				modelName = "openai/gpt-4o-mini"
			}
			entries = append(entries, oai_model_entry{
				ID:      modelName,
				Object:  "model",
				Created: now,
				OwnedBy: "openrouter",
			})
		} else {
			r.mu.RLock()
			modelPath := r.defaultModel
			r.mu.RUnlock()
			id := "local"
			if modelPath != "" {
				// Use filename without extension as the model ID.
				parts := strings.Split(strings.ReplaceAll(modelPath, "\\", "/"), "/")
				name := parts[len(parts)-1]
				if dot := strings.LastIndex(name, "."); dot > 0 {
					name = name[:dot]
				}
				id = name
			}
			entries = append(entries, oai_model_entry{
				ID:      id,
				Object:  "model",
				Created: now,
				OwnedBy: "janus-local",
			})
		}
	} else {
		modelName := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
		if modelName == "" {
			modelName = "ollama"
		}
		entries = append(entries, oai_model_entry{
			ID:      modelName,
			Object:  "model",
			Created: now,
			OwnedBy: "ollama",
		})
	}

	write_json(w, http.StatusOK, oai_models_response{
		Object: "list",
		Data:   entries,
	})
}

// ---------------------------------------------------------------------------
// /v1/chat/completions
// ---------------------------------------------------------------------------

// handle_v1_chat_completions is an OpenAI-compatible endpoint.
//
// Routing priority (evaluated in order):
//  1. X-Janus-Backend: cloud header when local-only mode is off
//     → cloud API (OpenAI or Anthropic via X-Janus-Cloud-Provider header)
//  2. Local GGUF engine loaded (provider != nil)
//     → llama.cpp Vulkan/CPU
//  3. Fallback
//     → Ollama proxy
func (r *router) handle_v1_chat_completions(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		write_json(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var oai_req oai_chat_request
	if err := json.NewDecoder(req.Body).Decode(&oai_req); err != nil {
		write_json(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
		return
	}

	reqID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	modelName := strings.TrimSpace(oai_req.Model)

	// Per-request model routing (openrouter/, gemini/, claude-, backend_config.json).
	if r.route_by_model(w, req, oai_req, reqID, created, modelName) {
		return
	}

	// Legacy header-based cloud routing.
	caller, err := cloud_caller_from_req(req)
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if caller != nil {
		r.completions_cloud(w, req, oai_req, reqID, created, caller)
		return
	}

	if r.provider != nil {
		r.completions_local(w, req, oai_req, reqID, created, modelName)
	} else {
		sessionID := strings.TrimSpace(req.Header.Get("X-Session-ID"))
		r.completions_ollama(w, req, oai_req, reqID, created, modelName, sessionID)
	}
}

// completions_local routes a chat completions request through the resident local engine.
// The model is kept loaded in r.provider — no per-request reload.
func (r *router) completions_local(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	reqID string,
	created int64,
	modelName string,
) {
	if err := r.ensureLocalModelForChat(modelName, req.Header.Get("X-Janus-Task")); err != nil {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": "model load: " + err.Error()})
		return
	}

	sp := oai_req.sampler_params()

	grammar, gerr := format.ResolveGrammar(oai_req.Grammar, oai_req.ResponseFormat)
	if gerr != nil {
		write_json(w, http.StatusBadRequest, map[string]any{"error": gerr.Error()})
		return
	}
	if grammar != "" {
		if err := engine.ValidateOutputGrammar(r.provider, grammar); err != nil {
			if errors.Is(err, engine.ErrGrammarInvalid) {
				write_json(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		sp.Grammar = grammar
	}

	// Inject role system prompt if no system message is already present.
	if r.prompts != nil {
		banner := r.prompts.ForRole("default")
		hasSystem := false
		for _, m := range oai_req.Messages {
			if m.Role == "system" {
				hasSystem = true
				break
			}
		}
		if !hasSystem && banner != "" {
			oai_req.Messages = append([]oai_message{{Role: "system", Content: banner}}, oai_req.Messages...)
		}
	}

	prompt := build_local_prompt(r.provider, oai_req.Messages)

	// Thinking off: pre-fill an empty reasoning block so the model answers
	// directly (the same mechanism as Qwen's enable_thinking=false).
	if !oai_req.thinking_enabled() {
		if tm, ok := r.provider.(thinking_model); ok && tm.IsThinkingModel() {
			prompt += think_open + "\n\n" + think_close + "\n\n"
		}
	}

	if oai_req.Stream {
		r.completions_local_stream_with_provider(w, req, r.provider, prompt, reqID, created, modelName, oai_req.MaxTokens, sp)
		return
	}

	info := &engine.GenInfo{FinishReason: "stop"}
	stream, err := start_local_generation(req.Context(), r.provider, prompt, oai_req.MaxTokens, sp, info)
	if err != nil {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var sb strings.Builder
	for piece := range stream {
		sb.WriteString(piece)
	}
	if ge, ok := r.provider.(gen_err_reporter); ok {
		if err := ge.LastGenErr(); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, engine.ErrGrammarInvalid) {
				status = http.StatusBadRequest
			}
			write_json(w, status, map[string]any{"error": err.Error()})
			return
		}
	}

	// Split before stripping end markers: gpt-oss closes its thinking with <|end|>.
	reasoning, content, _ := split_reasoning(sb.String())
	reasoning, content = engine.StripStopSequences(reasoning), strings.TrimSpace(engine.StripStopSequences(content))

	write_json(w, http.StatusOK, oai_chat_response{
		ID:      reqID,
		Object:  "chat.completion",
		Created: created,
		Model:   modelName,
		Choices: []oai_choice{{
			Index:        0,
			Message:      oai_message{Role: "assistant", Content: content, ReasoningContent: reasoning},
			FinishReason: info.FinishReason,
		}},
		Usage: oai_usage{
			PromptTokens:     info.PromptTokens,
			CompletionTokens: info.CompletionTokens,
			TotalTokens:      info.PromptTokens + info.CompletionTokens,
		},
	})
}

// start_local_generation tokenizes prompt and starts generation with the
// richest API the provider supports. info (if non-nil and supported) is
// filled in once the returned channel is drained.
func start_local_generation(ctx context.Context, prov engine.Provider, prompt string, maxTokens int, sp engine.SamplerParams, info *engine.GenInfo) (<-chan string, error) {
	tokens, err := prov.Tokenize(prompt)
	if err != nil {
		return nil, err
	}
	if ig, ok := prov.(info_generator); ok {
		return ig.GenerateWithInfo(ctx, tokens, maxTokens, sp, info)
	}
	if pg, ok := prov.(param_generator); ok {
		return pg.GenerateWith(ctx, tokens, maxTokens, sp)
	}
	return prov.Generate(ctx, tokens)
}

// completions_local_stream_with_provider streams using the provided ephemeral provider.
func (r *router) completions_local_stream_with_provider(
	w http.ResponseWriter,
	req *http.Request,
	prov engine.Provider,
	prompt string,
	reqID string,
	created int64,
	modelName string,
	maxTokens int,
	sp engine.SamplerParams,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported by server"})
		return
	}

	info := &engine.GenInfo{FinishReason: "stop"}
	stream, err := start_local_generation(req.Context(), prov, prompt, maxTokens, sp, info)
	if err != nil {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	send := func(reasoning, content string) {
		if reasoning == "" && content == "" {
			return
		}
		chunk := oai_stream_chunk{
			ID:      reqID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   modelName,
			Choices: []oai_stream_choice{{
				Index: 0,
				Delta: oai_stream_delta{Content: content, ReasoningContent: reasoning},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// <think> text goes out as reasoning_content, the answer as content.
	var splitter reasoning_splitter
	for piece := range stream {
		pr, pc := splitter.Push(piece)
		send(engine.StripStopSequences(pr), engine.StripStopSequences(pc))
	}
	fr, fc := splitter.Flush()
	send(engine.StripStopSequences(fr), engine.StripStopSequences(fc))

	stop := info.FinishReason
	finalChunk := oai_stream_chunk{
		ID:      reqID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   modelName,
		Choices: []oai_stream_choice{{
			Index:        0,
			Delta:        oai_stream_delta{},
			FinishReason: &stop,
		}},
	}
	data, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	flusher.Flush()
}

// completions_local_stream streams token pieces as SSE events using r.provider.
func (r *router) completions_local_stream(
	w http.ResponseWriter,
	req *http.Request,
	prompt string,
	reqID string,
	created int64,
	modelName string,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported by server"})
		return
	}

	tokens, err := r.provider.Tokenize(prompt)
	if err != nil {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	stream, err := r.provider.Generate(req.Context(), tokens)
	if err != nil {
		write_json(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for piece := range stream {
		chunk := oai_stream_chunk{
			ID:      reqID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   modelName,
			Choices: []oai_stream_choice{{
				Index: 0,
				Delta: oai_stream_delta{Content: piece},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	stop := "stop"
	finalChunk := oai_stream_chunk{
		ID:      reqID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   modelName,
		Choices: []oai_stream_choice{{
			Index:        0,
			Delta:        oai_stream_delta{},
			FinishReason: &stop,
		}},
	}
	data, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	flusher.Flush()
}

// completions_cloud routes a chat completions request to a cloud API.
func (r *router) completions_cloud(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	reqID string,
	created int64,
	caller cloud_caller,
) {
	maxTokens := oai_req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	result, err := caller.chat(req.Context(), oai_req.Messages, maxTokens, opt_float(oai_req.Temperature, 0))
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	write_json(w, http.StatusOK, oai_chat_response{
		ID:      reqID,
		Object:  "chat.completion",
		Created: created,
		Model:   caller.name(),
		Choices: []oai_choice{{
			Index:        0,
			Message:      oai_message{Role: "assistant", Content: result},
			FinishReason: "stop",
		}},
	})
}

// completions_ollama proxies a chat completions request to Ollama.
func (r *router) completions_ollama(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	reqID string,
	created int64,
	modelName string,
	sessionID string,
) {
	ollamaURL := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL"))
	client := new_ollama_client(ollamaURL)

	if modelName == "" || modelName == "ollama-default" || modelName == "ollama" || modelName == "local" {
		modelName = strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
	}

	msgs := make([]ollama_message, 0, len(oai_req.Messages)+1)
	// Inject role system prompt (editable from Config tab) if no system message exists.
	hasSystem := false
	for _, m := range oai_req.Messages {
		if m.Role == "system" {
			hasSystem = true
			break
		}
	}
	if !hasSystem {
		banner := "You are a helpful assistant. Respond in clear, plain English. Do not wrap your answer in JSON or any other structured format."
		if r.prompts != nil {
			banner = r.prompts.ForRole("default")
		}
		msgs = append(msgs, ollama_message{Role: "system", Content: banner})
	}
	for _, m := range oai_req.Messages {
		msgs = append(msgs, ollama_message{Role: m.Role, Content: m.Content})
	}

	// Only forward temperature when the caller set it — sending 0 by default
	// would force Ollama into greedy decoding on every request.
	opts := map[string]any{}
	if oai_req.Temperature != nil {
		opts["temperature"] = *oai_req.Temperature
	}
	if oai_req.MaxTokens > 0 {
		opts["num_predict"] = oai_req.MaxTokens
	}

	resp, err := client.chat(req.Context(), ollama_chat_request{
		Model:    modelName,
		Messages: msgs,
		Stream:   false,
		Options:  opts,
	})
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	write_json(w, http.StatusOK, oai_chat_response{
		ID:      reqID,
		Object:  "chat.completion",
		Created: created,
		Model:   modelName,
		Choices: []oai_choice{{
			Index:        0,
			Message:      oai_message{Role: "assistant", Content: resp.Message.Content},
			FinishReason: "stop",
		}},
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// split_messages extracts system and user content from a message list,
// dropping assistant turns. Only used for the single-turn llama2/alpaca
// formats; chat requests go through build_local_prompt.
func split_messages(msgs []oai_message) (system, user string) {
	var sb_sys, sb_usr strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "system":
			if sb_sys.Len() > 0 {
				sb_sys.WriteByte('\n')
			}
			sb_sys.WriteString(m.Content)
		case "user":
			if sb_usr.Len() > 0 {
				sb_usr.WriteByte('\n')
			}
			sb_usr.WriteString(m.Content)
		}
	}
	return sb_sys.String(), sb_usr.String()
}
