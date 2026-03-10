package raft

import "time"

// RaftConfig holds configuration for the Raft consensus layer.
type RaftConfig struct {
	// NodeId is the unique identity of this node (e.g., "ip:port").
	// If empty, the transport's bound address is used.
	NodeId string `mapstructure:"node_id"`

	// BindAddr is the local address for Raft TCP transport.
	BindAddr string `mapstructure:"bind_addr"`

	// MetaDir is the directory for Raft persistent storage (log, stable store, snapshots).
	MetaDir string `mapstructure:"meta_dir"`

	// Peers lists all master addresses (including self).
	// Empty or nil means single-node mode.
	Peers []string `mapstructure:"peers"`

	// SingleMode bootstraps a single-node cluster that immediately becomes leader.
	// Set when -peers=none.
	SingleMode bool `mapstructure:"single_mode"`

	// SnapshotThreshold controls how many logs are retained before a snapshot.
	SnapshotThreshold uint64 `mapstructure:"snapshot_threshold"`

	// SnapshotInterval is how often to check if we need a snapshot.
	SnapshotInterval time.Duration `mapstructure:"snapshot_interval"`

	// HeartbeatTimeout is the timeout between leader heartbeats.
	HeartbeatTimeout time.Duration `mapstructure:"heartbeat_timeout"`

	// ElectionTimeout is the timeout before triggering an election.
	ElectionTimeout time.Duration `mapstructure:"election_timeout"`

	// LeaderLeaseTimeout is how long a leader lease is valid.
	LeaderLeaseTimeout time.Duration `mapstructure:"leader_lease_timeout"`

	// CommitTimeout is how long to wait for log commits.
	CommitTimeout time.Duration `mapstructure:"commit_timeout"`

	// MaxAppendEntries is the max number of log entries to append at once.
	MaxAppendEntries int `mapstructure:"max_append_entries"`
}

// DefaultRaftConfig returns sensible defaults for Raft configuration.
func DefaultRaftConfig() *RaftConfig {
	return &RaftConfig{
		MetaDir:            "./raft",
		BindAddr:           "127.0.0.1:8080",
		SingleMode:         true,
		SnapshotThreshold:  8192,
		SnapshotInterval:   2 * time.Minute,
		HeartbeatTimeout:   1 * time.Second,
		ElectionTimeout:    1 * time.Second,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		MaxAppendEntries:   64,
	}
}

// Validate checks if the configuration is valid.
func (c *RaftConfig) Validate() error {
	if c.MetaDir == "" {
		return &ConfigError{Field: "meta_dir", Message: "cannot be empty"}
	}
	if c.BindAddr == "" {
		return &ConfigError{Field: "bind_addr", Message: "cannot be empty"}
	}
	if c.SnapshotThreshold == 0 {
		return &ConfigError{Field: "snapshot_threshold", Message: "must be positive"}
	}
	if c.HeartbeatTimeout <= 0 {
		return &ConfigError{Field: "heartbeat_timeout", Message: "must be positive"}
	}
	if c.ElectionTimeout <= 0 {
		return &ConfigError{Field: "election_timeout", Message: "must be positive"}
	}
	return nil
}

// ConfigError represents a configuration validation error.
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return "raft config: " + e.Field + " " + e.Message
}
