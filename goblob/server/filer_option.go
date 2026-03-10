package server

// FilerOption holds configuration for the filer server.
type FilerOption struct {
	// Host is the hostname or IP to bind to.
	Host string
	// Port is the HTTP port for the filer server.
	Port int
	// GRPCPort is the gRPC port for the filer server.
	GRPCPort int
	// Masters is the list of master server addresses.
	Masters []string
	// DefaultStoreDir is the default directory for filer store data.
	DefaultStoreDir string
	// MaxFileSizeMB is the maximum file size in MB.
	MaxFileSizeMB int
	// BucketsFolder is the folder name for bucket metadata.
	BucketsFolder string
	// DefaultReplication is the default replication setting.
	DefaultReplication string
	// DefaultCollection is the default collection name.
	DefaultCollection string
	// LogFlushIntervalSeconds is the interval for flushing log buffers.
	LogFlushIntervalSeconds int
	// ConcurrentUploadLimitMB is the max concurrent upload data in MB.
	ConcurrentUploadLimitMB int64
	// DataCenter is the data center this filer belongs to.
	DataCenter string
	// Rack is the rack this filer belongs to.
	Rack string
}

// DefaultFilerOption returns sensible defaults for filer server configuration.
func DefaultFilerOption() *FilerOption {
	return &FilerOption{
		Host:                     "",
		Port:                     8888,
		GRPCPort:                 18888,
		Masters:                  []string{"localhost:9333"},
		DefaultStoreDir:          "./tmp/filer",
		MaxFileSizeMB:            256,
		BucketsFolder:            "/buckets",
		DefaultReplication:       "000",
		DefaultCollection:        "default",
		LogFlushIntervalSeconds:  5,
		ConcurrentUploadLimitMB: 0,
		DataCenter:               "",
		Rack:                     "",
	}
}
