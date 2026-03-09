package storage

import (
	"fmt"
	"sync"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/storage/needle"
	"GoBlob/goblob/storage/volume"
)

// NeedleMapKind specifies the index backend.
type NeedleMapKind int

const (
	NeedleMapInMemory NeedleMapKind = iota
	NeedleMapLevelDb
)

// DiskDirectoryConfig holds configuration for one storage directory.
type DiskDirectoryConfig struct {
	Directory      string
	MaxVolumeCount int
	DiskType       types.DiskType
}

// Store is the top-level storage manager for a volume server.
type Store struct {
	Ip        string
	Port      int
	PublicUrl string

	Locations []*volume.DiskLocation

	needleMapKind NeedleMapKind
	dataCenter    string
	rack          string

	// Concurrency limiting
	uploadMu     sync.Mutex
	uploadCond   *sync.Cond
	downloadMu   sync.Mutex
	downloadCond *sync.Cond
}

// NewStore creates a new Store and loads existing volumes from all directories.
func NewStore(ip string, port int, dirs []DiskDirectoryConfig, mapKind NeedleMapKind) (*Store, error) {
	s := &Store{
		Ip:            ip,
		Port:          port,
		PublicUrl:     fmt.Sprintf("%s:%d", ip, port),
		needleMapKind: mapKind,
	}
	s.uploadCond = sync.NewCond(&s.uploadMu)
	s.downloadCond = sync.NewCond(&s.downloadMu)

	for _, dir := range dirs {
		maxVol := dir.MaxVolumeCount
		if maxVol <= 0 {
			maxVol = 8
		}
		dl, err := volume.NewDiskLocation(dir.Directory, maxVol)
		if err != nil {
			return nil, fmt.Errorf("create disk location %s: %w", dir.Directory, err)
		}
		// Load existing volumes from disk
		if err := dl.LoadExistingVolumes("", types.CurrentNeedleVersion); err != nil {
			return nil, fmt.Errorf("load volumes from %s: %w", dir.Directory, err)
		}
		s.Locations = append(s.Locations, dl)
	}
	return s, nil
}

// GetVolume looks up a volume across all locations.
func (s *Store) GetVolume(id types.VolumeId) (*volume.Volume, bool) {
	for _, dl := range s.Locations {
		if v, ok := dl.GetVolume(id); ok {
			return v, true
		}
	}
	return nil, false
}

// AllocateVolume creates a new volume on the location with the most capacity.
func (s *Store) AllocateVolume(id types.VolumeId, collection string, ver types.NeedleVersion) error {
	var best *volume.DiskLocation
	for _, dl := range s.Locations {
		if !dl.HasSpace() {
			continue
		}
		if best == nil || dl.AvailableVolumeCount() > best.AvailableVolumeCount() {
			best = dl
		}
	}
	if best == nil {
		return fmt.Errorf("no disk location has space for volume %d", id)
	}

	v, err := volume.NewVolume(best.Directory(), collection, id, ver)
	if err != nil {
		return fmt.Errorf("create volume %d: %w", id, err)
	}
	best.AddVolumeObject(v)
	return nil
}

// WriteVolumeNeedle writes a needle to the specified volume.
func (s *Store) WriteVolumeNeedle(vid types.VolumeId, n *needle.Needle) (types.Offset, uint32, error) {
	v, ok := s.GetVolume(vid)
	if !ok {
		return 0, 0, fmt.Errorf("volume %d not found", vid)
	}
	return v.WriteNeedle(n)
}

// ReadVolumeNeedle reads a needle by FileId (includes Cookie verification).
func (s *Store) ReadVolumeNeedle(vid types.VolumeId, fid types.FileId) (*needle.Needle, error) {
	v, ok := s.GetVolume(vid)
	if !ok {
		return nil, fmt.Errorf("volume %d not found", vid)
	}
	return v.ReadNeedleByFileId(fid)
}

// DeleteVolumeNeedle deletes a needle by NeedleId.
func (s *Store) DeleteVolumeNeedle(vid types.VolumeId, nid types.NeedleId) error {
	v, ok := s.GetVolume(vid)
	if !ok {
		return fmt.Errorf("volume %d not found", vid)
	}
	return v.DeleteNeedle(nid)
}

// Close closes all disk locations.
func (s *Store) Close() {
	for _, dl := range s.Locations {
		dl.Close()
	}
}

// SetDataCenter sets the data center label.
func (s *Store) SetDataCenter(dc string) { s.dataCenter = dc }

// SetRack sets the rack label.
func (s *Store) SetRack(rack string) { s.rack = rack }
