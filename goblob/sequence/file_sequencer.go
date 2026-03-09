package sequence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// FileSequencer is a file-based implementation of Sequencer.
// It persists the max file ID to a file and uses atomic write+rename for crash safety.
type FileSequencer struct {
	mu        sync.Mutex
	cfg       *Config
	maxFileId uint64
	nextId    uint64
	upperBound uint64
	filePath string
}

// NewFileSequencer creates a new FileSequencer.
func NewFileSequencer(cfg *Config) (*FileSequencer, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data_dir cannot be empty")
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	filePath := filepath.Join(cfg.DataDir, "max_needle_id")

	// Load existing max file ID
	maxFileId, err := loadMaxFileId(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load max file id: %w", err)
	}

	fs := &FileSequencer{
		cfg:        cfg,
		maxFileId:  maxFileId,
		nextId:     maxFileId + 1,
		upperBound: maxFileId,
		filePath:   filePath,
	}

	return fs, nil
}

// loadMaxFileId reads the max file ID from disk.
// Returns 0 if the file doesn't exist (fresh start).
func loadMaxFileId(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read file: %w", err)
	}

	// Parse as ASCII decimal
	str := string(data)
	if len(str) == 0 {
		return 0, nil
	}

	// Trim newline and whitespace
	for len(str) > 0 && (str[len(str)-1] == '\n' || str[len(str)-1] == '\r' || str[len(str)-1] == ' ') {
		str = str[:len(str)-1]
	}

	val, err := strconv.ParseUint(str, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse max file id: %w", err)
	}

	return val, nil
}

// saveMaxFileId atomically writes the max file ID to disk.
func saveMaxFileId(path string, maxId uint64) error {
	// Write to temporary file
	tmpPath := path + ".tmp"
	data := strconv.FormatUint(maxId, 10) + "\n"

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	if _, err := f.WriteString(data); err != nil {
		f.Close()
		return fmt.Errorf("failed to write data: %w", err)
	}

	// Sync to ensure data is on disk
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("failed to sync file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename: %w", err)
	}

	// Sync directory
	dir := filepath.Dir(path)
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open directory: %w", err)
	}
	defer dirFile.Close()

	if err := dirFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}

// NextFileId returns the next unique file ID.
func (fs *FileSequencer) NextFileId(ctx context.Context) (uint64, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Check if we need to allocate a new batch
	if fs.nextId > fs.upperBound {
		if err := fs.allocateBatch(); err != nil {
			return 0, fmt.Errorf("failed to allocate batch: %w", err)
		}
	}

	id := fs.nextId
	fs.nextId++

	// Update max file ID if necessary
	if id > fs.maxFileId {
		fs.maxFileId = id
	}

	return id, nil
}

// allocateBatch allocates a new batch of IDs by incrementing the persisted max file ID.
func (fs *FileSequencer) allocateBatch() error {
	// Calculate new upper bound
	newMaxFileId := fs.upperBound + uint64(fs.cfg.StepSize)

	// Persist to disk
	if err := saveMaxFileId(fs.filePath, newMaxFileId); err != nil {
		return fmt.Errorf("failed to save max file id: %w", err)
	}

	fs.nextId = fs.upperBound + 1
	fs.upperBound = newMaxFileId
	fs.maxFileId = newMaxFileId

	return nil
}

// GetMaxFileId returns the current maximum file ID that has been allocated.
func (fs *FileSequencer) GetMaxFileId() uint64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.maxFileId
}

// Close gracefully shuts down the sequencer.
func (fs *FileSequencer) Close() error {
	// Ensure current batch is persisted
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.nextId > 1 {
		// Persist the current max file ID
		if err := saveMaxFileId(fs.filePath, fs.maxFileId); err != nil {
			return fmt.Errorf("failed to save final max file id: %w", err)
		}
	}

	return nil
}
