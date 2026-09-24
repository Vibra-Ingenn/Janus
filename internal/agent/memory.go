// Package agent persists lightweight task memory for the kernel ReAct loop.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Experience is a single task outcome recorded in an agent's memory.
type Experience struct {
	TaskSummary  string    `json:"task"`
	Outcome      string    `json:"outcome"` // "success" | "failure" | "partial"
	ToolsUsed    []string  `json:"tools_used"`
	Iterations   int       `json:"iterations"`
	JudgeScore   int       `json:"judge_score"` // 0–10 run quality heuristic
	JudgePenalty int       `json:"judge_penalty"`
	Lesson       string    `json:"lesson,omitempty"`
	At           time.Time `json:"at"`
}

// AgentMemory is the persistent Pawngotchi memory for one agent role.
// It survives across restarts and grows over time.
type AgentMemory struct {
	mu         sync.Mutex
	path       string
	AgentID    string       `json:"agent_id"`
	TotalTasks int          `json:"total_tasks"`
	Successes  int          `json:"successes"`
	Failures   int          `json:"failures"`
	Lessons    []string     `json:"lessons"` // distilled lessons, kept short
	History    []Experience `json:"history"` // last 50 experiences
	UpdatedAt  time.Time    `json:"updated_at"`
}

const maxHistory = 50
const maxLessons = 20

// LoadMemory loads an agent's memory from disk, or creates a fresh one.
func LoadMemory(agentID string) *AgentMemory {
	dir := "logs"
	_ = os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "agent-"+agentID+".json")

	m := &AgentMemory{path: path, AgentID: agentID}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, m)
	}
	m.path = path
	return m
}

// Record adds a new experience, trims history, extracts lesson, and saves.
func (m *AgentMemory) Record(exp Experience) {
	m.mu.Lock()
	defer m.mu.Unlock()

	exp.At = time.Now()
	m.TotalTasks++
	switch exp.Outcome {
	case "success":
		m.Successes++
	case "failure":
		m.Failures++
	}

	// Distil a lesson from failures and low-score runs.
	if exp.Lesson != "" && (exp.Outcome == "failure" || exp.JudgeScore < 5) {
		m.Lessons = append(m.Lessons, exp.Lesson)
		if len(m.Lessons) > maxLessons {
			m.Lessons = m.Lessons[len(m.Lessons)-maxLessons:]
		}
	}

	m.History = append(m.History, exp)
	if len(m.History) > maxHistory {
		m.History = m.History[len(m.History)-maxHistory:]
	}
	m.UpdatedAt = time.Now()
	m.saveLocked()
}

func (m *AgentMemory) saveLocked() {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.path, data, 0644)
}

// SuccessRate returns 0.0–1.0; returns 1.0 if no tasks yet.
func (m *AgentMemory) SuccessRate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.TotalTasks == 0 {
		return 1.0
	}
	return float64(m.Successes) / float64(m.TotalTasks)
}

// SystemPromptSuffix injects memory context into the agent's system prompt
// so the agent "remembers" lessons from past work.
func (m *AgentMemory) SystemPromptSuffix() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.TotalTasks == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"\n\n--- YOUR EXPERIENCE LOG (Pawngotchi Memory) ---\n"+
			"Tasks completed: %d | Success rate: %.0f%%\n",
		m.TotalTasks, float64(m.Successes)/float64(m.TotalTasks)*100,
	))

	if len(m.Lessons) > 0 {
		sb.WriteString("Lessons learned from past failures:\n")
		for i, l := range m.Lessons {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, l))
		}
	}

	// Show last 3 experiences as recent context.
	recent := m.History
	if len(recent) > 3 {
		recent = recent[len(recent)-3:]
	}
	if len(recent) > 0 {
		sb.WriteString("Recent tasks:\n")
		for _, e := range recent {
			sb.WriteString(fmt.Sprintf("  - [%s] %s (score:%d)\n", e.Outcome, e.TaskSummary, e.JudgeScore))
		}
	}
	sb.WriteString("--- END MEMORY ---\n")
	return sb.String()
}

