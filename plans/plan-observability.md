# Feature: Logging & Observability

## 1. Purpose

The Observability subsystem provides structured logging, Prometheus metrics, and debug endpoints used uniformly across all GoBlob server roles. It is a cross-cutting concern: every subsystem imports it, but it imports nothing from GoBlob except the core types package.

Its goals:
- Structured, leveled logging with consistent field names
- Prometheus metrics with standardized labels (`role`, `volume_id`, etc.)
- A `/metrics` HTTP endpoint for Prometheus scraping
- Optional remote metrics push via Prometheus Pushgateway
- Debug endpoints (`/debug/vars`, `/debug/pprof/`)

## 2. Responsibilities

- Provide a `Logger` interface wrapping a structured logger (e.g., `log/slog`)
- Provide package-level log functions (`Info`, `Warn`, `Error`, `Debug`) for convenience
- Define and register all Prometheus metrics (counters, gauges, histograms)
- Expose a `StartMetricsServer(addr)` function to serve `/metrics` on a dedicated port
- Optionally push metrics to a Prometheus Pushgateway at a configurable interval
- Expose `expvar` debug endpoint at `GET /debug/vars`
- Register standard `net/http/pprof` endpoints

## 3. Non-Responsibilities

- Does not handle distributed tracing (no OpenTelemetry integration unless explicitly added)
- Does not aggregate logs centrally; log aggregation is the operator's responsibility
- Does not configure alerting rules
- Does not implement log rotation (use a log rotation tool externally)

## 4. Architecture Design

```
+----------------------------------------------------------+
|                    Any GoBlob Package                     |
|       obs.Info("volume written", "volume_id", vid)       |
+---------------------------+------------------------------+
                            |
              +-------------+-------------+
              |       goblob/obs           |
              |  Logger  Metrics          |
              +--+--------+---------------+
                 |        |
         slog.Logger  prometheus.Registry
                 |        |
             stderr    /metrics HTTP
```

All subsystems obtain a logger by calling `obs.New("subsystem_name")`, which returns a `*slog.Logger` with a `"subsystem"` attribute pre-attached.

Metrics are pre-registered in an `init()` function. Subsystems call `obs.MetricXxx.Inc()` etc. directly; there is no subsystem-local registry.

## 5. Core Data Structures (Go)

```go
package obs

import (
    "context"
    "log/slog"
    "net/http"
    _ "net/http/pprof" // register pprof handlers on DefaultServeMux
    "expvar"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "github.com/prometheus/client_golang/prometheus/push"
)

// LogLevel controls the minimum severity of log messages emitted.
type LogLevel = slog.Level

const (
    LevelDebug = slog.LevelDebug
    LevelInfo  = slog.LevelInfo
    LevelWarn  = slog.LevelWarn
    LevelError = slog.LevelError
)

// New returns a *slog.Logger pre-configured with the subsystem name as a
// permanent attribute. Use this instead of the global slog functions so
// that all log lines carry a "subsystem" key for filtering.
//
//   logger := obs.New("master")
//   logger.Info("volume created", "volume_id", vid)
func New(subsystem string) *slog.Logger {
    return slog.Default().With("subsystem", subsystem)
}

// SetLevel reconfigures the global log level at runtime (e.g., via an admin endpoint).
func SetLevel(level LogLevel)

// SetOutput replaces the global slog handler output (useful for testing).
func SetOutput(w io.Writer)

// ---------------------------------------------------------
// Prometheus Metrics Registry
// ---------------------------------------------------------

// All metrics are registered once on package init into the default registry.

var (
    // Master metrics
    MasterLeadershipGauge = prometheus.NewGauge(prometheus.GaugeOpts{
        Namespace: "goblob",
        Subsystem: "master",
        Name:      "is_leader",
        Help:      "1 if this master is the Raft leader, 0 otherwise.",
    })
    MasterVolumeCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Namespace: "goblob",
        Subsystem: "master",
        Name:      "volume_count",
        Help:      "Number of volumes tracked in topology.",
    }, []string{"collection", "replication"})
    MasterAssignRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
        Namespace: "goblob",
        Subsystem: "master",
        Name:      "assign_requests_total",
        Help:      "Total file ID assignment requests.",
    }, []string{"status"})

    // Volume server metrics
    VolumeServerNeedleWriteBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
        Namespace: "goblob",
        Subsystem: "volume",
        Name:      "needle_write_bytes_total",
        Help:      "Total bytes written as needles.",
    }, []string{"volume_id"})
    VolumeServerNeedleReadBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
        Namespace: "goblob",
        Subsystem: "volume",
        Name:      "needle_read_bytes_total",
        Help:      "Total bytes read from needles.",
    }, []string{"volume_id"})
    VolumeServerNeedleWriteLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Namespace: "goblob",
        Subsystem: "volume",
        Name:      "needle_write_latency_seconds",
        Help:      "Needle write latency.",
        Buckets:   prometheus.DefBuckets,
    }, []string{"volume_id"})
    VolumeServerNeedleReadLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Namespace: "goblob",
        Subsystem: "volume",
        Name:      "needle_read_latency_seconds",
        Help:      "Needle read latency.",
        Buckets:   prometheus.DefBuckets,
    }, []string{"volume_id"})
    VolumeServerDiskFreeBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Namespace: "goblob",
        Subsystem: "volume",
        Name:      "disk_free_bytes",
        Help:      "Free disk space per directory.",
    }, []string{"directory"})

    // Filer metrics
    FilerRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
        Namespace: "goblob",
        Subsystem: "filer",
        Name:      "requests_total",
        Help:      "Total filer requests.",
    }, []string{"method", "status"})
    FilerStoreLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Namespace: "goblob",
        Subsystem: "filer",
        Name:      "store_latency_seconds",
        Help:      "Filer metadata store operation latency.",
        Buckets:   prometheus.DefBuckets,
    }, []string{"operation", "backend"})
)

func init() {
    prometheus.MustRegister(
        MasterLeadershipGauge,
        MasterVolumeCount,
        MasterAssignRequests,
        VolumeServerNeedleWriteBytes,
        VolumeServerNeedleReadBytes,
        VolumeServerNeedleWriteLatency,
        VolumeServerNeedleReadLatency,
        VolumeServerDiskFreeBytes,
        FilerRequestsTotal,
        FilerStoreLatency,
    )
}

// MetricsServer is a long-running HTTP server exposing /metrics.
type MetricsServer struct {
    addr   string
    server *http.Server
}

// NewMetricsServer creates (but does not start) a metrics HTTP server.
func NewMetricsServer(addr string) *MetricsServer

// Start begins serving /metrics and /debug/* endpoints.
// It is non-blocking; the server runs in a background goroutine.
func (m *MetricsServer) Start()

// Stop gracefully shuts down the metrics server.
func (m *MetricsServer) Stop(ctx context.Context) error

// PushConfig controls optional metrics push to a Prometheus Pushgateway.
type PushConfig struct {
    // URL is the Pushgateway endpoint, e.g. "http://pushgateway:9091".
    URL string
    // Job is the Prometheus job label.
    Job string
    // Interval is how often to push metrics. Default: 15s.
    Interval time.Duration
}

// StartMetricsPusher starts a goroutine that pushes metrics at PushConfig.Interval.
// Call the returned cancel function to stop it.
func StartMetricsPusher(cfg PushConfig) (cancel func())
```

