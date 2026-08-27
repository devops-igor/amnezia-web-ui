package middleware

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

type tokenBucket struct {
	tokens     float64
	capacity   float64
	rate       float64 // tokens per second
	lastRefill time.Time
}

func (b *tokenBucket) allow(n float64, now time.Time) (bool, time.Duration) {
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = math.Min(b.capacity, b.tokens+elapsed*b.rate)
	b.lastRefill = now

	if b.tokens >= n {
		b.tokens -= n
		return true, 0
	}

	missing := n - b.tokens
	retrySeconds := math.Ceil(missing / b.rate)
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	return false, time.Duration(retrySeconds) * time.Second
}

// RateLimiter is a thread-safe in-memory token bucket rate limiter per client IP.
type RateLimiter struct {
	mu              sync.Mutex
	buckets         map[string]*tokenBucket
	rate            float64 // tokens per second
	burst           int
	entryTTL        time.Duration
	cleanupInterval time.Duration
	stopChan        chan struct{}
	stopped         bool
}

// NewRateLimiter creates a new RateLimiter with configurable rate, burst, and cleanup interval.
func NewRateLimiter(ratePerSecond float64, burst int, cleanupInterval, entryTTL time.Duration) *RateLimiter {
	if ratePerSecond <= 0 {
		ratePerSecond = 1.0
	}
	if burst <= 0 {
		burst = 1
	}
	if cleanupInterval <= 0 {
		cleanupInterval = 1 * time.Minute
	}
	if entryTTL <= 0 {
		entryTTL = 5 * time.Minute
	}

	rl := &RateLimiter{
		buckets:         make(map[string]*tokenBucket),
		rate:            ratePerSecond,
		burst:           burst,
		entryTTL:        entryTTL,
		cleanupInterval: cleanupInterval,
		stopChan:        make(chan struct{}),
	}

	go rl.cleanupLoop()
	return rl
}

// NewRateLimiterPerMinute constructs a rate limiter for N requests per minute with given burst.
func NewRateLimiterPerMinute(requestsPerMinute int, burst int) *RateLimiter {
	rate := float64(requestsPerMinute) / 60.0
	return NewRateLimiter(rate, burst, 1*time.Minute, 5*time.Minute)
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopChan:
			return
		case now := <-ticker.C:
			rl.cleanup(now)
		}
	}
}

func (rl *RateLimiter) cleanup(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for ip, bucket := range rl.buckets {
		if now.Sub(bucket.lastRefill) > rl.entryTTL {
			delete(rl.buckets, ip)
		}
	}
}

// Stop terminates the background cleanup goroutine safely.
func (rl *RateLimiter) Stop() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if !rl.stopped {
		rl.stopped = true
		close(rl.stopChan)
	}
}

// Allow checks whether a request from the given IP is allowed.
func (rl *RateLimiter) Allow(ip string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	bucket, exists := rl.buckets[ip]
	if !exists {
		bucket = &tokenBucket{
			tokens:     float64(rl.burst),
			capacity:   float64(rl.burst),
			rate:       rl.rate,
			lastRefill: now,
		}
		rl.buckets[ip] = bucket
	}

	return bucket.allow(1.0, now)
}

// Middleware wraps an http.Handler with rate limiting enforcement based on client IP.
func (rl *RateLimiter) Middleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := GetClientIPFromRequest(r)
			if clientIP == "" {
				clientIP = "unknown"
			}

			allowed, retryAfter := rl.Allow(clientIP)
			if !allowed {
				retrySec := int(retryAfter.Seconds())
				if retrySec < 1 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySec))
				WriteJSONError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Too many requests, please try again later")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit is a helper function to create rate limit middleware.
func RateLimit(limiter *RateLimiter) func(next http.Handler) http.Handler {
	return limiter.Middleware()
}
