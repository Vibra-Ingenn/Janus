package singleton

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// PortBusy describes who holds the listen address before this instance starts.
type PortBusy struct {
	HoldingJanus bool
	PID          string
	ProcessName  string
}

// CheckPortBeforeListen reports whether addr is free or who holds it.
func CheckPortBeforeListen(addr string) (free bool, busy *PortBusy) {
	if portFree(addr) {
		return true, nil
	}
	base := BrowserBaseURL(addr)
	if IsJanusServing(base) {
		return false, &PortBusy{HoldingJanus: true}
	}
	pid := pidOnPort(addr)
	name := processNameForPID(pid)
	return false, &PortBusy{HoldingJanus: false, PID: pid, ProcessName: name}
}

// IsJanusServing GETs /health and checks for a Janus service response.
func IsJanusServing(baseURL string) bool {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var h struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return false
	}
	if !h.OK {
		return false
	}
	id := strings.ToLower(strings.TrimSpace(h.ID))
	return id == "" || id == "janus"
}

// BrowserBaseURL is the console URL people should open (loopback host).
func BrowserBaseURL(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "http://127.0.0.1:8990"
	}
	if port == "" {
		port = "8990"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

// WaitUntilJanusHealth polls GET /health until ok or timeout.
func WaitUntilJanusHealth(baseURL string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if IsJanusServing(baseURL) {
			return true
		}
		time.Sleep(120 * time.Millisecond)
	}
	return false
}
