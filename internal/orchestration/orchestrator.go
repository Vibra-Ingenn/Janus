package orchestration

import (
"bytes"
"context"
"crypto/hmac"
"crypto/sha256"
"encoding/hex"
"encoding/json"
"fmt"
"io"
"log"
"net/http"
"os"
"os/exec"
"runtime"
"strconv"
"strings"
"sync"
"time"

"janus/internal/audit"
"janus/internal/tools"
)

// Protocol is the 7-Point Universal Execution Struct.
type Protocol struct {
	ProtocolID       string         `json:"protocol_id"`
	StatusBroadcast  string         `json:"broadcast_signal"`
	EntitySelector   string         `json:"target_entity"`
	PrimaryOperation string         `json:"operation_command"`
	OperationArgs    map[string]any `json:"operation_args,omitempty"`
	ExecutionBuffer  int            `json:"execution_buffer"`
	RemediationLogic string         `json:"failure_logic"`
	AuditReceipt     string         `json:"audit_receipt"`
}

// ProtocolAttemptRecord captures a single execution attempt within a Protocol run.
// It is stored in ProtocolResult.AttemptHistory to provide a durable,
// replayable record of every retry that occurred.
// (Distinct from contracts.AttemptRecord which belongs to the batch workflow engine.)
type ProtocolAttemptRecord struct {
Attempt   int       `json:"attempt"`
Output    string    `json:"output,omitempty"`
Error     string    `json:"error,omitempty"`
ExitCode  int       `json:"exit_code"`
Kind      string    `json:"kind"`           // "primary" | "fallback" | "heal-auto"
Timestamp time.Time `json:"timestamp"`
}

// ProtocolResult is the outcome of executing a Protocol.
type ProtocolResult struct {
ProtocolID     string                  `json:"protocol_id"`
Success        bool                    `json:"success"`
ExitCode        int                     `json:"exit_code,omitempty"`
Output          string                  `json:"output,omitempty"`
Error           string                  `json:"error,omitempty"`
Attempts        int                     `json:"attempts"`
FailureClass    FailureClass            `json:"failure_class,omitempty"`
AttemptHistory  []ProtocolAttemptRecord `json:"attempt_history,omitempty"`
StartedAt       time.Time               `json:"started_at"`
FinishedAt      time.Time               `json:"finished_at"`
AuditReceipt    string                  `json:"audit_receipt"`
}

// ProtocolBatchRequest is the input for BatchExecute via HTTP.
type ProtocolBatchRequest struct {
Protocols   []Protocol `json:"protocols"`
MaxParallel int        `json:"max_parallel,omitempty"`
}

// ProtocolBatchResult is the outcome of a batch execution.
type ProtocolBatchResult struct {
BatchSize int              `json:"batch_size"`
Succeeded int              `json:"succeeded"`
Failed    int              `json:"failed"`
Results   []ProtocolResult `json:"results"`
}

// CommandKind identifies the dispatch type of a PrimaryOperation.
type CommandKind string

const (
CommandKindShell CommandKind = "shell"
CommandKindHTTP  CommandKind = "http"
CommandKindTool  CommandKind = "tool"
CommandKindHeal  CommandKind = "heal"
)

type parsedCommand struct {
Kind       CommandKind
ShellArgs  []string
HTTPMethod string
HTTPURL    string
HTTPBody   string
ToolName   string
ToolArgs   map[string]any
HealTarget string
}

