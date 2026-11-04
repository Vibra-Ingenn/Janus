package singleton

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A freshly built janus.exe replaces an older copy of itself that is still running, so a rebuild never
// needs closing anything by hand. Only a copy started from the same exe file is replaced; any other
// Janus on the port is left alone.

// TakeoverHeader carries the exe path; browsers can't send it cross-site without a CORS preflight,
// which Janus never approves, so web pages can't shut Janus down.
const TakeoverHeader = "X-Janus-Takeover"

var startedAt = time.Now()

// Instance describes this running Janus.
type Instance struct {
	PID     int       `json:"pid"`
	Exe     string    `json:"exe"`
	Started time.Time `json:"started"`
}

func selfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Clean(exe)
}

// Self returns this process's instance info.
func Self() Instance { return Instance{PID: os.Getpid(), Exe: selfExe(), Started: startedAt} }

// HandleInstance serves GET /instance.
func HandleInstance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Self())
}

// HandleShutdown serves POST /instance/shutdown for a newer build taking over. It only accepts
// requests from this PC, with no browser Origin, that name this exe in TakeoverHeader.
func HandleShutdown(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if r.Method != http.MethodPost || ip == nil || !ip.IsLoopback() || r.Header.Get("Origin") != "" ||
		!sameJanusFile(selfExe(), filepath.Clean(r.Header.Get(TakeoverHeader))) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()
}

// sameJanusFile is true for this exe, or the "janus.old.exe" build.ps1 moved it to during a rebuild.
func sameJanusFile(other, me string) bool {
	other = filepath.Clean(other)
	if strings.EqualFold(other, me) {
		return true
	}
	return strings.EqualFold(filepath.Dir(other), filepath.Dir(me)) && strings.EqualFold(filepath.Base(other), "janus.old.exe")
}

// ReplaceIfStale asks the Janus at baseURL to shut down when it was started from this same exe file
// and that file has been rebuilt since. It returns true once the port at listenAddr is free.
func ReplaceIfStale(baseURL, listenAddr string) bool {
	baseURL = strings.TrimRight(baseURL, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/instance", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	var other Instance
	err = json.NewDecoder(resp.Body).Decode(&other)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		return false // an older Janus without /instance: leave it alone
	}
	me := selfExe()
	st, err := os.Stat(me)
	if err != nil || !sameJanusFile(other.Exe, me) || !st.ModTime().After(other.Started) {
		return false
	}
	sreq, _ := http.NewRequest(http.MethodPost, baseURL+"/instance/shutdown", nil)
	sreq.Header.Set(TakeoverHeader, me)
	sresp, err := http.DefaultClient.Do(sreq)
	if err != nil {
		return false
	}
	sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		return false
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if portFree(listenAddr) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}
