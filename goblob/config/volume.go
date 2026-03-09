package config

import (
	"GoBlob/goblob/core/types"
	"time"
)

// VolumeServerConfig holds configuration for the volume server.
type VolumeServerConfig struct {
	Port                       int                  `mapstructure:"port"`
	GRPCPort                   int                  `mapstructure:"grpc_port"`
	Masters                    []string             `mapstructure:"masters"`
	DataCenter                 string               `mapstructure:"data_center"`
	Rack                       string               `mapstructure:"rack"`
	Directories                []DiskDirectoryConfig `mapstructure:"directories"`
	IndexType                  string               `mapstructure:"index_type"`
	ReadMode                   string               `mapstructure:"read_mode"`
	CompactionBytesPerSecond   int64                `mapstructure:"compaction_bytes_per_second"`
	ConcurrentUploadLimitMB    int64                `mapstructure:"concurrent_upload_limit_mb"`
	ConcurrentDownloadLimitMB  int64                `mapstructure:"concurrent_download_limit_mb"`
	FileSizeLimitMB            int64                `mapstructure:"file_size_limit_mb"`
	HeartbeatInterval          time.Duration        `mapstructure:"heartbeat_interval"`
	PreStopSeconds             int                  `mapstructure:"pre_stop_seconds"`
	PublicPort                 int                  `mapstructure:"public_port"`
	AdminPort                  int                  `mapstructure:"admin_port"`
}

// DiskDirectoryConfig defines a single storage directory.
type DiskDirectoryConfig struct {
	Path                string         `mapstructure:"path"`
	IdxPath             string         `mapstructure:"idx_path"`
	MaxVolumeCount      int32          `mapstructure:"max_volume_count"`
	DiskType            types.DiskType `mapstructure:"disk_type"`
	MinFreeSpacePercent float32        `mapstructure:"min_free_space_percent"`
}
