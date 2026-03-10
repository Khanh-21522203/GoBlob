package security

import (
	"log/slog"
	"net/http"
)

// HardeningOption controls production hardening middleware stack.
type HardeningOption struct {
	RatePerSecond float64
	Burst         int
	MaxBodyBytes  int64
	Logger        *slog.Logger
}

// ApplyHardening wraps a handler with headers, audit, size limit and rate limit.
func ApplyHardening(next http.Handler, opt HardeningOption) http.Handler {
	h := next
	h = SecurityHeaders(h)
	h = AuditLog(opt.Logger, h)
	h = MaxBytesSizeLimit(opt.MaxBodyBytes)(h)
	rps := opt.RatePerSecond
	if rps <= 0 {
		rps = 200
	}
	rl := NewRateLimiter(rps, opt.Burst)
	h = rl.Middleware(h)
	return h
}
