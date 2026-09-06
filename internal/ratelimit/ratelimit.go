// Package ratelimit provides a simple in-process token-bucket rate limiter
// keyed by client IP. No external dependencies.
package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Limiter tracks per-IP token buckets.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64       // tokens per second
	burst   int           // max tokens
	cleanup time.Duration // evict idle buckets
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// New creates a Limiter. rate is requests/sec, burst is the max burst size.
func New(rate float64, burst int) *Limiter {
	l := &Limiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		burst:   burst,
		cleanup: 5 * time.Minute,
	}
	go l.reap()
	return l
}

// Allow returns true if the request from the given IP is within limits.
func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[ip]
	now := time.Now()
	if !ok {
		b = &bucket{tokens: float64(l.burst), lastSeen: now}
		l.buckets[ip] = b
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Middleware wraps an http.HandlerFunc with rate limiting.
// Returns 429 Too Many Requests when the limit is exceeded.
func (l *Limiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !l.Allow(ip) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func clientIP(r *http.Request) string {
	// Check X-Forwarded-For first (trusted proxy)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// May be comma-separated; take the first
		if idx := len(xff); idx > 0 {
			if h, _, err := net.SplitHostPort(xff); err == nil {
				return h
			}
			return xff
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// reap evicts idle buckets every cleanup interval.
func (l *Limiter) reap() {
	for {
		time.Sleep(l.cleanup)
		l.mu.Lock()
		cutoff := time.Now().Add(-l.cleanup)
		for ip, b := range l.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(l.buckets, ip)
			}
		}
		l.mu.Unlock()
	}
}
