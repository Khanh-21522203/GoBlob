package config

// MasterConfig holds configuration for the master server.
type MasterConfig struct {
	Port              int                `mapstructure:"port"`
	GRPCPort          int                `mapstructure:"grpc_port"`
	MetaDir           string             `mapstructure:"meta_dir"`
	Peers             []string           `mapstructure:"peers"`
	VolumeSizeLimitMB uint32             `mapstructure:"volume_size_limit_mb"`
	DefaultReplication string            `mapstructure:"default_replication"`
	GarbageThreshold  float64            `mapstructure:"garbage_threshold"`
	VolumeGrowth      VolumeGrowthConfig `mapstructure:"volume_growth"`
	Maintenance       MaintenanceConfig  `mapstructure:"maintenance"`
	DataCenter        string             `mapstructure:"data_center"`
	Rack              string             `mapstructure:"rack"`
	ReplicationAsMin  bool               `mapstructure:"replication_as_min"`
}

// VolumeGrowthConfig controls automatic volume growth.
type VolumeGrowthConfig struct {
	Copy1Count     int     `mapstructure:"copy_1"`
	Copy2Count     int     `mapstructure:"copy_2"`
	Copy3Count     int     `mapstructure:"copy_3"`
	CopyOtherCount int     `mapstructure:"copy_other"`
	Threshold      float64 `mapstructure:"threshold"`
}

// MaintenanceConfig controls automated maintenance tasks.
type MaintenanceConfig struct {
	Scripts      string `mapstructure:"scripts"`
	SleepMinutes int    `mapstructure:"sleep_minutes"`
}
