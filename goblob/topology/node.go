package topology

import (
	"strconv"
	"sync"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
)

// Node represents a node in the cluster topology tree.
// The hierarchy is: Topology → DataCenter → Rack → DataNode → DiskInfo → Volumes
type Node interface {
	// GetId returns the unique identifier for this node.
	GetId() string

	// GetParent returns the parent node in the topology tree.
	GetParent() Node

	// SetParent sets the parent node.
	SetParent(parent Node)

	// GetChildren returns all child nodes.
	GetChildren() map[string]Node

	// GetOrCreateChild gets or creates a child node by ID.
	GetOrCreateChild(id string, factory func() Node) Node

	// DeleteChild removes a child node.
	DeleteChild(id string)

	// IsAlive returns true if the node is alive (recently seen).
	IsAlive() bool

	// GetLastSeen returns the last time this node was seen.
	GetLastSeen() time.Time

	// UpdateLastSeen updates the last seen time.
	UpdateLastSeen()

	// Type conversion methods
	ToDataCenter() (*DataCenter, bool)
	ToRack() (*Rack, bool)
	ToDataNode() (*DataNode, bool)
}

// NodeImpl is the base implementation of the Node interface.
type NodeImpl struct {
	id       string
	parent   Node
	children map[string]Node
	lastSeen time.Time
	mu       sync.RWMutex
}

// NewNodeImpl creates a new base node.
func NewNodeImpl(id string) *NodeImpl {
	return &NodeImpl{
		id:       id,
		children: make(map[string]Node),
		lastSeen: time.Now(),
	}
}

// GetId returns the unique identifier for this node.
func (n *NodeImpl) GetId() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.id
}

// GetParent returns the parent node in the topology tree.
func (n *NodeImpl) GetParent() Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.parent
}

// SetParent sets the parent node.
func (n *NodeImpl) SetParent(parent Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.parent = parent
}

// GetChildren returns all child nodes.
func (n *NodeImpl) GetChildren() map[string]Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	// Return a copy to avoid race conditions
	result := make(map[string]Node, len(n.children))
	for k, v := range n.children {
		result[k] = v
	}
	return result
}

// GetOrCreateChild gets or creates a child node by ID.
func (n *NodeImpl) GetOrCreateChild(id string, factory func() Node) Node {
	n.mu.Lock()
	defer n.mu.Unlock()

	if child, ok := n.children[id]; ok {
		return child
	}

	child := factory()
	child.SetParent(n)
	n.children[id] = child
	return child
}

// DeleteChild removes a child node.
func (n *NodeImpl) DeleteChild(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.children, id)
}

// IsAlive returns true if the node is alive (seen within 30 seconds).
func (n *NodeImpl) IsAlive() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return time.Since(n.lastSeen) < 30*time.Second
}

// GetLastSeen returns the last time this node was seen.
func (n *NodeImpl) GetLastSeen() time.Time {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.lastSeen
}

// UpdateLastSeen updates the last seen time.
func (n *NodeImpl) UpdateLastSeen() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastSeen = time.Now()
}

// ChildCount returns the number of children.
func (n *NodeImpl) ChildCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.children)
}

// ForEachChild iterates over all children and calls the given function.
// Returns false if iteration was stopped early.
func (n *NodeImpl) ForEachChild(fn func(id string, child Node) bool) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for id, child := range n.children {
		if !fn(id, child) {
			return false
		}
	}
	return true
}

// GetOrCreateDataCenter gets or creates a data center by ID.
func (n *NodeImpl) GetOrCreateDataCenter(id string) *DataCenter {
	child := n.GetOrCreateChild(id, func() Node {
		return NewDataCenter(id)
	})
	return child.(*DataCenter)
}

// GetOrCreateRack gets or creates a rack by ID.
func (n *NodeImpl) GetOrCreateRack(id string) *Rack {
	child := n.GetOrCreateChild(id, func() Node {
		return NewRack(id)
	})
	return child.(*Rack)
}

// GetOrCreateDataNode gets or creates a data node by ID.
func (n *NodeImpl) GetOrCreateDataNode(id string) *DataNode {
	child := n.GetOrCreateChild(id, func() Node {
		return NewDataNode(id)
	})
	return child.(*DataNode)
}

// ToDataCenter attempts to cast this node to a DataCenter.
func (n *NodeImpl) ToDataCenter() (*DataCenter, bool) {
	return nil, false
}

// ToRack attempts to cast this node to a Rack.
func (n *NodeImpl) ToRack() (*Rack, bool) {
	return nil, false
}

// ToDataNode attempts to cast this node to a DataNode.
func (n *NodeImpl) ToDataNode() (*DataNode, bool) {
	return nil, false
}

// GetOrCreateChildFromVolumeLocation creates or finds nodes for a volume location.
// This is used when processing heartbeats to build the topology tree.
func GetOrCreateChildFromVolumeLocation(parent Node, dc, rack, dataNode string) Node {
	if dc == "" {
		return parent
	}

	dcNode := parent.GetOrCreateChild(dc, func() Node {
		return NewDataCenter(dc)
	})

	if rack == "" {
		return dcNode
	}

	rackNode := dcNode.GetOrCreateChild(rack, func() Node {
		return NewRack(rack)
	})

	if dataNode == "" {
		return rackNode
	}

	return rackNode.GetOrCreateChild(dataNode, func() Node {
		return NewDataNode(dataNode)
	})
}

// ServerAddressToNodeID converts a gRPC server address to a node ID.
func ServerAddressToNodeID(addr types.ServerAddress) string {
	return string(addr)
}

// NodeIDFromHeartbeat extracts the node ID from a heartbeat message.
func NodeIDFromHeartbeat(hb *master_pb.Heartbeat) string {
	if hb.GrpcPort > 0 {
		return hb.Ip + ":" + strconv.FormatUint(uint64(hb.GrpcPort), 10)
	}
	return hb.Ip + ":" + strconv.FormatUint(uint64(hb.Port), 10)
}
