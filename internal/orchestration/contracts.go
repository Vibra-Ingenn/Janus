package orchestration

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"
)

type ExecutionMode string

const (
	ExecutionModeLocalOnly      ExecutionMode = "local_only"
	ExecutionModeLocalPreferred ExecutionMode = "local_preferred"
	ExecutionModeCloudAllowed   ExecutionMode = "cloud_allowed"
)

type FailureClass string

const (
	FailureClassTransient         FailureClass = "transient"
	FailureClassPermanent         FailureClass = "permanent"
	FailureClassValidation        FailureClass = "validation"
	FailureClassTimeout           FailureClass = "timeout"
	FailureClassRateLimited       FailureClass = "rate_limited"
	FailureClassResourceExhausted FailureClass = "resource_exhausted"
	FailureClassPolicy            FailureClass = "policy"
	FailureClassOperatorRequired  FailureClass = "operator_required"
	FailureClassInfrastructure    FailureClass = "infrastructure"
)

type BatchJobStatus string

const (
	BatchJobStatusComplete         BatchJobStatus = "complete"
	BatchJobStatusPartial          BatchJobStatus = "partial"
	BatchJobStatusFailed           BatchJobStatus = "failed"
	BatchJobStatusAwaitingOperator BatchJobStatus = "awaiting_operator"
)

type BatchItemStatus string

const (
	BatchItemStatusComplete         BatchItemStatus = "complete"
	BatchItemStatusFailed           BatchItemStatus = "failed"
	BatchItemStatusAwaitingOperator BatchItemStatus = "awaiting_operator"
	BatchItemStatusCompensated      BatchItemStatus = "compensated"
)

type ResolutionAction string

const (
	ResolutionActionRetry          ResolutionAction = "retry"
	ResolutionActionOverrideOutput ResolutionAction = "override_output"
	ResolutionActionCompensate     ResolutionAction = "compensate"
	ResolutionActionFail           ResolutionAction = "fail"
)

type ToolReference struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

func (t ToolReference) CanonicalName() string {
	if t.Version == "" {
		return t.Name
	}
	return t.Name + "@" + t.Version
}

type SchemaField struct {
	Type        string                 `json:"type"`
	Description string                 `json:"description,omitempty"`
	Format      string                 `json:"format,omitempty"`
	Enum        []string               `json:"enum,omitempty"`
	Items       *SchemaField           `json:"items,omitempty"`
	Properties  map[string]SchemaField `json:"properties,omitempty"`
	Required    []string               `json:"required,omitempty"`
	Sensitive   bool                   `json:"sensitive,omitempty"`
}

