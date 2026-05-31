// Package middleware provides shared HTTP middleware for all Falari services.
package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// CORS returns middleware that sets CORS headers based on a configurable
// allowlist of origins. Pass an empty slice to disable CORS entirely.
// Use ["*"] to allow all origins (not recommended for production).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	// Pre-compute a set for O(1) lookup and detect wildcard.
	wildcard := false
	originSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimRight(o, "/")
		if o == "*" {
			wildcard = true
		}
		originSet[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := false
			if wildcard {
				allowed = true
			} else if _, ok := originSet[origin]; ok {
				allowed = true
			}

			if allowed {
				// Reflect the requesting origin (not "*") so credentials work.
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key, X-Admin-Signature, X-Admin-PublicKey")
				w.Header().Set("Vary", "Origin")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ipLimiter tracks a rate limiter per client IP.
type ipLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

func (l *ipLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lim, ok := l.limiters[ip]; ok {
		return lim
	}
	lim := rate.NewLimiter(l.rps, l.burst)
	l.limiters[ip] = lim
	return lim
}

// RateLimit returns middleware that enforces a per-IP request rate limit.
// rps is requests per second; burst is the maximum burst size.
// If rps <= 0, rate limiting is disabled.
func RateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	return RateLimitWithTrustedProxies(rps, burst, nil)
}

// RateLimitWithTrustedProxies trusts X-Forwarded-For and X-Real-Ip only when
// the direct peer address matches a configured trusted proxy CIDR or IP.
func RateLimitWithTrustedProxies(rps float64, burst int, trustedProxies []string) func(http.Handler) http.Handler {
	if rps <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	if burst < 1 {
		burst = int(rps) + 1
	}
	l := &ipLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
	proxies := parseTrustedProxies(trustedProxies)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, proxies)
			limiter := l.get(ip)
			if !limiter.Allow() {
				// P2-H02: Set proper Content-Type for JSON error response.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"rate limit exceeded"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the client IP from the request. Forwarded headers are
// trusted only when the immediate peer is a configured proxy.
func clientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	remoteIP := remoteAddrIP(r.RemoteAddr)
	if isTrustedProxy(remoteIP, trustedProxies) {
		return forwardedClientIP(r, remoteIP)
	}
	return remoteIP
}

func forwardedClientIP(r *http.Request, fallback string) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		if ip := strings.TrimSpace(xri); ip != "" {
			return ip
		}
	}
	return fallback
}

func remoteAddrIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return addr
}

func parseTrustedProxies(raw []string) []*net.IPNet {
	proxies := make([]*net.IPNet, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			proxies = append(proxies, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			proxies = append(proxies, network)
		}
	}
	return proxies
}

func isTrustedProxy(ip string, proxies []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, proxy := range proxies {
		if proxy.Contains(parsed) {
			return true
		}
	}
	return false
}

// Chain composes multiple middleware functions into a single middleware.
// Middleware is applied in reverse order so the first argument is outermost.
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
