// Package kernel implements the single-brain ReAct loop for Janus.
//
// Architecture: One LLM stays loaded. It thinks, emits a tool_call JSON object,
// observes results, and repeats until done. No agent handoffs, no model
// swapping. The AI is the Operator; the Go tools are the System Calls.
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"janus/internal/agent"
	"janus/internal/engine"
	"janus/internal/memory"
	"janus/internal/tools"
	"janus/internal/validation"
)

// MaxIterations is the hard ceiling on tool-call loops per task.
const MaxIterations = 30

// ProgressFunc is called after each iteration so the UI can show live updates.
type ProgressFunc func(iter int, toolName string, result tools.ToolResult)

// TaskResult is the final output of a kernel run.
type TaskResult struct {
	TaskID       string              `json:"task_id"`
	Input        string              `json:"input"`
	Status       string              `json:"status"` // "complete" | "failed" | "max_iterations"
	Summary      string              `json:"summary"`
	Iterations   int                 `json:"iterations"`
	ToolLog      []ToolLogEntry      `json:"tool_log"`
	Duration     float64             `json:"duration_seconds"`
	Verification *VerificationResult `json:"verification,omitempty"`
}

// ToolLogEntry records one tool invocation in the task history.
type ToolLogEntry struct {
	Iteration int              `json:"iteration"`
	Call      tools.ToolCall   `json:"call"`
	Result    tools.ToolResult `json:"result"`
}

// VerificationResult is the deterministic reliability layer applied after a
// kernel run. It makes ambiguity explicit instead of letting it masquerade as
// a successful execution.
type VerificationResult struct {
	Status                string   `json:"status"` // "valid" | "ambiguous" | "invalid"
	Issues                []string `json:"issues,omitempty"`
	RequiredAction        string   `json:"required_action,omitempty"`
	RequiresReview        bool     `json:"requires_review"`
	EscalationRecommended bool     `json:"escalation_recommended"`
	EscalationReason      string   `json:"escalation_reason,omitempty"`
}

// Kernel is the single-brain operator loop.
type Kernel struct {
	engine        engine.Provider
	registry      *tools.Registry
	memory        *agent.AgentMemory
	mem           *memory.DB // persistent human-layer memory (facts + project brief)
	executionMode string

	mu    sync.Mutex
	tasks map[string]*TaskResult
}

// New creates a Kernel wired to the shared engine and tool registry.
// mem may be nil — if provided, the human's project brief and remembered facts
// are injected into every system prompt so the AI always has the human context.
func New(eng engine.Provider, reg *tools.Registry, mem *memory.DB) *Kernel {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_EXECUTION_MODE")))
	if mode == "" {
		mode = "yolo"
	}
	return &Kernel{
		engine:        eng,
		registry:      reg,
		memory:        agent.LoadMemory("kernel"),
		mem:           mem,
		tasks:         make(map[string]*TaskResult),
		executionMode: mode,
	}
}

// GetExecutionMode returns the current execution mode of the kernel loop.
func (k *Kernel) GetExecutionMode() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.executionMode == "" {
		mode := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_EXECUTION_MODE")))
		if mode == "" {
			mode = "yolo"
		}
		k.executionMode = mode
	}
	return k.executionMode
}

// SetExecutionMode updates the current execution mode of the kernel loop dynamically.
func (k *Kernel) SetExecutionMode(mode string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "yolo" || mode == "monitored" || mode == "privileged" {
		k.executionMode = mode
	}
}


