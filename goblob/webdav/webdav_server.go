package webdav

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"GoBlob/goblob/obs"

	xwebdav "golang.org/x/net/webdav"
)

// Server hosts a WebDAV endpoint.
type Server struct {
	addr     string
	handler  http.Handler
	httpSrv  *http.Server
	username string
	password string
}

func NewServer(addr string, fs xwebdav.FileSystem, username, password string, middleware func(http.Handler) http.Handler) *Server {
	if fs == nil {
		fs = NewFilerFileSystem(".")
	}
	h := &xwebdav.Handler{FileSystem: fs, LockSystem: xwebdav.NewMemLS()}
	s := &Server{addr: addr, username: strings.TrimSpace(username), password: password}
	s.handler = s.wrapAuth(h)
	if middleware != nil {
		s.handler = middleware(s.handler)
	}
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	return s
}

func (s *Server) Start(ctx context.Context) error {
	if s == nil || s.httpSrv == nil {
		return nil
	}
	errCh := make(chan error, 1)
	go func() {
		obs.New("webdav").Info("webdav server starting", "addr", s.addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return s.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

const shutdownTimeout = 10 * time.Second

func (s *Server) wrapAuth(next http.Handler) http.Handler {
	if s.username == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.username || pass != s.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoBlob WebDAV"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Address(host string, port int) string {
	if strings.TrimSpace(host) == "" || host == "0.0.0.0" {
		return fmt.Sprintf(":%d", port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}
