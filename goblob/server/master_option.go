package server

import "time"

const (
	RaftEngineHashicorp = "hashicorp"
	RaftEngineNative    = "native"
)

// MasterOption holds configuration for the master server.
type MasterOption struct {
	// Host is the hostname or IP to bind to.
	Host string
	// Port is the HTTP port for the master server.
	Port int
	// GRPCPort is the gRPC port for the master server.
	GRPCPort int
	// RaftPort is the TCP port used by the Raft transport.
	// Defaults to Port+1 when 0.
	RaftPort int
	// MetaDir is the directory for storing Raft and sequencer data.
	MetaDir string
	// Peers is the list of initial Raft peer addresses.
	Peers []string
	// RaftEngine selects the consensus engine implementation.
	RaftEngine string
	// VolumeSizeLimitMB is the maximum size of each volume in megabytes.
	VolumeSizeLimitMB uint32
	// DefaultReplication is the default replica placement string (e.g., "000").
	DefaultReplication string
	// GarbageThreshold is the threshold for triggering volume garbage collection.
	GarbageThreshold float64
	// DataCenter is the data center this master belongs to.
	DataCenter string
	// Rack is the rack this master belongs to.
	Rack string
	// JwtExpireSeconds is the JWT token expiration time in seconds.
	JwtExpireSeconds int
	// MaintenanceScripts is the path to maintenance scripts.
	MaintenanceScripts string
	// MaintenanceSleep is the interval between maintenance runs.
	MaintenanceSleep time.Duration
	// ReplicationAsMin indicates whether the replication setting is a minimum.
	ReplicationAsMin bool
	// RatePerSecond is the per-IP HTTP rate limit (requests/sec). 0 = use default.
	RatePerSecond float64
}

// DefaultMasterOption returns sensible defaults for master server configuration.
func DefaultMasterOption() *MasterOption {
	return &MasterOption{
		Host:               "",
		Port:               9333,
		GRPCPort:           19333,
		MetaDir:            "/tmp/goblob/master",
		Peers:              nil,
		RaftEngine:         RaftEngineHashicorp,
		VolumeSizeLimitMB:  256,
		DefaultReplication: "000",
		GarbageThreshold:   0.3,
		DataCenter:         "",
		Rack:               "",
		JwtExpireSeconds:   10,
		MaintenanceScripts: "",
		MaintenanceSleep:   5 * time.Minute,
		ReplicationAsMin:   false,
	}
}
