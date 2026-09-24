package kernel

import (
	"context"
	"strings"
	"testing"

	"janus/internal/tools"
)

// mockEngine satisfies engine.Provider without a real LLM.
type mockEngine struct {
	idx       int
	responses []string // consumed in order; last entry is repeated
	err       error
}

func (m *mockEngine) LoadModel(path string) error { return nil }
func (m *mockEngine) Tokenize(text string) ([]int32, error) {
	return nil, nil
}
func (m *mockEngine) Generate(ctx context.Context, tokens []int32) (<-chan string, error) {
	ch := make(chan string)
	close(ch)
	return ch, nil
}
func (m *mockEngine) Unload()         {}
func (m *mockEngine) Backend() string { return "mock" }
func (m *mockEngine) Predict(ctx context.Context, prompt string) (string, error) {
	return m.next(), m.err
}
func (m *mockEngine) next() string {
	if len(m.responses) == 0 {
		return ""
	}
	i := m.idx
	if i >= len(m.responses) {
		i = len(m.responses) - 1
	}
	m.idx++
	return m.responses[i]
}

// doneJSON is a valid "done" tool call.
const doneJSON = `{"tool_call":{"name":"done","arguments":{"summary":"task complete"}}}`

// listFilesJSON is a valid "list_files" tool call that always succeeds.
const listFilesJSON = `{"tool_call":{"name":"list_files","arguments":{"path":"."}}}`

// calculateJSON is a valid "calculate" tool call.
const calculateJSON = `{"tool_call":{"name":"calculate","arguments":{"expression":"2 + 2"}}}`

func newKernel(eng *mockEngine) *Kernel {
	reg := tools.NewRegistry()
	return New(eng, reg, nil)
}

