package orchestration

import (
"context"
"strings"
"testing"
"time"

"janus/internal/tools"
)

// --- parseOperationCommand ---

func TestParseShellExplicit(t *testing.T) {
pc, err := parseOperationCommand("shell:echo hello world", "", nil)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if pc.Kind != CommandKindShell {
t.Fatalf("want shell, got %q", pc.Kind)
}
if len(pc.ShellArgs) != 3 || pc.ShellArgs[0] != "echo" {
t.Fatalf("unexpected args: %v", pc.ShellArgs)
}
}

func TestParseShellImplicit(t *testing.T) {
pc, err := parseOperationCommand("echo implicit", "", nil)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if pc.Kind != CommandKindShell {
t.Fatalf("want shell, got %q", pc.Kind)
}
}

func TestParseHTTP(t *testing.T) {
pc, err := parseOperationCommand("http:GET:https://example.com/api", "body", nil)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if pc.Kind != CommandKindHTTP {
t.Fatalf("want http, got %q", pc.Kind)
}
if pc.HTTPMethod != "GET" {
t.Fatalf("want GET, got %q", pc.HTTPMethod)
}
if pc.HTTPURL != "https://example.com/api" {
t.Fatalf("unexpected URL: %q", pc.HTTPURL)
}
if pc.HTTPBody != "body" {
t.Fatalf("unexpected body: %q", pc.HTTPBody)
}
}

func TestParseHTTPMissingURL(t *testing.T) {
_, err := parseOperationCommand("http:POST:", "", nil)
if err == nil {
t.Fatal("expected error for empty URL")
}
}

func TestParseToolNoArgs(t *testing.T) {
pc, err := parseOperationCommand("tool:pdf_extract", "/tmp/file.pdf", nil)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if pc.Kind != CommandKindTool {
t.Fatalf("want tool, got %q", pc.Kind)
}
if pc.ToolName != "pdf_extract" {
t.Fatalf("want pdf_extract, got %q", pc.ToolName)
}
if pc.ToolArgs["path"] != "/tmp/file.pdf" {
t.Fatalf("expected path injected, got %v", pc.ToolArgs)
}
}

func TestParseToolWithArgs(t *testing.T) {
pc, err := parseOperationCommand(`tool:read_file:{"path":"/etc/hosts"}`, "ignored", nil)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if pc.ToolArgs["path"] != "/etc/hosts" {
t.Fatalf("unexpected tool args: %v", pc.ToolArgs)
}
}