// systemPrompt builds the immutable system prompt for the kernel brain.
func (k *Kernel) systemPrompt() string {
	var sb strings.Builder

	// ── Human-layer context (injected first so the model prioritises it) ──
	// The project brief is what the human knows: goals, vision, expected outcomes.
	// Facts are persistent things the human has told Janus to remember.
	// Together they form the human side of the human-AI partnership.
	if k.mem != nil {
		if brief := k.mem.ProjectBriefAsContext(); brief != "" {
			sb.WriteString(brief)
		}
		if facts := k.mem.FactsAsContext(); facts != "" {
			sb.WriteString(facts)
			sb.WriteString("\n")
		}
	}

	sb.WriteString(`You are Janus, an autonomous AI operator running on a local Windows machine.
You accomplish tasks by calling tools. You have full access to the local filesystem and shell.

EFFICIENCY RULES — every token costs GPU time, so be concise:
1. ALWAYS prefer "scaffold" to create new projects. It generates all boilerplate instantly with zero tokens.
2. Use "patch_file" to modify existing files — only send the changed lines, not the whole file.
3. Use "append_file" to add code to existing files — cheaper than rewriting.
4. Use "run_command" as your master key — it can run any program, compiler, or script.
5. Only use "write_file" for small config files or when no better tool fits.
6. Call exactly ONE tool per turn.
7. When done, call the "done" tool with a brief summary.
8. If stuck after 3 failures, call "done" with a failure summary.

UNIVERSAL INGEST — use these when the user uploads ANY file and you need to read it:
- "auto_ingest" — **FIRST CHOICE** for uploaded files. Detects type and routes to the right parser.
- "docx_extract" — Word .docx files (letters, notes, transcripts).
- "ocr_extract" — Tesseract OCR for scanned PDFs or image-only files.
- "image_extract" — OCR on PNG/JPG/TIFF/BMP. Use for photos of documents, handwritten notes.

UNIVERSAL OUTPUT — use these when the user wants a polished file back:
- "render_pdf" — generate a formatted PDF from your text/Markdown. Use for letters, reports, summaries.
- "render_docx" — generate an editable Word .docx file. Use when the user wants to edit it afterward.

DECISION RULES for messy real-world input:
1. User uploaded a file and you need to read/parse it → call auto_ingest first.
2. User pastes messy text → treat as-is, you are the parser.
3. User says "write me a letter" or "export as PDF" → compose the text in your reply, then call render_pdf or render_docx to save it.
4. Never ask the user to convert the file themselves — use auto_ingest or ocr_extract.
NEVER write Python to do what these tools already do.

`)
	sb.WriteString(k.registry.GroupedSystemPromptBlock())
	sb.WriteString(k.memory.SystemPromptSuffix())
	return sb.String()
}

// formatPrompt builds the full prompt for one iteration of the loop.
func formatPrompt(systemPrompt, userTask string, history []ToolLogEntry) string {
	var sb strings.Builder
	sb.WriteString("<|im_start|>system\n")
	sb.WriteString(systemPrompt)
	sb.WriteString("<|im_end|>\n")

	sb.WriteString("<|im_start|>user\n")
	sb.WriteString(userTask)
	sb.WriteString("<|im_end|>\n")

	// Replay tool history so the model sees what it already did.
	for _, entry := range history {
		if entry.Call.Name == "_loop_guard" || entry.Call.Name == "_parse_error" {
			sb.WriteString("<|im_start|>system\n")
			if entry.Call.Name == "_parse_error" {
				sb.WriteString(fmt.Sprintf("SYSTEM WARNING: %s\nYou outputted:\n%s\n", entry.Result.Error, entry.Result.Output))
			} else {
				sb.WriteString(fmt.Sprintf("SYSTEM WARNING: %s\n", entry.Result.Error))
			}
			sb.WriteString("<|im_end|>\n")
			continue
		}

		// The model's tool call
		callJSON, _ := json.Marshal(map[string]any{
			"tool_call": map[string]any{
				"name":      entry.Call.Name,
				"arguments": entry.Call.Arguments,
			},
		})
		sb.WriteString("<|im_start|>assistant\n")
		sb.WriteString(string(callJSON))
		sb.WriteString("<|im_end|>\n")

		// The tool's result
		sb.WriteString("<|im_start|>tool\n")
		sb.WriteString(fmt.Sprintf("[%s] success=%v\n%s",
			entry.Result.ToolName, entry.Result.Success, entry.Result.Output))
		if entry.Result.Error != "" {
			sb.WriteString("\nERROR: ")
			sb.WriteString(entry.Result.Error)
		}
		sb.WriteString("<|im_end|>\n")
	}

	sb.WriteString("<|im_start|>assistant\n")
	return sb.String()
}

