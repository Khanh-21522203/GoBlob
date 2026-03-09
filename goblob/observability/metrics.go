package observability

import (
	"github.com/prometheus/client_golang/prometheus"
)

// NewRegistry creates a Prometheus registry (not the default global registry).
// Use a non-default registry so tests can create isolated registries.
func NewRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

// MustRegister registers collectors with the registry; panics on error.
func MustRegister(r *prometheus.Registry, cs ...prometheus.Collector) {
	r.MustRegister(cs...)
}

// NewDefaultRegistry creates a new registry with default collectors.
func NewDefaultRegistry() *prometheus.Registry {
	r := NewRegistry()
	MustRegister(r, prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return r
}
