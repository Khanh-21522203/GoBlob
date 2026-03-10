package topology

import (
	"fmt"
	"sync"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
	"GoBlob/goblob/util"
)

// VolumeLayout manages volumes for a specific combination of
// collection, replica placement, TTL, and disk type.
//
// Each volume ID maps to a SLICE of VolumeLocations — one per replica.
type VolumeLayout struct {
	collection  string
	replication string
	ttl         string
	diskType    types.DiskType
	// Maps VolumeId → all replica locations for that volume.
	writableVolumes *util.ConcurrentReadMap[uint32, []*VolumeLocation]
	readonlyVolumes *util.ConcurrentReadMap[uint32, []*VolumeLocation]
	crowdedVolumes  *util.ConcurrentReadMap[uint32, []*VolumeLocation]
	mu              sync.RWMutex
}

// VolumeGrowOption is used by PickForWrite to select a writable volume.
type VolumeGrowOption struct {
	Collection       string
	ReplicaPlacement types.ReplicaPlacement
	Ttl              string
	DiskType         types.DiskType
}

// NewVolumeLayout creates a new VolumeLayout.
func NewVolumeLayout(collection, replication, ttl string, diskType types.DiskType) *VolumeLayout {
	return &VolumeLayout{
		collection:      collection,
		replication:     replication,
		ttl:             ttl,
		diskType:        diskType,
		writableVolumes: util.NewConcurrentReadMap[uint32, []*VolumeLocation](),
		readonlyVolumes: util.NewConcurrentReadMap[uint32, []*VolumeLocation](),
		crowdedVolumes:  util.NewConcurrentReadMap[uint32, []*VolumeLocation](),
	}
}

func (vl *VolumeLayout) GetCollection() string  { return vl.collection }
func (vl *VolumeLayout) GetReplication() string { return vl.replication }
func (vl *VolumeLayout) GetTtl() string         { return vl.ttl }
func (vl *VolumeLayout) GetDiskType() types.DiskType { return vl.diskType }

// AddVolumeLayout registers a volume location (one DataNode hosting a replica).
// Multiple calls with the same VolumeId append locations (one per replica server).
func (vl *VolumeLayout) AddVolumeLayout(loc *VolumeLocation) {
	if loc == nil || loc.VolumeId == 0 {
		return
	}
	vid := loc.VolumeId
	if loc.IsReadOnly() {
		existing, _ := vl.readonlyVolumes.Get(vid)
		vl.readonlyVolumes.Set(vid, append(existing, loc))
	} else {
		existing, _ := vl.writableVolumes.Get(vid)
		vl.writableVolumes.Set(vid, append(existing, loc))
	}
}

// RemoveVolumeLocation removes ALL replica locations for a volume.
func (vl *VolumeLayout) RemoveVolumeLocation(vid uint32) {
	vl.writableVolumes.Delete(vid)
	vl.readonlyVolumes.Delete(vid)
	vl.crowdedVolumes.Delete(vid)
}

// RemoveDataNodeLocation removes a specific DataNode's location from a volume.
// If no locations remain for the volume, the volume entry is deleted.
func (vl *VolumeLayout) RemoveDataNodeLocation(vid uint32, dataNodeId string) {
	filterMap := func(m *util.ConcurrentReadMap[uint32, []*VolumeLocation]) {
		locs, ok := m.Get(vid)
		if !ok {
			return
		}
		filtered := locs[:0]
		for _, loc := range locs {
			if loc.DataNode == nil || loc.DataNode.GetId() != dataNodeId {
				filtered = append(filtered, loc)
			}
		}
		if len(filtered) == 0 {
			m.Delete(vid)
		} else {
			m.Set(vid, filtered)
		}
	}
	filterMap(vl.writableVolumes)
	filterMap(vl.readonlyVolumes)
	filterMap(vl.crowdedVolumes)
}

// LookupVolume returns all replica locations for a volume.
func (vl *VolumeLayout) LookupVolume(vid uint32) ([]*VolumeLocation, bool) {
	if locs, ok := vl.writableVolumes.Get(vid); ok {
		return locs, true
	}
	if locs, ok := vl.readonlyVolumes.Get(vid); ok {
		return locs, true
	}
	if locs, ok := vl.crowdedVolumes.Get(vid); ok {
		return locs, true
	}
	return nil, false
}

// PickForWrite selects a writable volume for a new upload.
// Returns the VolumeId and all replica locations, or an error if none available.
func (vl *VolumeLayout) PickForWrite(_ *VolumeGrowOption) (uint32, []*VolumeLocation, error) {
	var vid uint32
	var locs []*VolumeLocation
	vl.writableVolumes.Range(func(v uint32, ls []*VolumeLocation) bool {
		// Skip volumes whose DataNode has gone stale.
		if len(ls) > 0 && ls[0].DataNode != nil && !ls[0].DataNode.IsAlive() {
			return true
		}
		vid = v
		locs = ls
		return false // stop after first candidate
	})
	if vid == 0 {
		return 0, nil, fmt.Errorf("no writable volume available")
	}
	return vid, locs, nil
}

