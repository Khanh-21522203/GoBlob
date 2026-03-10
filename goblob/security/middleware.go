package security

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// HTTPMiddleware wraps an HTTP handler with Guard enforcement.
func HTTPMiddleware(guard *Guard, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !guard.Allowed(r, guard.HasJWTSigningKey()) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HTTPMiddlewareWithJWT wraps an HTTP handler with Guard enforcement and JWT verification.
func HTTPMiddlewareWithJWT(guard *Guard, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !guard.Allowed(r, true) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HTTPMiddlewareWithWhiteList wraps an HTTP handler with IP whitelist enforcement only.
func HTTPMiddlewareWithWhiteList(guard *Guard, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !guard.Allowed(r, false) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GRPCUnaryInterceptor creates a gRPC unary interceptor for Guard.
func GRPCUnaryInterceptor(guard *Guard) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Extract peer info from context using grpc/peer package
		var clientIP string
		if p, ok := peer.FromContext(ctx); ok {
			clientIP = p.Addr.String()
		}
		if clientIP != "" && !guard.isIPAllowed(clientIP) {
			return nil, status.Errorf(codes.PermissionDenied, "IP not allowed")
		}

		return handler(ctx, req)
	}
}

// GRPCStreamInterceptor creates a gRPC stream interceptor for Guard.
func GRPCStreamInterceptor(guard *Guard) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Extract peer info from context using grpc/peer package
		ctx := ss.Context()
		var clientIP string
		if p, ok := peer.FromContext(ctx); ok {
			clientIP = p.Addr.String()
		}
		if clientIP != "" && !guard.isIPAllowed(clientIP) {
			return status.Errorf(codes.PermissionDenied, "IP not allowed")
		}

		return handler(srv, ss)
	}
}

// peerKey is the context key for storing peer address.
type peerKey struct{}

// ContextWithPeer returns a context with the peer address.
func ContextWithPeer(ctx context.Context, peerAddr string) context.Context {
	return context.WithValue(ctx, peerKey{}, peerAddr)
}

// FilerHTTPMiddleware wraps an HTTP handler with filer-specific Guard enforcement.
func FilerHTTPMiddleware(guard *Guard, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !guard.FilerAllowed(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminHTTPMiddleware wraps an HTTP handler for admin endpoints.
func AdminHTTPMiddleware(guard *Guard, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Admin endpoints always require JWT if configured
		if !guard.Allowed(r, guard.HasJWTSigningKey()) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ChainHTTPMiddleware chains multiple HTTP middlewares.
func ChainHTTPMiddleware(guard *Guard, next http.Handler, middlewares ...MiddlewareType) http.Handler {
	h := next
	// Apply in reverse order so first middleware is outermost
	for i := len(middlewares) - 1; i >= 0; i-- {
		switch middlewares[i] {
		case MiddlewareJWT:
			h = HTTPMiddlewareWithJWT(guard, h)
		case MiddlewareWhitelist:
			h = HTTPMiddlewareWithWhiteList(guard, h)
		case MiddlewareFiler:
			h = FilerHTTPMiddleware(guard, h)
		case MiddlewareAdmin:
			h = AdminHTTPMiddleware(guard, h)
		default:
			h = HTTPMiddleware(guard, h)
		}
	}
	return h
}

// MiddlewareType specifies the type of middleware to apply.
type MiddlewareType int

const (
	MiddlewareDefault MiddlewareType = iota
	MiddlewareJWT
	MiddlewareWhitelist
	MiddlewareFiler
	MiddlewareAdmin
)
