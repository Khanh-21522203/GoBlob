package topology

import (
	"fmt"
	"sync"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
	"GoBlob/goblob/util"
)

// VolumeLayout manages volumes for a specific combination of:
// collection, replica placement, TTL, and disk type.
type VolumeLayout struct {
	collection      string
	replication     string
	ttl             string
	diskType        types.DiskType
	writableVolumes *util.ConcurrentReadMap[uint32, *VolumeLocation]
	readonlyVolumes *util.ConcurrentReadMap[uint32, *VolumeLocation]
	crowdedVolumes  *util.ConcurrentReadMap[uint32, *VolumeLocation]
	mu              sync.RWMutex
}

// NewVolumeLayout creates a new VolumeLayout.
func NewVolumeLayout(collection, replication, ttl string, diskType types.DiskType) *VolumeLayout {
	return &VolumeLayout{
		collection:      collection,
		replication:     replication,
		ttl:             ttl,
		diskType:        diskType,
		writableVolumes: util.NewConcurrentReadMap[uint32, *VolumeLocation](),
		readonlyVolumes: util.NewConcurrentReadMap[uint32, *VolumeLocation](),
		crowdedVolumes:  util.NewConcurrentReadMap[uint32, *VolumeLocation](),
	}
}

// GetCollection returns the collection name.
func (vl *VolumeLayout) GetCollection() string {
	return vl.collection
}

// GetReplication returns the replica placement.
func (vl *VolumeLayout) GetReplication() string {
	return vl.replication
}

// GetTtl returns the TTL.
func (vl *VolumeLayout) GetTtl() string {
	return vl.ttl
}

// GetDiskType returns the disk type.
func (vl *VolumeLayout) GetDiskType() types.DiskType {
	return vl.diskType
}

// AddVolumeLayout adds a volume location to the layout.
func (vl *VolumeLayout) AddVolumeLayout(loc *VolumeLocation) {
	if loc == nil {
		return
	}

	vid := loc.VolumeId
	if vid == 0 {
		return
	}

	// Check if volume is readonly
	if loc.IsReadOnly() {
		vl.readonlyVolumes.Set(vid, loc)
	} else {
		vl.writableVolumes.Set(vid, loc)
	}
}

// RemoveVolumeLocation removes a volume location from the layout.
func (vl *VolumeLayout) RemoveVolumeLocation(vid uint32) {
	vl.writableVolumes.Delete(vid)
	vl.readonlyVolumes.Delete(vid)
	vl.crowdedVolumes.Delete(vid)
}

// LookupVolume finds the location of a volume.
func (vl *VolumeLayout) LookupVolume(vid uint32) (*VolumeLocation, bool) {
	// Check writable volumes first
	if loc, ok := vl.writableVolumes.Get(vid); ok {
		return loc, true
	}

	// Then check readonly volumes
	if loc, ok := vl.readonlyVolumes.Get(vid); ok {
		return loc, true
	}

	// Finally check crowded volumes
	if loc, ok := vl.crowdedVolumes.Get(vid); ok {
		return loc, true
	}

	return nil, false
}

// ToVolumeLocations converts the layout to protobuf volume locations.
func (vl *VolumeLayout) ToVolumeLocations(vid uint32) []*master_pb.VolumeLocation {
	loc, ok := vl.LookupVolume(vid)
	if !ok || loc == nil {
		return nil
	}

	return []*master_pb.VolumeLocation{
		loc.ToProto(),
	}
}

// GetWritableVolumeCount returns the number of writable volumes.
func (vl *VolumeLayout) GetWritableVolumeCount() int {
	return vl.writableVolumes.Len()
}

// GetReadonlyVolumeCount returns the number of readonly volumes.
func (vl *VolumeLayout) GetReadonlyVolumeCount() int {
	return vl.readonlyVolumes.Len()
}

// GetCrowdedVolumeCount returns the number of crowded volumes.
func (vl *VolumeLayout) GetCrowdedVolumeCount() int {
	return vl.crowdedVolumes.Len()
}

// GetTotalVolumeCount returns the total number of volumes.
func (vl *VolumeLayout) GetTotalVolumeCount() int {
	return vl.GetWritableVolumeCount() + vl.GetReadonlyVolumeCount() + vl.GetCrowdedVolumeCount()
}

// HasFreeSpace returns true if there's free space for new volumes.
func (vl *VolumeLayout) HasFreeSpace() bool {
	// Check if any data node has free space
	hasFree := false
	vl.writableVolumes.Range(func(_ uint32, loc *VolumeLocation) bool {
		if loc.DataNode != nil {
			freeSlots := loc.DataNode.GetFreeVolumeSlots()
			for _, slots := range freeSlots {
				if slots > 0 {
					hasFree = true
					return false // stop iteration
				}
			}
		}
		return true
	})
	return hasFree
}

// FindEmptySlots finds data nodes with free volume slots.
func (vl *VolumeLayout) FindEmptySlots(replication string) []*DataNode {
	var candidates []*DataNode

	// Collect unique data nodes with free space
	seenNodes := make(map[string]*DataNode)

	vl.writableVolumes.Range(func(_ uint32, loc *VolumeLocation) bool {
		if loc.DataNode != nil && loc.DataNode.IsAlive() {
			freeSlots := loc.DataNode.GetFreeVolumeSlots()
			for diskType, slots := range freeSlots {
				if slots > 0 && diskType == vl.diskType {
					nodeId := loc.DataNode.GetId()
					if _, exists := seenNodes[nodeId]; !exists {
						seenNodes[nodeId] = loc.DataNode
						candidates = append(candidates, loc.DataNode)
					}
					break // found a free slot on this disk type
				}
			}
		}
		return true
	})

	return candidates
}