// Run executes a task through the kernel loop. Blocks until done or cancelled.
func (k *Kernel) Run(ctx context.Context, taskID, input string, onProgress ProgressFunc) *TaskResult {
	start := time.Now()

	result := &TaskResult{
		TaskID: taskID,
		Input:  input,
		Status: "running",
	}

	k.mu.Lock()
	k.tasks[taskID] = result
	k.mu.Unlock()

	if k.engine == nil {
		result.Status = "failed"
		result.Summary = "Inference engine is not initialized (check your INFERENCE_BACKEND environment variable or llama.dll installation)"
		result.Duration = time.Since(start).Seconds()
		return result
	}

	sysPrompt := k.systemPrompt()
	var history []ToolLogEntry
	parseErrorCount := 0 // consecutive parse errors (truncation loop guard)

	log.Printf("kernel[%s]: starting — %q", taskID, truncate(input, 80))

	for i := 1; i <= MaxIterations; i++ {
		select {
		case <-ctx.Done():
			result.Status = "failed"
			result.Summary = "cancelled: " + ctx.Err().Error()
			result.Iterations = i - 1
			result.Duration = time.Since(start).Seconds()
			result.Verification = verifyKernelResult(result)
			k.recordExperience(result)
			return result
		default:
		}

		prompt := formatPrompt(sysPrompt, input, history)
		log.Printf("kernel[%s]: iter %d — thinking (prompt_len=%d chars)", taskID, i, len(prompt))

		// Emit a "thinking" entry so the UI can show the model is working.
		thinkEntry := ToolLogEntry{
			Iteration: i,
			Call:      tools.ToolCall{Name: "_thinking"},
			Result:    tools.ToolResult{ToolName: "_thinking", Success: true, Output: "Model is generating next tool call…"},
		}
		result.ToolLog = append(history, thinkEntry)

		// Per-iteration timeout: 3 minutes max for one inference call.
		iterCtx, iterCancel := context.WithTimeout(ctx, 3*time.Minute)

		// Unconstrained generation. The system prompt instructs the model to
		// emit a tool_call JSON object; ParseToolCall recovers it from any
		// surrounding prose. (GBNF grammar constraints were removed — too many
		// models could not follow them reliably.)
		raw, err := k.engine.Predict(iterCtx, prompt)
		iterCancel()
		if err != nil {
			log.Printf("kernel[%s]: predict error at iter %d: %v", taskID, i, err)
			if iterCtx.Err() != nil {
				log.Printf("kernel[%s]: iter %d timed out — failing task cleanly", taskID, i)
				result.Status = "failed"
				result.Summary = fmt.Sprintf("inference timeout at iteration %d (3 min limit reached — model was too slow or context too long)", i)
			} else {
				result.Status = "failed"
				result.Summary = fmt.Sprintf("inference error at iteration %d: %v", i, err)
			}
			result.Iterations = i
			result.Duration = time.Since(start).Seconds()
			result.Verification = verifyKernelResult(result)
			k.recordExperience(result)
			return result
		}

		// Forced-prefix nudge: if the model produced prose with no tool_call,
		// re-prompt once with the opening of a tool_call object so a weaker
		// model completes the JSON structure instead of chatting.
		if !strings.Contains(raw, "tool_call") {
			const jsonPrefix = `{"tool_call": {"name": "`
			nudgeCtx, nudgeCancel := context.WithTimeout(ctx, 3*time.Minute)
			forced, ferr := k.engine.Predict(nudgeCtx, prompt+jsonPrefix)
			nudgeCancel()
			if ferr == nil && strings.TrimSpace(forced) != "" {
				raw = jsonPrefix + forced
			}
		}

		// Parse the tool call.
		tc, parseErr := tools.ParseToolCall(raw)
		if parseErr != nil {
			log.Printf("kernel[%s]: iter %d — could not parse tool call: %v (raw=%q)", taskID, i, parseErr, truncate(raw, 200))
			parseErrorCount++

			recoveryHint := "Your output could not be parsed as a tool call. Output ONLY valid JSON: {\"tool_call\": {\"name\": \"...\", \"arguments\": {...}}}"
			if strings.Contains(raw, "tool_call") && strings.Contains(parseErr.Error(), "unexpected end") {
				// Extract tool name from partial JSON for a more targeted hint.
				attemptedTool := "write_file"
				if strings.Contains(raw, "\"name\":\"run_command\"") || strings.Contains(raw, "\"name\": \"run_command\"") {
					attemptedTool = "run_command"
				}
				if parseErrorCount >= 3 {
					recoveryHint = "STUCK: You have produced truncated output " + fmt.Sprintf("%d", parseErrorCount) + " times in a row. " +
						"The content you are trying to pass is TOO LARGE for a single tool call. " +
						"MANDATORY: Use write_file with a SHORT summary instead, or call done{} to report what you accomplished so far."
				} else if attemptedTool == "run_command" {
					recoveryHint = "TRUNCATED: Your run_command call was too large and got cut off. " +
						"NEVER embed large content in run_command. Instead: " +
						"use write_file{\"path\": \"output.md\", \"content\": \"<content here>\"} — " +
						"write_file handles any size content. Do NOT use run_command to write files."
				} else {
					recoveryHint = "TRUNCATED: Your tool call was cut off. Break the content into smaller pieces: " +
						"(1) write_file for the first section, then (2) append_file for additional sections."
				}
			}
			entry := ToolLogEntry{
				Iteration: i,
				Call:      tools.ToolCall{Name: "_parse_error"},
				Result:    tools.ToolResult{ToolName: "_parse_error", Success: false, Error: recoveryHint, Output: truncate(raw, 300)},
			}
			history = append(history, entry)
			continue
		}
		parseErrorCount = 0 // reset on successful parse

		log.Printf("kernel[%s]: iter %d — tool=%q args=%v", taskID, i, tc.Name, tc.Arguments)

		// Check for "done" — the model signals completion.
		if tc.Name == "done" {
			doneResult := k.registry.Dispatch(tc)
			entry := ToolLogEntry{Iteration: i, Call: tc, Result: doneResult}
			history = append(history, entry)
			result.ToolLog = history
			result.Status = "complete"
			result.Summary = doneResult.Output
			result.Iterations = i
			result.Duration = time.Since(start).Seconds()
			result.Verification = verifyKernelResult(result)
			log.Printf("kernel[%s]: done in %d iterations (%.1fs) — %s", taskID, i, result.Duration, truncate(result.Summary, 120))
			k.recordExperience(result)
			return result
		}

		// Enforce execution mode: yolo, privileged, monitored
		if !k.checkExecutionApproval(tc) {
			log.Printf("kernel[%s]: tool call %q REJECTED BY USER in loop", taskID, tc.Name)
			tr := tools.ToolResult{
				ToolName: tc.Name,
				Success:  false,
				Error:    "USER REJECTED execution of this tool call. Please adjust your plan or ask the user for clarification/instructions if you cannot proceed without it.",
			}
			entry := ToolLogEntry{Iteration: i, Call: tc, Result: tr}
			history = append(history, entry)
			result.ToolLog = history
			if onProgress != nil {
				onProgress(i, tc.Name, tr)
			}
			continue
		}

		// Dispatch the tool.
		tr := k.registry.Dispatch(tc)

		// Zero-Trust: scan content being written to disk for incomplete placeholders.
		if tr.Success && (tc.Name == "write_file" || tc.Name == "scaffold" || tc.Name == "patch_file" || tc.Name == "append_file" || tc.Name == "multi_write") {
			if content, ok := tc.Arguments["content"].(string); ok {
				if valErr := validation.ValidateContent(content); valErr != nil {
					log.Printf("kernel[%s]: zero-trust REJECT iter %d — %v", taskID, i, valErr)
					tr = tools.ToolResult{
						ToolName: tc.Name,
						Success:  false,
						Error:    "ZERO-TRUST REJECTION: " + valErr.Error() + ". You MUST provide a complete, working implementation. Remove all TODO comments, 'pass' statements, and NotImplementedError placeholders. Write real, functional code.",
					}
				}
			}
		}

		entry := ToolLogEntry{Iteration: i, Call: tc, Result: tr}
		history = append(history, entry)
		result.ToolLog = history

		if onProgress != nil {
			onProgress(i, tc.Name, tr)
		}

		// Log success + error so failures are actually diagnosable.
		if tr.Success {
			log.Printf("kernel[%s]: iter %d — %s ok output_len=%d",
				taskID, i, tc.Name, len(tr.Output))
		} else {
			log.Printf("kernel[%s]: iter %d — %s FAILED: %s",
				taskID, i, tc.Name, truncate(tr.Error, 300))
		}

		// Loop guard: if the model has called the same tool and failed 3
		// times in a row, OR has called the same tool with the exact same
		// arguments 3 times consecutively, inject a strong recovery hint.
		if (!tr.Success && repeatedFailures(history, tc.Name) >= 3) || repeatedIdenticalCalls(history, tc.Name, tc.Arguments) >= 3 {
			var errStr string
			if !tr.Success && repeatedFailures(history, tc.Name) >= 3 {
				errStr = fmt.Sprintf("You have called %s and it has failed 3 times. STOP retrying. ", tc.Name)
			} else {
				errStr = fmt.Sprintf("You have called %s with the exact same arguments 3 times consecutively. STOP repeating. ", tc.Name)
			}
			hint := ToolLogEntry{
				Iteration: i,
				Call:      tools.ToolCall{Name: "_loop_guard"},
				Result: tools.ToolResult{
					ToolName: "_loop_guard",
					Success:  false,
					Error: errStr +
						"Call the 'done' tool with a summary explaining the failure to the user, or proceed with different steps or arguments. " +
						"Example: {\"tool_call\": {\"name\": \"done\", \"arguments\": {\"summary\": \"Unable to complete task — the document extraction or note generation is stuck.\"}}}",
				},
			}
			history = append(history, hint)
			result.ToolLog = history
		}
	}

	// Hit max iterations without calling "done".
	result.Status = "max_iterations"
	result.Summary = fmt.Sprintf("reached %d iterations without completion", MaxIterations)
	result.Iterations = MaxIterations
	result.Duration = time.Since(start).Seconds()
	result.Verification = verifyKernelResult(result)
	log.Printf("kernel[%s]: max iterations reached", taskID)
	k.recordExperience(result)
	return result
}

