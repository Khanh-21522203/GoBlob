package sequence

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// FileSequencer is the default implementation of Sequencer for single-master mode.
// It persists the max issued ID to disk using atomic write+rename for crash safety.
//
// State invariants:
//   current = last issued ID (in memory)
//   saved   = last value written to disk (always >= current; saved = current + step at each flush)
//
// On startup: current = saved = value read from file.
// On crash:   at most `step` IDs are wasted (no IDs are ever reused).
type FileSequencer struct {
	mu      sync.Mutex
	dir     string // directory containing the max_needle_id file
	step    uint64 // pre-allocation batch size
	current uint64 // last issued ID
	saved   uint64 // last value written to disk
}

// NewFileSequencer creates a new FileSequencer.
func NewFileSequencer(cfg *Config) (*FileSequencer, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data_dir cannot be empty")
	}
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	step := cfg.StepSize
	if step == 0 {
		step = 10000
	}

	filePath := filepath.Join(cfg.DataDir, "max_needle_id")
	saved, err := loadMaxFileId(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load max file id: %w", err)
	}

	return &FileSequencer{
		dir:     cfg.DataDir,
		step:    step,
		current: saved,
		saved:   saved,
	}, nil
}

// NextFileId returns the start of a batch of count unique IDs [start, start+count-1].
func (fs *FileSequencer) NextFileId(count uint64) uint64 {
	if count == 0 {
		count = 1
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// If the next allocation would exceed our saved ceiling, persist a new ceiling.
	if fs.current+count > fs.saved {
		newSaved := fs.current + fs.step + count
		if err := saveMaxFileId(filepath.Join(fs.dir, "max_needle_id"), newSaved); err != nil {
			// Log the error but continue — IDs remain unique in memory.
			// On restart the sequencer will jump forward by up to step IDs (expected behaviour).
			log.Printf("sequence: WARNING failed to persist max file id: %v", err)
		} else {
			fs.saved = newSaved
		}
	}

	start := fs.current + 1
	fs.current += count
	return start
}

// SetMax advances the sequencer so that all future IDs are > maxId.
// Used during recovery when volume servers report a higher NeedleId than saved.
func (fs *FileSequencer) SetMax(maxId uint64) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if maxId <= fs.current {
		return
	}
	fs.current = maxId
	// Persist immediately to survive a crash.
	newSaved := maxId + fs.step
	if err := saveMaxFileId(filepath.Join(fs.dir, "max_needle_id"), newSaved); err != nil {
		log.Printf("sequence: WARNING failed to persist SetMax: %v", err)
	} else {
		fs.saved = newSaved
	}
}

// GetMax returns the current maximum issued ID.
func (fs *FileSequencer) GetMax() uint64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.current
}

// Close persists the current state to disk.
func (fs *FileSequencer) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return saveMaxFileId(filepath.Join(fs.dir, "max_needle_id"), fs.current)
}

// loadMaxFileId reads the persisted max file ID.
// Returns 0 if the file does not exist (fresh start).
func loadMaxFileId(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read file: %w", err)
	}
	str := strings.TrimSpace(string(data))
	if str == "" {
		return 0, nil
	}
	val, err := strconv.ParseUint(str, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse max file id %q: %w", str, err)
	}
	return val, nil
}

// saveMaxFileId atomically writes maxId to disk using write-to-temp then rename.
func saveMaxFileId(path string, maxId uint64) error {
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
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("failed to sync file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename: %w", err)
	}

	// Directory fsync is not supported on Windows; skip it.
	if runtime.GOOS != "windows" {
		dir := filepath.Dir(path)
		dirFile, err := os.Open(dir)
		if err == nil {
			_ = dirFile.Sync() // best-effort; ignore error
			dirFile.Close()
		}
	}

	return nil
}
