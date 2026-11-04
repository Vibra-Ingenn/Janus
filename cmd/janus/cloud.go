package main

// cloud.go — lightweight HTTP clients for OpenAI and Anthropic cloud APIs.
//
// These are intentionally thin wrappers: no retries, no streaming batching,
// just enough to call the API and return a string.
//
// Cloud callers are only invoked when the request carries X-Janus-Backend: cloud
// and JANUS_LOCAL_ONLY is not enabled, with the relevant API key set.
//
// Never log request bodies at INFO level — they may contain private data.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"janus/pkg/engine"
)

// ---------------------------------------------------------------------------
// Shared interface
// ---------------------------------------------------------------------------

// cloud_caller is the minimal interface both OpenAI and Anthropic clients
// implement. prompt is already formatted; messages are raw chat turns.
type cloud_caller interface {
	// chat sends the message list and returns the assistant response text.
	chat(ctx context.Context, msgs []oai_message, maxTokens int, temperature float64) (string, error)
	// name returns a human-readable backend label for logging.
	name() string
}

// ---------------------------------------------------------------------------
// Request routing
// ---------------------------------------------------------------------------

func cloud_caller_from_req(r *http.Request) (cloud_caller, error) {
	if strings.EqualFold(os.Getenv("JANUS_LOCAL_ONLY"), "true") {
		return nil, nil
	}

	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Janus-Backend")), "cloud") {
		return nil, nil
	}

	provider := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Janus-Cloud-Provider")))
	if provider == "" {
		provider = "openai"
	}

	switch provider {
	case "anthropic":
		key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("cloud: ANTHROPIC_API_KEY not set")
		}
		return &anthropic_client{apiKey: key}, nil
	default: // openai
		key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("cloud: OPENAI_API_KEY not set")
		}
		base := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
		if base == "" {
			base = "https://api.openai.com"
		}
		model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
		if model == "" {
			model = "gpt-4o-mini"
		}
		return &openai_cloud_client{apiKey: key, baseURL: base, model: model}, nil
	}
}

// ---------------------------------------------------------------------------
// OpenAI client
// ---------------------------------------------------------------------------

type openai_cloud_client struct {
	apiKey  string
	baseURL string
	model   string
}

func (c *openai_cloud_client) name() string { return "openai/" + c.model }

func (c *openai_cloud_client) chat(
	ctx context.Context,
	msgs []oai_message,
	maxTokens int,
	temperature float64,
) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	body := map[string]any{
		"model":       c.model,
		"messages":    msgs,
		"max_tokens":  maxTokens,
		"temperature": temperature,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := cloud_http_client().Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var result oai_chat_response
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("openai: parse response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai: empty choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

// ---------------------------------------------------------------------------
// Anthropic client
// ---------------------------------------------------------------------------

type anthropic_client struct {
	apiKey string
}

func (c *anthropic_client) name() string { return "anthropic/claude" }

// anthropic message types (Anthropic uses a different wire format).
type anthropic_req_message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropic_request struct {
	Model     string                  `json:"model"`
	MaxTokens int                     `json:"max_tokens"`
	System    string                  `json:"system,omitempty"`
	Messages  []anthropic_req_message `json:"messages"`
}

type anthropic_content_block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropic_response struct {
	Content []anthropic_content_block `json:"content"`
}

func (c *anthropic_client) chat(
	ctx context.Context,
	msgs []oai_message,
	maxTokens int,
	temperature float64,
) (string, error) {
	return c.chat_with_model(ctx, msgs, maxTokens, temperature, "")
}

// chat_with_model is chat with an explicit Claude model ("" = ANTHROPIC_MODEL, then a default).
func (c *anthropic_client) chat_with_model(
	ctx context.Context,
	msgs []oai_message,
	maxTokens int,
	temperature float64,
	model string,
) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	model = strings.TrimSpace(model)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL"))
	}
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}

	// Split system from user/assistant turns.
	var system string
	var turns []anthropic_req_message
	for _, m := range msgs {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		if m.Role == "user" || m.Role == "assistant" {
			turns = append(turns, anthropic_req_message{Role: m.Role, Content: m.Content})
		}
	}

	body := anthropic_request{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  turns,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		engine.AnthropicMessagesURL(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := cloud_http_client().Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var result anthropic_response
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("anthropic: parse response: %w", err)
	}
	for _, block := range result.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic: no text block in response")
}

// ---------------------------------------------------------------------------
// Shared HTTP client + helpers
// ---------------------------------------------------------------------------

var _cloud_http *http.Client

func cloud_http_client() *http.Client {
	if _cloud_http == nil {
		_cloud_http = &http.Client{Timeout: 120 * time.Second}
	}
	return _cloud_http
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
