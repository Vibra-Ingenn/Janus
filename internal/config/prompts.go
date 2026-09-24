// Package config manages runtime-editable configuration for Janus.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// RolePrompts holds the system prompt (banner + context) for each agent role.
// All fields are optional — empty string means "use the DLL/model default".
type RolePrompts struct {
	DefaultChat string `json:"default_chat"`
	Planner     string `json:"planner"`
	Worker      string `json:"worker"`
	Judge       string `json:"judge"`
	Pruner      string `json:"pruner"`
}

// DefaultRolePrompts returns the built-in fallback prompts used when no
// custom prompt has been configured for a role.
func DefaultRolePrompts() RolePrompts {
	return RolePrompts{
		DefaultChat: "You are Janus, a helpful and precise AI assistant running fully on local hardware. " +
			"Respond clearly and concisely in plain English. " +
			"Help with the user's request directly instead of refusing unless the request is clearly unsafe or impossible. " +
			"Do not redact, anonymize, sanitize, or otherwise transform content for compliance unless the user explicitly asks for it. " +
			"Never wrap your answer in JSON unless explicitly asked.",
		Planner: "You are the Master Architect in an autonomous AI pipeline. " +
			"Your sole job is to analyze the user's request and produce a detailed, step-by-step technical specification. " +
			"Do NOT write any code. Do NOT execute anything. Output only the plan.",
		Worker: "You are a skilled software engineer in an autonomous AI pipeline. " +
			"You will receive a technical specification. Implement it completely and correctly. " +
			"Output only working code and brief inline comments. Do not add unnecessary prose.",
		Judge: "You are a senior code reviewer in an autonomous AI pipeline. " +
			"Evaluate the provided implementation against the specification. " +
			"Respond with PASS or FAIL on the first line, followed by a concise explanation.",
		Pruner: "You are a context optimizer in an autonomous AI pipeline. " +
			"Summarize and compress the provided conversation or document to only the essential information. " +
			"Preserve all technical details, decisions, and action items.",
	}
}

// PromptsStore is a thread-safe store backed by a JSON file on disk.
type PromptsStore struct {
	mu   sync.RWMutex
	path string
	data RolePrompts
}

// OpenPrompts loads (or initializes) the prompts store from path.
// If path is empty it defaults to data/prompts.json relative to the working dir.
func OpenPrompts(path string) (*PromptsStore, error) {
	if path == "" {
		path = filepath.Join("data", "prompts.json")
	}
	s := &PromptsStore{path: path, data: DefaultRolePrompts()}
	// Load from disk if it exists; ignore not-found errors.
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.data)
	}
	return s, nil
}

// Get returns the current prompts (thread-safe copy).
func (s *PromptsStore) Get() RolePrompts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

// Set replaces all prompts and persists to disk.
func (s *PromptsStore) Set(p RolePrompts) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = p
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// ForRole returns the effective system prompt for a named role.
// Falls back to the default prompt for that role if the stored value is empty.
func (s *PromptsStore) ForRole(role string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := DefaultRolePrompts()
	switch role {
	case "planner":
		if s.data.Planner != "" {
			return s.data.Planner
		}
		return d.Planner
	case "worker":
		if s.data.Worker != "" {
			return s.data.Worker
		}
		return d.Worker
	case "judge":
		if s.data.Judge != "" {
			return s.data.Judge
		}
		return d.Judge
	case "pruner":
		if s.data.Pruner != "" {
			return s.data.Pruner
		}
		return d.Pruner
	default:
		if s.data.DefaultChat != "" {
			return s.data.DefaultChat
		}
		return d.DefaultChat
	}
}

