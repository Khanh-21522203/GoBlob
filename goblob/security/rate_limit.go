package security

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type limiterEntry struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

// RateLimiter limits requests per remote host.
type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
	rps      float64
	burst    float64
	ttl      time.Duration
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
	if rps <= 0 {
		rps = 100
	}
	if burst <= 0 {
		burst = 50
	}
	return &RateLimiter{
		limiters: make(map[string]*limiterEntry),
		rps:      rps,
		burst:    float64(burst),
		ttl:      10 * time.Minute,
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.limiters[ip]
	if !ok {
		rl.limiters[ip] = &limiterEntry{
			tokens:   rl.burst - 1,
			last:     now,
			lastSeen: now,
		}
		if len(rl.limiters)%128 == 0 {
			rl.cleanupLocked(now)
		}
		return true
	}

	elapsed := now.Sub(entry.last).Seconds()
	if elapsed > 0 {
		entry.tokens += elapsed * rl.rps
		if entry.tokens > rl.burst {
			entry.tokens = rl.burst
		}
	}
	entry.last = now
	entry.lastSeen = now
	if entry.tokens < 1 {
		return false
	}
	entry.tokens -= 1
	return true
}

func (rl *RateLimiter) cleanupLocked(now time.Time) {
	for ip, entry := range rl.limiters {
		if now.Sub(entry.lastSeen) > rl.ttl {
			delete(rl.limiters, ip)
		}
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := GetActualRemoteHost(r)
		if !rl.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetActualRemoteHost extracts the request origin host from forwarding headers.
func GetActualRemoteHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
