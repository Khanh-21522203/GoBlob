package topology

import "GoBlob/goblob/core/types"

// Rack represents a rack in the cluster topology.
// It contains data nodes as children.
type Rack struct {
	*NodeImpl
}

// NewRack creates a new Rack.
func NewRack(id string) *Rack {
	return &Rack{
		NodeImpl: NewNodeImpl(id),
	}
}

// ToDataCenter returns nil for a Rack.
func (r *Rack) ToDataCenter() (*DataCenter, bool) {
	return nil, false
}

// ToRack returns this node as a Rack.
func (r *Rack) ToRack() (*Rack, bool) {
	return r, true
}

// ToDataNode returns nil for a Rack.
func (r *Rack) ToDataNode() (*DataNode, bool) {
	return nil, false
}

// GetOrCreateDataNode gets or creates a data node by ID.
func (r *Rack) GetOrCreateDataNode(id string) *DataNode {
	return r.NodeImpl.GetOrCreateChild(id, func() Node {
		return NewDataNode(id)
	}).(*DataNode)
}

// ForEachDataNode iterates over all data nodes in this rack.
func (r *Rack) ForEachDataNode(fn func(dn *DataNode) bool) bool {
	return r.ForEachChild(func(id string, child Node) bool {
		dn, ok := child.(*DataNode)
		if !ok {
			return true // skip non-datanode nodes
		}
		return fn(dn)
	})
}

// GetDataCenter returns the parent data center.
func (r *Rack) GetDataCenter() string {
	parent := r.GetParent()
	if parent == nil {
		return ""
	}
	return parent.GetId()
}

// GetTotalVolumeCount returns the total number of volumes in this rack.
func (r *Rack) GetTotalVolumeCount() int {
	count := 0

	r.ForEachDataNode(func(dn *DataNode) bool {
		count += dn.GetVolumeCount()
		return true
	})

	return count
}

// GetActiveDataNodeCount returns the number of active (alive) data nodes.
func (r *Rack) GetActiveDataNodeCount() int {
	count := 0

	r.ForEachDataNode(func(dn *DataNode) bool {
		if dn.IsAlive() {
			count++
		}
		return true
	})

	return count
}

// GetFreeVolumeSlots returns the number of free volume slots across all disk types.
func (r *Rack) GetFreeVolumeSlots() map[types.DiskType]int {
	result := make(map[types.DiskType]int)

	r.ForEachDataNode(func(dn *DataNode) bool {
		for diskType, slots := range dn.GetFreeVolumeSlots() {
			result[diskType] += slots
		}
		return true
	})

	return result
}
