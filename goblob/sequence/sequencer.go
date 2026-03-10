package sequence

// Sequencer generates globally unique, monotonically increasing NeedleIds.
// This is essential for distributed file systems to avoid ID collisions.
type Sequencer interface {
	// NextFileId returns the start of a batch of count unique IDs.
	// Caller receives IDs [start, start+count-1] inclusive.
	NextFileId(count uint64) uint64

	// SetMax advances the counter to at least maxId (used during recovery).
	// Called when volume servers report higher NeedleIds than the sequencer knows.
	SetMax(maxId uint64)

	// GetMax returns the current maximum issued ID.
	GetMax() uint64

	// Close gracefully shuts down the sequencer and releases resources.
	Close() error
}

// Config holds common configuration for sequencer implementations.
type Config struct {
	// StepSize is how many IDs to pre-allocate at once (for crash safety).
	// On crash, at most StepSize IDs may be wasted. Default: 10000.
	StepSize uint64

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