## 6. Public Interfaces

```go
// Logging
func New(subsystem string) *slog.Logger
func SetLevel(level LogLevel)

// Metrics server
func NewMetricsServer(addr string) *MetricsServer
func (m *MetricsServer) Start()
func (m *MetricsServer) Stop(ctx context.Context) error

// Metrics push
func StartMetricsPusher(cfg PushConfig) (cancel func())

// All prometheus.Counter, prometheus.Gauge, prometheus.Histogram variables
// are exported package-level vars for direct use by subsystems.
```

## 7. Internal Algorithms

### Metrics Server Setup
1. Create an `http.ServeMux`
2. Register `promhttp.Handler()` at `GET /metrics`
3. Register `expvar.Handler()` at `GET /debug/vars`
4. The `net/http/pprof` `init()` registers pprof routes on `http.DefaultServeMux`; use a separate mux to avoid exposing pprof on the metrics port unless intended
5. Create `http.Server{Handler: mux}`
6. Start `go server.ListenAndServe()` in background goroutine
7. Log the metrics address at INFO level

### Metrics Push Loop
```
func pusherLoop(cfg PushConfig, stop <-chan struct{}):
    pusher = push.New(cfg.URL, cfg.Job)
    ticker = time.NewTicker(cfg.Interval)
    for:
        select:
        case <-ticker.C:
            err = pusher.Push()
            if err: log.Warn("metrics push failed", "err", err)
        case <-stop:
            return
```

### Structured Logging Setup
On package `init()`:
1. Create a `slog.HandlerOptions{Level: LevelInfo}` (configurable)
2. Create `slog.NewJSONHandler(os.Stderr, opts)` for structured JSON output
3. Call `slog.SetDefault(slog.New(handler))`
4. Subsystems call `obs.New("name")` to get a child logger

## 8. Persistence Model

No persistence. Metrics are in-memory counters/gauges reset on process restart. Log output goes to stderr (or a configured writer); rotation is external.

## 9. Concurrency Model

- `*slog.Logger` is safe for concurrent use
- Prometheus metric types (`Counter`, `Gauge`, `Histogram`) are safe for concurrent use by design
- `MetricsServer.Start()` runs in a single goroutine; `Stop()` uses `server.Shutdown(ctx)`
- `SetLevel()` uses an `atomic.Int64` internally (slog's `LevelVar`)

## 10. Configuration

```go
type ObsConfig struct {
    // LogLevel is the minimum log level. Default: "info".
    LogLevel string `mapstructure:"log_level"`
    // LogFormat is "json" or "text". Default: "json".
    LogFormat string `mapstructure:"log_format"`
    // MetricsAddr is the address for the /metrics HTTP server. Default: "" (disabled).
    MetricsAddr string `mapstructure:"metrics_addr"`
    // PushgatewayURL is the Pushgateway address. Default: "" (disabled).
    PushgatewayURL string `mapstructure:"pushgateway_url"`
    // PushgatewayJob is the Prometheus job label. Default: "goblob".
    PushgatewayJob string `mapstructure:"pushgateway_job"`
    // PushInterval is the Pushgateway push frequency. Default: 15s.
    PushInterval time.Duration `mapstructure:"push_interval"`
}
```

## 11. Observability

The observability subsystem is itself observed minimally:
- A startup `INFO` log line when the metrics server begins listening
- A `WARN` log line on each failed metrics push

## 12. Testing Strategy

- **Unit tests**:
  - `TestNew`: verify returned logger has `subsystem` attribute
  - `TestMetricsRegistration`: verify all metrics register without panic
  - `TestMetricsServer`: start server, GET `/metrics`, assert 200 and Prometheus text format
  - `TestPusherCancellation`: start pusher, cancel, verify goroutine exits
- **Metric value tests**: Increment a counter in a test, scrape `/metrics`, parse text format, assert counter value

All tests use `t.TempDir()` and ephemeral ports (`:0`) to avoid conflicts.

## 13. Open Questions

None.
