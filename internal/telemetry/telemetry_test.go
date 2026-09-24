package telemetry

import (
	"testing"
	"time"
)

func TestCollectorCounters(t *testing.T) {
	c := New()

	c.TasksSubmitted.Add(3)
	c.TasksCompleted.Add(2)
	c.TasksFailed.Add(1)
	c.ChatRequests.Add(10)
	c.SwarmRuns.Add(5)
	c.ToolCalls.Add(7)

	snap := c.Snap()

	if snap.TasksSubmitted != 3 {
		t.Errorf("TasksSubmitted = %d, want 3", snap.TasksSubmitted)
	}
	if snap.TasksCompleted != 2 {
		t.Errorf("TasksCompleted = %d, want 2", snap.TasksCompleted)
	}
	if snap.TasksFailed != 1 {
		t.Errorf("TasksFailed = %d, want 1", snap.TasksFailed)
	}
	if snap.ChatRequests != 10 {
		t.Errorf("ChatRequests = %d, want 10", snap.ChatRequests)
	}
	if snap.SwarmRuns != 5 {
		t.Errorf("SwarmRuns = %d, want 5", snap.SwarmRuns)
	}
	if snap.ToolCalls != 7 {
		t.Errorf("ToolCalls = %d, want 7", snap.ToolCalls)
	}
}

func TestCollectorLatency(t *testing.T) {
	c := New()

	c.RecordLatency(100 * time.Millisecond)
	c.RecordLatency(200 * time.Millisecond)

	snap := c.Snap()
	// Average should be ~150ms
	if snap.AvgLatencyMs < 140 || snap.AvgLatencyMs > 160 {
		t.Errorf("AvgLatencyMs = %.1f, want ~150", snap.AvgLatencyMs)
	}
}

func TestCollectorUptime(t *testing.T) {
	c := New()
	time.Sleep(10 * time.Millisecond)
	snap := c.Snap()
	if snap.UptimeSeconds <= 0 {
		t.Errorf("UptimeSeconds = %.3f, want > 0", snap.UptimeSeconds)
	}
}

func TestCollectorZeroLatency(t *testing.T) {
	c := New()
	snap := c.Snap()
	if snap.AvgLatencyMs != 0 {
		t.Errorf("AvgLatencyMs = %.1f, want 0 when no latency recorded", snap.AvgLatencyMs)
	}
}