// ─── helper / pure-function tests ────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello…"},
		{"", 5, ""},
		{"ab", 2, "ab"},
		{"abc", 2, "ab…"},
	}
	for _, c := range cases {
		if got := truncate(c.s, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestRepeatedFailuresNone(t *testing.T) {
	if n := repeatedFailures(nil, "foo"); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestRepeatedFailuresCounts(t *testing.T) {
	history := []ToolLogEntry{
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
	}
	if n := repeatedFailures(history, "foo"); n != 3 {
		t.Fatalf("expected 3, got %d", n)
	}
}

func TestRepeatedFailuresResetsOnSuccess(t *testing.T) {
	history := []ToolLogEntry{
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: true}},
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
	}
	if n := repeatedFailures(history, "foo"); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

func TestRepeatedFailuresSkipsLoopGuard(t *testing.T) {
	history := []ToolLogEntry{
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
		{Call: tools.ToolCall{Name: "_loop_guard"}, Result: tools.ToolResult{Success: false}},
		{Call: tools.ToolCall{Name: "foo"}, Result: tools.ToolResult{Success: false}},
	}
	if n := repeatedFailures(history, "foo"); n != 2 {
		t.Fatalf("expected 2 (loop_guard skipped), got %d", n)
	}
}

func TestRepeatedIdenticalCalls(t *testing.T) {
	args1 := map[string]any{"path": "test.pdf", "language": "eng"}
	args2 := map[string]any{"path": "test.pdf", "language": "spa"}

	history := []ToolLogEntry{
		{Call: tools.ToolCall{Name: "ocr_extract", Arguments: args1}, Result: tools.ToolResult{Success: true}},
		{Call: tools.ToolCall{Name: "ocr_extract", Arguments: args1}, Result: tools.ToolResult{Success: true}},
		{Call: tools.ToolCall{Name: "ocr_extract", Arguments: args1}, Result: tools.ToolResult{Success: true}},
	}

	if n := repeatedIdenticalCalls(history, "ocr_extract", args1); n != 3 {
		t.Fatalf("expected 3 identical calls, got %d", n)
	}

	if n := repeatedIdenticalCalls(history, "ocr_extract", args2); n != 0 {
		t.Fatalf("expected 0 identical calls for args2, got %d", n)
	}
}

func TestRepeatedIdenticalCallsWithInterruption(t *testing.T) {
	args1 := map[string]any{"path": "test.pdf", "language": "eng"}
	args2 := map[string]any{"path": "test.pdf", "language": "spa"}

	history := []ToolLogEntry{
		{Call: tools.ToolCall{Name: "ocr_extract", Arguments: args1}, Result: tools.ToolResult{Success: true}},
		{Call: tools.ToolCall{Name: "ocr_extract", Arguments: args2}, Result: tools.ToolResult{Success: true}},
		{Call: tools.ToolCall{Name: "ocr_extract", Arguments: args1}, Result: tools.ToolResult{Success: true}},
	}

	if n := repeatedIdenticalCalls(history, "ocr_extract", args1); n != 1 {
		t.Fatalf("expected 1 identical call due to interruption, got %d", n)
	}
}

func TestFormatPromptContainsChatML(t *testing.T) {
	prompt := formatPrompt("SYSTEM", "USER_TASK", nil)
	for _, want := range []string{
		"<|im_start|>system", "SYSTEM",
		"<|im_start|>user", "USER_TASK",
		"<|im_start|>assistant",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("formatPrompt missing %q", want)
		}
	}
}

func TestFormatPromptReplayHistory(t *testing.T) {
	history := []ToolLogEntry{
		{
			Iteration: 1,
			Call:      tools.ToolCall{Name: "list_files", Arguments: map[string]any{"path": "."}},
			Result:    tools.ToolResult{ToolName: "list_files", Success: true, Output: "loop.go"},
		},
	}
	prompt := formatPrompt("SYS", "TASK", history)
	if !strings.Contains(prompt, "list_files") {
		t.Error("history not replayed in prompt")
	}
	if !strings.Contains(prompt, "loop.go") {
		t.Error("tool output not replayed in prompt")
	}
}

func TestFormatPromptFormatWarnings(t *testing.T) {
	history := []ToolLogEntry{
		{
			Iteration: 1,
			Call:      tools.ToolCall{Name: "_loop_guard"},
			Result:    tools.ToolResult{ToolName: "_loop_guard", Success: false, Error: "repeated call warning"},
		},
		{
			Iteration: 2,
			Call:      tools.ToolCall{Name: "_parse_error"},
			Result:    tools.ToolResult{ToolName: "_parse_error", Success: false, Error: "json error", Output: "raw output block"},
		},
	}
	prompt := formatPrompt("SYS", "TASK", history)
	if strings.Contains(prompt, "_loop_guard") {
		t.Error("should not format _loop_guard in assistant tool call")
	}
	if strings.Contains(prompt, "_parse_error") {
		t.Error("should not format _parse_error in assistant tool call")
	}
	if !strings.Contains(prompt, "SYSTEM WARNING: repeated call warning") {
		t.Error("loop guard warning missing from system prompt")
	}
	if !strings.Contains(prompt, "SYSTEM WARNING: json error") || !strings.Contains(prompt, "raw output block") {
		t.Error("parse error info missing from system prompt")
	}
}

// ─── Kernel struct tests ──────────────────────────────────────────────────────

func TestKernelNewAndStatus(t *testing.T) {
	k := newKernel(&mockEngine{responses: []string{doneJSON}})
	if k == nil {
		t.Fatal("New returned nil")
	}
	if k.Status("nonexistent") != nil {
		t.Error("Status should return nil for unknown task")
	}
}

func TestKernelRunComplete(t *testing.T) {
	eng := &mockEngine{responses: []string{doneJSON}}
	k := newKernel(eng)

	result := k.Run(context.Background(), "task-1", "say hello", nil)

	if result.Status != "complete" {
		t.Fatalf("expected complete, got %q (summary: %s)", result.Status, result.Summary)
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
	if result.Verification == nil || result.Verification.Status != "valid" {
		t.Fatalf("expected valid verification, got %#v", result.Verification)
	}
}

func TestKernelRunStoresResult(t *testing.T) {
	eng := &mockEngine{responses: []string{doneJSON}}
	k := newKernel(eng)
	k.Run(context.Background(), "task-store", "hello", nil)

	if k.Status("task-store") == nil {
		t.Error("Status should return result after Run")
	}
}

func TestKernelRunCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run

	eng := &mockEngine{responses: []string{doneJSON}}
	k := newKernel(eng)
	result := k.Run(ctx, "task-cancel", "work", nil)

	if result.Status != "failed" {
		t.Fatalf("expected failed for cancelled context, got %q", result.Status)
	}
	if !strings.Contains(result.Summary, "cancel") {
		t.Errorf("summary should mention cancel, got %q", result.Summary)
	}
}

func TestKernelRunMaxIterations(t *testing.T) {
	// Always return a successful list_files call — never calls done.
	eng := &mockEngine{responses: []string{listFilesJSON}}
	k := newKernel(eng)

	result := k.Run(context.Background(), "task-max", "work forever", nil)

	if result.Status != "max_iterations" {
		t.Fatalf("expected max_iterations, got %q", result.Status)
	}
	if result.Iterations != MaxIterations {
		t.Errorf("expected %d iterations, got %d", MaxIterations, result.Iterations)
	}
}

func TestKernelRunParseError(t *testing.T) {
	// First response is unparseable JSON; second is a valid done call.
	eng := &mockEngine{responses: []string{"not valid json at all", doneJSON}}
	k := newKernel(eng)

	result := k.Run(context.Background(), "task-parse", "work", nil)

	// Should eventually complete after parse error recovery.
	if result.Status != "complete" {
		t.Fatalf("expected complete after recovery, got %q", result.Status)
	}
	// There should be a _parse_error synthetic entry in the log.
	found := false
	for _, e := range result.ToolLog {
		if e.Call.Name == "_parse_error" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected _parse_error entry in tool log")
	}
}

func TestKernelVerifierRequiresToolEvidence(t *testing.T) {
	eng := &mockEngine{responses: []string{doneJSON}}
	k := newKernel(eng)

	result := k.Run(context.Background(), "task-missing-tool", "Calculate 2+2 and call done.", nil)

	if result.Status != "complete" {
		t.Fatalf("expected complete, got %q", result.Status)
	}
	if result.Verification == nil {
		t.Fatal("expected verification result")
	}
	if result.Verification.Status != "ambiguous" {
		t.Fatalf("expected ambiguous verification, got %#v", result.Verification)
	}
	found := false
	for _, issue := range result.Verification.Issues {
		if issue == "missing_required_tool:calculate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing calculate issue, got %#v", result.Verification.Issues)
	}
}

