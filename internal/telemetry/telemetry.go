// Package telemetry provides lightweight in-process counters for Janus.
// No data ever leaves the process — this is strictly local observability.
package telemetry

import (
	"sync"
	"sync/atomic"
	"time"
)

// Collector accumulates operational metrics across the lifetime of the server.
type Collector struct {
	startedAt time.Time

	TasksSubmitted atomic.Int64
	TasksCompleted atomic.Int64
	TasksFailed    atomic.Int64

	ChatRequests  atomic.Int64
	ChatTokensIn  atomic.Int64
	ChatTokensOut atomic.Int64

	SwarmRuns      atomic.Int64
	SwarmCompleted atomic.Int64
	SwarmFailed    atomic.Int64

	ToolCalls atomic.Int64

	// latency tracking (nanoseconds)
	latMu    sync.Mutex
	latSum   int64
	latCount int64
}

// New creates a Collector anchored to now.
func New() *Collector {
	return &Collector{startedAt: time.Now()}
}

// RecordLatency records a single operation latency.
func (c *Collector) RecordLatency(d time.Duration) {
	c.latMu.Lock()
	c.latSum += d.Nanoseconds()
	c.latCount++
	c.latMu.Unlock()
}

// Snapshot is the JSON-serializable telemetry response.
type Snapshot struct {
	UptimeSeconds  float64 `json:"uptime_seconds"`
	TasksSubmitted int64   `json:"tasks_submitted"`
	TasksCompleted int64   `json:"tasks_completed"`
	TasksFailed    int64   `json:"tasks_failed"`
	ChatRequests   int64   `json:"chat_requests"`
	ChatTokensIn   int64   `json:"chat_tokens_in"`
	ChatTokensOut  int64   `json:"chat_tokens_out"`
	SwarmRuns      int64   `json:"swarm_runs"`
	SwarmCompleted int64   `json:"swarm_completed"`
	SwarmFailed    int64   `json:"swarm_failed"`
	ToolCalls      int64   `json:"tool_calls"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
}

// Snap returns a point-in-time snapshot.
func (c *Collector) Snap() Snapshot {
	c.latMu.Lock()
	var avgMs float64
	if c.latCount > 0 {
		avgMs = float64(c.latSum) / float64(c.latCount) / 1e6
	}
	c.latMu.Unlock()

	return Snapshot{
		UptimeSeconds:  time.Since(c.startedAt).Seconds(),
		TasksSubmitted: c.TasksSubmitted.Load(),
		TasksCompleted: c.TasksCompleted.Load(),
		TasksFailed:    c.TasksFailed.Load(),
		ChatRequests:   c.ChatRequests.Load(),
		ChatTokensIn:   c.ChatTokensIn.Load(),
		ChatTokensOut:  c.ChatTokensOut.Load(),
		SwarmRuns:      c.SwarmRuns.Load(),
		SwarmCompleted: c.SwarmCompleted.Load(),
		SwarmFailed:    c.SwarmFailed.Load(),
		ToolCalls:      c.ToolCalls.Load(),
		AvgLatencyMs:   avgMs,
	}
}

