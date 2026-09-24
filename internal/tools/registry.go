package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
)

// ToolCall is the parsed JSON tool call emitted by the model.
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
	// Category groups tools for hierarchical routing. The model sees category
	// headers before individual tools, preventing attention collapse with large
	// tool sets. Valid values: filesystem | shell | code | document | medical |
	// security | utility | skills. Defaults to "utility" if empty.
	Category string `json:"category,omitempty"`
}

// AIEngine is the inference engine interface used by tools.
type AIEngine interface {
	Predict(ctx context.Context, prompt string) (string, error)
}

// Registry holds all registered tools and dispatches calls.
type Registry struct {
	mu       sync.RWMutex
	tools    map[string]registeredTool
	builtins map[string]registeredTool // Copy of original built-in implementations
	Engine   AIEngine
}

type registeredTool struct {
	def     ToolDef
	handler ToolFunc
}

// NewRegistry creates a Registry with built-in tools pre-registered.
func NewRegistry() *Registry {
	r := &Registry{
		tools:    make(map[string]registeredTool),
		builtins: make(map[string]registeredTool),
	}
	r.registerBuiltins()
	r.registerSmartTools()
	// Medical tools are private-build only — not included in open-source Janus.
	r.registerUniversalTools()
	for k, v := range r.tools {
		r.builtins[k] = v
	}
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
	sb.WriteString("\nRules: valid JSON only. Use double quotes. Do not use semicolons, comments, markdown, or trailing commas.")
	sb.WriteString("\n\nAvailable tools:\n")
	for _, d := range defs {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", d.Name, d.Description))
		for pName, pDesc := range d.Parameters {
			sb.WriteString(fmt.Sprintf("    %s: %s\n", pName, pDesc))
		}
	}
	return sb.String()
}

