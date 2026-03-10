package config

import (
	"github.com/spf13/viper"
)

// Loader interface for loading configuration.
type Loader interface {
	LoadMasterConfig() (*MasterConfig, error)
	LoadVolumeConfig() (*VolumeServerConfig, error)
	LoadFilerConfig() (*FilerConfig, error)
	LoadSecurityConfig() (*SecurityConfig, error)
}

// ViperLoader implements Loader using Viper.
type ViperLoader struct {
	viper *viper.Viper
}

// NewViperLoader creates a Loader using Viper, searching standard config paths.
// cliFlags overrides file values: key = mapstructure tag name, value = CLI arg value.
func NewViperLoader(cliFlags map[string]interface{}) Loader {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Add config paths
	for _, path := range ConfigSearchPaths() {
		v.AddConfigPath(path)
	}

	// Read config file (silently ignore if not found)
	v.SetConfigType("toml")
	v.ReadInConfig()

	// Apply CLI overrides
	for key, value := range cliFlags {
		v.Set(key, value)
	}

	return &ViperLoader{viper: v}
}

// ConfigSearchPaths returns standard config file search paths.
func ConfigSearchPaths() []string {
	return []string{
		".",
		"$HOME/.goblob/",
		"/usr/local/etc/goblob/",
		"/etc/goblob/",
	}
}

// setDefaults sets default configuration values that are common across all servers.
func setDefaults(v *viper.Viper) {
	// Common defaults can go here
	// Most defaults are now set per-config-type in the Load methods
}

// LoadMasterConfig loads and validates master configuration.
func (l *ViperLoader) LoadMasterConfig() (*MasterConfig, error) {
	v := l.viper
	v.SetConfigName("master")

	// Try to read the config file; ignore errors if not found
	v.ReadInConfig()

	// Set master-specific defaults
	v.SetDefault("port", DefaultMasterHTTPPort)
	v.SetDefault("grpc_port", DefaultMasterGRPCPort)
	v.SetDefault("volume_size_limit_mb", DefaultVolumeSizeLimitMB)
	v.SetDefault("default_replication", DefaultReplication)
	v.SetDefault("garbage_threshold", DefaultGarbageThreshold)
	v.SetDefault("volume_growth.copy_1", DefaultVolumeGrowthCopy1)
	v.SetDefault("volume_growth.copy_2", DefaultVolumeGrowthCopy2)
	v.SetDefault("volume_growth.copy_3", DefaultVolumeGrowthCopy3)
	v.SetDefault("volume_growth.copy_other", DefaultVolumeGrowthCopyOther)
	v.SetDefault("volume_growth.threshold", DefaultVolumeGrowthThreshold)
	v.SetDefault("maintenance.sleep_minutes", DefaultMaintenanceSleepMinutes)

	var cfg MasterConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := ValidateMasterConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadVolumeConfig loads and validates volume server configuration.
func (l *ViperLoader) LoadVolumeConfig() (*VolumeServerConfig, error) {
	v := l.viper
	v.SetConfigName("volume")

	// Try to read the config file; ignore errors if not found
	v.ReadInConfig()

	// Set volume-specific defaults
	v.SetDefault("port", DefaultVolumeHTTPPort)
	v.SetDefault("grpc_port", DefaultVolumeGRPCPort)
	v.SetDefault("index_type", DefaultIndexType)
	v.SetDefault("read_mode", DefaultReadMode)
	v.SetDefault("heartbeat_interval", DefaultHeartbeatInterval)
	v.SetDefault("pre_stop_seconds", DefaultPreStopSeconds)
	v.SetDefault("max_file_size_mb", DefaultMaxFileSizeMB)

	var cfg VolumeServerConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := ValidateVolumeConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadFilerConfig loads and validates filer configuration.
func (l *ViperLoader) LoadFilerConfig() (*FilerConfig, error) {
	v := l.viper
	v.SetConfigName("filer")

	// Try to read the config file; ignore errors if not found
	v.ReadInConfig()

	// Set filer-specific defaults
	v.SetDefault("port", DefaultFilerHTTPPort)
	v.SetDefault("grpc_port", DefaultFilerGRPCPort)
	v.SetDefault("max_file_size_mb", DefaultMaxFileSizeMB)
	v.SetDefault("max_filename_length", DefaultMaxFilenameLength)
	v.SetDefault("buckets_folder", DefaultBucketsFolder)
	v.SetDefault("log_flush_interval_seconds", DefaultLogFlushIntervalSeconds)

	var cfg FilerConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := ValidateFilerConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadSecurityConfig loads security configuration from security.toml.
func (l *ViperLoader) LoadSecurityConfig() (*SecurityConfig, error) {
	v := l.viper
	v.SetConfigName("security")
	_ = v.ReadInConfig()

	var cfg SecurityConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Constants for default values
const (
	DefaultMasterHTTPPort          = 9333
	DefaultMasterGRPCPort          = 19333
	DefaultVolumeHTTPPort          = 8080
	DefaultVolumeGRPCPort          = 18080
	DefaultFilerHTTPPort           = 8888
	DefaultFilerGRPCPort           = 18888
	DefaultS3HTTPPort              = 8333
	DefaultVolumeSizeLimitMB       = 30000
	DefaultReplication             = "000"
	DefaultGarbageThreshold        = 0.3
	DefaultVolumeGrowthCopy1       = 7
	DefaultVolumeGrowthCopy2       = 6
	DefaultVolumeGrowthCopy3       = 3
	DefaultVolumeGrowthCopyOther   = 1
	DefaultVolumeGrowthThreshold   = 0.9
	DefaultMaintenanceSleepMinutes = 17
	DefaultIndexType               = "leveldb"
	DefaultReadMode                = "redirect"
	DefaultHeartbeatInterval       = "5s"
	DefaultPreStopSeconds          = 10
	DefaultMaxFileSizeMB           = 4
	DefaultMaxFilenameLength       = 255
	DefaultBucketsFolder           = "/buckets"
	DefaultLogFlushIntervalSeconds = 60
)