func parseOperationCommand(cmd, target string, opArgs map[string]any) (parsedCommand, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return parsedCommand{}, fmt.Errorf("operation_command must not be empty")
	}

	// Compatibility layer: map legacy Vibra action names to standard orchestrator commands
	switch strings.ToLower(cmd) {
	case "file_op":
		action, _ := opArgs["operation"].(string)
		if action == "" {
			action, _ = opArgs["action"].(string)
		}
		switch strings.ToLower(action) {
		case "read":
			cmd = "tool:read_file"
		case "write":
			cmd = "tool:write_file"
		case "delete":
			cmd = "tool:delete"
		default:
			cmd = "tool:list_dir"
		}
	case "http_request":
		method, _ := opArgs["method"].(string)
		if method == "" {
			method = "GET"
		}
		url, _ := opArgs["url"].(string)
		if url == "" {
			url = target
		}
		cmd = fmt.Sprintf("http:%s:%s", strings.ToUpper(method), url)
	case "run_script":
		cmd = "tool:run_command"
	case "ai_call":
		cmd = "tool:ai_call"
	case "notify":
		cmd = "tool:notify"
	case "transform":
		cmd = "tool:transform"
	default:
		// Auto-prefix known tool names without tool: prefix for backward and LLM compatibility
		if !strings.Contains(cmd, ":") {
			switch strings.ToLower(cmd) {
			case "read_file", "write_file", "list_dir", "calculate", "get_time",
				"search_files", "run_command", "create_dir", "delete", "done",
				"run_vibra_recipe", "pdf_extract", "ocr_extract", "image_extract",
				"docx_extract", "heal_document", "render_pdf", "render_docx", "search_web":
				cmd = "tool:" + cmd
			}
		}
	}
if strings.HasPrefix(cmd, "shell:") {
raw := strings.TrimPrefix(cmd, "shell:")
args := shellSplit(raw)
if len(args) == 0 {
return parsedCommand{}, fmt.Errorf("shell command is empty after shell: prefix")
}
return parsedCommand{Kind: CommandKindShell, ShellArgs: args}, nil
}
if strings.HasPrefix(cmd, "http:") {
rest := strings.TrimPrefix(cmd, "http:")
idx := strings.Index(rest, ":")
if idx < 0 {
return parsedCommand{}, fmt.Errorf("http command must be http:METHOD:URL, got %q", cmd)
}
method := strings.ToUpper(rest[:idx])
url := rest[idx+1:]
if url == "" {
return parsedCommand{}, fmt.Errorf("http command URL is empty in %q", cmd)
}
return parsedCommand{Kind: CommandKindHTTP, HTTPMethod: method, HTTPURL: url, HTTPBody: target}, nil
}
if strings.HasPrefix(cmd, "heal:") {
healTarget := strings.TrimPrefix(cmd, "heal:")
if strings.TrimSpace(healTarget) == "" {
healTarget = target
}
if strings.TrimSpace(healTarget) == "" {
return parsedCommand{}, fmt.Errorf("heal command requires a file path: heal:<path>")
}
return parsedCommand{Kind: CommandKindHeal, HealTarget: healTarget}, nil
}
if strings.HasPrefix(cmd, "tool:") {
		rest := strings.TrimPrefix(cmd, "tool:")
		var toolName, jsonArgs string
		jIdx := strings.Index(rest, "{")
		if jIdx >= 0 {
			colIdx := strings.LastIndex(rest[:jIdx], ":")
			if colIdx >= 0 {
				toolName = strings.TrimSpace(rest[:colIdx])
				jsonArgs = strings.TrimSpace(rest[colIdx+1:])
			} else {
				toolName = strings.TrimSpace(rest[:jIdx])
			}
		} else {
			if strings.HasPrefix(rest, "builtin:") {
				sub := strings.TrimPrefix(rest, "builtin:")
				subIdx := strings.Index(sub, ":")
				if subIdx >= 0 {
					toolName = "builtin:" + strings.TrimSpace(sub[:subIdx])
					jsonArgs = strings.TrimSpace(sub[subIdx+1:])
				} else {
					toolName = strings.TrimSpace(rest)
				}
			} else {
				idx := strings.Index(rest, ":")
				if idx >= 0 {
					toolName = strings.TrimSpace(rest[:idx])
					jsonArgs = strings.TrimSpace(rest[idx+1:])
				} else {
					toolName = strings.TrimSpace(rest)
				}
			}
		}
		if toolName == "" {
			return parsedCommand{}, fmt.Errorf("tool command must be tool:<name> or tool:<name>:<json-args>")
		}
		toolArgsMap := map[string]any{}
		if len(opArgs) > 0 {
			for k, v := range opArgs {
				toolArgsMap[k] = v
			}
		} else if jsonArgs != "" {
			cleanedJSON := cleanJSONRawNewlines(jsonArgs)
			if err := json.Unmarshal([]byte(cleanedJSON), &toolArgsMap); err != nil {
				if err2 := json.Unmarshal([]byte(jsonArgs), &toolArgsMap); err2 != nil {
					return parsedCommand{}, fmt.Errorf("tool command json args invalid: %w", err)
				}
			}
		}
		if toolArgsMap["path"] == nil && target != "" {
			toolArgsMap["path"] = target
		}
		return parsedCommand{Kind: CommandKindTool, ToolName: toolName, ToolArgs: toolArgsMap}, nil
}
args := shellSplit(cmd)
if len(args) == 0 {
return parsedCommand{}, fmt.Errorf("operation_command produced an empty argument list")
}
return parsedCommand{Kind: CommandKindShell, ShellArgs: args}, nil
}