// VolumeLocation represents the location of a volume in the cluster.
type VolumeLocation struct {
	DataNode   *DataNode
	VolumeId   uint32
	collection string
	replication string
	ttl         string
	diskType    types.DiskType
	size        uint64
	isReadOnly  bool
}

// NewVolumeLocation creates a new VolumeLocation.
func NewVolumeLocation(dataNode *DataNode, vid uint32) *VolumeLocation {
	return &VolumeLocation{
		DataNode: dataNode,
		VolumeId: vid,
	}
}

// GetDataNode returns the data node.
func (vl *VolumeLocation) GetDataNode() *DataNode {
	return vl.DataNode
}

// GetVolumeId returns the volume ID.
func (vl *VolumeLocation) GetVolumeId() uint32 {
	return vl.VolumeId
}

// IsReadOnly returns true if the volume is read-only.
func (vl *VolumeLocation) IsReadOnly() bool {
	return vl.isReadOnly
}

// SetReadOnly sets the read-only status.
func (vl *VolumeLocation) SetReadOnly(readonly bool) {
	vl.isReadOnly = readonly
}

// GetSize returns the volume size in bytes.
func (vl *VolumeLocation) GetSize() uint64 {
	return vl.size
}

// SetSize sets the volume size.
func (vl *VolumeLocation) SetSize(size uint64) {
	vl.size = size
}

// ToProto converts the volume location to protobuf format.
func (vl *VolumeLocation) ToProto() *master_pb.VolumeLocation {
	if vl == nil {
		return nil
	}

	return &master_pb.VolumeLocation{
		Url:        vl.DataNode.GetUrl(),
		PublicUrl:  vl.DataNode.GetPublicUrl(),
		DataCenter: vl.DataNode.GetDataCenter(),
		Rack:       vl.DataNode.GetRack(),
	}
}

// VolumeLayoutCollection manages multiple VolumeLayout instances.
type VolumeLayoutCollection struct {
	layouts map[string]*VolumeLayout // key: "collection:replication:ttl:diskType"
	mu      sync.RWMutex
}

// NewVolumeLayoutCollection creates a new VolumeLayoutCollection.
func NewVolumeLayoutCollection() *VolumeLayoutCollection {
	return &VolumeLayoutCollection{
		layouts: make(map[string]*VolumeLayout),
	}
}

// GetOrCreate gets or creates a volume layout.
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

// Get gets a volume layout without creating it.
func (vlc *VolumeLayoutCollection) Get(collection, replication, ttl string, diskType types.DiskType) *VolumeLayout {
	key := vlc.makeKey(collection, replication, ttl, diskType)

	vlc.mu.RLock()
	defer vlc.mu.RUnlock()

	return vlc.layouts[key]
}

// RemoveVolumeFromDataNode removes a volume from all layouts for a specific data node.
func (vlc *VolumeLayoutCollection) RemoveVolumeFromDataNode(dataNodeId string, vid uint32) {
	vlc.mu.Lock()
	defer vlc.mu.Unlock()

	for _, vl := range vlc.layouts {
		// Check if this volume is on this data node
		if loc, ok := vl.LookupVolume(vid); ok {
			if loc.DataNode != nil && loc.DataNode.GetId() == dataNodeId {
				vl.RemoveVolumeLocation(vid)
			}
		}
	}
}

// RemoveDataNode removes all volumes for a data node from all layouts.
func (vlc *VolumeLayoutCollection) RemoveDataNode(dataNodeId string) {
	vlc.mu.Lock()
	defer vlc.mu.Unlock()

	for _, vl := range vlc.layouts {
		// Remove all volumes from this data node
		vl.writableVolumes.Range(func(vid uint32, loc *VolumeLocation) bool {
			if loc.DataNode != nil && loc.DataNode.GetId() == dataNodeId {
				vl.writableVolumes.Delete(vid)
			}
			return true
		})

		vl.readonlyVolumes.Range(func(vid uint32, loc *VolumeLocation) bool {
			if loc.DataNode != nil && loc.DataNode.GetId() == dataNodeId {
				vl.readonlyVolumes.Delete(vid)
			}
			return true
		})

		vl.crowdedVolumes.Range(func(vid uint32, loc *VolumeLocation) bool {
			if loc.DataNode != nil && loc.DataNode.GetId() == dataNodeId {
				vl.crowdedVolumes.Delete(vid)
			}
			return true
		})
	}
}

// LookupVolume finds the location of a volume across all layouts.
func (vlc *VolumeLayoutCollection) LookupVolume(vid uint32) []*master_pb.VolumeLocation {
	var results []*master_pb.VolumeLocation

	vlc.mu.RLock()
	defer vlc.mu.RUnlock()

	for _, vl := range vlc.layouts {
		if loc, ok := vl.LookupVolume(vid); ok {
			results = append(results, loc.ToProto())
		}
	}

	return results
}

// makeKey creates a key for the layouts map.
func (vlc *VolumeLayoutCollection) makeKey(collection, replication, ttl string, diskType types.DiskType) string {
	return fmt.Sprintf("%s:%s:%s:%s", collection, replication, ttl, diskType)
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
