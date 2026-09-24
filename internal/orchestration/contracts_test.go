package orchestration

import (
	"strings"
	"testing"
	"time"

	"janus/internal/tools"
)

func TestRetryPolicyBackoffDeterministic(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:        4,
		InitialInterval:    2 * time.Second,
		MaximumInterval:    30 * time.Second,
		BackoffCoefficient: 2,
		JitterPercent:      10,
	}

	first := policy.BackoffDelay(2, "batch:item")
	second := policy.BackoffDelay(2, "batch:item")
	if first != second {
		t.Fatalf("expected deterministic backoff, got %v and %v", first, second)
	}
	if first < 3600*time.Millisecond || first > 4400*time.Millisecond {
		t.Fatalf("expected backoff with 10%% jitter around 4s, got %v", first)
	}
}

func TestRetryPolicyCanRetry(t *testing.T) {
	policy := DefaultRetryPolicy()
	if !policy.CanRetry(1, time.Second, FailureClassTransient) {
		t.Fatal("transient failures should be retryable")
	}
	if policy.CanRetry(1, time.Second, FailureClassValidation) {
		t.Fatal("validation failures should not be retryable")
	}
}

func TestMergeRetryPolicyPreservesBaseValues(t *testing.T) {
	base := RetryPolicy{
		MaxAttempts:        7,
		InitialInterval:    9 * time.Second,
		MaximumInterval:    45 * time.Second,
		ExpirationInterval: 20 * time.Minute,
		BackoffCoefficient: 3,
		JitterPercent:      20,
		EscalateAfter:      4,
		NonRetryable:       []FailureClass{FailureClassValidation},
	}
	override := &RetryPolicy{MaxAttempts: 2}

	merged := MergeRetryPolicy(base, override)
	if merged.MaxAttempts != 2 {
		t.Fatalf("expected override max attempts, got %d", merged.MaxAttempts)
	}
	if merged.InitialInterval != 9*time.Second {
		t.Fatalf("expected base initial interval to survive merge, got %v", merged.InitialInterval)
	}
	if merged.BackoffCoefficient != 3 {
		t.Fatalf("expected base backoff coefficient to survive merge, got %v", merged.BackoffCoefficient)
	}
}

func TestToolContractValidateRequiresIdempotency(t *testing.T) {
	contract := ToolContract{
		Name:          "unsafe_tool",
		Description:   "test",
		Deterministic: true,
		Idempotent:    false,
		InputSchema:   ToolSchema{Type: "object"},
		OutputSchema:  ToolSchema{Type: "object"},
		RetryPolicy:   DefaultRetryPolicy(),
	}
	err := contract.Validate()
	if err == nil || !strings.Contains(err.Error(), "idempotent") {
		t.Fatalf("expected idempotency validation error, got %v", err)
	}
}

func TestDraftContractFromToolDef(t *testing.T) {
	def := tools.ToolDef{
		Name:        "run_command",
		Description: "Execute a shell command",
		Parameters: map[string]string{
			"command":     "Shell command",
			"working_dir": "Optional working directory",
		},
	}

	contract := DraftContractFromToolDef(def, DraftContractOptions{
		Version:       "v1",
		Category:      "system",
		ExecutionMode: ExecutionModeLocalOnly,
		RequiresAudit: true,
	})

	if err := contract.Validate(); err != nil {
		t.Fatalf("draft contract should validate: %v", err)
	}
	if contract.InputSchema.Properties["command"].Type != "string" {
		t.Fatalf("expected command field to default to string, got %q", contract.InputSchema.Properties["command"].Type)
	}
	if !contract.RequiresAudit {
		t.Fatal("expected requires_audit to be preserved")
	}
}

func TestSelectFallback(t *testing.T) {
	fallbacks := []FallbackEdge{
		{Tool: ToolReference{Name: "tool-b"}, On: []FailureClass{FailureClassTimeout}},
		{Tool: ToolReference{Name: "tool-c"}, On: []FailureClass{FailureClassValidation}},
	}

	tool, ok := SelectFallback(fallbacks, FailureClassTimeout, map[string]struct{}{})
	if !ok {
		t.Fatal("expected timeout fallback")
	}
	if tool.Name != "tool-b" {
		t.Fatalf("expected tool-b, got %q", tool.Name)
	}
}