func shellSplit(s string) []string {
var args []string
var current strings.Builder
inSingle := false
inDouble := false
runes := []rune(s)
for i := 0; i < len(runes); i++ {
r := runes[i]
switch {
case r == '\'' && !inDouble:
inSingle = !inSingle
case r == '"' && !inSingle:
inDouble = !inDouble
case r == '\\' && inDouble && i+1 < len(runes):
next := runes[i+1]
if next == '"' || next == '\\' {
i++
current.WriteRune(next)
} else {
current.WriteRune(r)
}
case (r == ' ' || r == '\t') && !inSingle && !inDouble:
if current.Len() > 0 {
args = append(args, current.String())
current.Reset()
}
default:
current.WriteRune(r)
}
}
if current.Len() > 0 {
args = append(args, current.String())
}
return args
}

// FailureStrategy is the parsed form of the RemediationLogic field.
type FailureStrategy struct {
Kind        string
MaxRetries  int
Backoff     bool
FallbackCmd string
}

func parseFailureLogic(logic string) FailureStrategy {
logic = strings.TrimSpace(logic)
switch logic {
case "", "fail":
return FailureStrategy{Kind: "fail"}
case "ignore":
return FailureStrategy{Kind: "ignore"}
case "dlq":
return FailureStrategy{Kind: "dlq"}
case "heal-auto":
return FailureStrategy{Kind: "heal-auto"}
}
if strings.HasPrefix(logic, "retry:") {
rest := strings.TrimPrefix(logic, "retry:")
parts := strings.SplitN(rest, ":", 2)
n, err := strconv.Atoi(parts[0])
if err != nil || n < 1 {
n = 3
}
backoff := len(parts) == 2 && strings.EqualFold(parts[1], "backoff")
return FailureStrategy{Kind: "retry", MaxRetries: n, Backoff: backoff}
}
if strings.HasPrefix(logic, "fallback:") {
fb := strings.TrimPrefix(logic, "fallback:")
return FailureStrategy{Kind: "fallback", FallbackCmd: fb}
}
return FailureStrategy{Kind: "fail"}
}

func classifyFailure(err error, output string) FailureClass {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error() + " " + output)

	permanentSignals := []string{
		"no such file",
		"file not found",
		"does not exist",
		"not found",
		"cannot find",
		"cannot locate",
		"unsupported",
		"invalid",
		"malformed",
		"corrupt",
		"parse",
		"schema",
		"validation",
		"permission denied",
		"access denied",
		"not a directory",
		"is a directory",
		"too large",
		"missing",
		"required",
		"outside the workspace",
	}
	for _, sig := range permanentSignals {
		if strings.Contains(msg, sig) {
			return FailureClassPermanent
		}
	}

	transientSignals := []string{
		"timeout",
		"timed out",
		"connection refused",
		"connection reset",
		"i/o timeout",
		"temporary",
		"try again",
		"resource busy",
		"too many open",
		"deadline exceeded",
		"no space left",
		"locked",
		"http 429",
		"http 503",
		"http 502",
		"http 504",
	}
	for _, sig := range transientSignals {
		if strings.Contains(msg, sig) {
			return FailureClassTransient
		}
	}

	return ""
}

// isDocumentTarget returns true if the target path looks like a document that
// the heal_document tool can attempt to recover.
func isDocumentTarget(target string) bool {
t := strings.ToLower(strings.TrimSpace(target))
if t == "" {
return false
}
for _, ext := range []string{".pdf", ".docx", ".doc", ".hl7", ".txt", ".csv", ".xml", ".json", ".png", ".jpg", ".jpeg", ".tiff", ".bmp"} {
if strings.HasSuffix(t, ext) {
return true
}
}
return false
}

