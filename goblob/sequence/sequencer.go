package sequence

import (
	"context"
)

// Sequencer generates globally unique, monotonically increasing NeedleIds.
// This is essential for distributed file systems to avoid ID collisions.
type Sequencer interface {
	// NextFileId returns the next unique file ID.
	// Returns an error if the context is canceled or the sequencer fails.
	NextFileId(ctx context.Context) (uint64, error)

	// GetMaxFileId returns the current maximum file ID that has been allocated.
	GetMaxFileId() uint64

	// Close gracefully shuts down the sequencer and releases resources.
	Close() error
}

// Config holds common configuration for sequencer implementations.
type Config struct {
	// StepSize is how many IDs to allocate at once (for performance).
	// Default is 10000.
	StepSize int

	// DataDir is the directory for persistent storage.
	DataDir string
}

// DefaultConfig returns sensible defaults for sequencer configuration.
func DefaultConfig() *Config {
	return &Config{
		StepSize: 10000,
		DataDir:  "./sequence",
	}
}
