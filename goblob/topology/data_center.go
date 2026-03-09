package topology

import "GoBlob/goblob/core/types"

// DataCenter represents a data center in the cluster topology.
// It contains racks as children.
type DataCenter struct {
	*NodeImpl
}

// NewDataCenter creates a new DataCenter.
func NewDataCenter(id string) *DataCenter {
	return &DataCenter{
		NodeImpl: NewNodeImpl(id),
	}
}

// ToDataCenter returns this node as a DataCenter.
func (dc *DataCenter) ToDataCenter() (*DataCenter, bool) {
	return dc, true
}

// ToRack returns nil for a DataCenter.
func (dc *DataCenter) ToRack() (*Rack, bool) {
	return nil, false
}

// ToDataNode returns nil for a DataCenter.
func (dc *DataCenter) ToDataNode() (*DataNode, bool) {
	return nil, false
}

// GetOrCreateRack gets or creates a rack by ID.
func (dc *DataCenter) GetOrCreateRack(id string) *Rack {
	return dc.NodeImpl.GetOrCreateChild(id, func() Node {
		return NewRack(id)
	}).(*Rack)
}

// ForEachRack iterates over all racks in this data center.
func (dc *DataCenter) ForEachRack(fn func(rack *Rack) bool) bool {
	return dc.ForEachChild(func(id string, child Node) bool {
		rack, ok := child.(*Rack)
		if !ok {
			return true // skip non-rack nodes
		}
		return fn(rack)
	})
}

// GetAvailableDiskTypes returns all disk types available in this data center.
func (dc *DataCenter) GetAvailableDiskTypes() []types.DiskType {
	diskTypes := make(map[types.DiskType]bool)

	dc.ForEachRack(func(rack *Rack) bool {
		rack.ForEachDataNode(func(dn *DataNode) bool {
			for diskType := range dn.disks {
				diskTypes[diskType] = true
			}
			return true
		})
		return true
	})

	result := make([]types.DiskType, 0, len(diskTypes))
	for dt := range diskTypes {
		result = append(result, dt)
	}
	return result
}

// GetTotalVolumeCount returns the total number of volumes in this data center.
func (dc *DataCenter) GetTotalVolumeCount() int {
	count := 0

	dc.ForEachRack(func(rack *Rack) bool {
		rack.ForEachDataNode(func(dn *DataNode) bool {
			count += dn.GetVolumeCount()
			return true
		})
		return true
	})

	return count
}

// GetActiveDataNodeCount returns the number of active (alive) data nodes.
func (dc *DataCenter) GetActiveDataNodeCount() int {
	count := 0

	dc.ForEachRack(func(rack *Rack) bool {
		rack.ForEachDataNode(func(dn *DataNode) bool {
			if dn.IsAlive() {
				count++
			}
			return true
		})
		return true
	})

	return count
}

// GetFreeVolumeSlots returns the number of free volume slots across all disk types.
func (dc *DataCenter) GetFreeVolumeSlots() map[types.DiskType]int {
	result := make(map[types.DiskType]int)

	dc.ForEachRack(func(rack *Rack) bool {
		rack.ForEachDataNode(func(dn *DataNode) bool {
			for diskType, slots := range dn.GetFreeVolumeSlots() {
				result[diskType] += slots
			}
			return true
		})
		return true
	})

	return result
}