func computeAuditReceipt(p Protocol, output string, finishedAt time.Time) string {
mac := hmac.New(sha256.New, []byte(audit.GetChainSecret()))
mac.Write([]byte(p.ProtocolID))
mac.Write([]byte("|"))
mac.Write([]byte(p.PrimaryOperation))
mac.Write([]byte("|"))
mac.Write([]byte(p.EntitySelector))
mac.Write([]byte("|"))
mac.Write([]byte(output))
mac.Write([]byte("|"))
mac.Write([]byte(finishedAt.UTC().Format(time.RFC3339Nano)))
return hex.EncodeToString(mac.Sum(nil))
}

func broadcast(signal string, result ProtocolResult) {
if signal == "" {
return
}
if strings.HasPrefix(signal, "http://") || strings.HasPrefix(signal, "https://") {
body, err := json.Marshal(result)
if err != nil {
log.Printf("orchestrator: broadcast marshal error for %s: %v", result.ProtocolID, err)
return
}
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
req, err := http.NewRequestWithContext(ctx, http.MethodPost, signal, bytes.NewReader(body))
if err != nil {
log.Printf("orchestrator: broadcast request build for %s: %v", result.ProtocolID, err)
return
}
req.Header.Set("Content-Type", "application/json")
resp, err := http.DefaultClient.Do(req)
if err != nil {
log.Printf("orchestrator: broadcast POST %s -> %s: %v", result.ProtocolID, signal, err)
return
}
resp.Body.Close()
log.Printf("orchestrator: broadcast %s -> %s: HTTP %d", result.ProtocolID, signal, resp.StatusCode)
return
}
audit.Write(audit.Event{
Kind:   audit.KindToolResult,
TaskID: result.ProtocolID,
Tool:   signal,
Output: truncateOutput(result.Output, 256),
OK:     result.Success,
Details: map[string]string{
"audit_receipt": result.AuditReceipt,
"attempts":      strconv.Itoa(result.Attempts),
},
})
}

func executeShell(ctx context.Context, args []string) (string, int, error) {
if len(args) == 0 {
return "", 1, fmt.Errorf("empty shell command")
}
var cmd *exec.Cmd
if runtime.GOOS == "windows" {
cmdLine := strings.Join(args, " ")
cmd = exec.CommandContext(ctx, "cmd", "/C", cmdLine)
} else {
cmd = exec.CommandContext(ctx, args[0], args[1:]...)
}
cmd.Env = os.Environ()
var outBuf, errBuf bytes.Buffer
cmd.Stdout = &outBuf
cmd.Stderr = &errBuf
err := cmd.Run()
combined := outBuf.String()
if errBuf.Len() > 0 {
if combined != "" {
combined += "\n"
}
combined += "[stderr]\n" + errBuf.String()
}
combined = strings.TrimRight(combined, "\n")
exitCode := 0
if err != nil {
if exitErr, ok := err.(*exec.ExitError); ok {
exitCode = exitErr.ExitCode()
} else {
exitCode = 1
}
}
return combined, exitCode, err
}

func executeHTTP(ctx context.Context, pc parsedCommand) (string, int, error) {
var bodyReader io.Reader
if pc.HTTPBody != "" {
bodyReader = strings.NewReader(pc.HTTPBody)
}
req, err := http.NewRequestWithContext(ctx, pc.HTTPMethod, pc.HTTPURL, bodyReader)
if err != nil {
return "", 1, fmt.Errorf("http request build: %w", err)
}
if pc.HTTPBody != "" {
req.Header.Set("Content-Type", "application/json")
}
req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Janus/1.0")
resp, err := http.DefaultClient.Do(req)
if err != nil {
return "", 1, fmt.Errorf("http request: %w", err)
}
defer resp.Body.Close()
body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
bodyStr := strings.TrimSpace(string(body))
if resp.StatusCode >= 400 {
return bodyStr, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodyStr)
}
if readErr != nil {
return bodyStr, resp.StatusCode, fmt.Errorf("reading response body: %w", readErr)
}
return bodyStr, resp.StatusCode, nil
}

