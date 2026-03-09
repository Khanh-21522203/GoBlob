package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Error("expected non-nil registry")
	}
}

func TestNewDefaultRegistry(t *testing.T) {
	r := NewDefaultRegistry()
	if r == nil {
		t.Error("expected non-nil registry")
	}

	// Check that default collectors are registered
	metrics, err := r.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	// Should have go and process metrics
	found := false
	for _, m := range metrics {
		if m.GetName() == "go_threads" || m.GetName() == "process_cpu_seconds_total" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected default collectors to be registered")
	}
}

func TestMustRegister(t *testing.T) {
	r := NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_counter",
		Help: "A test counter",
	})

	// Should not panic
	MustRegister(r, counter)
}

func TestMetricsHandler(t *testing.T) {
	r := NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_counter",
		Help: "A test counter",
	})
	r.MustRegister(counter)

	handler := MetricsHandler(r)
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if body == "" {
		t.Error("expected non-empty response body")
	}
}

func TestPProfHandlers(t *testing.T) {
	handlers := PProfHandlers()

	expectedPaths := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/goroutine",
		"/debug/pprof/heap",
	}

	for _, path := range expectedPaths {
		if _, ok := handlers[path]; !ok {
			t.Errorf("missing handler for %s", path)
		}
	}
}

func TestNewLogger(t *testing.T) {
	logger := NewLogger(0, false) // InfoLevel, console
	if logger == nil {
		t.Error("expected non-nil logger")
	}

	loggerJSON := NewLogger(0, true) // InfoLevel, JSON
	if loggerJSON == nil {
		t.Error("expected non-nil logger")
	}
}

func TestNewNopLogger(t *testing.T) {
	logger := NewNopLogger()
	if logger == nil {
		t.Error("expected non-nil logger")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"debug", "debug"},
		{"info", "info"},
		{"warn", "warn"},
		{"error", "error"},
		{"fatal", "fatal"},
		{"invalid", "info"}, // defaults to info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			level := ParseLevel(tt.input)
			if level.String() != tt.output {
				t.Errorf("ParseLevel(%q) = %s, want %s", tt.input, level.String(), tt.output)
			}
		})
	}
}
