package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type ollama_client struct {
	base_url    string
	http_client *http.Client
}

func new_ollama_client(base_url string) *ollama_client {
	base_url = strings.TrimRight(base_url, "/")
	if base_url == "" {
		base_url = "http://127.0.0.1:11434"
	}

	return &ollama_client{
		base_url: base_url,
		http_client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout: 5 * time.Second,
				}).DialContext,
			},
		},
	}
}

type ollama_message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollama_chat_request struct {
	Model    string           `json:"model"`
	Messages []ollama_message `json:"messages"`
	Stream   bool             `json:"stream"`
	Format   string           `json:"format,omitempty"`
	Options  map[string]any   `json:"options,omitempty"`
}

type ollama_chat_response struct {
	Model   string         `json:"model"`
	Message ollama_message `json:"message"`
	Done    bool           `json:"done"`
	Error   string         `json:"error,omitempty"`
}

type ollama_tags_response struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func (c *ollama_client) chat(ctx context.Context, req ollama_chat_request) (ollama_chat_response, error) {
	if c == nil {
		return ollama_chat_response{}, errors.New("nil ollama client")
	}
	if req.Model == "" {
		return ollama_chat_response{}, errors.New("ollama model is required")
	}

	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return ollama_chat_response{}, err
	}

	http_req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base_url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return ollama_chat_response{}, err
	}
	http_req.Header.Set("content-type", "application/json")

	resp, err := c.http_client.Do(http_req)
	if err != nil {
		return ollama_chat_response{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return ollama_chat_response{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ollama_chat_response{}, fmt.Errorf("ollama http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out ollama_chat_response
	if err := json.Unmarshal(raw, &out); err != nil {
		return ollama_chat_response{}, fmt.Errorf("ollama invalid json: %w", err)
	}
	if out.Error != "" {
		return ollama_chat_response{}, errors.New(out.Error)
	}

	return out, nil
}

func (c *ollama_client) tags(ctx context.Context) ([]string, error) {
	if c == nil {
		return nil, errors.New("nil ollama client")
	}

	http_req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base_url+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http_client.Do(http_req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("ollama http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out ollama_tags_response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ollama invalid json: %w", err)
	}

	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		if strings.TrimSpace(m.Name) != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

