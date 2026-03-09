package stats

import "github.com/prometheus/client_golang/prometheus"

var (
	// Master metrics
	MasterAssignRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goblob_master_assign_requests_total",
		Help: "Total number of file ID assign requests handled by master",
	})
	MasterLookupRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goblob_master_lookup_requests_total",
		Help: "Total number of volume lookup requests handled by master",
	})
	MasterHeartbeatReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goblob_master_heartbeat_received_total",
		Help: "Total number of heartbeats received by master",
	})

	// Volume server metrics
	VolumeServerReadBytes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goblob_volume_read_bytes_total",
		Help: "Total bytes read from volume files",
	})
	VolumeServerWriteBytes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goblob_volume_write_bytes_total",
		Help: "Total bytes written to volume files",
	})
	VolumeServerNeedleCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "goblob_volume_needle_count",
		Help: "Number of needles per volume",
	}, []string{"volume_id"})
	VolumeServerVolumeSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "goblob_volume_size_bytes",
		Help: "Size of each volume in bytes",
	}, []string{"volume_id"})

	// Filer metrics
	FilerMetadataOps = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "goblob_filer_metadata_ops_total",
		Help: "Total number of filer metadata operations",
	}, []string{"op"}) // op: create, update, delete, list
	FilerLookupCacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goblob_filer_lookup_cache_hits_total",
		Help: "Total number of filer lookup cache hits",
	})
	FilerLookupCacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goblob_filer_lookup_cache_misses_total",
		Help: "Total number of filer lookup cache misses",
	})

	// Request latencies
	RequestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "goblob_request_duration_seconds",
		Help:    "Request latency in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"server", "op"})

	// S3 gateway metrics
	S3RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "goblob_s3_requests_total",
		Help: "Total number of S3 API requests",
	}, []string{"method", "bucket"})
	S3RequestErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "goblob_s3_request_errors_total",
		Help: "Total number of S3 API request errors",
	}, []string{"method", "bucket", "error_type"})
	S3BytesReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "goblob_s3_bytes_received_total",
		Help: "Total bytes received via S3 API",
	}, []string{"bucket"})
	S3BytesSent = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "goblob_s3_bytes_sent_total",
		Help: "Total bytes sent via S3 API",
	}, []string{"bucket"})

	// Admin shell metrics
	AdminShellCommandsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "goblob_admin_shell_commands_total",
		Help: "Total number of admin shell commands executed",
	}, []string{"command", "status"})
)

// Register adds all declared metrics to the given registry.
func Register(r *prometheus.Registry) {
	r.MustRegister(
		// Master metrics
		MasterAssignRequests,
		MasterLookupRequests,
		MasterHeartbeatReceived,

		// Volume server metrics
		VolumeServerReadBytes,
		VolumeServerWriteBytes,
		VolumeServerNeedleCount,
		VolumeServerVolumeSize,

		// Filer metrics
		FilerMetadataOps,
		FilerLookupCacheHits,
		FilerLookupCacheMisses,

		// Request latencies
		RequestLatency,

		// S3 gateway metrics
		S3RequestsTotal,
		S3RequestErrors,
		S3BytesReceived,
		S3BytesSent,

		// Admin shell metrics
		AdminShellCommandsTotal,
	)
}