func executeTool(pc parsedCommand, reg *tools.Registry) (string, int, error) {
	if reg == nil {
		return "", 1, fmt.Errorf("tool registry is not available")
	}
	name := pc.ToolName
	useBuiltin := false
	if strings.HasPrefix(name, "builtin:") {
		name = strings.TrimPrefix(name, "builtin:")
		useBuiltin = true
	}
	var result tools.ToolResult
	if useBuiltin {
		result = reg.DispatchBuiltin(tools.ToolCall{
			Name:      name,
			Arguments: pc.ToolArgs,
		})
	} else {
		result = reg.Dispatch(tools.ToolCall{
			Name:      name,
			Arguments: pc.ToolArgs,
		})
	}
	if !result.Success {
		return result.Error, 1, fmt.Errorf("tool %q failed: %s", pc.ToolName, result.Error)
	}
	return result.Output, 0, nil
}

func executeCommand(ctx context.Context, pc parsedCommand, reg *tools.Registry) (string, int, error) {
switch pc.Kind {
case CommandKindShell:
return executeShell(ctx, pc.ShellArgs)
case CommandKindHTTP:
return executeHTTP(ctx, pc)
case CommandKindTool:
return executeTool(pc, reg)
case CommandKindHeal:
return executeHeal(pc, reg)
default:
return "", 1, fmt.Errorf("unknown command kind: %q", pc.Kind)
}
}

// executeHeal dispatches a heal: command to the heal_document tool.
// It uses the tool registry so the full healing logic in tools.HealDocument
// is invoked and the result is returned as a JSON-encoded HealReport.
func executeHeal(pc parsedCommand, reg *tools.Registry) (string, int, error) {
if reg == nil {
return "", 1, fmt.Errorf("tool registry is not available for heal command")
}
result := reg.Dispatch(tools.ToolCall{
Name:      "heal_document",
Arguments: map[string]any{"path": pc.HealTarget},
})
if !result.Success {
msg := result.Error
if msg == "" {
msg = result.Output
}
return result.Output, 1, fmt.Errorf("heal failed: %s", msg)
}
return result.Output, 0, nil
}

