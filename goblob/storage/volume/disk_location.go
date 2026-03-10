package volume

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"GoBlob/goblob/core/types"
)

// DiskLocation manages all volumes stored in a single directory.
type DiskLocation struct {
	directory      string
	maxVolumeCount int

	mu      sync.RWMutex
	volumes map[types.VolumeId]*Volume

	usedSize   uint64
	maxSize    uint64
	isReadOnly bool
}

// NewDiskLocation creates a DiskLocation for the given directory.
func NewDiskLocation(directory string, maxVolumeCount int) (*DiskLocation, error) {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", directory, err)
	}

	maxSize, err := getDiskSize(directory)
	if err != nil {
		return nil, fmt.Errorf("get disk size: %w", err)
	}

	dl := &DiskLocation{
		directory:      directory,
		maxVolumeCount: maxVolumeCount,
		maxSize:        maxSize,
		volumes:        make(map[types.VolumeId]*Volume),
	}

	if err := dl.calculateUsedSize(); err != nil {
		return nil, fmt.Errorf("calculate used size: %w", err)
	}

	return dl, nil
}

// LoadExistingVolumes scans the directory for existing .dat files and opens each volume.
// Filenames must be in the format "{volumeId}.dat".
func (dl *DiskLocation) LoadExistingVolumes(collection string, version types.NeedleVersion) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	entries, err := os.ReadDir(dl.directory)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", dl.directory, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, VolumeFileExtension) {
			continue
		}
		// Parse volume ID from filename "{id}.dat"
		idStr := name[:len(name)-len(VolumeFileExtension)]
		id64, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			continue // not a volume file
		}
		id := types.VolumeId(id64)
		if _, exists := dl.volumes[id]; exists {
			continue
		}

		v, err := NewVolume(dl.directory, collection, id, version)
		if err != nil {
			return fmt.Errorf("load volume %d: %w", id, err)
		}
		dl.volumes[id] = v
	}
	return nil
}

// GetVolume returns the Volume for the given ID, or (nil, false) if not found.
func (dl *DiskLocation) GetVolume(id types.VolumeId) (*Volume, bool) {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	v, ok := dl.volumes[id]
	return v, ok
}

// AddVolumeObject registers an already-created Volume in this location.
func (dl *DiskLocation) AddVolumeObject(v *Volume) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.volumes[v.ID()] = v
}

// Directory returns the directory path.
func (dl *DiskLocation) Directory() string { return dl.directory }

// MaxVolumeCount returns the configured maximum number of volumes.
func (dl *DiskLocation) MaxVolumeCount() int { return dl.maxVolumeCount }

// AvailableVolumeCount returns how many more volumes can be created here.
func (dl *DiskLocation) AvailableVolumeCount() int {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return dl.maxVolumeCount - len(dl.volumes)
}

// VolumeCount returns the number of currently loaded volumes.
func (dl *DiskLocation) VolumeCount() int {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return len(dl.volumes)
}

// HasSpace returns true if the location can accept a new volume.
func (dl *DiskLocation) HasSpace() bool {
	dl.mu.RLock()
	count := len(dl.volumes)
	usedSize := dl.usedSize
	maxSize := dl.maxSize
	dl.mu.RUnlock()

	if dl.maxVolumeCount > 0 && count >= dl.maxVolumeCount {
		return false
	}
	// If disk size is not available (e.g., on Windows), allow space based on volume count only
	if maxSize == 0 {
		return true
	}
	const minFreeSpace = 1 * 1024 * 1024 * 1024 // 1 GB
	free := int64(maxSize) - int64(usedSize)
	return free >= minFreeSpace
}

// IsReadOnly returns whether this location is read-only.
func (dl *DiskLocation) IsReadOnly() bool {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return dl.isReadOnly
}

// SetReadOnly sets the read-only flag.
func (dl *DiskLocation) SetReadOnly(v bool) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.isReadOnly = v
}

// UsedSize returns total bytes used by volume files.
func (dl *DiskLocation) UsedSize() uint64 {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return dl.usedSize
}

// MaxSize returns the total disk capacity.
func (dl *DiskLocation) MaxSize() uint64 {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return dl.maxSize
}

// FreeSpace returns available bytes.
func (dl *DiskLocation) FreeSpace() uint64 {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	// If disk size is not available (e.g., on Windows), return a reasonable default
	if dl.maxSize == 0 {
		return 100 * 1024 * 1024 * 1024 // 100 GB placeholder
	}
	if dl.maxSize <= dl.usedSize {
		return 0
	}
	return dl.maxSize - dl.usedSize
}

// GetVolumePath returns the .dat path for the given volume ID.
// Filename format: "{id}.dat" (no collection prefix)
func (dl *DiskLocation) GetVolumePath(id types.VolumeId) string {
	return filepath.Join(dl.directory, fmt.Sprintf("%d", id)+VolumeFileExtension)
}

// GetIndexPath returns the .idx path for the given volume ID.
func (dl *DiskLocation) GetIndexPath(id types.VolumeId) string {
	return filepath.Join(dl.directory, fmt.Sprintf("%d", id)+IndexFileExtension)
}

// VolumeExists returns true if the .dat file for the given ID exists on disk.
func (dl *DiskLocation) VolumeExists(id types.VolumeId) bool {
	_, err := os.Stat(dl.GetVolumePath(id))
	return err == nil
}

// DeleteVolume closes the volume, removes it from the map, and deletes the files.
func (dl *DiskLocation) DeleteVolume(id types.VolumeId) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	if dl.isReadOnly {
		return fmt.Errorf("disk location is read-only")
	}

	if v, ok := dl.volumes[id]; ok {
		v.Close()
		delete(dl.volumes, id)
	}

	if err := os.Remove(dl.GetVolumePath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove dat: %w", err)
	}
	if err := os.Remove(dl.GetIndexPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove idx: %w", err)
	}

	_ = dl.calculateUsedSizeUnlocked()
	return nil
}

// GetVolumes returns all loaded volume IDs, sorted ascending.
func (dl *DiskLocation) GetVolumes() []types.VolumeId {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	ids := make([]types.VolumeId, 0, len(dl.volumes))
	for id := range dl.volumes {
		ids = append(ids, id)
	}
	// simple sort
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[i] > ids[j] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}

// Close releases all resources held by this location.
func (dl *DiskLocation) Close() error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	for _, v := range dl.volumes {
		v.Close()
	}
	dl.volumes = make(map[types.VolumeId]*Volume)
	return nil
}

// String returns a human-readable description.
func (dl *DiskLocation) String() string {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	free := int64(dl.maxSize) - int64(dl.usedSize)
	if free < 0 {
		free = 0
	}
	return fmt.Sprintf("DiskLocation{dir=%s, volumes=%d/%d, used=%d/%d bytes, free=%d bytes}",
		dl.directory, len(dl.volumes), dl.maxVolumeCount,
		dl.usedSize, dl.maxSize, free)
}

func (dl *DiskLocation) calculateUsedSize() error {
	return dl.calculateUsedSizeUnlocked()
}

func (dl *DiskLocation) calculateUsedSizeUnlocked() error {
	var total int64
	err := filepath.Walk(dl.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(path, VolumeFileExtension) || strings.HasSuffix(path, IndexFileExtension) {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return err
	}
	dl.usedSize = uint64(total)
	return nil
}

func getDiskSize(directory string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(directory, &stat); err != nil {
		return 0, fmt.Errorf("statfs: %w", err)
	}
	return stat.Blocks * uint64(stat.Bsize), nil
}
