package obs

import (
	"context"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"
)

// MetricsServer serves prometheus metrics and debug endpoints.
type MetricsServer struct {
	addr   string
	server *http.Server
	logger *slog.Logger
}

func NewMetricsServer(addr string) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promHandler())
	mux.Handle("/debug/vars", expvar.Handler())
	registerPprofHandlers(mux)

	return &MetricsServer{
		addr: addr,
		server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 30 * time.Second,
		},
		logger: New("metrics"),
	}
}

func (m *MetricsServer) Start() {
	if m == nil || m.server == nil {
		return
	}
	go func() {
		m.logger.Info("metrics server starting", "addr", m.addr)
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.logger.Error("metrics server error", "error", err)
		}
	}()
}

func (m *MetricsServer) Stop(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	m.logger.Info("metrics server stopping", "addr", m.addr)
	return m.server.Shutdown(ctx)
}

func promHandler() http.Handler {
	return prometheusHandler()
}

func registerPprofHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
}

func ensureMetricsAddr(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}
