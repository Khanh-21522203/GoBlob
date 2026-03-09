package observability

import (
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler returns an HTTP handler for /metrics using the given registry.
func MetricsHandler(r *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(r, promhttp.HandlerOpts{})
}

// PProfHandlers returns a map of path → handler for /debug/pprof/* endpoints.
func PProfHandlers() map[string]http.Handler {
	handlers := make(map[string]http.Handler)

	handlers["/debug/pprof/"] = http.HandlerFunc(pprof.Index)
	handlers["/debug/pprof/cmdline"] = http.HandlerFunc(pprof.Cmdline)
	handlers["/debug/pprof/profile"] = http.HandlerFunc(pprof.Profile)
	handlers["/debug/pprof/symbol"] = http.HandlerFunc(pprof.Symbol)
	handlers["/debug/pprof/trace"] = http.HandlerFunc(pprof.Trace)
	handlers["/debug/pprof/goroutine"] = pprof.Handler("goroutine")
	handlers["/debug/pprof/heap"] = pprof.Handler("heap")
	handlers["/debug/pprof/threadcreate"] = pprof.Handler("threadcreate")
	handlers["/debug/pprof/block"] = pprof.Handler("block")
	handlers["/debug/pprof/mutex"] = pprof.Handler("mutex")
	handlers["/debug/pprof/allocs"] = pprof.Handler("allocs")

	return handlers
}

// RegisterPProfHandlers registers all pprof handlers with the given mux.
func RegisterPProfHandlers(mux *http.ServeMux) {
	handlers := PProfHandlers()
	for path, handler := range handlers {
		mux.Handle(path, handler)
	}
}
