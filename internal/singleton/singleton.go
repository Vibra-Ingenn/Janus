// Package singleton guarantees that only one janus process ever serves the
// listen address. It exists because stale/elevated leftover instances holding
// the port silently swallowed requests while a new instance printed a
// misleading "listening" line — the classic "my fix did nothing" trap.
package singleton

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Ensure makes this the only janus instance able to serve addr.
//
// It (1) best-effort kills sibling janus processes (same-user ones die), then
// (2) requires addr to be free. If the port is still held by a process it
// cannot kill (e.g. an elevated leftover), it returns an error naming the PID
// so the caller can fail loudly instead of starting a confused second instance.
func Ensure(addr string) error {
	self := os.Getpid()
	killSiblings(self)

	// Give killed siblings a moment to release the socket, then poll.
	deadline := time.Now().Add(6 * time.Second)
	for {
		if portFree(addr) {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	if pid := pidOnPort(addr); pid != "" && pid != strconv.Itoa(self) {
		return fmt.Errorf(
			"another process (PID %s) is already holding %s and could not be stopped "+
				"(it may be running elevated). Close it, then start janus again:\n"+
				"    powershell -Command \"Stop-Process -Id %s -Force\"\n"+
				"or launch janus from an elevated terminal so it can clear leftovers itself",
			pid, addr, pid)
	}
	return fmt.Errorf("listen address %s is in use and could not be freed", addr)
}

// portFree reports whether addr can be bound right now.
func portFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// killSiblings force-terminates every janus process except self. Best-effort:
// cross-privilege kills fail and are handled by the port check in Ensure.
func killSiblings(self int) {
	for _, pid := range siblingPIDs(self) {
		if runtime.GOOS == "windows" {
			_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		} else {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}
		log.Printf("singleton: stopped stale janus process PID %d", pid)
	}
}

// siblingPIDs returns PIDs of other janus processes.
func siblingPIDs(self int) []int {
	var out []int
	if runtime.GOOS == "windows" {
		b, err := exec.Command("tasklist", "/FI", "IMAGENAME eq janus.exe", "/FO", "CSV", "/NH").Output()
		if err != nil {
			return out
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Split(line, ",")
			if len(fields) < 2 {
				continue
			}
			pidStr := strings.Trim(strings.TrimSpace(fields[1]), "\"")
			if pid, err := strconv.Atoi(pidStr); err == nil && pid != self {
				out = append(out, pid)
			}
		}
		return out
	}
	// POSIX: pgrep by name, excluding self.
	b, err := exec.Command("pgrep", "-x", "janus").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Fields(string(b)) {
		if pid, err := strconv.Atoi(line); err == nil && pid != self {
			out = append(out, pid)
		}
	}
	return out
}

// pidOnPort returns the PID listening on addr's port, or "" if none/unknown.
func pidOnPort(addr string) string {
	port := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		port = addr[i+1:]
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	b, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "LISTENING") {
			continue
		}
		if !strings.Contains(line, ":"+port+" ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			return fields[len(fields)-1]
		}
	}
	return ""
}
