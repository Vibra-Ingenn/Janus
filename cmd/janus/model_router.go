package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"janus/pkg/engine"
	"janus/pkg/routerconfig"
)

const maxFallbackAttempts = 5

var (
	backendConfig     *routerconfig.Config
	backendConfigPath string
	backendConfigMod  time.Time
	backendConfigMu   sync.RWMutex
)

func init_backend_config() {
	reload_backend_config(true)
}

func reload_backend_config(force bool) {
	path := routerconfig.FindConfigFile()
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	backendConfigMu.RLock()
	sameFile := path == backendConfigPath && info.ModTime().Equal(backendConfigMod)
	backendConfigMu.RUnlock()
	if !force && sameFile {
		return
	}

	cfg, err := routerconfig.Load(path)
	if err != nil {
		log.Printf("model_router: failed to load %s: %v", path, err)
		return
	}
	backendConfigMu.Lock()
	backendConfig = cfg
	backendConfigPath = path
	backendConfigMod = info.ModTime()
	backendConfigMu.Unlock()
	log.Printf("model_router: loaded %d backends from %s", len(cfg.Backends), path)
}

func get_backend_config() *routerconfig.Config {
	backendConfigMu.RLock()
	cfg := backendConfig
	backendConfigMu.RUnlock()
	return cfg
}

type model_route struct {
	kind       string
	model      string
	backend_id string
	local_path string
}

func parse_model_route(model_name string, task_header string) model_route {
	reload_backend_config(false)
	cfg := get_backend_config()

	model_name = strings.TrimSpace(model_name)
	task := strings.TrimSpace(task_header)

	// Dropdown / explicit backend id wins over task default routing.
	if cfg != nil && model_name != "" {
		if entry := cfg.BackendByID(model_name); entry != nil {
			return route_from_backend(entry)
		}
	}

	if cfg != nil && task != "" {
		if backend_id := cfg.ResolveTaskBackend(task); backend_id != "" {
			if entry := cfg.BackendByID(backend_id); entry != nil {
				return route_from_backend(entry)
			}
		}
	}

	lower := strings.ToLower(model_name)
	switch {
	case lower == "" || lower == "janus" || lower == "local" || lower == "default":
		return model_route{kind: "local", model: model_name}
	case strings.HasPrefix(lower, "anthropic/"):
		cloud_model := strings.TrimPrefix(model_name, "anthropic/")
		return model_route{kind: "anthropic", model: cloud_model}
	case strings.HasPrefix(lower, "claude-"):
		return model_route{kind: "anthropic", model: model_name}
	case strings.HasPrefix(lower, "openrouter/"):
		cloud_model := strings.TrimPrefix(model_name, "openrouter/")
		cloud_model = strings.TrimPrefix(cloud_model, "openrouter/")
		return model_route{kind: "openrouter", model: cloud_model}
	case lower == "openrouter":
		model := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
		if model == "" {
			model = "anthropic/claude-3.5-sonnet"
		}
		return model_route{kind: "openrouter", model: model, backend_id: "openrouter"}
	case strings.HasPrefix(lower, "gemini/"):
		return model_route{kind: "gemini", model: strings.TrimPrefix(model_name, "gemini/")}
	case lower == "gemini" || lower == "gemini-thinking" || lower == "gemini-flash-lite":
		model := "gemini-2.0-flash-exp"
		if cfg != nil {
			if entry := cfg.BackendByID(lower); entry != nil && entry.DefaultModel != "" {
				model = entry.DefaultModel
			}
		}
		return model_route{kind: "gemini", model: model, backend_id: lower}
	default:
		return model_route{kind: "local", model: model_name}
	}
}

func route_from_backend(entry *routerconfig.BackendEntry) model_route {
	switch strings.ToLower(entry.Type) {
	case "anthropic":
		model := entry.DefaultModel
		if model == "" {
			model = strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL"))
		}
		if model == "" {
			model = "claude-sonnet-4-20250514"
		}
		return model_route{kind: "anthropic", model: model, backend_id: entry.ID}
	case "openrouter":
		model := entry.DefaultModel
		if model == "" {
			model = strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
		}
		return model_route{kind: "openrouter", model: model, backend_id: entry.ID}
	case "gemini":
		model := entry.DefaultModel
		if model == "" {
			model = "gemini-2.0-flash-exp"
		}
		return model_route{kind: "gemini", model: model, backend_id: entry.ID}
	default:
		path := entry.ModelPath
		if strings.EqualFold(entry.ID, "local") {
			path = ""
		}
		return model_route{
			kind:       "local",
			model:      entry.ID,
			backend_id: entry.ID,
			local_path: path,
		}
	}
}

