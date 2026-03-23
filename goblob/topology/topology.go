package topology

import (
	"fmt"
	"sort"
	"sync"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
)

// Topology is the root of the cluster topology tree.
// It manages the hierarchical structure: DataCenter → Rack → DataNode → DiskInfo → Volumes
type Topology struct {
	*NodeImpl
	volumeLayouts *VolumeLayoutCollection
	nextVolumeId  uint32
	mu            sync.RWMutex
}

// NewTopology creates a new empty Topology.
func NewTopology() *Topology {
	return &Topology{
		NodeImpl:      NewNodeImpl("root"),
		volumeLayouts: NewVolumeLayoutCollection(),
		nextVolumeId:  1,
	}
}

// ProcessJoinMessage processes a heartbeat from a volume server and updates the topology.
// This is called on every heartbeat and must be fast and idempotent.
func (t *Topology) ProcessJoinMessage(hb *master_pb.Heartbeat) error {
	if hb == nil {
		return fmt.Errorf("heartbeat is nil")
	}

	// Extract node info
	dataCenter := hb.DataCenter
	rack := hb.Rack
	nodeId := NodeIDFromHeartbeat(hb)

	// Find or create the data node node
	dataNode := t.findOrCreateDataNode(dataCenter, rack, nodeId)

	// Update the node from heartbeat (sets URL, etc.)
	if err := dataNode.UpdateFromHeartbeat(hb); err != nil {
		return fmt.Errorf("failed to update data node from heartbeat: %w", err)
	}

	// Update the node's last seen time
	dataNode.UpdateLastSeen()

	knownVolumes := make(map[uint32]struct{})
	for _, vol := range dataNode.GetVolumes() {
		knownVolumes[vol.GetId()] = struct{}{}
	}

	// Process volumes
	for _, volInfo := range hb.Volumes {
		if err := t.processVolume(dataNode, volInfo); err != nil {
			return fmt.Errorf("failed to process volume %d: %w", volInfo.Id, err)
		}
		delete(knownVolumes, volInfo.GetId())
	}

	// Process deleted volumes
	for _, vid := range hb.DeletedVids {
		t.removeVolume(dataNode, vid)
		delete(knownVolumes, vid)
	}

	// Process new volume IDs (if any)
	for _, vid := range hb.NewVids {
		// New volumes are already in the Volumes list, so we just need to ensure they're tracked
		// This is a no-op since we already processed all volumes above
		_ = vid
	}

	// Heartbeats send full current volume set, so any leftover known volume is stale.
	for vid := range knownVolumes {
		t.removeVolume(dataNode, vid)
	}

	// Update max volume counts
	if len(hb.MaxVolumeCounts) > 0 {
		dataNode.UpdateMaxVolumeCounts(hb.MaxVolumeCounts)
	}

	return nil
}

// findOrCreateDataNode finds or creates a data node in the topology tree.
func (t *Topology) findOrCreateDataNode(dc, rack, nodeId string) *DataNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	dcNode := t.GetOrCreateDataCenter(dc)
	rackNode := dcNode.GetOrCreateRack(rack)
	return rackNode.GetOrCreateDataNode(nodeId)
}

// processVolume processes a single volume from a heartbeat.
func (t *Topology) processVolume(dataNode *DataNode, volInfo *master_pb.VolumeInformationMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Get or create the volume layout for this collection.
	// Preserve the empty string as-is — normalising "" to "default" would
	// create a different layout key than what handleAssign uses (collection "").
	collection := volInfo.Collection

	// Convert replica placement uint32 to string (e.g., 0 -> "000")
	replication := types.ParseReplicaPlacement(byte(volInfo.ReplicaPlacement)).String()

	ttl := volInfo.Ttl
	diskType := volInfo.DiskType
	if diskType == "" {
		diskType = string(types.DefaultDiskType)
	}

	// Get or create volume layout
	vl := t.volumeLayouts.GetOrCreate(collection, replication, ttl, types.DiskType(diskType))

	// Add volume to the layout, preserving the readonly flag from the heartbeat.
	vl.AddVolumeLayout(&VolumeLocation{
		DataNode:   dataNode,
		VolumeId:   volInfo.Id,
		isReadOnly: volInfo.ReadOnly,
	})

	// Update the data node's disk info
	dataNode.AddOrUpdateVolume(volInfo)
	t.setMaxVolumeIDUnsafe(volInfo.Id)

	return nil
}

// removeVolume removes a volume from the topology.
func (t *Topology) removeVolume(dataNode *DataNode, vid uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Find and remove the volume from all layouts
	t.volumeLayouts.RemoveVolumeFromDataNode(dataNode.GetId(), vid)

	// Remove from the data node
	dataNode.DeleteVolume(vid)
}

// LookupVolumeLocation looks up the locations for a given volume.
func (t *Topology) LookupVolumeLocation(vid uint32) []*master_pb.VolumeLocation {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.volumeLayouts.LookupVolume(vid)
}

// AllocateVolumeCandidates finds candidate data nodes for allocating a new volume.
func (t *Topology) AllocateVolumeCandidates(collection string, replicaPlacement types.ReplicaPlacement, ttl string, diskType types.DiskType) ([]*DataNode, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	_ = collection
	_ = ttl
	_ = replicaPlacement

	var candidates []*DataNode
	t.forEachNode(t.NodeImpl, func(node Node) bool {
		dn, ok := node.ToDataNode()
		if !ok {
			return true
		}
		if !dn.IsAlive() || !dn.HasFreeSlot(diskType) {
			return true
		}
		candidates = append(candidates, dn)
		return true
	})
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].GetId() < candidates[j].GetId()
	})
	return candidates, nil
}