// SetVolumeWritable moves a volume from readonly/crowded to writable.
func (vl *VolumeLayout) SetVolumeWritable(vid uint32) {
	if locs, ok := vl.readonlyVolumes.Get(vid); ok {
		vl.readonlyVolumes.Delete(vid)
		vl.writableVolumes.Set(vid, locs)
		return
	}
	if locs, ok := vl.crowdedVolumes.Get(vid); ok {
		vl.crowdedVolumes.Delete(vid)
		vl.writableVolumes.Set(vid, locs)
	}
}

// SetVolumeReadOnly moves a volume from writable/crowded to readonly.
func (vl *VolumeLayout) SetVolumeReadOnly(vid uint32) {
	if locs, ok := vl.writableVolumes.Get(vid); ok {
		vl.writableVolumes.Delete(vid)
		vl.readonlyVolumes.Set(vid, locs)
		return
	}
	if locs, ok := vl.crowdedVolumes.Get(vid); ok {
		vl.crowdedVolumes.Delete(vid)
		vl.readonlyVolumes.Set(vid, locs)
	}
}

// HasWritableVolume returns true if there is at least one writable volume.
func (vl *VolumeLayout) HasWritableVolume() bool {
	return vl.writableVolumes.Len() > 0
}

// GetWritableVolumeCount returns the number of writable volumes.
func (vl *VolumeLayout) GetWritableVolumeCount() int { return vl.writableVolumes.Len() }

// GetReadonlyVolumeCount returns the number of readonly volumes.
func (vl *VolumeLayout) GetReadonlyVolumeCount() int { return vl.readonlyVolumes.Len() }

// GetCrowdedVolumeCount returns the number of crowded volumes.
func (vl *VolumeLayout) GetCrowdedVolumeCount() int { return vl.crowdedVolumes.Len() }

// GetTotalVolumeCount returns the total number of distinct volume IDs.
func (vl *VolumeLayout) GetTotalVolumeCount() int {
	return vl.GetWritableVolumeCount() + vl.GetReadonlyVolumeCount() + vl.GetCrowdedVolumeCount()
}

// HasFreeSpace returns true if any DataNode hosting a writable volume has free slots.
func (vl *VolumeLayout) HasFreeSpace() bool {
	hasFree := false
	vl.writableVolumes.Range(func(_ uint32, locs []*VolumeLocation) bool {
		for _, loc := range locs {
			if loc.DataNode != nil {
				for _, slots := range loc.DataNode.GetFreeVolumeSlots() {
					if slots > 0 {
						hasFree = true
						return false
					}
				}
			}
		}
		return true
	})
	return hasFree
}

// FindEmptySlots returns DataNodes with free volume slots for this disk type.
func (vl *VolumeLayout) FindEmptySlots(_ string) []*DataNode {
	seenNodes := make(map[string]*DataNode)
	var candidates []*DataNode

	vl.writableVolumes.Range(func(_ uint32, locs []*VolumeLocation) bool {
		for _, loc := range locs {
			if loc.DataNode == nil || !loc.DataNode.IsAlive() {
				continue
			}
			nodeId := loc.DataNode.GetId()
			if _, seen := seenNodes[nodeId]; seen {
				continue
			}
			freeSlots := loc.DataNode.GetFreeVolumeSlots()
			if slots, ok := freeSlots[vl.diskType]; ok && slots > 0 {
				seenNodes[nodeId] = loc.DataNode
				candidates = append(candidates, loc.DataNode)
			}
		}
		return true
	})
	return candidates
}

// ToVolumeLocations converts all replica locations for a volume to protobuf format.
func (vl *VolumeLayout) ToVolumeLocations(vid uint32) []*master_pb.VolumeLocation {
	locs, ok := vl.LookupVolume(vid)
	if !ok {
		return nil
	}
	result := make([]*master_pb.VolumeLocation, 0, len(locs))
	for _, loc := range locs {
		if p := loc.ToProto(); p != nil {
			result = append(result, p)
		}
	}
	return result
}

// VolumeLocation represents one server hosting a volume replica.
type VolumeLocation struct {
	DataNode   *DataNode
	VolumeId   uint32
	collection  string
	replication string
	ttl         string
	diskType    types.DiskType
	size        uint64
	isReadOnly  bool
}

// NewVolumeLocation creates a new VolumeLocation.
func NewVolumeLocation(dataNode *DataNode, vid uint32) *VolumeLocation {
	return &VolumeLocation{DataNode: dataNode, VolumeId: vid}
}

func (vl *VolumeLocation) GetDataNode() *DataNode { return vl.DataNode }
func (vl *VolumeLocation) GetVolumeId() uint32    { return vl.VolumeId }
func (vl *VolumeLocation) IsReadOnly() bool        { return vl.isReadOnly }
func (vl *VolumeLocation) SetReadOnly(ro bool)     { vl.isReadOnly = ro }
func (vl *VolumeLocation) GetSize() uint64         { return vl.size }
func (vl *VolumeLocation) SetSize(size uint64)     { vl.size = size }

