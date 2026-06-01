package chain

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// rateLimitWindow is the sliding window duration for rate limiting.
	rateLimitWindow = 1 * time.Minute
	// rateLimitMaxRequests is the maximum number of requests per IP per window.
	rateLimitMaxRequests = 120
	// rateLimitCleanupInterval controls how often stale entries are purged.
	rateLimitCleanupInterval = 5 * time.Minute
)

type rateLimiter struct {
	mu       sync.Mutex
	entries  map[string]*rateEntry
	lastClean time.Time
}

type rateEntry struct {
	timestamps []time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		entries:   make(map[string]*rateEntry),
		lastClean: time.Now(),
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	now := time.Now()
	cutoff := now.Add(-rateLimitWindow)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Periodic cleanup of stale entries.
	if now.Sub(rl.lastClean) > rateLimitCleanupInterval {
		rl.cleanupLocked(cutoff)
		rl.lastClean = now
	}

	entry, ok := rl.entries[ip]
	if !ok {
		entry = &rateEntry{}
		rl.entries[ip] = entry
	}

	// Remove expired timestamps.
	start := 0
	for start < len(entry.timestamps) && entry.timestamps[start].Before(cutoff) {
		start++
	}
	entry.timestamps = entry.timestamps[start:]

	if len(entry.timestamps) >= rateLimitMaxRequests {
		return false
	}
	entry.timestamps = append(entry.timestamps, now)
	return true
}

func (rl *rateLimiter) cleanupLocked(cutoff time.Time) {
	for ip, entry := range rl.entries {
		start := 0
		for start < len(entry.timestamps) && entry.timestamps[start].Before(cutoff) {
			start++
		}
		if start == len(entry.timestamps) {
			delete(rl.entries, ip)
		} else {
			entry.timestamps = entry.timestamps[start:]
		}
	}
}

// rateLimitMiddleware wraps an http.Handler with per-IP rate limiting.
// Read-only endpoints (GET /health, /status) bypass the limiter.
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	limiter := newRateLimiter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health and status probes.
		if r.URL.Path == "/health" || r.URL.Path == "/status" {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientIP(r)
		if !limiter.allow(ip) {
			writeError(w, http.StatusTooManyRequests, errTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the client IP from the request, respecting X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain.
		if idx := len(xff); idx > 0 {
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					return xff[:i]
				}
			}
			return xff
		}
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
