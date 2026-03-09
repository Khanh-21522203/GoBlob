package config

// FilerConfig holds configuration for the filer server.
type FilerConfig struct {
	Port                    int      `mapstructure:"port"`
	GRPCPort                int      `mapstructure:"grpc_port"`
	Masters                 []string `mapstructure:"masters"`
	DefaultStoreDir         string   `mapstructure:"default_store_dir"`
	MaxFileSizeMB           int      `mapstructure:"max_file_size_mb"`
	EncryptVolumeData       bool     `mapstructure:"encrypt_volume_data"`
	MaxFilenameLength       uint32   `mapstructure:"max_filename_length"`
	BucketsFolder           string   `mapstructure:"buckets_folder"`
	DefaultReplication      string   `mapstructure:"default_replication"`
	DefaultCollection       string   `mapstructure:"default_collection"`
	LogFlushIntervalSeconds int      `mapstructure:"log_flush_interval_seconds"`
	ConcurrentUploadLimitMB int64    `mapstructure:"concurrent_upload_limit_mb"`
	DataCenter              string   `mapstructure:"data_center"`
	Rack                    string   `mapstructure:"rack"`
}
