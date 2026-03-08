# Feature: Configuration System

## 1. Purpose

The Configuration System provides a unified mechanism for loading, parsing, and exposing configuration to every GoBlob subsystem. It handles per-role TOML config files (`master.toml`, `filer.toml`, `security.toml`), CLI flag integration, and default values via Viper.

Every server role (master, volume, filer, S3 gateway) has its own typed `Config` struct. The configuration system loads from well-known search paths, merges with CLI flag overrides, and validates required fields at startup.

## 2. Responsibilities

- Define typed `Config` structs for each server role
- Load TOML config files from well-known search paths
- Merge CLI flag overrides into config (CLI takes precedence over file)
- Provide default values for all optional fields
- Validate required fields and reject invalid combinations
- Expose a `SecurityConfig` struct loaded from `security.toml`
- Provide config search paths: `.`, `$HOME/.goblob/`, `/etc/goblob/`
- Expose per-path filer configuration (`FilerPathConfig`) for collection/replication/TTL overrides

## 3. Non-Responsibilities

- Does not reload config at runtime (except TLS certs, handled by security subsystem)
- Does not push config to remote nodes
- Does not implement distributed config (etcd, consul); config is file-based
- Does not validate topology constraints (e.g., enough nodes for replication factor)

## 4. Architecture Design

```
CLI flags (pflag/cobra)
        |
        v
+-------------------------------+
|   Viper config loader          |
|   - Read TOML file            |
|   - Merge env vars (optional) |
|   - Apply CLI flag overrides  |
|   - Apply defaults            |
+-------------------------------+
        |
        v
+-------------------------------+
|   Unmarshal into typed Config |
|   structs per subsystem       |
+-------------------------------+
        |
   +----+----+----+----+
   |    |    |    |    |
MasterConfig VolumeConfig FilerConfig SecurityConfig
```

**Search path order** (first found wins):
1. Current working directory `.`
2. `$HOME/.goblob/`
3. `/usr/local/etc/goblob/`
4. `/etc/goblob/`

**Precedence** (highest to lowest):
1. Explicit CLI flags
2. Environment variables (optional, `GOBLOB_` prefix)
3. Config file values
4. Viper defaults

## 5. Core Data Structures (Go)