// Execute runs a Protocol through the full 7-point lifecycle.
func Execute(ctx context.Context, p Protocol, reg *tools.Registry) ProtocolResult {
started := time.Now().UTC()
result := ProtocolResult{
ProtocolID: p.ProtocolID,
StartedAt:  started,
}

	if p.ExecutionBuffer <= 0 {
		p.ExecutionBuffer = 30
	}
	execCtx, cancelExec := context.WithTimeout(ctx, time.Duration(p.ExecutionBuffer)*time.Second)
	defer cancelExec()

	pc, parseErr := parseOperationCommand(p.PrimaryOperation, p.EntitySelector, p.OperationArgs)
if parseErr != nil {
	result.Error = fmt.Sprintf("invalid operation_command: %v", parseErr)
	result.FinishedAt = time.Now().UTC()
	result.AuditReceipt = computeAuditReceipt(p, result.Output, result.FinishedAt)

	// Validate the receipt if one was provided
	if p.AuditReceipt != "" && result.AuditReceipt != p.AuditReceipt {
		result.Success = false
		result.FailureClass = FailureClassValidation
		result.Error = fmt.Sprintf("audit receipt mismatch: expected %q, got %q", p.AuditReceipt, result.AuditReceipt)
		result.ExitCode = 1
	}

	audit.Error(p.ProtocolID, "orchestrator", result.Error)
	return result
}

strategy := parseFailureLogic(p.RemediationLogic)
maxAttempts := 1
if strategy.Kind == "retry" {
maxAttempts = strategy.MaxRetries + 1
}

var lastOutput string
var lastErr error
var lastCode int
var lastFailClass FailureClass

for attempt := 1; attempt <= maxAttempts; attempt++ {
result.Attempts = attempt
if attempt > 1 && strategy.Backoff {
wait := time.Duration(1<<uint(attempt-2)) * time.Second
if wait > 30*time.Second {
wait = 30 * time.Second
}
			select {
			case <-time.After(wait):
			case <-execCtx.Done():
				lastErr = execCtx.Err()
				break
			}
		}
		var out string
		var code int
		var runErr error
		out, code, runErr = executeCommand(execCtx, pc, reg)

// Record every attempt in the durable history.
rec := ProtocolAttemptRecord{
Attempt:   attempt,
Output:    truncateOutput(out, 512),
ExitCode:  code,
Kind:      "primary",
Timestamp: time.Now().UTC(),
}
if runErr != nil {
rec.Error = runErr.Error()
}
result.AttemptHistory = append(result.AttemptHistory, rec)

lastOutput, lastCode, lastErr = out, code, runErr
if lastErr == nil {
break
}

lastFailClass = classifyFailure(lastErr, lastOutput)
log.Printf("orchestrator: %s attempt %d/%d failed (exit %d, class %s): %v",
p.ProtocolID, attempt, maxAttempts, lastCode, lastFailClass, lastErr)

// Permanent failures will not improve on retry — bail out early.
if lastFailClass == FailureClassPermanent {
log.Printf("orchestrator: %s permanent failure — skipping remaining retries", p.ProtocolID)
break
}
}

result.FailureClass = lastFailClass

if lastErr != nil {
switch strategy.Kind {
case "ignore":
result.Success = true
result.Output = lastOutput
result.ExitCode = 0

		case "fallback":
			fbPC, fbErr := parseOperationCommand(strategy.FallbackCmd, p.EntitySelector, nil)
if fbErr != nil {
result.Error = fmt.Sprintf("fallback parse failed: %v (original: %v)", fbErr, lastErr)
result.ExitCode = lastCode
			} else {
				fbOut, fbCode, fbRunErr := executeCommand(execCtx, fbPC, reg)
rec := ProtocolAttemptRecord{
Attempt:   result.Attempts + 1,
Output:    truncateOutput(fbOut, 512),
ExitCode:  fbCode,
Kind:      "fallback",
Timestamp: time.Now().UTC(),
}
if fbRunErr != nil {
rec.Error = fbRunErr.Error()
}
result.AttemptHistory = append(result.AttemptHistory, rec)
if fbRunErr != nil {
result.Error = fmt.Sprintf("fallback also failed: %v (original: %v)", fbRunErr, lastErr)
result.ExitCode = fbCode
} else {
result.Success = true
result.Output = fbOut
result.ExitCode = fbCode
}
}

case "heal-auto":
// Auto-dispatch heal_document on the target entity when a document
// operation fails. This is the AI-in-the-middle recovery path: Janus
// tries to extract whatever it can from the broken file before giving
// up and telling the user what manual action is needed.
healTarget := p.EntitySelector
// If the primary command was a tool with a path arg, prefer that path.
if pc.Kind == CommandKindTool {
if pathArg, ok := pc.ToolArgs["path"].(string); ok && pathArg != "" {
healTarget = pathArg
}
}
if strings.TrimSpace(healTarget) == "" || !isDocumentTarget(healTarget) {
result.Error = fmt.Sprintf("heal-auto: no recoverable document target (original: %v)", lastErr)
result.ExitCode = lastCode
} else {
healResult := reg.Dispatch(tools.ToolCall{
Name:      "heal_document",
Arguments: map[string]any{"path": healTarget},
})
rec := ProtocolAttemptRecord{
Attempt:   result.Attempts + 1,
Output:    truncateOutput(healResult.Output, 512),
ExitCode:  0,
Kind:      "heal-auto",
Timestamp: time.Now().UTC(),
}
if !healResult.Success {
rec.Error = healResult.Error
rec.ExitCode = 1
}
result.AttemptHistory = append(result.AttemptHistory, rec)
if healResult.Success {
result.Success = true
result.Output = healResult.Output
result.ExitCode = 0
} else {
result.Error = fmt.Sprintf("heal-auto recovery failed: %s (original: %v)", healResult.Error, lastErr)
result.ExitCode = 1
}
}

case "dlq":
result.Error = fmt.Sprintf("dead-letter: %v", lastErr)
result.ExitCode = lastCode
audit.Write(audit.Event{
Kind:   audit.KindError,
TaskID: p.ProtocolID,
Tool:   "orchestrator-dlq",
Output: result.Error,
OK:     false,
Details: map[string]string{
"command":    p.PrimaryOperation,
"target":     p.EntitySelector,
"dlq_reason": "max_attempts_exceeded",
},
})

default: // "fail"
result.Error = lastErr.Error()
result.ExitCode = lastCode
}
} else {
result.Success = true
result.Output = lastOutput
result.ExitCode = lastCode
}

result.FinishedAt = time.Now().UTC()
auditContent := lastOutput
if !result.Success && result.Error != "" {
auditContent = result.Error
}
	result.AuditReceipt = computeAuditReceipt(p, auditContent, result.FinishedAt)

	// Validate the receipt if one was provided
	if p.AuditReceipt != "" && result.AuditReceipt != p.AuditReceipt {
		result.Success = false
		result.FailureClass = FailureClassValidation
		result.Error = fmt.Sprintf("audit receipt mismatch: expected %q, got %q", p.AuditReceipt, result.AuditReceipt)
		result.ExitCode = 1
	}

	audit.Write(audit.Event{
Kind:   audit.KindToolResult,
TaskID: p.ProtocolID,
Tool:   "orchestrator",
Input:  truncateOutput(p.PrimaryOperation, 256),
Output: truncateOutput(auditContent, 512),
OK:     result.Success,
Details: map[string]string{
"audit_receipt":    result.AuditReceipt,
"status_broadcast": p.StatusBroadcast,
"entity_selector":  truncateOutput(p.EntitySelector, 128),
"attempts":         strconv.Itoa(result.Attempts),
"failure_class":    string(result.FailureClass),
},
})

broadcast(p.StatusBroadcast, result)
return result
}