func verifyKernelResult(r *TaskResult) *VerificationResult {
	v := &VerificationResult{
		Status:         "valid",
		RequiredAction: "none",
	}

	if strings.TrimSpace(r.Summary) == "" {
		v.addIssue("missing_final_summary")
		v.markInvalid("rerun_or_human_review")
	}

	if r.Status == "failed" {
		v.addIssue("kernel_failed")
		v.markInvalid("human_review")
		v.recommendEscalation("kernel ended in failed state")
	}
	if r.Status == "max_iterations" {
		v.addIssue("max_iterations_reached")
		v.markInvalid("split_task_or_escalate_model")
		v.recommendEscalation("kernel reached the hard iteration limit")
	}

	parseErrors := countSyntheticTool(r.ToolLog, "_parse_error")
	if parseErrors >= 3 {
		v.addIssue(fmt.Sprintf("repeated_parse_errors:%d", parseErrors))
		v.markAmbiguous("escalate_model_or_reduce_context")
		v.recommendEscalation("repeated parse failures indicate the current model is not reliably following the tool-call protocol")
	}

	thinkAborts := countSyntheticTool(r.ToolLog, "_think_abort")
	if thinkAborts >= 2 {
		v.addIssue(fmt.Sprintf("repeated_think_aborts:%d", thinkAborts))
		v.markAmbiguous("escalate_model_or_simplify_prompt")
		v.recommendEscalation("model repeatedly emitted hidden reasoning into constrained JSON")
	}

	failedTools := failedRealTools(r.ToolLog)
	if len(failedTools) > 0 && !summaryAcknowledgesFailure(r.Summary) {
		v.addIssue("unacknowledged_tool_failure:" + strings.Join(failedTools, ","))
		v.markAmbiguous("human_review")
	}

	if r.Status == "complete" {
		if len(r.ToolLog) == 0 || r.ToolLog[len(r.ToolLog)-1].Call.Name != "done" {
			v.addIssue("complete_without_done_tool")
			v.markInvalid("rerun_or_human_review")
		}
		for _, required := range requiredToolsForInput(r.Input) {
			if !toolWasCalled(r.ToolLog, required) {
				v.addIssue("missing_required_tool:" + required)
				v.markAmbiguous("human_review")
			}
		}
	}

	if v.Status == "valid" {
		v.RequiresReview = false
		v.EscalationRecommended = false
		v.EscalationReason = ""
	}
	return v
}