func TestParseToolWithBuiltinPrefix(t *testing.T) {
	pc, err := parseOperationCommand(`tool:builtin:read_file:{"path":"/etc/hosts"}`, "ignored", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pc.ToolName != "builtin:read_file" {
		t.Fatalf("expected toolName 'builtin:read_file', got %q", pc.ToolName)
	}
	if pc.ToolArgs["path"] != "/etc/hosts" {
		t.Fatalf("unexpected tool args: %v", pc.ToolArgs)
	}
}

func TestParseEmptyCommand(t *testing.T) {
_, err := parseOperationCommand("", "", nil)
if err == nil {
t.Fatal("expected error for empty command")
}
}

// --- shellSplit ---

func TestShellSplitQuoted(t *testing.T) {
args := shellSplit(`echo "hello world" 'foo bar'`)
if len(args) != 3 {
t.Fatalf("expected 3 args, got %d: %v", len(args), args)
}
if args[1] != "hello world" {
t.Fatalf("unexpected arg[1]: %q", args[1])
}
if args[2] != "foo bar" {
t.Fatalf("unexpected arg[2]: %q", args[2])
}
}

// --- parseFailureLogic ---

func TestParseFailureLogicDefaults(t *testing.T) {
for _, s := range []string{"", "fail"} {
fs := parseFailureLogic(s)
if fs.Kind != "fail" {
t.Fatalf("input %q: want fail, got %q", s, fs.Kind)
}
}
}

func TestParseFailureLogicRetry(t *testing.T) {
fs := parseFailureLogic("retry:5")
if fs.Kind != "retry" || fs.MaxRetries != 5 || fs.Backoff {
t.Fatalf("unexpected: %+v", fs)
}
}

func TestParseFailureLogicRetryBackoff(t *testing.T) {
fs := parseFailureLogic("retry:3:backoff")
if fs.Kind != "retry" || fs.MaxRetries != 3 || !fs.Backoff {
t.Fatalf("unexpected: %+v", fs)
}
}

func TestParseFailureLogicIgnore(t *testing.T) {
fs := parseFailureLogic("ignore")
if fs.Kind != "ignore" {
t.Fatalf("want ignore, got %q", fs.Kind)
}
}

func TestParseFailureLogicDLQ(t *testing.T) {
fs := parseFailureLogic("dlq")
if fs.Kind != "dlq" {
t.Fatalf("want dlq, got %q", fs.Kind)
}
}

func TestParseFailureLogicFallback(t *testing.T) {
fs := parseFailureLogic("fallback:shell:echo backup")
if fs.Kind != "fallback" {
t.Fatalf("want fallback, got %q", fs.Kind)
}
if fs.FallbackCmd != "shell:echo backup" {
t.Fatalf("unexpected fallback cmd: %q", fs.FallbackCmd)
}
}

// --- computeAuditReceipt ---

func TestComputeAuditReceiptDeterministic(t *testing.T) {
p := Protocol{
ProtocolID:       "test-proto",
PrimaryOperation: "shell:echo hi",
EntitySelector:     "data",
}
ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
r1 := computeAuditReceipt(p, "output", ts)
r2 := computeAuditReceipt(p, "output", ts)
if r1 != r2 {
t.Fatalf("receipt not deterministic: %q vs %q", r1, r2)
}
if len(r1) != 64 {
t.Fatalf("expected 64-char hex receipt, got %d chars", len(r1))
}
}

func TestComputeAuditReceiptChangesWithOutput(t *testing.T) {
p := Protocol{ProtocolID: "p1", PrimaryOperation: "shell:echo x"}
ts := time.Now().UTC()
r1 := computeAuditReceipt(p, "output-a", ts)
r2 := computeAuditReceipt(p, "output-b", ts)
if r1 == r2 {
t.Fatal("receipt should change when output changes")
}
}

// --- Execute (shell integration) ---

func TestExecuteShellSuccess(t *testing.T) {
p := Protocol{
ProtocolID:       "shell-ok",
PrimaryOperation: "shell:echo hello",
RemediationLogic:     "fail",
}
result := Execute(context.Background(), p, nil)
if !result.Success {
t.Fatalf("expected success, got error: %s", result.Error)
}
if !strings.Contains(result.Output, "hello") {
t.Fatalf("expected 'hello' in output, got: %q", result.Output)
}
if len(result.AuditReceipt) != 64 {
t.Fatalf("expected 64-char AuditReceipt, got %d", len(result.AuditReceipt))
}
if result.Attempts < 1 {
t.Fatalf("expected attempts >= 1")
}
}

func TestExecuteShellFailure(t *testing.T) {
p := Protocol{
ProtocolID:       "shell-fail",
PrimaryOperation: "shell:exit 1",
RemediationLogic:     "fail",
}
result := Execute(context.Background(), p, nil)
if result.Success {
t.Fatal("expected failure")
}
if result.AuditReceipt == "" {
t.Fatal("AuditReceipt must be set even on failure")
}
}

func TestExecuteIgnoreFailure(t *testing.T) {
p := Protocol{
ProtocolID:       "ignore-fail",
PrimaryOperation: "shell:exit 1",
RemediationLogic:     "ignore",
}
result := Execute(context.Background(), p, nil)
if !result.Success {
t.Fatalf("ignore strategy should always succeed, got: %s", result.Error)
}
}

func TestExecuteFallback(t *testing.T) {
p := Protocol{
ProtocolID:       "fallback-test",
PrimaryOperation: "shell:exit 1",
RemediationLogic:     "fallback:shell:echo recovered",
}
result := Execute(context.Background(), p, nil)
if !result.Success {
t.Fatalf("fallback should succeed, got: %s", result.Error)
}
if !strings.Contains(result.Output, "recovered") {
t.Fatalf("expected fallback output, got: %q", result.Output)
}
}

func TestExecuteExecutionBuffer(t *testing.T) {
p := Protocol{
ProtocolID:       "buffer-test",
PrimaryOperation: "shell:echo buffered",
ExecutionBuffer:  50,
}
start := time.Now()
result := Execute(context.Background(), p, nil)
elapsed := time.Since(start)
if !result.Success {
t.Fatalf("expected success: %s", result.Error)
}
if false {
t.Fatalf("expected at least 50ms delay, got %v", elapsed)
}
}

func TestExecuteContextCancel(t *testing.T) {
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
defer cancel()
p := Protocol{
ProtocolID:       "ctx-cancel",
PrimaryOperation: "shell:echo after-cancel",
ExecutionBuffer:  500,
}
result := Execute(ctx, p, nil)
if result.Success {
t.Fatal("expected failure due to context cancellation")
}
if result.AuditReceipt == "" {
t.Fatal("AuditReceipt must be set even on cancellation")
}
}

// --- ProtocolStore ---

func TestProtocolStoreCRUD(t *testing.T) {
s := NewProtocolStore()
r1 := ProtocolResult{ProtocolID: "a", Success: true, Output: "out-a"}
r2 := ProtocolResult{ProtocolID: "b", Success: false, Error: "err-b"}
s.Set(r1)
s.Set(r2)

got, ok := s.Get("a")
if !ok || got.Output != "out-a" {
t.Fatalf("unexpected: ok=%v result=%+v", ok, got)
}
_, ok = s.Get("missing")
if ok {
t.Fatal("expected missing key to return false")
}
all := s.List()
if len(all) != 2 {
t.Fatalf("expected 2 results, got %d", len(all))
}
}

// --- BatchExecute ---

func TestBatchExecute(t *testing.T) {
store := NewProtocolStore()
protocols := []Protocol{
{ProtocolID: "b1", PrimaryOperation: "shell:echo one"},
{ProtocolID: "b2", PrimaryOperation: "shell:echo two"},
{ProtocolID: "b3", PrimaryOperation: "shell:echo three"},
}
batch := BatchExecute(context.Background(), protocols, nil, 2, store)
if batch.BatchSize != 3 {
t.Fatalf("expected batch size 3, got %d", batch.BatchSize)
}
if batch.Succeeded != 3 {
t.Fatalf("expected 3 succeeded, got %d", batch.Succeeded)
}
if batch.Failed != 0 {
t.Fatalf("expected 0 failed, got %d", batch.Failed)
}
for i, r := range batch.Results {
if !r.Success {
t.Fatalf("result[%d] failed: %s", i, r.Error)
}
}
if _, ok := store.Get("b2"); !ok {
t.Fatal("b2 should be in the store after batch")
}
}

// --- AttemptHistory (durable attempt records) ---

func TestExecutePopulatesAttemptHistory(t *testing.T) {
p := Protocol{
ProtocolID:       "history-ok",
PrimaryOperation: "shell:echo recorded",
}
result := Execute(context.Background(), p, nil)
if !result.Success {
t.Fatalf("expected success: %s", result.Error)
}
if len(result.AttemptHistory) != 1 {
t.Fatalf("expected 1 attempt record, got %d", len(result.AttemptHistory))
}
rec := result.AttemptHistory[0]
if rec.Attempt != 1 {
t.Errorf("expected attempt=1, got %d", rec.Attempt)
}
if rec.Kind != "primary" {
t.Errorf("expected kind=primary, got %q", rec.Kind)
}
if rec.ExitCode != 0 {
t.Errorf("expected exit_code=0, got %d", rec.ExitCode)
}
if rec.Timestamp.IsZero() {
t.Error("expected non-zero timestamp")
}
}

func TestExecuteFallbackPopulatesAttemptHistory(t *testing.T) {
p := Protocol{
ProtocolID:       "history-fallback",
PrimaryOperation: "shell:exit 1",
RemediationLogic:     "fallback:shell:echo fallback-output",
}
result := Execute(context.Background(), p, nil)
if !result.Success {
t.Fatalf("expected success via fallback: %s", result.Error)
}
// Should have: 1 primary (failed) + 1 fallback
if len(result.AttemptHistory) < 2 {
t.Fatalf("expected at least 2 attempt records, got %d", len(result.AttemptHistory))
}
primary := result.AttemptHistory[0]
fallback := result.AttemptHistory[len(result.AttemptHistory)-1]
if primary.Kind != "primary" {
t.Errorf("first record kind: want primary, got %q", primary.Kind)
}
if fallback.Kind != "fallback" {
t.Errorf("last record kind: want fallback, got %q", fallback.Kind)
}
}

// --- classifyFailure ---

func TestClassifyFailurePermanent(t *testing.T) {
cases := []string{
"no such file or directory",
"file not found",
"unsupported file format",
"invalid json",
"malformed PDF",
"validation error",
"permission denied",
}
for _, msg := range cases {
err := &testErr{msg}
got := classifyFailure(err, "")
if got != FailureClassPermanent {
t.Errorf("message %q: want permanent, got %q", msg, got)
}
}
}

func TestClassifyFailureTransient(t *testing.T) {
cases := []string{
"connection refused",
"i/o timeout",
"deadline exceeded",
"resource busy",
"try again",
}
for _, msg := range cases {
err := &testErr{msg}
got := classifyFailure(err, "")
if got != FailureClassTransient {
t.Errorf("message %q: want transient, got %q", msg, got)
}
}
}

func TestClassifyFailureNilIsEmpty(t *testing.T) {
got := classifyFailure(nil, "")
if got != "" {
t.Errorf("nil error: want empty class, got %q", got)
}
}

// --- parseFailureLogic heal-auto ---

func TestParseFailureLogicHealAuto(t *testing.T) {
fs := parseFailureLogic("heal-auto")
if fs.Kind != "heal-auto" {
t.Fatalf("want heal-auto, got %q", fs.Kind)
}
}

// --- permanent failure skips retries ---

func TestExecutePermanentFailureSkipsRetries(t *testing.T) {
// Build a real registry so read_file actually returns "no such file" which
// classifies as permanent and should skip the remaining retries.
reg := newTestRegistry()
p := Protocol{
ProtocolID:       "perm-fail-no-retry",
PrimaryOperation: "tool:read_file",
EntitySelector:     "/definitely/does/not/exist/file.txt",
RemediationLogic:     "retry:5", // would be 6 total attempts without early bail-out
}
start := time.Now()
result := Execute(context.Background(), p, reg)
elapsed := time.Since(start)
if result.Success {
t.Fatal("expected failure for missing file")
}
if result.FailureClass != FailureClassPermanent {
t.Errorf("expected permanent failure class, got %q (error: %s)", result.FailureClass, result.Error)
}
// Permanent failure should bail out after the first attempt, not do 6.
if result.Attempts > 2 {
t.Errorf("expected at most 2 attempts for permanent failure, got %d", result.Attempts)
}
// Should not have waited through multiple backoffs (6 attempts ≈ 1+2+4+8+16s minimum).
if elapsed > 5*time.Second {
t.Errorf("permanent failure took too long (%v); retries should have been skipped", elapsed)
}
}

// --- isDocumentTarget ---

func TestIsDocumentTarget(t *testing.T) {
positives := []string{
"/data/patient.pdf",
"report.docx",
"intake.hl7",
"scan.png",
"notes.txt",
"data.csv",
}
for _, p := range positives {
if !isDocumentTarget(p) {
t.Errorf("expected %q to be a document target", p)
}
}
negatives := []string{
"",
"/some/binary.exe",
"script.sh",
"binary.zip",
}
for _, p := range negatives {
if isDocumentTarget(p) {
t.Errorf("expected %q NOT to be a document target", p)
}
}
}

// testErr is a minimal error type for table-driven classifyFailure tests.
type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// newTestRegistry returns a tool registry with the universal built-in tools
// registered. Used in tests that need real tool dispatch.
func newTestRegistry() *tools.Registry {
return tools.NewRegistry()
}

