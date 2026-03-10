package obs

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	registerOnce sync.Once

	// Master metrics.
	MasterLeadershipGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "goblob",
		Subsystem: "master",
		Name:      "is_leader",
		Help:      "1 if this master is the Raft leader, 0 otherwise.",
	})
	MasterVolumeCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "goblob",
		Subsystem: "master",
		Name:      "volume_count",
		Help:      "Current number of volumes in topology.",
	})
	MasterAssignRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "goblob",
		Subsystem: "master",
		Name:      "assign_requests_total",
		Help:      "Total assign requests handled by master.",
	})

	// Volume metrics.
	VolumeServerNeedleWriteBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goblob",
		Subsystem: "volume",
		Name:      "needle_write_bytes_total",
		Help:      "Total bytes written to volume needles.",
	}, []string{"volume"})
	VolumeServerNeedleReadBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goblob",
		Subsystem: "volume",
		Name:      "needle_read_bytes_total",
		Help:      "Total bytes read from volume needles.",
	}, []string{"volume"})
	VolumeServerNeedleWriteLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "goblob",
		Subsystem: "volume",
		Name:      "needle_write_latency_seconds",
		Help:      "Latency histogram for needle writes.",
		Buckets:   prometheus.DefBuckets,
	})
	VolumeServerNeedleReadLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "goblob",
		Subsystem: "volume",
		Name:      "needle_read_latency_seconds",
		Help:      "Latency histogram for needle reads.",
		Buckets:   prometheus.DefBuckets,
	})
	VolumeServerDiskFreeBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "goblob",
		Subsystem: "volume",
		Name:      "disk_free_bytes",
		Help:      "Free bytes by volume disk location.",
	}, []string{"directory"})

	// Filer metrics.
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
		Help:      "Latency histogram for filer store operations.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"op"})
)

func init() {
	registerMetrics()
}

func registerMetrics() {
	registerOnce.Do(func() {
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
	})
}