// cloud_routing_allowed is false only when "Use only this PC" is switched on (JANUS_LOCAL_ONLY=true/1).
func cloud_routing_allowed() (bool, string) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_LOCAL_ONLY")))
	if v == "true" || v == "1" {
		return false, "Use only this PC is on (JANUS_LOCAL_ONLY), so cloud models are off"
	}
	return true, ""
}

func key_ok(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	lower := strings.ToLower(key)
	return !strings.Contains(lower, "your_") && !strings.Contains(lower, "changeme")
}

func (r *router) route_by_model(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	req_id string,
	created int64,
	model_name string,
) bool {
	route := parse_model_route(model_name, req.Header.Get("X-Janus-Task"))
	switch route.kind {
	case "openrouter", "gemini", "anthropic":
		if ok, reason := cloud_routing_allowed(); !ok {
			log.Printf("model_router: cloud blocked for %q: %s", route.backend_id, reason)
			if r.try_fallback(w, req, oai_req, req_id, created, route.backend_id, reason) {
				return true
			}
			write_json(w, http.StatusForbidden, map[string]any{"error": reason})
			return true
		}
	}
	switch route.kind {
	case "openrouter":
		r.completions_openrouter(w, req, oai_req, req_id, created, route.model, route.backend_id)
		return true
	case "gemini":
		r.completions_gemini(w, req, oai_req, req_id, created, route.model, route.backend_id)
		return true
	case "anthropic":
		r.completions_anthropic(w, req, oai_req, req_id, created, route.model, route.backend_id)
		return true
	case "local":
		if route.local_path != "" {
			if _, err := r.hotSwapLocalModel(route.local_path, modelSwapOptions{Label: route.backend_id}); err != nil {
				writeLocalModelLoadError(w, err)
				return true
			}
		}
		label := route.model
		if route.backend_id != "" {
			label = route.backend_id
		}
		if label == "" {
			label = "local"
		}
		r.completions_local(w, req, oai_req, req_id, created, label)
		return true
	default:
		return false
	}
}

func (r *router) completions_anthropic(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	req_id string,
	created int64,
	model_name string,
	backend_id string,
) {
	if backend_id == "" {
		backend_id = "anthropic"
	}

	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if !key_ok(key) {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, "ANTHROPIC_API_KEY not set") {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": "anthropic: ANTHROPIC_API_KEY not set in Janus .env"})
		return
	}

	if model_name == "" {
		model_name = strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL"))
	}
	if model_name == "" {
		model_name = "claude-sonnet-4-20250514"
	}

	max_tokens := oai_req.MaxTokens
	if max_tokens <= 0 {
		max_tokens = 4096
	}

	client := &anthropic_client{apiKey: key}
	text, err := client.chat_with_model(req.Context(), oai_req.Messages, max_tokens, opt_float(oai_req.Temperature, 0.7), model_name)
	if err != nil {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, err.Error()) {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{
			"error":      "anthropic: " + err.Error(),
			"backend_id": backend_id,
		})
		return
	}

	if is_refusal_response(text) {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, "model refusal") {
			return
		}
	}

	model_label := backend_id
	if model_label == "" || model_label == "anthropic" {
		model_label = "anthropic/" + model_name
	}

	write_json(w, http.StatusOK, oai_chat_response{
		ID:      req_id,
		Object:  "chat.completion",
		Created: created,
		Model:   model_label,
		Choices: []oai_choice{{
			Index:        0,
			Message:      oai_message{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
	})
}

func (r *router) completions_openrouter(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	req_id string,
	created int64,
	model_name string,
	backend_id string,
) {
	if backend_id == "" {
		backend_id = "openrouter"
	}

	key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if key == "" {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, "OPENROUTER_API_KEY not set") {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": "openrouter: OPENROUTER_API_KEY not set"})
		return
	}

	if model_name == "" {
		model_name = strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
		if model_name == "" {
			model_name = "anthropic/claude-3.5-sonnet"
		}
	}

	max_tokens := oai_req.MaxTokens
	if max_tokens <= 0 {
		max_tokens = 4096
	}
	temp := opt_float(oai_req.Temperature, 0.2)

	body := map[string]any{
		"model":       model_name,
		"messages":    oai_req.Messages,
		"max_tokens":  max_tokens,
		"temperature": temp,
		"stream":      false,
	}
	if oai_req.ResponseFormat != nil {
		body["response_format"] = oai_req.ResponseFormat
	}
	if strings.TrimSpace(oai_req.Grammar) != "" {
		log.Printf("model_router: dropping grammar field for forwarded openrouter request (local GGUF only)")
	}
	data, _ := json.Marshal(body)

	http_req, err := http.NewRequestWithContext(req.Context(), http.MethodPost,
		engine.OpenRouterChatCompletionsURL(), bytes.NewReader(data))
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	http_req.Header.Set("Authorization", "Bearer "+key)
	http_req.Header.Set("Content-Type", "application/json")
	http_req.Header.Set("HTTP-Referer", "http://localhost:8080")
	http_req.Header.Set("X-Title", "Janus")

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(http_req)
	if err != nil {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, err.Error()) {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": "openrouter: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		err_msg := fmt.Sprintf("openrouter HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, err_msg) {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": err_msg})
		return
	}

	var result oai_chat_response
	if err := json.Unmarshal(raw, &result); err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": "openrouter parse: " + err.Error()})
		return
	}
	if len(result.Choices) == 0 {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, "openrouter: empty choices") {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": "openrouter: empty choices"})
		return
	}

	reply := strings.TrimSpace(result.Choices[0].Message.Content)
	if is_refusal_response(reply) {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, "model refusal") {
			return
		}
	}

	result.ID = req_id
	result.Created = created
	if backend_id != "" {
		result.Model = backend_id
	} else {
		result.Model = "openrouter/" + model_name
	}
	write_json(w, http.StatusOK, result)
}