// ParseToolCall extracts a ToolCall from raw model output. It first tries strict
// JSON, then retries with extracted/sanitized candidates for common local-model
// mistakes such as prose wrappers, semicolon separators, and trailing junk.
func ParseToolCall(raw string) (ToolCall, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ToolCall{}, fmt.Errorf("tools: parse tool_call: empty model output")
	}

	candidates := []string{raw}
	if extracted := extractToolCallJSON(raw); extracted != "" {
		candidates = append(candidates, extracted)
	} else if partial := extractPartialToolCallJSON(raw); partial != "" {
		candidates = append(candidates, partial)
	}

	var firstErr error
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		for _, attempt := range []string{candidate, sanitizeToolCallJSON(candidate)} {
			attempt = strings.TrimSpace(attempt)
			if attempt == "" || seen[attempt] {
				continue
			}
			seen[attempt] = true
			tc, err := strictParseToolCall(attempt)
			if err == nil {
				return tc, nil
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	if firstErr == nil {
		firstErr = fmt.Errorf("no tool_call object found")
	}
	return ToolCall{}, fmt.Errorf("tools: parse tool_call: %w", firstErr)
}

func strictParseToolCall(raw string) (ToolCall, error) {
	var wrapper struct {
		TC ToolCall `json:"tool_call"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return ToolCall{}, err
	}
	if wrapper.TC.Name == "" {
		return ToolCall{}, fmt.Errorf("empty tool name")
	}
	return wrapper.TC, nil
}

// extractToolCallJSON finds the first complete {"tool_call":...} JSON object
// in raw by scanning for the opening brace and counting depth.
func extractToolCallJSON(raw string) string {
	idx := findToolCallStart(raw)
	if idx < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := idx; i < len(raw); i++ {
		c := raw[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[idx : i+1]
			}
		}
	}
	return ""
}

func extractPartialToolCallJSON(raw string) string {
	idx := findToolCallStart(raw)
	if idx < 0 {
		return ""
	}
	return raw[idx:]
}

func findToolCallStart(raw string) int {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '{' {
			continue
		}
		rest := strings.TrimLeft(raw[i+1:], " \t\r\n")
		if strings.HasPrefix(rest, `"tool_call"`) {
			return i
		}
	}
	return -1
}

func sanitizeToolCallJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = trimAfterBalancedObject(raw)
	raw = normalizeSemicolons(raw)
	raw = stripTrailingCommas(raw)
	raw = closeMissingDelimiters(raw)
	raw = trimAfterBalancedObject(raw)
	return strings.TrimSpace(raw)
}

func trimAfterBalancedObject(raw string) string {
	depth := 0
	inString := false
	escape := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[:i+1]
			}
		}
	}
	return raw
}

func normalizeSemicolons(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))
	inString := false
	escape := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if escape {
			escape = false
			sb.WriteByte(c)
			continue
		}
		if c == '\\' && inString {
			escape = true
			sb.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = !inString
			sb.WriteByte(c)
			continue
		}
		if c == ';' && !inString {
			next := nextNonSpace(raw, i+1)
			if next == '}' || next == ']' || next == 0 {
				continue
			}
			sb.WriteByte(',')
			continue
		}
		if c == '.' && !inString {
			next := nextNonSpace(raw, i+1)
			if next >= '0' && next <= '9' {
				sb.WriteByte(c)
				continue
			}
			if next == '}' || next == ']' || next == 0 {
				continue
			}
			sb.WriteByte(',')
			continue
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

func stripTrailingCommas(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))
	inString := false
	escape := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if escape {
			escape = false
			sb.WriteByte(c)
			continue
		}
		if c == '\\' && inString {
			escape = true
			sb.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = !inString
			sb.WriteByte(c)
			continue
		}
		if c == ',' && !inString {
			next := nextNonSpace(raw, i+1)
			if next == '}' || next == ']' {
				continue
			}
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

func closeMissingDelimiters(raw string) string {
	var closers []byte
	inString := false
	escape := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			closers = append(closers, '}')
		case '[':
			closers = append(closers, ']')
		case '}', ']':
			if len(closers) > 0 && closers[len(closers)-1] == c {
				closers = closers[:len(closers)-1]
			}
		}
	}
	if len(closers) == 0 && !inString {
		return raw
	}
	var sb strings.Builder
	sb.Grow(len(raw) + len(closers) + 1)
	sb.WriteString(raw)
	if inString {
		sb.WriteByte('"')
	}
	for i := len(closers) - 1; i >= 0; i-- {
		sb.WriteByte(closers[i])
	}
	return sb.String()
}

func nextNonSpace(raw string, start int) byte {
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return raw[i]
		}
	}
	return 0
}

// categoryOrder defines the display order of tool categories.
var categoryOrder = []string{"filesystem", "shell", "code", "document", "security", "utility", "skills"}

// categoryLabel maps category keys to human-readable headers.
var categoryLabel = map[string]string{
	"filesystem": "FILESYSTEM — file and directory operations",
	"shell":      "SHELL — run commands and scripts",
	"code":       "CODE — scaffolding and code editing",
	"document":   "DOCUMENT — PDF, DOCX, OCR, rendering",
	"security":   "SECURITY — encryption, anonymization, audit",
	"utility":    "UTILITY — math, time, task control",
	"skills":     "SKILLS — custom installed tools",
}

// GroupedSystemPromptBlock returns a tool list grouped by category.
// This prevents attention collapse when there are 20+ tools by giving the
// model clear category headers to navigate before picking a specific tool.
func (r *Registry) GroupedSystemPromptBlock() string {
	defs := r.Definitions()
	if len(defs) == 0 {
		return ""
	}

	// Group by category; default to "utility" when unset.
	groups := make(map[string][]ToolDef)
	for _, d := range defs {
		cat := d.Category
		if cat == "" {
			cat = "utility"
		}
		groups[cat] = append(groups[cat], d)
	}

	var sb strings.Builder
	sb.WriteString("You have access to the following tools, grouped by category.\n")
	sb.WriteString("Identify the relevant category first, then select the specific tool.\n")
	sb.WriteString("Output ONLY valid JSON matching this exact schema (no prose, no markdown):\n")
	sb.WriteString(`{"tool_call": {"name": "<tool_name>", "arguments": {<args>}}}`)
	sb.WriteString("\nRules: double quotes only; no semicolons, comments, markdown fences, or trailing commas.\n")
	sb.WriteString("\n\n")

	for _, cat := range categoryOrder {
		tools, ok := groups[cat]
		if !ok {
			continue
		}
		label := categoryLabel[cat]
		if label == "" {
			label = strings.ToUpper(cat)
		}
		sb.WriteString(fmt.Sprintf("[%s]\n", label))
		for _, d := range tools {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", d.Name, d.Description))
			for pName, pDesc := range d.Parameters {
				sb.WriteString(fmt.Sprintf("      %s: %s\n", pName, pDesc))
			}
		}
		sb.WriteString("\n")
	}

	// Emit any unknown categories not in categoryOrder.
	knownCats := make(map[string]bool)
	for _, c := range categoryOrder {
		knownCats[c] = true
	}
	for cat, tools := range groups {
		if knownCats[cat] {
			continue
		}
		sb.WriteString(fmt.Sprintf("[%s]\n", strings.ToUpper(cat)))
		for _, d := range tools {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", d.Name, d.Description))
			for pName, pDesc := range d.Parameters {
				sb.WriteString(fmt.Sprintf("      %s: %s\n", pName, pDesc))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func (r *Registry) Dispatch(tc ToolCall) ToolResult {
	tc = r.repairToolCall(tc)

	r.mu.RLock()
	t, ok := r.tools[tc.Name]
	r.mu.RUnlock()
	if !ok {
		log.Printf("\n==================================================\n🛑 WHAT BROKE: Tool Execution Failed\n📍 WHERE IT BROKE: Tool Registry (%s)\n❓ WHY IT BROKE: Unknown or unregistered tool requested\n💡 WHAT TO DO: Auto-rerouted or register skill in internal/protocols\n==================================================\n", tc.Name)
		return ToolResult{
			ToolName: tc.Name,
			Success:  false,
			Error:    fmt.Sprintf("unknown tool: %q", tc.Name),
		}
	}
	res := t.handler(tc.Arguments)
	if !res.Success {
		log.Printf("\n==================================================\n🛑 WHAT BROKE: Tool [%s] Failed\n📍 WHERE IT BROKE: internal/tools (%s)\n❓ WHY IT BROKE: %s\n==================================================\n", tc.Name, tc.Name, res.Error)
	} else {
		log.Printf("tool [%s] executed OK (output_len=%d)", tc.Name, len(res.Output))
	}
	return res
}

// DispatchBuiltin dispatches a tool call directly to the original built-in handler.
func (r *Registry) DispatchBuiltin(tc ToolCall) ToolResult {
	tc = r.repairToolCall(tc)

	r.mu.RLock()
	t, ok := r.builtins[tc.Name]
	r.mu.RUnlock()
	if !ok {
		return ToolResult{
			ToolName: tc.Name,
			Success:  false,
			Error:    fmt.Sprintf("unknown built-in tool: %q", tc.Name),
		}
	}
	return t.handler(tc.Arguments)
}

func (r *Registry) repairToolCall(tc ToolCall) ToolCall {
	if tc.Arguments == nil {
		tc.Arguments = make(map[string]any)
	}
	normalizePathAliases(tc.Arguments)

	return tc
}

func normalizePathAliases(args map[string]any) {
	if firstStringArg(args, "path") != "" {
		return
	}
	for _, alias := range []string{"file_path", "document_path", "file", "filepath"} {
		if v := firstStringArg(args, alias); v != "" {
			args["path"] = v
			return
		}
	}
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