```go
package config

import (
    "time"
    "goblob/core/types"
)

// MasterConfig holds all configuration for the Master Server.
type MasterConfig struct {
    // Port is the HTTP listen port. Default: 9333.
    Port int `mapstructure:"port"`
    // GRPCPort is the gRPC listen port. Default: Port + 10000.
    GRPCPort int `mapstructure:"grpc_port"`
    // MetaDir is the directory for Raft log and snapshot storage.
    MetaDir string `mapstructure:"meta_dir"`
    // Peers is the list of other master server addresses for Raft quorum.
    // Empty or "none" means single-master mode.
    Peers []string `mapstructure:"peers"`
    // VolumeSizeLimitMB is the maximum size of a single volume in MB. Default: 30000 (30 GB).
    VolumeSizeLimitMB uint32 `mapstructure:"volume_size_limit_mb"`
    // DefaultReplication is the default replica placement if none specified. Default: "000".
    DefaultReplication string `mapstructure:"default_replication"`
    // GarbageThreshold is the fraction of deleted data that triggers compaction. Default: 0.3.
    GarbageThreshold float64 `mapstructure:"garbage_threshold"`
    // VolumeGrowth contains strategy parameters for automatic volume creation.
    VolumeGrowth VolumeGrowthConfig `mapstructure:"volume_growth"`
    // Maintenance contains automated script configuration.
    Maintenance MaintenanceConfig `mapstructure:"maintenance"`
    // DataCenter is the data center label for this master node.
    DataCenter string `mapstructure:"data_center"`
    // Rack is the rack label for this master node.
    Rack string `mapstructure:"rack"`
    // ReplicationAsMin treats replication level as minimum rather than exact.
    ReplicationAsMin bool `mapstructure:"replication_as_min"`
}

// VolumeGrowthConfig controls how aggressively the master creates new volumes.
type VolumeGrowthConfig struct {
    // Copy1Count is volumes to create for replication "000" (no replication). Default: 7.
    Copy1Count int `mapstructure:"copy_1"`
    // Copy2Count is volumes to create for replication "001". Default: 6.
    Copy2Count int `mapstructure:"copy_2"`
    // Copy3Count is volumes to create for replication "010". Default: 3.
    Copy3Count int `mapstructure:"copy_3"`
    // CopyOtherCount is volumes for other replication types. Default: 1.
    CopyOtherCount int `mapstructure:"copy_other"`
    // Threshold is the fraction of writable volumes that may be full before growing. Default: 0.9.
    Threshold float64 `mapstructure:"threshold"`
}

// MaintenanceConfig controls automated cluster maintenance scripts.
type MaintenanceConfig struct {
    // Scripts is a newline/semicolon-separated list of shell commands to run periodically.
    Scripts string `mapstructure:"scripts"`
    // SleepMinutes is the interval between maintenance runs. Default: 17.
    SleepMinutes int `mapstructure:"sleep_minutes"`
}

// VolumeServerConfig holds configuration for a Volume Server instance.
type VolumeServerConfig struct {
    // Port is the HTTP listen port. Default: 8080.
    Port int `mapstructure:"port"`
    // GRPCPort overrides the derived gRPC port. Default: Port + 10000.
    GRPCPort int `mapstructure:"grpc_port"`
    // Masters is the list of master server addresses for discovery.
    Masters []string `mapstructure:"masters"`
    // DataCenter label for this volume server. Default: "".
    DataCenter string `mapstructure:"data_center"`
    // Rack label. Default: "".
    Rack string `mapstructure:"rack"`
    // Directories is the list of data directories, each paired with MaxVolumeCount.
    Directories []DiskDirectoryConfig `mapstructure:"directories"`
    // IndexType controls the needle index backend: "memory", "leveldb", "leveldbMedium", "leveldbLarge".
    IndexType string `mapstructure:"index_type"`
    // ReadMode controls behavior when volume is not local: "local", "proxy", "redirect".
    ReadMode string `mapstructure:"read_mode"`
    // CompactionBytesPerSecond throttles compaction I/O. Default: 0 (unlimited).
    CompactionBytesPerSecond int64 `mapstructure:"compaction_bytes_per_second"`
    // ConcurrentUploadLimitMB caps in-flight upload data in MB. Default: 0 (unlimited).
    ConcurrentUploadLimitMB int64 `mapstructure:"concurrent_upload_limit_mb"`
    // ConcurrentDownloadLimitMB caps in-flight download data in MB. Default: 0 (unlimited).
    ConcurrentDownloadLimitMB int64 `mapstructure:"concurrent_download_limit_mb"`
    // FileSizeLimitMB is the maximum file size to accept. Default: 256.
    FileSizeLimitMB int64 `mapstructure:"file_size_limit_mb"`
    // HeartbeatInterval is how often to send heartbeats to master. Default: 5s.
    HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval"`
    // PreStopSeconds is how many seconds to stop heartbeats before shutdown. Default: 10.
    PreStopSeconds int `mapstructure:"pre_stop_seconds"`
    // PublicPort is a separate read-only HTTP port. Default: 0 (disabled).
    PublicPort int `mapstructure:"public_port"`
    // LevelDBTimeoutHours offloads idle LevelDB indices after this many hours. Default: 0 (never).
    LevelDBTimeoutHours int `mapstructure:"leveldb_timeout_hours"`
}

// DiskDirectoryConfig pairs a data directory with its constraints.
type DiskDirectoryConfig struct {
    // Path is the filesystem path to the directory. Required.
    Path string `mapstructure:"path"`
    // IdxPath is an optional separate directory for .idx files (for faster SSDs).
    IdxPath string `mapstructure:"idx_path"`
    // MaxVolumeCount is the maximum number of volumes to host here. Default: 8.
    MaxVolumeCount int32 `mapstructure:"max_volume_count"`
    // DiskType is "hdd", "ssd", or a custom tag. Default: "".
    DiskType types.DiskType `mapstructure:"disk_type"`
    // Tags is a list of custom placement tags.
    Tags []string `mapstructure:"tags"`
    // MinFreeSpacePercent marks the directory read-only when free space drops below this. Default: 1.
    MinFreeSpacePercent float32 `mapstructure:"min_free_space_percent"`
}