func (r *router) completions_gemini(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	req_id string,
	created int64,
	model_name string,
	backend_id string,
) {
	if backend_id == "" {
		backend_id = "gemini"
	}

	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" || strings.Contains(strings.ToLower(key), "your_gemini") {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, "GEMINI_API_KEY not set") {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": "gemini: GEMINI_API_KEY not set"})
		return
	}

	if model_name == "" {
		model_name = "gemini-2.0-flash-exp"
	}

	system, turns := split_gemini_messages(oai_req.Messages)
	max_tokens := oai_req.MaxTokens
	if max_tokens <= 0 {
		max_tokens = 4096
	}
	temp := opt_float(oai_req.Temperature, 0.1)

	contents := make([]map[string]any, 0, len(turns))
	for _, m := range turns {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role": role,
			"parts": []map[string]string{
				{"text": m.Content},
			},
		})
	}

	gen_cfg := map[string]any{
		"temperature":     temp,
		"maxOutputTokens": max_tokens,
	}
	body := map[string]any{
		"contents":         contents,
		"generationConfig": gen_cfg,
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": system}},
		}
	}
	data, _ := json.Marshal(body)

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		model_name, key,
	)
	http_req, err := http.NewRequestWithContext(req.Context(), http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	http_req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(http_req)
	if err != nil {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, err.Error()) {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": "gemini: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		err_msg := fmt.Sprintf("gemini HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, err_msg) {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": err_msg})
		return
	}

	text, err := parse_gemini_response(raw)
	if err != nil {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, err.Error()) {
			return
		}
		write_json(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	if is_refusal_response(text) {
		if r.try_fallback(w, req, oai_req, req_id, created, backend_id, "model refusal") {
			return
		}
	}

	model_label := backend_id
	if model_label == "" {
		model_label = "gemini/" + model_name
	}

	write_json(w, http.StatusOK, oai_chat_response{
		ID:      req_id,
		Object:  "chat.completion",
		Created: created,
		Model:   model_label,
		Choices: []oai_choice{{
			Index:        0,
			Message:      oai_message{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
	})
}

func split_gemini_messages(msgs []oai_message) (system string, turns []oai_message) {
	for _, m := range msgs {
		if m.Role == "system" {
			if system != "" {
				system += "\n"
			}
			system += m.Content
			continue
		}
		if m.Role == "user" || m.Role == "assistant" {
			turns = append(turns, m)
		}
	}
	return system, turns
}

func parse_gemini_response(raw []byte) (string, error) {
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("gemini parse: %w", err)
	}
	if result.Error.Message != "" {
		return "", fmt.Errorf("gemini: %s", result.Error.Message)
	}
	if len(result.Candidates) == 0 {
		return "", fmt.Errorf("gemini: empty candidates")
	}
	var sb strings.Builder
	for _, part := range result.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("gemini: empty text in response")
	}
	return text, nil
}

func parse_tried_backends(req *http.Request) map[string]bool {
	tried := map[string]bool{}
	for _, id := range strings.Split(req.Header.Get("X-Janus-Tried-Backends"), ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			tried[id] = true
		}
	}
	return tried
}

func mark_backend_tried(req *http.Request, backend_id string) {
	if backend_id == "" {
		return
	}
	tried := strings.TrimSpace(req.Header.Get("X-Janus-Tried-Backends"))
	if tried != "" {
		tried += ","
	}
	tried += backend_id
	req.Header.Set("X-Janus-Tried-Backends", tried)
}

