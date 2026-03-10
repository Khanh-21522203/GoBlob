package cluster

import (
	"fmt"
	"sync"
	"time"

	"GoBlob/goblob/pb/master_pb"
)

// ClusterRegistry tracks non-volume nodes (filers, S3 gateways, etc.)
// that connect via the KeepConnected bidirectional stream.
type ClusterRegistry struct {
	mu    sync.RWMutex
	nodes map[string]*ClusterNode

	// expireDeadNodesTicker triggers periodic cleanup
	ticker        *time.Ticker
	stopCh        chan struct{}
	expireSeconds int
}

// ClusterNode represents a non-volume node in the cluster.
type ClusterNode struct {
	id            string
	clientType    string
	clientAddress string
	version       string
	filerGroup    string
	dataCenter    string
	rack          string
	grpcPort      string
	lastSeen      time.Time
	mu            sync.RWMutex
}

// NewClusterRegistry creates a new ClusterRegistry.
func NewClusterRegistry() *ClusterRegistry {
	cr := &ClusterRegistry{
		nodes:         make(map[string]*ClusterNode),
		ticker:        time.NewTicker(30 * time.Second),
		stopCh:        make(chan struct{}),
		expireSeconds: 30,
	}

	// Start background goroutine to expire dead nodes
	go cr.expireLoop()

	return cr
}

// Register adds or updates a node registration from a KeepConnected request.
// If the node already exists, only lastSeen is updated (other fields are refreshed too).
func (cr *ClusterRegistry) Register(req *master_pb.KeepConnectedRequest) (*ClusterNode, error) {
	if req == nil {
		return nil, fmt.Errorf("keep connected request is nil")
	}

	nodeId := req.GetClientAddress()

	cr.mu.Lock()
	defer cr.mu.Unlock()

	if existing, ok := cr.nodes[nodeId]; ok {
		// Update mutable fields without replacing the object.
		existing.mu.Lock()
		existing.clientType = req.GetClientType()
		existing.version = req.GetVersion()
		existing.filerGroup = req.GetFilerGroup()
		existing.dataCenter = req.GetDataCenter()
		existing.rack = req.GetRack()
		existing.grpcPort = req.GetGrpcPort()
		existing.lastSeen = time.Now()
		existing.mu.Unlock()
		return existing, nil
	}

	node := &ClusterNode{
		id:            nodeId,
		clientType:    req.GetClientType(),
		clientAddress: req.GetClientAddress(),
		version:       req.GetVersion(),
		filerGroup:    req.GetFilerGroup(),
		dataCenter:    req.GetDataCenter(),
		rack:          req.GetRack(),
		grpcPort:      req.GetGrpcPort(),
		lastSeen:      time.Now(),
	}
	cr.nodes[nodeId] = node
	return node, nil
}

// Unregister removes a node from the registry.
func (cr *ClusterRegistry) Unregister(nodeId string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	delete(cr.nodes, nodeId)
}

// UpdateLastSeen updates the last seen time for a node.
func (cr *ClusterRegistry) UpdateLastSeen(nodeId string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if node, ok := cr.nodes[nodeId]; ok {
		node.mu.Lock()
		node.lastSeen = time.Now()
		node.mu.Unlock()
	}
}

// GetNode returns a node by ID.
func (cr *ClusterRegistry) GetNode(nodeId string) (*ClusterNode, bool) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	node, ok := cr.nodes[nodeId]
	return node, ok
}

// ListNodes returns all registered nodes, optionally filtered by clientType.
// Pass an empty string to return all nodes.
func (cr *ClusterRegistry) ListNodes(clientType string) []*ClusterNode {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	var result []*ClusterNode
	for _, node := range cr.nodes {
		if clientType == "" || node.clientType == clientType {
			result = append(result, node)
		}
	}
	return result
}

// ForEachNode iterates over all nodes.
func (cr *ClusterRegistry) ForEachNode(fn func(node *ClusterNode) bool) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	for _, node := range cr.nodes {
		if !fn(node) {
			break
		}
	}
}

// GetNodeCount returns the number of registered nodes.
func (cr *ClusterRegistry) GetNodeCount() int {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return len(cr.nodes)
}

// expireLoop runs periodically to remove dead nodes.
func (cr *ClusterRegistry) expireLoop() {
	for {
		select {
		case <-cr.ticker.C:
			cr.ExpireDeadNodes()
		case <-cr.stopCh:
			return
		}
	}
}

// ExpireDeadNodes removes nodes that haven't been seen recently.
func (cr *ClusterRegistry) ExpireDeadNodes() {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	expireBefore := time.Now().Add(-time.Duration(cr.expireSeconds) * time.Second)

	for nodeId, node := range cr.nodes {
		node.mu.RLock()
		lastSeen := node.lastSeen
		node.mu.RUnlock()

		if lastSeen.Before(expireBefore) {
			delete(cr.nodes, nodeId)
		}
	}
}

// Shutdown gracefully shuts down the registry.
func (cr *ClusterRegistry) Shutdown() {
	close(cr.stopCh)
	cr.ticker.Stop()
}

// ClusterNode methods

// GetId returns the node ID.
func (n *ClusterNode) GetId() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.id
}

// GetClientType returns the client type (e.g., "filer", "s3").
func (n *ClusterNode) GetClientType() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.clientType
}

// GetClientAddress returns the client address.
func (n *ClusterNode) GetClientAddress() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.clientAddress
}

// GetVersion returns the client version.
func (n *ClusterNode) GetVersion() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.version
}

// GetFilerGroup returns the filer group (for filers).
func (n *ClusterNode) GetFilerGroup() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.filerGroup
}

// GetDataCenter returns the data center.
func (n *ClusterNode) GetDataCenter() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.dataCenter
}

// GetRack returns the rack.
func (n *ClusterNode) GetRack() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.rack
}

// GetGrpcPort returns the gRPC port.
func (n *ClusterNode) GetGrpcPort() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.grpcPort
}

// IsAlive returns true if the node is alive (recently seen).
func (n *ClusterNode) IsAlive() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return time.Since(n.lastSeen) < 30*time.Second
}

// GetLastSeen returns the last seen time.
func (n *ClusterNode) GetLastSeen() time.Time {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.lastSeen
}

// ToProto converts the cluster node to a KeepConnectedResponse.
func (n *ClusterNode) ToProto() *master_pb.KeepConnectedResponse {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return &master_pb.KeepConnectedResponse{
		// Volume locations would be populated by the caller based on current topology
	}
}