func truncateOutput(s string, n int) string {
if len(s) <= n {
return s
}
return s[:n] + "..."
}

// ProtocolStore is a goroutine-safe in-memory store for protocol execution results.
type ProtocolStore struct {
mu      sync.RWMutex
results map[string]ProtocolResult
}

func NewProtocolStore() *ProtocolStore {
return &ProtocolStore{results: make(map[string]ProtocolResult)}
}

func (s *ProtocolStore) Set(result ProtocolResult) {
s.mu.Lock()
defer s.mu.Unlock()
s.results[result.ProtocolID] = result
}

func (s *ProtocolStore) Get(protocolID string) (ProtocolResult, bool) {
s.mu.RLock()
defer s.mu.RUnlock()
r, ok := s.results[protocolID]
return r, ok
}

func (s *ProtocolStore) List() []ProtocolResult {
s.mu.RLock()
defer s.mu.RUnlock()
out := make([]ProtocolResult, 0, len(s.results))
for _, r := range s.results {
out = append(out, r)
}
return out
}

// BatchExecute runs a slice of Protocols concurrently up to maxParallel goroutines.
func BatchExecute(ctx context.Context, protocols []Protocol, reg *tools.Registry, maxParallel int, store *ProtocolStore) ProtocolBatchResult {
if maxParallel < 1 {
maxParallel = 4
}
results := make([]ProtocolResult, len(protocols))
sem := make(chan struct{}, maxParallel)
var wg sync.WaitGroup
for i, p := range protocols {
wg.Add(1)
go func(idx int, proto Protocol) {
defer wg.Done()
sem <- struct{}{}
defer func() { <-sem }()
r := Execute(ctx, proto, reg)
results[idx] = r
if store != nil {
store.Set(r)
}
}(i, p)
}
wg.Wait()
batch := ProtocolBatchResult{BatchSize: len(results), Results: results}
for _, r := range results {
if r.Success {
batch.Succeeded++
} else {
batch.Failed++
}
}
return batch
}

func cleanJSONRawNewlines(s string) string {
	var sb strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				sb.WriteByte(ch)
				continue
			}
			if ch == '\\' {
				escaped = true
				sb.WriteByte(ch)
				continue
			}
			if ch == '"' {
				inString = false
				sb.WriteByte(ch)
				continue
			}
			if ch == '\n' {
				sb.WriteString("\\n")
				continue
			}
			if ch == '\r' {
				sb.WriteString("\\r")
				continue
			}
			if ch == '\t' {
				sb.WriteString("\\t")
				continue
			}
			sb.WriteByte(ch)
		} else {
			if ch == '"' {
				inString = true
			}
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}
