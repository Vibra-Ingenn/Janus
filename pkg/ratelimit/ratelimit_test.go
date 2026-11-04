package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowBurst(t *testing.T) {
	l := New(1, 3) // 1 req/sec, burst of 3
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Errorf("request %d should be allowed within burst", i+1)
		}
	}
	// 4th should be rejected (burst exhausted, no time to refill)
	if l.Allow("1.2.3.4") {
		t.Error("4th request should be rate limited")
	}
}

func TestAllowDifferentIPs(t *testing.T) {
	l := New(1, 1)
	if !l.Allow("1.1.1.1") {
		t.Error("first IP should be allowed")
	}
	if !l.Allow("2.2.2.2") {
		t.Error("second IP should be allowed (separate bucket)")
	}
}

func TestMiddleware429(t *testing.T) {
	l := New(1, 1)
	handler := l.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// First request: OK
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first request: status = %d, want 200", w.Code)
	}

	// Second request: 429
	w2 := httptest.NewRecorder()
	handler(w2, req)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: status = %d, want 429", w2.Code)
	}
}
