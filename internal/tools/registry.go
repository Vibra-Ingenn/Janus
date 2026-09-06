package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// ToolCall is the parsed output from a grammar-constrained generation.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolResult holds the output from executing a tool.
type ToolResult struct {
	ToolName string `json:"tool_name"`
	Success  bool   `json:"success"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// ToolFunc is the handler signature for a registered tool.
type ToolFunc func(args map[string]any) ToolResult

// ToolDef describes a tool available to the model.
type ToolDef struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]string `json:"parameters"` // param_name -> description
}

// Registry holds all registered tools and dispatches calls.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]registeredTool
}

type registeredTool struct {
	def     ToolDef
	handler ToolFunc
}

// NewRegistry creates a Registry with built-in tools pre-registered.
func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]registeredTool)}
	r.registerBuiltins()
	r.registerSmartTools()
	r.registerUniversalTools()
	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(def ToolDef, handler ToolFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[def.Name] = registeredTool{def: def, handler: handler}
}

// Definitions returns all tool definitions for injection into system prompts.
func (r *Registry) Definitions() []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.def)
	}
	return defs
}

// SystemPromptBlock returns a formatted string of tool definitions
// suitable for injection into the system prompt.
func (r *Registry) SystemPromptBlock() string {
	defs := r.Definitions()
	if len(defs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("You have access to the following tools. To use a tool, output ONLY a JSON object matching this exact schema:\n")
	sb.WriteString(`{"tool_call": {"name": "<tool_name>", "arguments": {<args>}}}`)
	sb.WriteString("\n\nAvailable tools:\n")
	for _, d := range defs {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", d.Name, d.Description))
		for pName, pDesc := range d.Parameters {
			sb.WriteString(fmt.Sprintf("    %s: %s\n", pName, pDesc))
		}
	}
	return sb.String()
}

// ParseToolCall extracts a ToolCall from raw model output.
// The output should be grammar-constrained JSON.
func ParseToolCall(raw string) (ToolCall, error) {
	raw = strings.TrimSpace(raw)
	var wrapper struct {
		TC ToolCall `json:"tool_call"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return ToolCall{}, fmt.Errorf("tools: parse tool_call: %w", err)
	}
	if wrapper.TC.Name == "" {
		return ToolCall{}, fmt.Errorf("tools: empty tool name")
	}
	return wrapper.TC, nil
}

// Dispatch executes the named tool with the given arguments.
func (r *Registry) Dispatch(tc ToolCall) ToolResult {
	r.mu.RLock()
	t, ok := r.tools[tc.Name]
	r.mu.RUnlock()
	if !ok {
		return ToolResult{
			ToolName: tc.Name,
			Success:  false,
			Error:    fmt.Sprintf("unknown tool: %q", tc.Name),
		}
	}
	return t.handler(tc.Arguments)
}
