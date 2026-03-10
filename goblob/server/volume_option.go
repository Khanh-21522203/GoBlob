package server

import "time"

// VolumeServerOption holds configuration for the volume server.
type VolumeServerOption struct {
	// Host is the hostname or IP to bind to.
	Host string
	// Port is the HTTP port for the volume server.
	Port int
	// GRPCPort is the gRPC port for the volume server.
	GRPCPort int
	// PublicUrl is the public URL for this volume server.
	PublicUrl string
	// Masters is the list of master server addresses.
	Masters []string
	// DataCenter is the data center this volume server belongs to.
	DataCenter string
	// Rack is the rack this volume server belongs to.
	Rack string
	// Directories is the list of storage directories.
	Directories []DiskDirectoryConfig
	// IndexType is the needle map kind ("mem", "leveldb", "leveldb2", "leveldbMedium", "leveldbLarge").
	IndexType string
	// ReadMode is the volume read mode ("local", "proxy", "redirect").
	ReadMode string
	// ConcurrentUploadLimitMB is the max concurrent upload data in MB.
	ConcurrentUploadLimitMB int64
	// ConcurrentDownloadLimitMB is the max concurrent download data in MB.
	ConcurrentDownloadLimitMB int64
	// FileSizeLimitMB is the max file size in MB.
	FileSizeLimitMB int64
	// HeartbeatInterval is the interval between heartbeats to master.
	HeartbeatInterval time.Duration
	// PreStopSeconds is the seconds to wait before shutdown.
	PreStopSeconds int
	// PulseSeconds is the interval for pulse updates.
	PulseSeconds int
	// CacheMaxEntries is the max number of needles to cache in memory (0 = disabled).
	CacheMaxEntries int
}

// DiskDirectoryConfig defines a single storage directory.
type DiskDirectoryConfig struct {
	Path                string
	IdxPath             string
	MaxVolumeCount      int32
	DiskType            string
	MinFreeSpacePercent float32
}

// DefaultVolumeServerOption returns sensible defaults for volume server configuration.
func DefaultVolumeServerOption() *VolumeServerOption {
	return &VolumeServerOption{
		Host:                      "",
		Port:                      8080,
		GRPCPort:                  18080,
		PublicUrl:                 "",
		Masters:                   []string{"localhost:9333"},
		DataCenter:                "",
		Rack:                      "",
		Directories:               []DiskDirectoryConfig{{Path: "./tmp", MaxVolumeCount: 7}},
		IndexType:                 "mem",
		ReadMode:                  "local",
		ConcurrentUploadLimitMB:   0,
		ConcurrentDownloadLimitMB: 0,
		FileSizeLimitMB:           256,
		HeartbeatInterval:         5 * time.Second,
		PreStopSeconds:            10,
		PulseSeconds:              5,
		CacheMaxEntries:           1024,
	}
}