// ToProto converts the volume location to protobuf format.
func (vl *VolumeLocation) ToProto() *master_pb.VolumeLocation {
	if vl == nil || vl.DataNode == nil {
		return nil
	}
	return &master_pb.VolumeLocation{
		Url:        vl.DataNode.GetUrl(),
		PublicUrl:  vl.DataNode.GetPublicUrl(),
		DataCenter: vl.DataNode.GetDataCenter(),
		Rack:       vl.DataNode.GetRack(),
	}
}

// --- VolumeLayoutCollection ---

// VolumeLayoutCollection manages multiple VolumeLayout instances keyed by
// "collection:replication:ttl:diskType".
type VolumeLayoutCollection struct {
	layouts map[string]*VolumeLayout
	mu      sync.RWMutex
}

func NewVolumeLayoutCollection() *VolumeLayoutCollection {
	return &VolumeLayoutCollection{layouts: make(map[string]*VolumeLayout)}
}

func (vlc *VolumeLayoutCollection) makeKey(collection, replication, ttl string, diskType types.DiskType) string {
	return fmt.Sprintf("%s:%s:%s:%s", collection, replication, ttl, diskType)
}

// GetOrCreate gets or creates a volume layout for the given parameters.
func (vlc *VolumeLayoutCollection) GetOrCreate(collection, replication, ttl string, diskType types.DiskType) *VolumeLayout {
	key := vlc.makeKey(collection, replication, ttl, diskType)
	vlc.mu.Lock()
	defer vlc.mu.Unlock()
	if vl, ok := vlc.layouts[key]; ok {
		return vl
	}
	vl := NewVolumeLayout(collection, replication, ttl, diskType)
	vlc.layouts[key] = vl
	return vl
}

// Get returns a layout without creating it.
func (vlc *VolumeLayoutCollection) Get(collection, replication, ttl string, diskType types.DiskType) *VolumeLayout {
	key := vlc.makeKey(collection, replication, ttl, diskType)
	vlc.mu.RLock()
	defer vlc.mu.RUnlock()
	return vlc.layouts[key]
}

// RemoveVolumeFromDataNode removes a DataNode's location from a specific volume across all layouts.
func (vlc *VolumeLayoutCollection) RemoveVolumeFromDataNode(dataNodeId string, vid uint32) {
	vlc.mu.Lock()
	defer vlc.mu.Unlock()
	for _, vl := range vlc.layouts {
		vl.RemoveDataNodeLocation(vid, dataNodeId)
	}
}

// RemoveDataNode removes all volume locations belonging to a DataNode from all layouts.
func (vlc *VolumeLayoutCollection) RemoveDataNode(dataNodeId string) {
	vlc.mu.Lock()
	defer vlc.mu.Unlock()

	for _, vl := range vlc.layouts {
		removeFromMap := func(m *util.ConcurrentReadMap[uint32, []*VolumeLocation]) {
			var toDelete []uint32
			type update struct {
				vid  uint32
				locs []*VolumeLocation
			}
			var toUpdate []update

			m.Range(func(vid uint32, locs []*VolumeLocation) bool {
				filtered := locs[:0]
				for _, loc := range locs {
					if loc.DataNode == nil || loc.DataNode.GetId() != dataNodeId {
						filtered = append(filtered, loc)
					}
				}
				if len(filtered) == 0 {
					toDelete = append(toDelete, vid)
				} else if len(filtered) < len(locs) {
					toUpdate = append(toUpdate, update{vid, filtered})
				}
				return true
			})
			for _, vid := range toDelete {
				m.Delete(vid)
			}
			for _, u := range toUpdate {
				m.Set(u.vid, u.locs)
			}
		}
		removeFromMap(vl.writableVolumes)
		removeFromMap(vl.readonlyVolumes)
		removeFromMap(vl.crowdedVolumes)
	}
}

// LookupVolume finds all protobuf locations for a volume across all layouts.
func (vlc *VolumeLayoutCollection) LookupVolume(vid uint32) []*master_pb.VolumeLocation {
	vlc.mu.RLock()
	defer vlc.mu.RUnlock()

	var results []*master_pb.VolumeLocation
	for _, vl := range vlc.layouts {
		if locs, ok := vl.LookupVolume(vid); ok {
			for _, loc := range locs {
				if p := loc.ToProto(); p != nil {
					results = append(results, p)
				}
			}
		}
	}
	return results
}

// ForEachLayout iterates over all layouts.
func (vlc *VolumeLayoutCollection) ForEachLayout(fn func(vl *VolumeLayout) bool) {
	vlc.mu.RLock()
	defer vlc.mu.RUnlock()
	for _, vl := range vlc.layouts {
		if !fn(vl) {
			break
		}
	}
}

// GetLayoutCount returns the number of layouts.
func (vlc *VolumeLayoutCollection) GetLayoutCount() int {
	vlc.mu.RLock()
	defer vlc.mu.RUnlock()
	return len(vlc.layouts)
}
