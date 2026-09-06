// Package kernel implements the single-brain ReAct loop for Janus.
//
// Architecture: One LLM stays loaded. It thinks, calls tools via GBNF-constrained
// JSON, observes results, and repeats until done. No agent handoffs, no model
// swapping. The AI is the Operator; the Go tools are the System Calls.
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"janus/internal/engine"
	"janus/internal/tools"
	"janus/internal/validation"
)

// MaxIterations is the hard ceiling on tool-call loops per task.
const MaxIterations = 30

// ProgressFunc is called after each iteration so the UI can show live updates.
type ProgressFunc func(iter int, toolName string, result tools.ToolResult)

// TaskResult is the final output of a kernel run.
type TaskResult struct {
	TaskID     string         `json:"task_id"`
	Input      string         `json:"input"`
	Status     string         `json:"status"` // "complete" | "failed" | "max_iterations"
	Summary    string         `json:"summary"`
	Iterations int            `json:"iterations"`
	ToolLog    []ToolLogEntry `json:"tool_log"`
	Duration   float64        `json:"duration_seconds"`
}

// ToolLogEntry records one tool invocation in the task history.
type ToolLogEntry struct {
	Iteration int              `json:"iteration"`
	Call      tools.ToolCall   `json:"call"`
	Result    tools.ToolResult `json:"result"`
}

// Kernel is the single-brain operator loop.
type Kernel struct {
	engine   engine.Provider
	registry *tools.Registry

	mu    sync.Mutex
	tasks map[string]*TaskResult
}

// New creates a Kernel wired to the shared engine and tool registry.
func New(eng engine.Provider, reg *tools.Registry) *Kernel {
	return &Kernel{
		engine:   eng,
		registry: reg,
		tasks:    make(map[string]*TaskResult),
	}
}

// systemPrompt builds the immutable system prompt for the kernel brain.
func (k *Kernel) systemPrompt() string {
	var sb strings.Builder
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
- "auto_ingest" — **FIRST CHOICE** for unknown files. Sniffs type and routes to the right parser (PDF/DOCX/image/text). Use this when the user attaches something and you don't know its format.
- "docx_extract" — Word .docx files (documents, transcripts, reports).
- "ocr_extract" — Tesseract OCR for scanned PDFs or image-only documents. Try this if pdf_extract returned "(no text found)".
- "image_extract" — OCR on PNG/JPG/TIFF/BMP. Use for photos of documents, handwritten notes.

UNIVERSAL OUTPUT — use these when the user wants a polished file back:
- "render_pdf" — generate a formatted PDF from your text/Markdown. Use for letters, reports, discharge summaries.
- "render_docx" — generate an editable Word .docx file. Use when the user wants to edit it afterward.

DECISION RULES for messy real-world input:
1. User uploaded a file and you don't know the format → call auto_ingest first.
2. pdf_extract returned "(no text found)" → the PDF is scanned; call ocr_extract.
3. User pastes messy text (copy-paste, dictation, etc.) → treat as-is, you are the parser.
4. User says "write me a letter" or "export as PDF" → compose the text in your reply, then call render_pdf or render_docx to save it.
5. Never ask the user to convert the file themselves — use auto_ingest / ocr_extract.

Common workflows:
- Text extraction: auto_ingest → (fallback ocr_extract if needed) → render output.
- Document creation: compose text → render_docx or render_pdf.
NEVER write Python to do what these tools already do.

`)
	sb.WriteString(k.registry.SystemPromptBlock())
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
			sb.WriteString("\nERROR: " + entry.Result.Error)
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

		// GBNF-constrained generation → guaranteed valid tool_call JSON.
		raw, err := k.engine.PredictConstrained(iterCtx, prompt, tools.ToolCallGrammar, tools.GrammarRoot)
		if err != nil {
			log.Printf("kernel[%s]: predict error at iter %d: %v", taskID, i, err)
			// If the iteration context already expired (timeout), don't attempt
			// an unconstrained fallback — the engine won't honour the dead context
			// and would run indefinitely. Fail the task immediately instead.
			if iterCtx.Err() != nil {
				iterCancel()
				log.Printf("kernel[%s]: iter %d timed out — failing task cleanly (no unconstrained retry)", taskID, i)
				result.Status = "failed"
				result.Summary = fmt.Sprintf("inference timeout at iteration %d (3 min limit reached — model was too slow or context too long)", i)
				result.Iterations = i
				result.Duration = time.Since(start).Seconds()
				k.recordExperience(result)
				return result
			}
			// <think>-in-grammar abort: add a recovery hint and retry.
			if strings.Contains(err.Error(), "<think>") {
				iterCancel()
				log.Printf("kernel[%s]: iter %d — constrained generate aborted (<think> block in JSON); injecting recovery hint", taskID, i)
				entry := ToolLogEntry{
					Iteration: i,
					Call:      tools.ToolCall{Name: "_think_abort"},
					Result: tools.ToolResult{
						ToolName: "_think_abort",
						Success:  false,
						Error:    "Your previous response contained a <think> reasoning block inside the JSON output. You MUST output ONLY the raw JSON tool_call with no thinking tags. Output exactly: {\"tool_call\": {\"name\": \"...\", \"arguments\": {...}}}",
					},
				}
				history = append(history, entry)
				continue
			}
			// Context is still live — try unconstrained as a grammar-failure fallback.
			raw, err = k.engine.Predict(iterCtx, prompt)
			if err != nil {
				iterCancel()
				result.Status = "failed"
				result.Summary = fmt.Sprintf("inference error at iteration %d: %v", i, err)
				result.Iterations = i
				result.Duration = time.Since(start).Seconds()
				k.recordExperience(result)
				return result
			}
		}
		iterCancel()

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
			log.Printf("kernel[%s]: done in %d iterations (%.1fs) — %s", taskID, i, result.Duration, truncate(result.Summary, 120))
			k.recordExperience(result)
			return result
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
		// times in a row, inject a strong recovery hint on the next pass
		// so it stops spinning on an unsolvable input.
		if !tr.Success && repeatedFailures(history, tc.Name) >= 3 {
			hint := ToolLogEntry{
				Iteration: i,
				Call:      tools.ToolCall{Name: "_loop_guard"},
				Result: tools.ToolResult{
					ToolName: "_loop_guard",
					Success:  false,
					Error: "You have called " + tc.Name + " and it has failed 3 times. STOP retrying. " +
						"Call the 'done' tool with a summary explaining the failure to the user. " +
						"Example: {\"tool_call\": {\"name\": \"done\", \"arguments\": {\"summary\": \"Unable to extract text — the PDF appears to be encrypted or scanned.\"}}}",
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
	log.Printf("kernel[%s]: max iterations reached", taskID)
	k.recordExperience(result)
	return result
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
	if r.Status == "complete" {
		outcome = "success"
		score = 8
	} else if r.Status == "max_iterations" {
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
	if outcome != "success" {
		lesson = fmt.Sprintf("Task %q ended with status %s after %d iterations. Summary: %s",
			truncate(r.Input, 60), r.Status, r.Iterations, truncate(r.Summary, 120))
	}
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
