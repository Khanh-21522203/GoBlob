package security

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type auditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// AuditLog records auth/admin/mutation events and errors.
func AuditLog(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		aw := &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(aw, r)
		if aw.status >= 400 || isMutation(r.Method) || hasAuthorization(r) {
			logger.LogAttrs(r.Context(), slog.LevelInfo, "audit",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", aw.status),
				slog.String("remote_host", GetActualRemoteHost(r)),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			)
		}
	})
}

func isMutation(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func hasAuthorization(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get("Authorization")) != ""
}