func (v *VerificationResult) addIssue(issue string) {
	for _, existing := range v.Issues {
		if existing == issue {
			return
		}
	}
	v.Issues = append(v.Issues, issue)
}

func (v *VerificationResult) markAmbiguous(action string) {
	if v.Status == "valid" {
		v.Status = "ambiguous"
	}
	if v.RequiredAction == "" || v.RequiredAction == "none" {
		v.RequiredAction = action
	}
	v.RequiresReview = true
}

func (v *VerificationResult) markInvalid(action string) {
	v.Status = "invalid"
	v.RequiredAction = action
	v.RequiresReview = true
}

func (v *VerificationResult) recommendEscalation(reason string) {
	v.EscalationRecommended = true
	if v.EscalationReason == "" {
		v.EscalationReason = reason
	}
}

func countSyntheticTool(log []ToolLogEntry, name string) int {
	count := 0
	for _, entry := range log {
		if entry.Call.Name == name {
			count++
		}
	}
	return count
}

func failedRealTools(log []ToolLogEntry) []string {
	seen := make(map[string]bool)
	var failed []string
	for _, entry := range log {
		name := entry.Call.Name
		if strings.HasPrefix(name, "_") || name == "done" || entry.Result.Success {
			continue
		}
		if !seen[name] {
			seen[name] = true
			failed = append(failed, name)
		}
	}
	return failed
}