// FilerConfig holds configuration for a Filer Server instance.
type FilerConfig struct {
    // Port is the HTTP listen port. Default: 8888.
    Port int `mapstructure:"port"`
    // GRPCPort overrides the derived gRPC port. Default: Port + 10000.
    GRPCPort int `mapstructure:"grpc_port"`
    // Masters is the list of master server addresses.
    Masters []string `mapstructure:"masters"`
    // DefaultStoreDir is where the embedded LevelDB2 store lives. Default: "./filemetadir".
    DefaultStoreDir string `mapstructure:"default_store_dir"`
    // MaxFileSizeMB is the size threshold above which files are split into chunks. Default: 4.
    MaxFileSizeMB int `mapstructure:"max_file_size_mb"`
    // EncryptVolumeData enables volume-level encryption of blobs. Default: false.
    EncryptVolumeData bool `mapstructure:"encrypt_volume_data"`
    // MaxFilenameLength is the maximum allowed filename length. Default: 255.
    MaxFilenameLength uint32 `mapstructure:"max_filename_length"`
    // BucketsFolder is the filer path prefix for S3 buckets. Default: "/buckets".
    BucketsFolder string `mapstructure:"buckets_folder"`
    // DefaultReplication is used when none is specified per request. Default: "000".
    DefaultReplication string `mapstructure:"default_replication"`
    // DefaultCollection is used when none is specified per request. Default: "".
    DefaultCollection string `mapstructure:"default_collection"`
    // LogFlushIntervalSeconds is how often the metadata log buffer flushes. Default: 60.
    LogFlushIntervalSeconds int `mapstructure:"log_flush_interval_seconds"`
    // ConcurrentUploadLimitMB caps in-flight upload data. Default: 0 (unlimited).
    ConcurrentUploadLimitMB int64 `mapstructure:"concurrent_upload_limit_mb"`
}

// SecurityConfig is loaded from security.toml and applies to all server roles.
type SecurityConfig struct {
    // JWT controls write-access token signing.
    JWT JWTConfig `mapstructure:"jwt"`
    // Guard configures the IP whitelist.
    Guard GuardConfig `mapstructure:"guard"`
    // HTTPS holds per-service TLS certificate paths.
    HTTPS HTTPSConfig `mapstructure:"https"`
    // GRPC holds per-service gRPC TLS certificate paths.
    GRPC GRPCTLSConfig `mapstructure:"grpc"`
    // CORS holds allowed origins for browser access.
    CORS CORSConfig `mapstructure:"cors"`
}

// JWTConfig holds JWT signing key configuration.
type JWTConfig struct {
    // Signing is the write-access signing key configuration.
    Signing JWTKeyConfig `mapstructure:"signing"`
    // FilerSigning is the filer-access signing key.
    FilerSigning JWTKeyConfig `mapstructure:"filer_signing"`
}

// JWTKeyConfig holds a single JWT key and its TTL.
type JWTKeyConfig struct {
    // Key is the HMAC-SHA256 signing secret. Empty = no JWT required.
    Key string `mapstructure:"key"`
    // ExpiresAfterSeconds is the token lifetime. Default: 10. 0 = no expiry.
    ExpiresAfterSeconds int `mapstructure:"expires_after_seconds"`
    // Read holds the separate read-only key (optional).
    Read JWTKeyLeafConfig `mapstructure:"read"`
}

// JWTKeyLeafConfig holds a signing key without recursive read field.
type JWTKeyLeafConfig struct {
    Key                 string `mapstructure:"key"`
    ExpiresAfterSeconds int    `mapstructure:"expires_after_seconds"`
}

// GuardConfig configures the IP whitelist.
type GuardConfig struct {
    // WhiteList is a comma-separated list of IPs and CIDR ranges.
    WhiteList string `mapstructure:"white_list"`
}

// HTTPSConfig holds TLS certificate paths for each service's HTTPS listener.
type HTTPSConfig struct {
    Master TLSCertConfig `mapstructure:"master"`
    Volume TLSCertConfig `mapstructure:"volume"`
    Filer  TLSCertConfig `mapstructure:"filer"`
}

// GRPCTLSConfig holds TLS certificate paths for each service's gRPC listener.
type GRPCTLSConfig struct {
    Master TLSCertConfig `mapstructure:"master"`
    Volume TLSCertConfig `mapstructure:"volume"`
    Filer  TLSCertConfig `mapstructure:"filer"`
}

// TLSCertConfig holds cert/key/ca paths for a single service.
type TLSCertConfig struct {
    // Cert is the path to the TLS certificate file (PEM).
    Cert string `mapstructure:"cert"`
    // Key is the path to the TLS private key file (PEM).
    Key string `mapstructure:"key"`
    // CA is the path to the CA certificate for mTLS. Empty = no mTLS.
    CA string `mapstructure:"ca"`
}

