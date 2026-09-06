package engine

import "time"

// BatonStatus is the lifecycle state of a task baton.
type BatonStatus string

const (
	BatonPending  BatonStatus = "pending"
	BatonComplete BatonStatus = "complete"
	BatonFailed   BatonStatus = "failed"
	BatonReview   BatonStatus = "review"
)

// BatonRoute is the Judge's routing decision after auditing Worker output.
type BatonRoute string

const (
	RouteNext       BatonRoute = "next"        // pass forward to next checkpoint
	RouteDeepReview BatonRoute = "deep_review" // send back to Worker with issues
	RouteFailed     BatonRoute = "failed"      // escalate to Master Architect
)

// Baton is the immutable relay-race context passed between agents.
//
// Unix philosophy: every agent receives exactly one Baton and returns exactly
// one Baton. Nothing else crosses agent boundaries — no shared memory, no
// global state. Agents call With* methods to produce a new Baton rather than
// mutating the one they received.
//
// Flow:
//
//	Master Architect → Worker → Judge ──pass──► Pruner → next checkpoint
//	                              └──review──► Worker  (retry, max 2x)
//	                              └──failed──► Master Architect (re-plan)
type Baton struct {
	TaskID     string      `json:"task_id"`
	Checkpoint string      `json:"checkpoint"`       // "plan" | "build" | "judge" | "prune"
	Spec       string      `json:"spec"`             // structured spec from Architect
	Input      string      `json:"input"`            // raw user input or retry instructions
	Output     string      `json:"output,omitempty"` // what this agent produced
	Status     BatonStatus `json:"status"`
	Next       string      `json:"next_checkpoint,omitempty"`
	Route      BatonRoute  `json:"route,omitempty"`
	Confidence float64     `json:"confidence,omitempty"` // Judge confidence 0.0–1.0
	Issues     []string    `json:"issues,omitempty"`     // Judge feedback for Worker retry
	Keep       []string    `json:"keep,omitempty"`       // Pruner: items that survive
	Retries    int         `json:"retries,omitempty"`    // Worker retry count
	CreatedAt  time.Time   `json:"created_at"`
}

// WithOutput returns a new Baton with the agent's output and status set.
func (b Baton) WithOutput(output string, status BatonStatus) Baton {
	out := b
	out.Output = output
	out.Status = status
	out.CreatedAt = time.Now()
	return out
}

// WithVerdict returns a new Baton carrying the Judge's routing decision.
func (b Baton) WithVerdict(route BatonRoute, confidence float64, issues, keep []string) Baton {
	out := b
	out.Route = route
	out.Confidence = confidence
	out.Issues = issues
	out.Keep = keep
	out.CreatedAt = time.Now()
	return out
}

// WithRetry returns a new Baton for sending back to the Worker.
// The previous Output is cleared so the Worker starts fresh with the issues.
func (b Baton) WithRetry(issues []string) Baton {
	out := b
	out.Status = BatonReview
	out.Issues = issues
	out.Output = ""
	out.Retries++
	out.CreatedAt = time.Now()
	return out
}