func TestKernelVerifierAcceptsToolEvidence(t *testing.T) {
	eng := &mockEngine{responses: []string{calculateJSON, doneJSON}}
	k := newKernel(eng)

	result := k.Run(context.Background(), "task-calc", "Calculate 2+2 and call done.", nil)

	if result.Status != "complete" {
		t.Fatalf("expected complete, got %q", result.Status)
	}
	if result.Verification == nil || result.Verification.Status != "valid" {
		t.Fatalf("expected valid verification, got %#v", result.Verification)
	}
}

func TestKernelVerifierEscalatesRepeatedParseErrors(t *testing.T) {
	// Each response contains "tool_call" (so the forced-prefix nudge is skipped)
	// but is malformed JSON, producing three consecutive parse errors.
	eng := &mockEngine{responses: []string{"tool_call not json", "tool_call still bad", "tool_call again bad", doneJSON}}
	k := newKernel(eng)

	result := k.Run(context.Background(), "task-parse-escalate", "work", nil)

	if result.Status != "complete" {
		t.Fatalf("expected complete after recovery, got %q", result.Status)
	}
	if result.Verification == nil {
		t.Fatal("expected verification result")
	}
	if !result.Verification.EscalationRecommended {
		t.Fatalf("expected escalation recommendation, got %#v", result.Verification)
	}
	if result.Verification.Status != "ambiguous" {
		t.Fatalf("expected ambiguous verification, got %#v", result.Verification)
	}
}

func TestKernelRunLoopGuard(t *testing.T) {
	// First 3 calls fail (run_command with bad cmd), then done.
	failCall := `{"tool_call":{"name":"run_command","arguments":{"command":"__nonexistent_janus_test_cmd__"}}}`
	eng := &mockEngine{responses: []string{failCall, failCall, failCall, doneJSON}}
	k := newKernel(eng)

	result := k.Run(context.Background(), "task-loop", "work", nil)

	if result.Status != "complete" {
		t.Fatalf("expected complete, got %q", result.Status)
	}
	found := false
	for _, e := range result.ToolLog {
		if e.Call.Name == "_loop_guard" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected _loop_guard entry after 3 consecutive failures")
	}
}

func TestKernelRunProgressCallback(t *testing.T) {
	eng := &mockEngine{responses: []string{listFilesJSON, doneJSON}}
	k := newKernel(eng)

	called := 0
	k.Run(context.Background(), "task-progress", "work", func(iter int, toolName string, _ tools.ToolResult) {
		called++
		if iter < 1 {
			t.Errorf("iter should be >= 1, got %d", iter)
		}
		if toolName == "" {
			t.Error("toolName should not be empty")
		}
	})

	if called == 0 {
		t.Error("progress callback was never called")
	}
}

func TestKernelRunDuration(t *testing.T) {
	eng := &mockEngine{responses: []string{doneJSON}}
	k := newKernel(eng)
	result := k.Run(context.Background(), "task-dur", "hello", nil)

	if result.Duration < 0 {
		t.Error("duration should be non-negative")
	}
}

func TestKernelSystemPromptContainsTools(t *testing.T) {
	k := newKernel(&mockEngine{})
	sp := k.systemPrompt()
	if !strings.Contains(sp, "done") {
		t.Error("system prompt should contain tool list")
	}
	if !strings.Contains(strings.ToLower(sp), "janus") {
		t.Error("system prompt should mention Janus")
	}
}

