package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type OpenRouterBackend struct {
	apiKey  string
	model   string
	mu      sync.Mutex
	prompts map[*int32]string
}

func NewOpenRouterBackend() (*OpenRouterBackend, error) {
	key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if key == "" {
		return nil, errors.New("OPENROUTER_API_KEY is not set")
	}
	model := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
	if model == "" {
		model = "openai/gpt-4o-mini"
	}
	return &OpenRouterBackend{
		apiKey:  key,
		model:   model,
		prompts: make(map[*int32]string),
	}, nil
}

func (b *OpenRouterBackend) LoadModel(path string) error {
	// Models are hosted in the cloud, so nothing to load locally.
	return nil
}

func (b *OpenRouterBackend) Tokenize(text string) ([]int32, error) {
	count := len(text) / 4
	if count == 0 && len(text) > 0 {
		count = 1
	}
	tokens := make([]int32, count)

	if len(tokens) > 0 {
		b.mu.Lock()
		if b.prompts == nil {
			b.prompts = make(map[*int32]string)
		}
		b.prompts[&tokens[0]] = text
		b.mu.Unlock()
	}

	return tokens, nil
}

func (b *OpenRouterBackend) Generate(ctx context.Context, tokens []int32) (<-chan string, error) {
	var prompt string
	if len(tokens) > 0 {
		b.mu.Lock()
		prompt = b.prompts[&tokens[0]]
		delete(b.prompts, &tokens[0])
		b.mu.Unlock()
	}

	if prompt == "" {
		outChan := make(chan string)
		close(outChan)
		return outChan, nil
	}

	reqBody := map[string]any{
		"model": b.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream": true,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openrouter stream: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OpenRouterChatCompletionsURL(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "http://localhost:8080")
	req.Header.Set("X-Title", "Janus IDE")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter stream request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openrouter stream HTTP %d: %s", resp.StatusCode, string(raw))
	}

	outChan := make(chan string, 100)

	go func() {
		defer resp.Body.Close()
		defer close(outChan)

		reader := bufio.NewReader(resp.Body)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			if line == "data: [DONE]" {
				return
			}

			if strings.HasPrefix(line, "data: ") {
				payload := strings.TrimPrefix(line, "data: ")
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					continue
				}
				if len(chunk.Choices) > 0 {
					content := chunk.Choices[0].Delta.Content
					if content != "" {
						select {
						case outChan <- content:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}
	}()

	return outChan, nil
}

func (b *OpenRouterBackend) Predict(ctx context.Context, prompt string) (string, error) {
	// OpenRouter expects Chat Completion format.
	// We'll wrap the raw prompt in a single user message.
	reqBody := map[string]any{
		"model": b.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	data, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OpenRouterChatCompletionsURL(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "http://localhost:8080") // Recommended by OpenRouter
	req.Header.Set("X-Title", "Janus IDE")                  // Recommended by OpenRouter

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", errors.New("openrouter: empty choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

func (b *OpenRouterBackend) Unload() {}

func (b *OpenRouterBackend) Backend() string {
	return "openrouter"
}

