// Package audit writes a tamper-evident append-only JSONL audit trail to
// logs/audit.jsonl.  Every agent action, tool call, task submission, and
// pipeline event is recorded here for HIPAA compliance review.
package audit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// EventKind labels the type of audit event.
type EventKind string

const (
	KindTaskSubmit    EventKind = "task_submit"
	KindTaskComplete  EventKind = "task_complete"
	KindAgentStart    EventKind = "agent_start"
	KindAgentDone     EventKind = "agent_done"
	KindToolCall      EventKind = "tool_call"
	KindToolResult    EventKind = "tool_result"
	KindAgentSpawn    EventKind = "agent_spawn"
	KindAgentDestroy  EventKind = "agent_destroy"
	KindJudgeVerdict  EventKind = "judge_verdict"
	KindReviewerRetry EventKind = "reviewer_retry"
	KindModelLoad     EventKind = "model_load"
	KindError         EventKind = "error"
)

// Event is one line in the audit log.
type Event struct {
	At       time.Time         `json:"at"`
	Kind     EventKind         `json:"kind"`
	TaskID   string            `json:"task_id,omitempty"`
	Agent    string            `json:"agent,omitempty"`
	Tool     string            `json:"tool,omitempty"`
	Input    string            `json:"input,omitempty"`
	Output   string            `json:"output,omitempty"`
	Score    int               `json:"score,omitempty"`
	OK       bool              `json:"ok"`
	Details  map[string]string `json:"details,omitempty"`
	PrevHMAC string            `json:"prev_hmac"`
	HMAC     string            `json:"hmac"`
}

// ChainSecret was the old exported HMAC key — replaced by the env-backed chainSecret below.
// chainSecret is the HMAC key for audit chain integrity.
// Set JANUS_AUDIT_SECRET in env. If unset in HIPAA mode, Init() will log a warning.
var chainSecret string

func getChainSecret() string {
	if chainSecret != "" {
		return chainSecret
	}
	if s := os.Getenv("JANUS_AUDIT_SECRET"); s != "" {
		return s
	}
	return "janus-audit-chain-CHANGE-AT-BUILD" // fallback only
}

// GetChainSecret returns the active HMAC chain secret. Used externally by packages
// that need to verify audit log entries (e.g. orchestration audit receipts).
func GetChainSecret() string { return getChainSecret() }

// hipaaMode controls whether inputs/outputs are redacted in audit entries.
// When true, raw text is replaced with a SHA-256 hash — PHI never sits in the log.
var hipaaMode atomic.Bool

// SetHIPAAMode enables or disables PHI redaction in the audit log.
// Call this at startup and whenever the compliance toggle changes.
func SetHIPAAMode(on bool) { hipaaMode.Store(on) }

// redactPHI replaces s with "[REDACTED:sha256:<hex>]" when HIPAA mode is on.
// This lets auditors verify a value was logged without exposing the raw text.
func redactPHI(s string) string {
	if !hipaaMode.Load() || s == "" {
		return s
	}
	h := sha256.Sum256([]byte(s))
	return "[REDACTED:sha256:" + hex.EncodeToString(h[:8]) + "…]"
}

// Logger is a goroutine-safe append-only JSONL audit writer.
type Logger struct {
	mu       sync.Mutex
	file     *os.File
	lastHMAC string // HMAC of previous entry for chaining
}

var defaultLogger *Logger
var initOnce sync.Once

// Close closes the audit log file handle. Exported for testing.
func Close() {
	if defaultLogger != nil && defaultLogger.file != nil {
		defaultLogger.file.Close()
	}
	defaultLogger = nil
	initOnce = sync.Once{}
}

// Init opens (or creates) the audit log file.  Safe to call multiple times.
func Init(dir string) {
	initOnce.Do(func() {
		if dir == "" {
			dir = "logs"
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("audit: cannot create %s: %v", dir, err)
			return
		}
		f, err := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			log.Printf("audit: cannot open audit.jsonl: %v", err)
			return
		}
		defaultLogger = &Logger{file: f}
		log.Printf("audit: logging to %s/audit.jsonl", dir)

		chainSecret = os.Getenv("JANUS_AUDIT_SECRET")
		if chainSecret == "" {
			if os.Getenv("JANUS_HIPAA_STRICT") == "true" {
				log.Printf("audit: WARNING — JANUS_AUDIT_SECRET is not set; audit chain is using the default insecure key. Set JANUS_AUDIT_SECRET to a random 32+ char value.")
			}
			chainSecret = "janus-audit-chain-CHANGE-AT-BUILD"
		}
	})
}

// Write appends one event to the audit log.  Silently drops if not initialised.
func Write(e Event) {
	if defaultLogger == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()

	// Chain: embed the previous line's HMAC, then compute this line's HMAC.
	e.PrevHMAC = defaultLogger.lastHMAC
	e.HMAC = "" // zero before computing
	payload, err := json.Marshal(e)
	if err != nil {
		return
	}
	mac := hmac.New(sha256.New, []byte(getChainSecret()))
	mac.Write(payload)
	e.HMAC = hex.EncodeToString(mac.Sum(nil))
	defaultLogger.lastHMAC = e.HMAC

	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	line = append(line, '\n')
	_, _ = defaultLogger.file.Write(line)
}

// Task shortcuts

func TaskSubmit(taskID, input string) {
	Write(Event{Kind: KindTaskSubmit, TaskID: taskID, Input: redactPHI(truncate(input, 512)), OK: true})
}

func TaskComplete(taskID, output string, ok bool) {
	Write(Event{Kind: KindTaskComplete, TaskID: taskID, Output: redactPHI(truncate(output, 512)), OK: ok})
}

func AgentStart(taskID, agent string) {
	Write(Event{Kind: KindAgentStart, TaskID: taskID, Agent: agent, OK: true})
}

func AgentDone(taskID, agent, summary string, ok bool) {
	Write(Event{Kind: KindAgentDone, TaskID: taskID, Agent: agent, Output: redactPHI(truncate(summary, 256)), OK: ok})
}

func ToolCall(taskID, agent, tool, input string) {
	Write(Event{Kind: KindToolCall, TaskID: taskID, Agent: agent, Tool: tool, Input: redactPHI(truncate(input, 256)), OK: true})
}

func ToolResult(taskID, agent, tool, output string, ok bool) {
	Write(Event{Kind: KindToolResult, TaskID: taskID, Agent: agent, Tool: tool, Output: redactPHI(truncate(output, 256)), OK: ok})
}

func AgentSpawn(taskID, parentAgent, childName, task string) {
	Write(Event{Kind: KindAgentSpawn, TaskID: taskID, Agent: parentAgent,
		Details: map[string]string{"child": childName, "task": redactPHI(truncate(task, 256))}, OK: true})
}

func JudgeVerdict(taskID string, score int, approved bool, feedback string) {
	Write(Event{Kind: KindJudgeVerdict, TaskID: taskID, Score: score, OK: approved,
		Output: truncate(feedback, 256)})
}

func ReviewerRetry(taskID string, attempt int, reason string) {
	Write(Event{Kind: KindReviewerRetry, TaskID: taskID, OK: false,
		Details: map[string]string{"attempt": itoa(attempt), "reason": truncate(reason, 256)}})
}

func ModelLoad(modelPath string, ok bool, detail string) {
	Write(Event{Kind: KindModelLoad, OK: ok,
		Details: map[string]string{"path": modelPath, "detail": truncate(detail, 256)}})
}

func Error(taskID, agent, msg string) {
	Write(Event{Kind: KindError, TaskID: taskID, Agent: agent, Output: truncate(msg, 512), OK: false})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

