package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"janus/internal/bridge"
	"janus/internal/engine"
)

type oaiMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type oaiChatRequest struct {
	Model         string       `json:"model"`
	Messages      []oaiMessage `json:"messages"`
	Stream        bool         `json:"stream"`
	MaxTokens     int          `json:"max_tokens"`
	Temperature   *float64     `json:"temperature"`
	TopP          *float64     `json:"top_p"`
	RepeatPenalty *float64     `json:"repeat_penalty"`
	Think         *bool        `json:"think"`
}

func (o oaiChatRequest) thinkingEnabled() bool {
	if o.Think != nil {
		return *o.Think
	}
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("JANUS_THINK")), "false")
}

func optFloat(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func (o oaiChatRequest) samplerParams() engine.SamplerParams {
	penalty, lastN := engine.DefaultRepeatPenalty()
	return engine.SamplerParams{
		Temp:          optFloat(o.Temperature, 0.7),
		TopP:          optFloat(o.TopP, 0.9),
		RepeatPenalty: optFloat(o.RepeatPenalty, penalty),
		RepeatLastN:   lastN,
	}
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type oaiChoice struct {
	Index        int        `json:"index"`
	Message      oaiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type oaiChatResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []oaiChoice `json:"choices"`
	Usage   oaiUsage    `json:"usage"`
}

type oaiStreamDelta struct {
	Role             string `json:"role,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type oaiStreamChoice struct {
	Index        int            `json:"index"`
	Delta        oaiStreamDelta `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type oaiStreamChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []oaiStreamChoice `json:"choices"`
}

type oaiModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type oaiModelsResponse struct {
	Object string          `json:"object"`
	Data   []oaiModelEntry `json:"data"`
}

type chatFormatter interface {
	FormatChat(msgs []bridge.ChatMessage) (string, bool)
}

type thinkingModel interface {
	IsThinkingModel() bool
}

type infoGenerator interface {
	GenerateWithInfo(ctx context.Context, tokens []int32, maxNewTokens int, sp engine.SamplerParams, info *engine.GenInfo) (<-chan string, error)
}

type paramGenerator interface {
	GenerateWith(ctx context.Context, tokens []int32, maxNewTokens int, sp engine.SamplerParams) (<-chan string, error)
}

type genErrReporter interface {
	LastGenErr() error
}

func (s *server) handleV1Models(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	now := time.Now().Unix()
	entries := []oaiModelEntry{}

	if s.provider != nil {
		backend := strings.TrimSpace(os.Getenv("INFERENCE_BACKEND"))
		if backend == "openrouter" {
			modelName := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
			if modelName == "" {
				modelName = "openai/gpt-4o-mini"
			}
			entries = append(entries, oaiModelEntry{ID: modelName, Object: "model", Created: now, OwnedBy: "openrouter"})
		} else {
			s.mu.RLock()
			modelPath := s.defaultModel
			s.mu.RUnlock()
			id := "local"
			if modelPath != "" {
				parts := strings.Split(strings.ReplaceAll(modelPath, "\\", "/"), "/")
				name := parts[len(parts)-1]
				if dot := strings.LastIndex(name, "."); dot > 0 {
					name = name[:dot]
				}
				id = name
			}
			entries = append(entries, oaiModelEntry{ID: id, Object: "model", Created: now, OwnedBy: "janus-local"})
		}
	} else {
		modelName := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
		if modelName == "" {
			modelName = "ollama"
		}
		entries = append(entries, oaiModelEntry{ID: modelName, Object: "model", Created: now, OwnedBy: "ollama"})
	}

	writeJSON(w, http.StatusOK, oaiModelsResponse{Object: "list", Data: entries})
}

func (s *server) handleV1ChatCompletions(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var oaiReq oaiChatRequest
	if err := json.NewDecoder(req.Body).Decode(&oaiReq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
		return
	}

	reqID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	modelName := strings.TrimSpace(oaiReq.Model)

	if s.provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no local engine loaded"})
		return
	}

	sp := oaiReq.samplerParams()
	prompt := buildLocalPrompt(s.provider, oaiReq.Messages)

	if !oaiReq.thinkingEnabled() {
		if tm, ok := s.provider.(thinkingModel); ok && tm.IsThinkingModel() {
			prompt += thinkOpen + "\n\n" + thinkClose + "\n\n"
		}
	}

	if oaiReq.Stream {
		s.streamCompletion(w, req, prompt, reqID, created, modelName, oaiReq.MaxTokens, sp)
		return
	}

	info := &engine.GenInfo{FinishReason: "stop"}
	stream, err := startGeneration(req.Context(), s.provider, prompt, oaiReq.MaxTokens, sp, info)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var sb strings.Builder
	for piece := range stream {
		sb.WriteString(piece)
	}
	if ge, ok := s.provider.(genErrReporter); ok {
		if err := ge.LastGenErr(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}

	reasoning, content, _ := splitReasoning(engine.StripStopSequences(sb.String()))
	writeJSON(w, http.StatusOK, oaiChatResponse{
		ID:      reqID,
		Object:  "chat.completion",
		Created: created,
		Model:   modelName,
		Choices: []oaiChoice{{
			Index:        0,
			Message:      oaiMessage{Role: "assistant", Content: content, ReasoningContent: reasoning},
			FinishReason: info.FinishReason,
		}},
		Usage: oaiUsage{
			PromptTokens:     info.PromptTokens,
			CompletionTokens: info.CompletionTokens,
			TotalTokens:      info.PromptTokens + info.CompletionTokens,
		},
	})
}

func startGeneration(ctx context.Context, prov engine.Provider, prompt string, maxTokens int, sp engine.SamplerParams, info *engine.GenInfo) (<-chan string, error) {
	tokens, err := prov.Tokenize(prompt)
	if err != nil {
		return nil, err
	}
	if ig, ok := prov.(infoGenerator); ok {
		return ig.GenerateWithInfo(ctx, tokens, maxTokens, sp, info)
	}
	if pg, ok := prov.(paramGenerator); ok {
		return pg.GenerateWith(ctx, tokens, maxTokens, sp)
	}
	return prov.Generate(ctx, tokens)
}

func (s *server) streamCompletion(
	w http.ResponseWriter,
	req *http.Request,
	prompt string,
	reqID string,
	created int64,
	modelName string,
	maxTokens int,
	sp engine.SamplerParams,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}

	info := &engine.GenInfo{FinishReason: "stop"}
	stream, err := startGeneration(req.Context(), s.provider, prompt, maxTokens, sp, info)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
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
		chunk := oaiStreamChunk{
			ID:      reqID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   modelName,
			Choices: []oaiStreamChoice{{
				Index: 0,
				Delta: oaiStreamDelta{Content: content, ReasoningContent: reasoning},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	var splitter reasoningSplitter
	for piece := range stream {
		send(splitter.Push(engine.StripStopSequences(piece)))
	}
	send(splitter.Flush())

	stop := info.FinishReason
	finalChunk := oaiStreamChunk{
		ID:      reqID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   modelName,
		Choices: []oaiStreamChoice{{
			Index:        0,
			Delta:        oaiStreamDelta{},
			FinishReason: &stop,
		}},
	}
	data, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	flusher.Flush()
}

func buildLocalPrompt(p engine.Provider, msgs []oaiMessage) string {
	pf := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_PROMPT_FORMAT")))
	switch pf {
	case "llama2", "alpaca":
		system, user := splitMessages(msgs)
		return formatFixedPrompt(pf, system, user)
	case "chatml":
		return formatChatML(msgs)
	}
	if f, ok := p.(chatFormatter); ok {
		chat := make([]bridge.ChatMessage, 0, len(msgs))
		for _, m := range msgs {
			chat = append(chat, bridge.ChatMessage{Role: m.Role, Content: m.Content})
		}
		if out, ok := f.FormatChat(chat); ok {
			return out
		}
	}
	return formatChatML(msgs)
}

func formatChatML(msgs []oaiMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&sb, "<|im_start|>%s\n%s<|im_end|>\n", m.Role, m.Content)
	}
	sb.WriteString("<|im_start|>assistant\n")
	return sb.String()
}

func formatFixedPrompt(format, system, user string) string {
	switch format {
	case "llama2":
		return fmt.Sprintf("[INST] <<SYS>>\n%s\n<</SYS>>\n\n%s [/INST]", system, user)
	case "alpaca":
		return fmt.Sprintf("### Instruction:\n%s\n\n### Input:\n%s\n\n### Response:", system, user)
	default:
		return formatChatML([]oaiMessage{{Role: "system", Content: system}, {Role: "user", Content: user}})
	}
}

func splitMessages(msgs []oaiMessage) (system, user string) {
	var sbSys, sbUsr strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "system":
			if sbSys.Len() > 0 {
				sbSys.WriteByte('\n')
			}
			sbSys.WriteString(m.Content)
		case "user":
			if sbUsr.Len() > 0 {
				sbUsr.WriteByte('\n')
			}
			sbUsr.WriteString(m.Content)
		}
	}
	return sbSys.String(), sbUsr.String()
}