func summaryAcknowledgesFailure(summary string) bool {
	s := strings.ToLower(summary)
	for _, marker := range []string{"fail", "failed", "unable", "could not", "cannot", "can't", "error", "blocked"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func requiredToolsForInput(input string) []string {
	s := strings.ToLower(input)
	required := make(map[string]bool)
	if strings.Contains(s, "calculate") || strings.Contains(s, "math") || strings.Contains(s, "2+2") {
		required["calculate"] = true
	}
	if strings.Contains(s, "read file") || strings.Contains(s, "show file") {
		required["read_file"] = true
	}
	if strings.Contains(s, "write file") || strings.Contains(s, "create file") || strings.Contains(s, "save file") {
		required["write_file"] = true
	}
	if strings.Contains(s, "list files") || strings.Contains(s, "directory") {
		required["list_dir"] = true
	}
	if strings.Contains(s, "extract") && (strings.Contains(s, "pdf") || strings.Contains(s, "document")) {
		required["auto_ingest"] = true
	}
	out := make([]string, 0, len(required))
	for tool := range required {
		out = append(out, tool)
	}
	return out
}

func toolWasCalled(log []ToolLogEntry, tool string) bool {
	for _, entry := range log {
		if entry.Call.Name == tool && entry.Result.Success {
			return true
		}
	}
	return false
}

// Status returns the current result for a task, or nil if not found.
func (k *Kernel) Status(taskID string) *TaskResult {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.tasks[taskID]
}

// recordExperience writes the task outcome to pwnagotchi memory.
func (k *Kernel) recordExperience(r *TaskResult) {
	outcome := "failure"
	score := 2
	switch r.Status {
	case "complete":
		outcome = "success"
		score = 8
	case "max_iterations":
		outcome = "partial"
		score = 4
	}

	toolsUsed := make(map[string]bool)
	for _, entry := range r.ToolLog {
		toolsUsed[entry.Call.Name] = true
	}
	var toolList []string
	for t := range toolsUsed {
		toolList = append(toolList, t)
	}

	lesson := ""
	if outcome != "success" && k.engine != nil {
		var logBrief strings.Builder
		logBrief.WriteString(fmt.Sprintf("Task Input: %s\n", r.Input))
		logBrief.WriteString(fmt.Sprintf("Status: %s after %d iterations.\n", r.Status, r.Iterations))
		logBrief.WriteString("Execution log:\n")
		logStart := len(r.ToolLog) - 5
		if logStart < 0 {
			logStart = 0
		}
		for _, entry := range r.ToolLog[logStart:] {
			logBrief.WriteString(fmt.Sprintf("- Iter %d: called %s, success=%v, error=%s\n",
				entry.Iteration, entry.Call.Name, entry.Result.Success, entry.Result.Error))
		}

		distillPrompt := fmt.Sprintf("<|im_start|>system\nYou are the Janus Experience Distiller. Analyze the failed task and execution log above. Write a single, short, actionable lesson (under 15 words) for future agents explaining what mistake to avoid or how to succeed (e.g., 'Do not add trailing semicolons after JSON tool calls'). Answer with ONLY the distilled lesson string, no intro or quotes.<|im_end|>\n<|im_start|>user\nTask and Log:\n%s\nDistilled Actionable Lesson:<|im_end|>\n<|im_start|>assistant\n", logBrief.String())

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if rawLesson, err := k.engine.Predict(ctx, distillPrompt); err == nil {
			lesson = strings.TrimSpace(rawLesson)
			lesson = strings.Trim(lesson, "\"`'")
		}
		cancel()
	}

	if lesson == "" && outcome != "success" {
		lesson = fmt.Sprintf("Task %q ended with status %s after %d iterations. Summary: %s",
			truncate(r.Input, 60), r.Status, r.Iterations, truncate(r.Summary, 120))
	}

	k.memory.Record(agent.Experience{
		TaskSummary:  truncate(r.Input, 80),
		Outcome:      outcome,
		ToolsUsed:    toolList,
		Iterations:   r.Iterations,
		JudgeScore:   score,
		JudgePenalty: 0,
		Lesson:       lesson,
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// repeatedFailures walks history backwards and counts consecutive failed
// calls to the given tool name. Stops at the first success or different
// tool. Loop-guard entries are skipped so they don't reset the counter.
func repeatedFailures(history []ToolLogEntry, toolName string) int {
	count := 0
	for i := len(history) - 1; i >= 0; i-- {
		e := history[i]
		if e.Call.Name == "_loop_guard" {
			continue
		}
		if e.Call.Name == toolName && !e.Result.Success {
			count++
			continue
		}
		break
	}
	return count
}

// repeatedIdenticalCalls walks history backwards and counts consecutive calls
// to the given tool name with the exact same arguments. Stops at the first
// mismatch or different tool. Loop-guard entries are skipped.
func repeatedIdenticalCalls(history []ToolLogEntry, toolName string, args map[string]any) int {
	count := 0
	for i := len(history) - 1; i >= 0; i-- {
		e := history[i]
		if e.Call.Name == "_loop_guard" {
			continue
		}
		if e.Call.Name == toolName && reflect.DeepEqual(e.Call.Arguments, args) {
			count++
			continue
		}
		break
	}
	return count
}

func (k *Kernel) checkExecutionApproval(tc tools.ToolCall) bool {
	mode := k.GetExecutionMode()


	if tc.Name == "done" || tc.Name == "_thinking" || tc.Name == "_loop_guard" || tc.Name == "_parse_error" {
		return true
	}

	switch mode {
	case "yolo":
		return true

	case "monitored":
		return promptUserApproval(tc.Name, tc.Arguments)

	case "privileged":
		if isPreApprovedTool(tc.Name) {
			log.Printf("kernel: tool %q is pre-approved in privileged mode, running automatically", tc.Name)
			return true
		}
		return promptUserApproval(tc.Name, tc.Arguments)

	default:
		log.Printf("kernel: unknown execution mode %q, falling back to monitored", mode)
		return promptUserApproval(tc.Name, tc.Arguments)
	}
}

func isPreApprovedTool(name string) bool {
	switch name {
	case "read_file", "list_dir", "search_files":
		return true
	case "calculate", "get_time", "done":
		return true
	case "auto_ingest", "docx_extract", "ocr_extract", "image_extract",
		"render_pdf", "render_docx":
		return true
	}
	return false
}

func promptUserApproval(name string, args map[string]any) bool {
	argsJSON, _ := json.Marshal(args)
	fmt.Printf("\n==================================================\n")
	fmt.Printf("⚠️  APPROVAL REQUIRED (Execution Mode Gated)\n")
	fmt.Printf("📍 TOOL CALL: %s\n", name)
	fmt.Printf("❓ ARGUMENTS: %s\n", string(argsJSON))
	fmt.Printf("👉 Approve this action? [y/N]: ")
	fmt.Printf("\n==================================================\n")

	var input string
	_, err := fmt.Scanln(&input)
	if err != nil {
		return false
	}
	input = strings.ToLower(strings.TrimSpace(input))
	return input == "y" || input == "yes"
}

