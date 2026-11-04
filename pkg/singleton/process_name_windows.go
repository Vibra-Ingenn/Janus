//go:build windows

package singleton

import (
	"os/exec"
	"strings"
)

func processNameForPID(pid string) string {
	pid = strings.TrimSpace(pid)
	if pid == "" {
		return "unknown program"
	}
	b, err := exec.Command("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return "PID " + pid
	}
	line := strings.TrimSpace(string(b))
	if line == "" || strings.Contains(line, "No tasks") {
		return "PID " + pid
	}
	fields := strings.Split(line, ",")
	if len(fields) > 0 {
		return strings.Trim(strings.TrimSpace(fields[0]), "\"")
	}
	return "PID " + pid
}