// CORSConfig holds Cross-Origin Resource Sharing settings.
type CORSConfig struct {
    AllowedOrigins string `mapstructure:"allowed_origins"`
}
```

## 6. Public Interfaces

```go
package config

// Loader loads and validates configuration for a given role.
type Loader interface {
    // LoadMasterConfig loads master.toml merged with any CLI flags.
    LoadMasterConfig() (*MasterConfig, error)
    // LoadVolumeConfig loads volume server configuration.
    LoadVolumeConfig() (*VolumeServerConfig, error)
    // LoadFilerConfig loads filer server configuration.
    LoadFilerConfig() (*FilerConfig, error)
    // LoadSecurityConfig loads security.toml; returns zero-value SecurityConfig if absent.
    LoadSecurityConfig() (*SecurityConfig, error)
}

// NewViperLoader creates a Loader backed by Viper, searching the standard config paths.
// cliFlags is a map of flag name -> value to override file config.
func NewViperLoader(cliFlags map[string]interface{}) Loader

// ConfigSearchPaths returns the ordered list of directories to search for config files.
func ConfigSearchPaths() []string
```

## 7. Internal Algorithms

### Config Loading Algorithm
1. Create a new `viper.Viper` instance (not global, to allow test isolation)
2. Set all default values (see §10)
3. Set config name (e.g., `"master"`) and type `"toml"`
4. Add all search paths from `ConfigSearchPaths()`
5. Call `viper.ReadInConfig()` — silently ignore `ConfigFileNotFoundError`
6. Override each key present in `cliFlags` with `viper.Set(key, value)`
7. Call `viper.Unmarshal(&cfg)` into the typed Config struct
8. Run `validate(cfg)` — return error on constraint violations

### Validation Rules
- `MasterConfig.Peers`: count must be odd (1, 3, 5) unless empty/none
- `MasterConfig.MetaDir`: must be writable (attempt `os.MkdirAll` + temp file write)
- `VolumeServerConfig.Directories`: at least 1 directory required
- `VolumeServerConfig.IndexType`: must be one of `memory|leveldb|leveldbMedium|leveldbLarge`
- `VolumeServerConfig.ReadMode`: must be one of `local|proxy|redirect`
- `FilerConfig.Masters`: at least 1 master address required

## 8. Persistence Model

Config files are read-only from GoBlob's perspective. They are TOML text files written by the operator. No runtime config writes occur.

## 9. Concurrency Model

Config structs are loaded once at startup and passed to subsystems as immutable values (pointer to struct, never mutated after init). No locks needed. The TLS certificate file watcher (in the security subsystem) re-reads cert files from disk but does not mutate the `SecurityConfig` struct.

## 10. Configuration

Default values set via `viper.SetDefault`:

| Key | Default |
|-----|---------|
| `port` (master) | 9333 |
| `port` (volume) | 8080 |
| `port` (filer) | 8888 |
| `volume_size_limit_mb` | 30000 |
| `default_replication` | "000" |
| `garbage_threshold` | 0.3 |
| `volume_growth.copy_1` | 7 |
| `volume_growth.copy_2` | 6 |
| `volume_growth.copy_3` | 3 |
| `volume_growth.copy_other` | 1 |
| `volume_growth.threshold` | 0.9 |
| `maintenance.sleep_minutes` | 17 |
| `index_type` | "leveldb" |
| `read_mode` | "redirect" |
| `heartbeat_interval` | "5s" |
| `pre_stop_seconds` | 10 |
| `max_file_size_mb` | 4 |
| `max_filename_length` | 255 |
| `buckets_folder` | "/buckets" |
| `log_flush_interval_seconds` | 60 |
| `jwt.signing.expires_after_seconds` | 10 |
| `jwt.filer_signing.expires_after_seconds` | 10 |

## 11. Observability

Config loading logs the path of the resolved config file at `INFO` level. If no file is found, it logs `INFO: no config file found, using defaults`. Validation failures are logged at `ERROR` before returning.

No metrics are emitted by the config system.

## 12. Testing Strategy

- **Unit tests**: For each `loadXxxConfig()` function, test:
  - No config file present → only defaults
  - Config file with all fields → full unmarshal
  - CLI override takes precedence over file value
  - Invalid field values → validation error returned
- **Test isolation**: Each test creates its own `viper.Viper` instance (not the global singleton)
- **Temp files**: Tests write TOML config to `t.TempDir()` and point the search path there

## 13. Open Questions

None.
