package reqlog

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Entry is one API request for the Server screen.
type Entry struct {
	Time     string `json:"time"`
	Client   string `json:"client"`
	Model    string `json:"model"`
	Tokens   string `json:"tokens"`
	Speed    string `json:"speed"`
	Result   string `json:"result"`
	Status   int    `json:"status"`
}

const maxEntries = 200

var (
	mu      sync.RWMutex
	entries []Entry
	paused  bool
)

// Record adds a log line (newest first in API).
func Record(e Entry) {
	mu.Lock()
	defer mu.Unlock()
	if paused {
		return
	}
	entries = append([]Entry{e}, entries...)
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
}

// List returns recent entries.
func List() []Entry {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

// Clear removes all entries.
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	entries = nil
}

// SetPaused freezes recording.
func SetPaused(p bool) {
	mu.Lock()
	defer mu.Unlock()
	paused = p
}

// Paused reports whether logging is frozen.
func Paused() bool {
	mu.RLock()
	defer mu.RUnlock()
	return paused
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Middleware logs /v1/* requests with status and duration.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		model := r.URL.Query().Get("model")
		if model == "" {
			model = "—"
		}
		client := r.Header.Get("X-Client-Name")
		if client == "" {
			client = r.Header.Get("User-Agent")
		}
		if len(client) > 48 {
			client = client[:48]
		}
		res := "ok"
		if sw.status >= 400 {
			res = http.StatusText(sw.status)
		}
		Record(Entry{
			Time:   start.Format("15:04:05"),
			Client: client,
			Model:  model,
			Tokens: "—",
			Speed:  time.Since(start).Round(time.Millisecond).String(),
			Result: res,
			Status: sw.status,
		})
	})
}
