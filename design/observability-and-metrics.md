# Observability and Metrics

### Purpose

Expose runtime telemetry for GoBlob services via Prometheus metrics, debug endpoints, and optional pushgateway publishing.

### Scope

**In scope:**
- Metric registration definitions in `goblob/obs/metrics.go`.
- Metrics/debug HTTP server in `goblob/obs/metrics_server.go`.
- Pushgateway loop in `goblob/obs/push.go`.
- Command-level runtime wiring in `goblob/command/metrics_runtime.go`.

**Out of scope:**
- Business logic that increments metrics in service handlers.
- External dashboard/alert configuration.

### Primary User Flow

1. Operator starts service command with `-metricsPort`.
2. Runtime starts metrics server on `<host>:<metricsPort>`.
3. Scrapers read `/metrics`; engineers inspect `/debug/pprof/*` and `/debug/vars`.
4. Optional pushgateway config (`-pushgatewayURL`, `-pushgatewayJob`) triggers periodic push loop.
5. On shutdown, runtime stops push loop and gracefully closes metrics server.

### System Flow

1. Command boot (`startMetricsRuntime`) checks port:
   - `port <= 0`: no metrics server, no-op shutdown.
   - otherwise create `obs.NewMetricsServer(addr)` and start it.
2. Metrics server mux includes:
   - `/metrics` (Prometheus handler),
   - `/debug/vars` (expvar),
   - `/debug/pprof/*` handlers.
3. Push loop (`StartMetricsPusher`):
   - if URL provided, ticker pushes `prometheus.DefaultGatherer` every interval.
4. Shutdown path:
   - cancel push loop,
   - call `MetricsServer.Stop(ctx)`.

### Data Model

- Registered collectors (`goblob/obs/metrics.go`):
  - Master: leadership gauge, volume count gauge, assign request counter.
  - Volume: read/write bytes counters, read/write latency histograms, disk free gauge vector.
  - Raft: commit/log/snapshot/peer/contact gauges, apply error counter.
  - Filer: request counter vector, store latency histogram vector.
- Metric names are namespaced under `goblob_*` and registered once via `registerOnce`.

### Interfaces and Contracts

- Runtime control:
  - `startMetricsRuntime(host,port,pushURL,pushJob) *metricsRuntime`.
  - `metricsRuntime.Shutdown(ctx)`.
- Metrics server endpoints:
  - `GET /metrics`
  - `GET /debug/vars`
  - `GET /debug/pprof/` and subpaths.
- Push config contract (`obs.PushConfig`):
  - `URL`, `Job`, `Interval`; blank URL disables push.

### Dependencies

**Internal modules:**
- All major services update collectors from `obs/metrics.go`.
- Command package controls metrics server lifecycle.

**External services/libraries:**
- Prometheus Go client (`client_golang`).
- Optional pushgateway endpoint for push mode.

### Failure Modes and Edge Cases

- Invalid/occupied metrics listen address logs server startup/runtime errors.
- Pushgateway failures are logged as warnings and retried on next tick.
- Duplicate collector registration is prevented by `sync.Once`; additional init attempts are ignored.
- Metrics disabled when `metricsPort=0`, reducing observability unless explicitly configured.

### Observability and Debugging

- Metrics server logs start/stop and listen errors through `obs.New("metrics")`.
- Push loop logs `pushgateway push failed` warnings.
- Debugging entry points:
  - `/metrics` for collector output.
  - `/debug/pprof/*` for CPU/heap/goroutine profiling.
  - `/debug/vars` for expvar state.

### Risks and Notes

- Metrics coverage depends on manual instrumentation in handlers; some subsystems (e.g., S3 handler paths) currently have limited dedicated counters.
- Push model sends full default registry each interval; high-cardinality labels can increase push payload size.

Changes:

