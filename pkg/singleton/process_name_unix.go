//go:build !windows

package singleton

import "strings"

func processNameForPID(pid string) string {
	pid = strings.TrimSpace(pid)
	if pid == "" {
		return "unknown program"
	}
	return "PID " + pid
}