type ToolSchema struct {
	Type                 string                 `json:"type"`
	Description          string                 `json:"description,omitempty"`
	Properties           map[string]SchemaField `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties bool                   `json:"additional_properties"`
}

type TimeoutPolicy struct {
	ScheduleToClose time.Duration `json:"schedule_to_close,omitempty"`
	StartToClose    time.Duration `json:"start_to_close,omitempty"`
	Heartbeat       time.Duration `json:"heartbeat,omitempty"`
}

func DefaultTimeoutPolicy() TimeoutPolicy {
	return TimeoutPolicy{
		ScheduleToClose: 10 * time.Minute,
		StartToClose:    2 * time.Minute,
	}
}

type RetryPolicy struct {
	MaxAttempts        int            `json:"max_attempts,omitempty"`
	InitialInterval    time.Duration  `json:"initial_interval,omitempty"`
	MaximumInterval    time.Duration  `json:"maximum_interval,omitempty"`
	ExpirationInterval time.Duration  `json:"expiration_interval,omitempty"`
	BackoffCoefficient float64        `json:"backoff_coefficient,omitempty"`
	JitterPercent      int            `json:"jitter_percent,omitempty"`
	EscalateAfter      int            `json:"escalate_after,omitempty"`
	NonRetryable       []FailureClass `json:"non_retryable,omitempty"`
	DeadLetterQueue    string         `json:"dead_letter_queue,omitempty"`
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:        5,
		InitialInterval:    2 * time.Second,
		MaximumInterval:    1 * time.Minute,
		ExpirationInterval: 15 * time.Minute,
		BackoffCoefficient: 2,
		JitterPercent:      15,
		EscalateAfter:      3,
		NonRetryable: []FailureClass{
			FailureClassPermanent,
			FailureClassValidation,
			FailureClassPolicy,
		},
	}
}

func (p RetryPolicy) Normalized() RetryPolicy {
	base := DefaultRetryPolicy()
	if p.MaxAttempts > 0 {
		base.MaxAttempts = p.MaxAttempts
	}
	if p.InitialInterval > 0 {
		base.InitialInterval = p.InitialInterval
	}
	if p.MaximumInterval > 0 {
		base.MaximumInterval = p.MaximumInterval
	}
	if p.ExpirationInterval > 0 {
		base.ExpirationInterval = p.ExpirationInterval
	}
	if p.BackoffCoefficient > 0 {
		base.BackoffCoefficient = p.BackoffCoefficient
	}
	if p.JitterPercent > 0 {
		base.JitterPercent = p.JitterPercent
	}
	if p.EscalateAfter > 0 {
		base.EscalateAfter = p.EscalateAfter
	}
	if len(p.NonRetryable) > 0 {
		base.NonRetryable = append([]FailureClass(nil), p.NonRetryable...)
	}
	if p.DeadLetterQueue != "" {
		base.DeadLetterQueue = p.DeadLetterQueue
	}
	if base.MaximumInterval < base.InitialInterval {
		base.MaximumInterval = base.InitialInterval
	}
	return base
}

func (p RetryPolicy) Validate() error {
	p = p.Normalized()
	if p.MaxAttempts < 1 {
		return fmt.Errorf("retry policy max_attempts must be >= 1")
	}
	if p.InitialInterval <= 0 {
		return fmt.Errorf("retry policy initial_interval must be > 0")
	}
	if p.MaximumInterval < p.InitialInterval {
		return fmt.Errorf("retry policy maximum_interval must be >= initial_interval")
	}
	if p.BackoffCoefficient < 1 {
		return fmt.Errorf("retry policy backoff_coefficient must be >= 1")
	}
	if p.JitterPercent < 0 || p.JitterPercent > 100 {
		return fmt.Errorf("retry policy jitter_percent must be between 0 and 100")
	}
	if p.EscalateAfter > p.MaxAttempts {
		return fmt.Errorf("retry policy escalate_after must be <= max_attempts")
	}
	return nil
}

func (p RetryPolicy) IsRetryable(class FailureClass) bool {
	for _, nonRetryable := range p.Normalized().NonRetryable {
		if class == nonRetryable {
			return false
		}
	}
	switch class {
	case FailureClassTransient, FailureClassTimeout, FailureClassRateLimited, FailureClassResourceExhausted, FailureClassInfrastructure:
		return true
	default:
		return false
	}
}

func (p RetryPolicy) CanRetry(attempt int, elapsed time.Duration, class FailureClass) bool {
	p = p.Normalized()
	if !p.IsRetryable(class) || attempt >= p.MaxAttempts {
		return false
	}
	if p.ExpirationInterval > 0 && elapsed >= p.ExpirationInterval {
		return false
	}
	return true
}

func (p RetryPolicy) BackoffDelay(attempt int, seed string) time.Duration {
	p = p.Normalized()
	if attempt < 1 {
		attempt = 1
	}
	backoff := float64(p.InitialInterval) * math.Pow(p.BackoffCoefficient, float64(attempt-1))
	if max := float64(p.MaximumInterval); backoff > max {
		backoff = max
	}
	if p.JitterPercent == 0 {
		return time.Duration(backoff)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	_, _ = h.Write([]byte(fmt.Sprintf(":%d", attempt)))
	jitterWindow := float64(p.JitterPercent) / 100
	normalized := float64(h.Sum32()%10001) / 10000
	factor := (1 - jitterWindow) + (2*jitterWindow)*normalized
	return time.Duration(backoff * factor)
}

type FallbackEdge struct {
	Tool   ToolReference  `json:"tool"`
	On     []FailureClass `json:"on"`
	Reason string         `json:"reason,omitempty"`
}

type CompensationPlan struct {
	Tool     ToolReference   `json:"tool"`
	Input    json.RawMessage `json:"input,omitempty"`
	Required bool            `json:"required,omitempty"`
}

type ToolContract struct {
	Name                 string         `json:"name"`
	Version              string         `json:"version,omitempty"`
	Category             string         `json:"category,omitempty"`
	Description          string         `json:"description"`
	Tags                 []string       `json:"tags,omitempty"`
	Deterministic        bool           `json:"deterministic"`
	Idempotent           bool           `json:"idempotent"`
	RequiresAudit        bool           `json:"requires_audit,omitempty"`
	RequiresPHIRedaction bool           `json:"requires_phi_redaction,omitempty"`
	ExecutionMode        ExecutionMode  `json:"execution_mode,omitempty"`
	InputSchema          ToolSchema     `json:"input_schema"`
	OutputSchema         ToolSchema     `json:"output_schema"`
	TimeoutPolicy        TimeoutPolicy  `json:"timeout_policy,omitempty"`
	RetryPolicy          RetryPolicy    `json:"retry_policy,omitempty"`
	Fallbacks            []FallbackEdge `json:"fallbacks,omitempty"`
}

func (c ToolContract) Reference() ToolReference {
	return ToolReference{Name: c.Name, Version: c.Version}
}

func (c ToolContract) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("tool contract name is required")
	}
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("tool contract %q description is required", c.Name)
	}
	if !c.Idempotent {
		return fmt.Errorf("tool contract %q must be idempotent for durable retries", c.Name)
	}
	if c.InputSchema.Type == "" || c.OutputSchema.Type == "" {
		return fmt.Errorf("tool contract %q must define input and output schemas", c.Name)
	}
	if err := c.RetryPolicy.Validate(); err != nil {
		return fmt.Errorf("tool contract %q retry policy invalid: %w", c.Name, err)
	}
	return nil
}

type BatchItem struct {
	ItemID         string            `json:"item_id"`
	Tool           ToolReference     `json:"tool"`
	Input          json.RawMessage   `json:"input"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Retry          *RetryPolicy      `json:"retry,omitempty"`
	Fallbacks      []FallbackEdge    `json:"fallbacks,omitempty"`
	Compensation   *CompensationPlan `json:"compensation,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type BatchJobRequest struct {
	BatchID                 string            `json:"batch_id"`
	WorkflowKey             string            `json:"workflow_key,omitempty"`
	TenantID                string            `json:"tenant_id,omitempty"`
	ActorID                 string            `json:"actor_id,omitempty"`
	HIPAAStrict             bool              `json:"hipaa_strict"`
	RequireAuditTrail       bool              `json:"require_audit_trail"`
	RequireManualResolution bool              `json:"require_manual_resolution"`
	OperatorTimeout         time.Duration     `json:"operator_timeout,omitempty"`
	MaxParallelism          int               `json:"max_parallelism,omitempty"`
	DefaultRetryPolicy      RetryPolicy       `json:"default_retry_policy,omitempty"`
	Items                   []BatchItem       `json:"items"`
	Metadata                map[string]string `json:"metadata,omitempty"`
}

func (r BatchJobRequest) Validate() error {
	if strings.TrimSpace(r.BatchID) == "" {
		return fmt.Errorf("batch_id is required")
	}
	if len(r.Items) == 0 {
		return fmt.Errorf("batch job %q has no items", r.BatchID)
	}
	if err := r.DefaultRetryPolicy.Validate(); err != nil {
		return fmt.Errorf("batch job %q default retry policy invalid: %w", r.BatchID, err)
	}
	return nil
}

type ToolInvocationRequest struct {
	WorkflowID     string          `json:"workflow_id"`
	BatchID        string          `json:"batch_id"`
	ItemID         string          `json:"item_id"`
	RunID          string          `json:"run_id,omitempty"`
	TenantID       string          `json:"tenant_id,omitempty"`
	ActorID        string          `json:"actor_id,omitempty"`
	Tool           ToolReference   `json:"tool"`
	IdempotencyKey string          `json:"idempotency_key"`
	ExecutionMode  ExecutionMode   `json:"execution_mode"`
	RequireAudit   bool            `json:"require_audit"`
	Attempt        int             `json:"attempt"`
	Payload        json.RawMessage `json:"payload"`
}

type ArtifactRef struct {
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	ContentType string `json:"content_type,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

type ToolInvocationResult struct {
	Tool         ToolReference   `json:"tool"`
	Success      bool            `json:"success"`
	FailureClass FailureClass    `json:"failure_class,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	Artifacts    []ArtifactRef   `json:"artifacts,omitempty"`
	StartedAt    time.Time       `json:"started_at,omitempty"`
	FinishedAt   time.Time       `json:"finished_at,omitempty"`
}

type AttemptRecord struct {
	Attempt      int           `json:"attempt"`
	Tool         ToolReference `json:"tool"`
	Success      bool          `json:"success"`
	FailureClass FailureClass  `json:"failure_class,omitempty"`
	Summary      string        `json:"summary,omitempty"`
	ErrorMessage string        `json:"error_message,omitempty"`
	Artifacts    []ArtifactRef `json:"artifacts,omitempty"`
	StartedAt    time.Time     `json:"started_at,omitempty"`
	FinishedAt   time.Time     `json:"finished_at,omitempty"`
}

type OperatorResolution struct {
	ItemID         string           `json:"item_id"`
	Action         ResolutionAction `json:"action"`
	OperatorID     string           `json:"operator_id,omitempty"`
	Reason         string           `json:"reason,omitempty"`
	ToolOverride   ToolReference    `json:"tool_override,omitempty"`
	InputOverride  json.RawMessage  `json:"input_override,omitempty"`
	OutputOverride json.RawMessage  `json:"output_override,omitempty"`
}

type BatchItemResult struct {
	ItemID       string              `json:"item_id"`
	Status       BatchItemStatus     `json:"status"`
	FinalTool    ToolReference       `json:"final_tool,omitempty"`
	Output       json.RawMessage     `json:"output,omitempty"`
	FailureClass FailureClass        `json:"failure_class,omitempty"`
	Error        string              `json:"error,omitempty"`
	Attempts     []AttemptRecord     `json:"attempts,omitempty"`
	Artifacts    []ArtifactRef       `json:"artifacts,omitempty"`
	Resolution   *OperatorResolution `json:"resolution,omitempty"`
}

type BatchJobResult struct {
	BatchID          string            `json:"batch_id"`
	Status           BatchJobStatus    `json:"status"`
	TotalItems       int               `json:"total_items"`
	CompletedItems   int               `json:"completed_items"`
	FailedItems      int               `json:"failed_items"`
	AwaitingOperator int               `json:"awaiting_operator"`
	Results          []BatchItemResult `json:"results"`
}

func MergeRetryPolicy(base RetryPolicy, override *RetryPolicy) RetryPolicy {
	merged := base.Normalized()
	if override == nil {
		return merged
	}
	if override.MaxAttempts > 0 {
		merged.MaxAttempts = override.MaxAttempts
	}
	if override.InitialInterval > 0 {
		merged.InitialInterval = override.InitialInterval
	}
	if override.MaximumInterval > 0 {
		merged.MaximumInterval = override.MaximumInterval
	}
	if override.ExpirationInterval > 0 {
		merged.ExpirationInterval = override.ExpirationInterval
	}
	if override.BackoffCoefficient > 0 {
		merged.BackoffCoefficient = override.BackoffCoefficient
	}
	if override.JitterPercent > 0 {
		merged.JitterPercent = override.JitterPercent
	}
	if override.EscalateAfter > 0 {
		merged.EscalateAfter = override.EscalateAfter
	}
	if len(override.NonRetryable) > 0 {
		merged.NonRetryable = append([]FailureClass(nil), override.NonRetryable...)
	}
	if override.DeadLetterQueue != "" {
		merged.DeadLetterQueue = override.DeadLetterQueue
	}
	if merged.MaximumInterval < merged.InitialInterval {
		merged.MaximumInterval = merged.InitialInterval
	}
	return merged
}

func SelectFallback(fallbacks []FallbackEdge, class FailureClass, used map[string]struct{}) (ToolReference, bool) {
	for _, fallback := range fallbacks {
		if fallback.Tool.Name == "" {
			continue
		}
		if _, seen := used[fallback.Tool.CanonicalName()]; seen {
			continue
		}
		if len(fallback.On) == 0 {
			return fallback.Tool, true
		}
		for _, allowed := range fallback.On {
			if allowed == class {
				return fallback.Tool, true
			}
		}
	}
	return ToolReference{}, false
}

func AggregateBatchStatus(results []BatchItemResult) BatchJobStatus {
	if len(results) == 0 {
		return BatchJobStatusFailed
	}
	var complete, failed, waiting int
	for _, result := range results {
		switch result.Status {
		case BatchItemStatusComplete, BatchItemStatusCompensated:
			complete++
		case BatchItemStatusAwaitingOperator:
			waiting++
		default:
			failed++
		}
	}
	switch {
	case waiting > 0:
		return BatchJobStatusAwaitingOperator
	case failed == 0:
		return BatchJobStatusComplete
	case complete > 0:
		return BatchJobStatusPartial
	default:
		return BatchJobStatusFailed
	}
}

func stableKeys(m map[string]SchemaField) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