// GetVolumeLayout returns the volume layout for the given parameters.
func (t *Topology) GetVolumeLayout(collection, replication, ttl string, diskType types.DiskType) *VolumeLayout {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.volumeLayouts.Get(collection, replication, ttl, diskType)
}

// GetOrCreateVolumeLayout gets or creates a volume layout.
func (t *Topology) GetOrCreateVolumeLayout(collection, replication, ttl string, diskType types.DiskType) *VolumeLayout {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.volumeLayouts.GetOrCreate(collection, replication, ttl, diskType)
}

// HasFreeSpace checks if there's free space for a new volume.
func (t *Topology) HasFreeSpace(collection, replication, ttl string, diskType types.DiskType) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	vl := t.volumeLayouts.Get(collection, replication, ttl, diskType)
	if vl == nil {
		return false
	}

	return vl.HasFreeSpace()
}

// GetDataCenter gets a data center by ID.
func (t *Topology) GetDataCenter(id string) *DataCenter {
	t.mu.RLock()
	defer t.mu.RUnlock()

	child := t.GetChildren()[id]
	if child == nil {
		return nil
	}
	dc, _ := child.ToDataCenter()
	return dc
}

// ForEachDataCenter iterates over all data centers.
func (t *Topology) ForEachDataCenter(fn func(dc *DataCenter) bool) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.ForEachChild(func(id string, child Node) bool {
		dc, ok := child.ToDataCenter()
		if !ok {
			return true // skip non-datacenter nodes
		}
		return fn(dc)
	})
}

// GetTotalDataCenterCount returns the number of data centers.
func (t *Topology) GetTotalDataCenterCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ChildCount()
}

// SyncDeadNodeRemoval removes dead nodes from the topology.
// This should be called periodically to clean up stale entries.
func (t *Topology) SyncDeadNodeRemoval() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.forEachDeadNode(func(node Node) bool {
		if dn, ok := node.ToDataNode(); ok {
			// Remove all volumes from this node
			t.volumeLayouts.RemoveDataNode(dn.GetId())
		}

		// Remove the node from its parent
		parent := node.GetParent()
		if parent != nil {
			parent.DeleteChild(node.GetId())
		}

		return true
	})
}

// forEachDeadNode iterates over all dead nodes and calls the given function.
func (t *Topology) forEachDeadNode(fn func(Node) bool) {
	t.forEachNode(t.NodeImpl, func(node Node) bool {
		if !node.IsAlive() && node.GetId() != "root" {
			return fn(node)
		}
		return true
	})
}

// forEachNode recursively iterates over all nodes.
func (t *Topology) forEachNode(node Node, fn func(Node) bool) bool {
	if !fn(node) {
		return false
	}

	for _, child := range node.GetChildren() {
		if !t.forEachNode(child, fn) {
			return false
		}
	}

	return true
}

// ToProto converts the topology to a format suitable for gRPC responses.
func (t *Topology) ToProto() []*master_pb.VolumeLocation {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Collect all volume locations
	var result []*master_pb.VolumeLocation

	t.forEachNode(t.NodeImpl, func(node Node) bool {
		if dn, ok := node.ToDataNode(); ok {
			// Convert data node volumes to proto format
			for range dn.GetVolumes() {
				result = append(result, &master_pb.VolumeLocation{
					Url:        dn.GetUrl(),
					PublicUrl:  dn.GetPublicUrl(),
					DataCenter: dn.GetDataCenter(),
					Rack:       dn.GetRack(),
				})
			}
		}
		return true
	})

	return result
}

// NextVolumeId allocates and returns the next logical volume ID.
func (t *Topology) NextVolumeId() uint32 {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.nextVolumeId == 0 {
		t.nextVolumeId = 1
	}
	vid := t.nextVolumeId
	t.nextVolumeId++
	return vid
}

// AllocateNextVolumeId allocates the next volume ID, optionally persisting it
// (for example by replicating max volume ID via Raft) after releasing the lock.
// The lock is released before calling persist to prevent deadlocks when persist
// calls back into topology methods (e.g. SetMaxVolumeId from the Raft FSM).
func (t *Topology) AllocateNextVolumeId(persist func(uint32) error) (uint32, error) {
	t.mu.Lock()
	if t.nextVolumeId == 0 {
		t.nextVolumeId = 1
	}
	vid := t.nextVolumeId
	t.nextVolumeId++
	t.mu.Unlock()

	if persist != nil {
		if err := persist(vid); err != nil {
			return 0, err
		}
	}
	return vid, nil
}

// SetMaxVolumeId advances the internal allocator to at least maxVolumeId+1.
func (t *Topology) SetMaxVolumeId(maxVolumeId uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setMaxVolumeIDUnsafe(maxVolumeId)
}

// GetMaxVolumeId returns the highest observed volume ID.
func (t *Topology) GetMaxVolumeId() uint32 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.nextVolumeId == 0 {
		return 0
	}
	return t.nextVolumeId - 1
}

func (t *Topology) setMaxVolumeIDUnsafe(maxVolumeId uint32) {
	if maxVolumeId >= t.nextVolumeId {
		t.nextVolumeId = maxVolumeId + 1
	}
}