func is_refusal_response(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	patterns := []string{
		"cannot assist", "can't assist", "cannot help", "can't help",
		"i'm sorry", "i am sorry", "against my", "against our",
		"not able to provide", "unable to provide", "must decline",
		"i must refuse", "cannot provide", "can't provide",
		"against policy", "content policy", "safety guidelines",
		"as an ai", "i cannot support", "won't help", "will not help",
		"not appropriate", "refuse to",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func (r *router) try_fallback(
	w http.ResponseWriter,
	req *http.Request,
	oai_req oai_chat_request,
	req_id string,
	created int64,
	failed_backend_id string,
	reason string,
) bool {
	reload_backend_config(false)
	cfg := get_backend_config()
	task := strings.TrimSpace(req.Header.Get("X-Janus-Task"))
	chain := []string(nil)
	if cfg != nil {
		chain = cfg.FallbackChainForTask(task)
	}
	if len(chain) == 0 {
		return false
	}

	tried := parse_tried_backends(req)
	if failed_backend_id != "" {
		tried[failed_backend_id] = true
	}
	if len(tried) >= maxFallbackAttempts {
		return false
	}

	log.Printf("model_router: %s failed (%s), trying fallback chain (task=%q)", failed_backend_id, truncate(reason, 120), task)

	for _, backend_id := range chain {
		if tried[backend_id] {
			continue
		}
		entry := cfg.BackendByID(backend_id)
		if entry == nil {
			continue
		}
		mark_backend_tried(req, backend_id)
		route := route_from_backend(entry)
		switch route.kind {
		case "openrouter", "gemini", "anthropic":
			if ok, _ := cloud_routing_allowed(); !ok {
				continue
			}
			if route.kind == "openrouter" {
				r.completions_openrouter(w, req, oai_req, req_id, created, route.model, backend_id)
				return true
			}
			if route.kind == "anthropic" {
				r.completions_anthropic(w, req, oai_req, req_id, created, route.model, backend_id)
				return true
			}
			r.completions_gemini(w, req, oai_req, req_id, created, route.model, backend_id)
			return true
		case "local":
			if r.provider != nil {
				if route.local_path != "" {
					if _, err := r.hotSwapLocalModel(route.local_path, modelSwapOptions{Label: backend_id}); err != nil {
						log.Printf("model_router: fallback load %q: %v", route.local_path, err)
						continue
					}
				}
				model_label := route.model
				if model_label == "" {
					model_label = "local"
				}
				r.completions_local(w, req, oai_req, req_id, created, model_label)
				return true
			}
		}
	}
	return false
}

func list_configured_models() []oai_model_entry {
	reload_backend_config(false)
	cfg := get_backend_config()
	now := time.Now().Unix()
	if cfg == nil {
		return nil
	}
	out := make([]oai_model_entry, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		id := b.ID
		switch strings.ToLower(b.Type) {
		case "openrouter":
			if b.DefaultModel != "" {
				id = "openrouter/" + b.DefaultModel
			} else {
				id = "openrouter"
			}
		case "gemini":
			if b.DefaultModel != "" {
				id = "gemini/" + b.DefaultModel
			}
		}
		out = append(out, oai_model_entry{
			ID:      id,
			Object:  "model",
			Created: now,
			OwnedBy: b.Type,
		})
	}
	return out
}

func list_configured_model_entries() []map[string]any {
	reload_backend_config(false)
	cfg := get_backend_config()
	if cfg == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		entry := map[string]any{
			"name":     b.ID,
			"path":     b.ID,
			"backend":  b.Type,
			"label":    b.Name,
			"use_case": b.UseCase,
		}
		if strings.EqualFold(b.Type, "vulkan") || strings.EqualFold(b.Type, "cpu") {
			entry["path"] = b.ModelPath
		}
		out = append(out, entry)
	}
	return out
}

func openrouter_chat(ctx context.Context, model_name string, msgs []oai_message, max_tokens int, temperature float64) (string, error) {
	key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if key == "" {
		return "", fmt.Errorf("OPENROUTER_API_KEY not set")
	}
	if model_name == "" {
		model_name = "anthropic/claude-3.5-sonnet"
	}
	if max_tokens <= 0 {
		max_tokens = 4096
	}
	if temperature == 0 {
		temperature = 0.2
	}

	body := map[string]any{
		"model":       model_name,
		"messages":    msgs,
		"max_tokens":  max_tokens,
		"temperature": temperature,
	}
	data, _ := json.Marshal(body)

	http_req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		engine.OpenRouterChatCompletionsURL(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	http_req.Header.Set("Authorization", "Bearer "+key)
	http_req.Header.Set("Content-Type", "application/json")
	http_req.Header.Set("HTTP-Referer", "http://localhost:8080")
	http_req.Header.Set("X-Title", "Janus")

	resp, err := cloud_http_client().Do(http_req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var result oai_chat_response
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openrouter: empty choices")
	}
	return result.Choices[0].Message.Content, nil
}
