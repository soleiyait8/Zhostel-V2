// Package httpx holds small HTTP helpers shared by both services.
package httpx

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ClientIP returns the caller's address. X-Forwarded-For is honoured only
// when trustProxy is set (the service sits behind the bundled reverse proxy);
// the right-most entry is the one the proxy itself observed.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// JSON writes v as a JSON response.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes {"error": {"code":..., "message":...}}.
func Error(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// RateLimiter is a fixed-window per-key limiter, adequate for a single
// instance. Run one replica per service or move this to the proxy.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*bucket
}

type bucket struct {
	start time.Time
	n     int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{limit: limit, window: window, hits: map[string]*bucket{}}
	go func() {
		for range time.Tick(window) {
			rl.mu.Lock()
			for k, b := range rl.hits {
				if time.Since(b.start) > window {
					delete(rl.hits, k)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

// Allow records a hit for key and reports whether it is within the limit.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.hits[key]
	if !ok || now.Sub(b.start) > rl.window {
		rl.hits[key] = &bucket{start: now, n: 1}
		return true
	}
	b.n++
	return b.n <= rl.limit
}

// Reset clears a key (e.g. after a successful login).
func (rl *RateLimiter) Reset(key string) {
	rl.mu.Lock()
	delete(rl.hits, key)
	rl.mu.Unlock()
}

// SecurityHeaders adds conservative headers to API responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
