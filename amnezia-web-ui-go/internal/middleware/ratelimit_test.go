package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	// 5 requests per second, burst 2
	limiter := NewRateLimiter(5.0, 2, 50*time.Millisecond, 100*time.Millisecond)
	defer limiter.Stop()

	ip := "192.168.1.100"

	// Request 1 -> OK (tokens: 2 -> 1)
	allowed, _ := limiter.Allow(ip)
	if !allowed {
		t.Errorf("request 1 should be allowed")
	}

	// Request 2 -> OK (tokens: 1 -> 0)
	allowed, _ = limiter.Allow(ip)
	if !allowed {
		t.Errorf("request 2 should be allowed")
	}

	// Request 3 -> Denied (tokens: 0)
	allowed, retryAfter := limiter.Allow(ip)
	if allowed {
		t.Errorf("request 3 should be rate limited")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter should be positive, got %v", retryAfter)
	}

	// Different IP should still have its own bucket and succeed
	allowedOther, _ := limiter.Allow("192.168.1.101")
	if !allowedOther {
		t.Errorf("different IP should have separate bucket and be allowed")
	}

	// Wait for refill (0.25s at 5/s refilled > 1 token)
	time.Sleep(250 * time.Millisecond)
	allowedRefilled, _ := limiter.Allow(ip)
	if !allowedRefilled {
		t.Errorf("request after refill should be allowed")
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	limiter := NewRateLimiterPerMinute(60, 2)
	defer limiter.Stop()

	handlerCalled := 0
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled++
		w.WriteHeader(http.StatusOK)
	})

	middlewareHandler := RateLimit(limiter)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	// Request 1 -> 200
	w1 := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(w1, req)
	if w1.Code != http.StatusOK {
		t.Errorf("req 1: got %d, want 200", w1.Code)
	}

	// Request 2 -> 200
	w2 := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Errorf("req 2: got %d, want 200", w2.Code)
	}

	// Request 3 -> 429
	w3 := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(w3, req)
	if w3.Code != http.StatusTooManyRequests {
		t.Errorf("req 3: got %d, want 429", w3.Code)
	}
	if retry := w3.Header().Get("Retry-After"); retry == "" {
		t.Errorf("expected Retry-After header on 429 response")
	}
}

func TestRateLimiterCleanupAndStop(t *testing.T) {
	limiter := NewRateLimiter(1.0, 1, 10*time.Millisecond, 20*time.Millisecond)

	limiter.Allow("1.1.1.1")
	limiter.mu.Lock()
	if len(limiter.buckets) != 1 {
		t.Errorf("expected 1 bucket, got %d", len(limiter.buckets))
	}
	limiter.mu.Unlock()

	// Wait for cleanup
	time.Sleep(50 * time.Millisecond)

	limiter.mu.Lock()
	if len(limiter.buckets) != 0 {
		t.Errorf("expected 0 buckets after cleanup, got %d", len(limiter.buckets))
	}
	limiter.mu.Unlock()

	limiter.Stop()
	// Stopping again should be safe (no panic)
	limiter.Stop()
}
