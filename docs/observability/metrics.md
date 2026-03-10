# Observability Metrics

Expose metrics with `-metricsPort` on each command.

Endpoints:
- `/metrics`
- `/debug/vars`
- `/debug/pprof/`

Selected metrics:
- `goblob_master_is_leader`
- `goblob_master_volume_count`
- `goblob_master_assign_requests_total`
- `goblob_volume_needle_write_bytes_total`
- `goblob_volume_needle_read_bytes_total`
- `goblob_filer_requests_total`
- `goblob_filer_store_latency_seconds`
