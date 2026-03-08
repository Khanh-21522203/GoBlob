# Feature: Cluster Registry

## 1. Purpose

The Cluster Registry tracks all live non-volume nodes that are connected to the master: filer servers and S3 gateways. It is maintained via the `KeepConnected` bidirectional gRPC stream on the master.

## 2. Responsibilities

- **Node registration**: Accept `KeepConnectedRequest` from filers and S3 gateways; register them in the in-memory node map
- **Node expiry**: Detect dead connections and remove the node from the registry
- **Peer discovery**: Return the current set of connected nodes of a given type (e.g., all known filers) to callers
- **Volume location streaming**: Stream volume location updates (`VolumeLocation` messages) to connected filer and S3 clients over the `KeepConnected` stream so they stay in sync without polling the master
- **Cluster type enum**: Define the canonical node type strings (`"filer"`, `"s3"`)

## 3. Non-Responsibilities

- Does not manage volume server topology (that is `plan-topology`)
- Does not implement gRPC transport (that is `plan-grpc-transport`)
- Does not perform Raft consensus (that is `plan-raft-consensus`)
- Does not store blob data or file metadata
- Does not track MQ brokers or FUSE mount clients — those features are out of scope for v1

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                    Cluster Registry                           |
+--------------------------------------------------------------+
|                                                               |
|  Master gRPC KeepConnected handler                           |
|       |                                                      |
|  +----+----+                                                 |
|  | Cluster |  <-- KeepConnectedRequest (filer, s3)           |
|  |         |  --> KeepConnectedResponse (volume locations)   |
|  |  nodes  |                                                 |
|  |  map    |                                                 |
|  +---------+                                                 |
+--------------------------------------------------------------+
```

### KeepConnected Stream Lifecycle

```
Client (filer/s3)                  Master KeepConnected handler
    |                                        |
    |== KeepConnected stream open ==========>|
    |                                        |
    |-- KeepConnectedRequest{clientType,     |
    |     clientAddress, ...} ------------->|
    |                                        |--> Cluster.Register(node)
    |                                        |
    |<-- KeepConnectedResponse{              |
    |      volume_locations[],              |
    |      metrics_address}                 |
    |                                        |
    |   (stream stays open)                  |
    |                                        |
    | [topology changes]                     |
    |                                        |--> Cluster.NotifyVolumeLocations()
    |<-- KeepConnectedResponse{locations} --|
    |                                        |
    | [client dies / network error]          |
    |                                        |--> Cluster.Unregister(node)
```

## 5. Core Data Structures (Go)

```go
package cluster

import (
    "sync"
    "time"
    "goblob/core/types"
)

// NodeType identifies the kind of service connecting to the master.
type NodeType string

const (
    NodeTypeFiler   NodeType = "filer"
    NodeTypeS3      NodeType = "s3"
)

// ClusterNode represents one connected non-volume node.
type ClusterNode struct {
    // Address is the node's gRPC address (host:grpcPort).
    Address types.ServerAddress

    // NodeType is the kind of service.
    NodeType NodeType

    // Version is the GoBlob version string of the connected node.
    Version string

    // DataCenter and Rack locate the node in the topology.
    DataCenter string
    Rack       string

    // FilerGroup groups filer instances that share the same metadata store.
    FilerGroup string

    // LastSeen is updated on every received KeepConnected message.
    LastSeen time.Time
}

// Cluster is the in-memory registry of all connected non-volume nodes.
type Cluster struct {
    // nodes is keyed by node address.
    nodes map[types.ServerAddress]*ClusterNode
    mu    sync.RWMutex

    // onNodeChange is called whenever a node is added or removed.
    // Used by master to update topology notifications.
    onNodeChange func(node *ClusterNode, isAdd bool)
}

```

## 6. Public Interfaces

```go
package cluster

// NewCluster creates an empty cluster registry.
func NewCluster(onNodeChange func(node *ClusterNode, isAdd bool)) *Cluster

// Register adds or updates a node in the registry.
func (c *Cluster) Register(node *ClusterNode)

// Unregister removes a node from the registry by address.
func (c *Cluster) Unregister(addr types.ServerAddress)

// GetNodes returns all currently registered nodes of the given type.
// Pass "" to return all nodes.
func (c *Cluster) GetNodes(nodeType NodeType) []*ClusterNode

// GetNode returns the node with the given address, or nil if not found.
func (c *Cluster) GetNode(addr types.ServerAddress) *ClusterNode

```

## 7. Internal Algorithms

### KeepConnected gRPC handler (on master)
```
KeepConnected(stream):
  var node *ClusterNode

  for:
    req, err = stream.Recv()
    if err:
      if node != nil:
        cluster.Unregister(node.Address)
        log.Info("node disconnected", "addr", node.Address, "type", node.NodeType)
      return

    if node == nil:
      // First message — register the node
      node = &ClusterNode{
        Address:    types.ServerAddress(req.ClientAddress),
        NodeType:   NodeType(req.ClientType),
        Version:    req.Version,
        DataCenter: req.DataCenter,
        Rack:       req.Rack,
        FilerGroup: req.FilerGroup,
        LastSeen:   time.Now(),
      }
      cluster.Register(node)
      log.Info("node connected", "addr", node.Address, "type", node.NodeType)
    else:
      node.LastSeen = time.Now()

    // Send current volume locations to the client
    locations = topology.BuildVolumeLocations()
    stream.Send(&master_pb.KeepConnectedResponse{
      VolumeLocations: locations,
      MetricsAddress:  metricsAddr,
    })
```

### Volume location streaming

When the topology changes (heartbeat received with new/deleted volumes), the master calls `NotifyVolumeLocations()` which sends an updated `KeepConnectedResponse` to all connected clients:

```
NotifyVolumeLocations(newVids, deletedVids):
  cluster.mu.RLock()
  clients = copyNodes(cluster.nodes)
  cluster.mu.RUnlock()

  for each client in clients:
    client.stream.Send(&master_pb.KeepConnectedResponse{
      VolumeLocations: []*master_pb.VolumeLocation{
        {
          Url:         masterAddr,
          NewVids:     newVids,
          DeletedVids: deletedVids,
        },
      },
    })
```

## 8. Persistence Model

The `Cluster` registry is **in-memory only**. Nodes re-register on reconnection. The master does not need to persist the cluster state because clients hold the `KeepConnected` stream open and re-register on reconnect.

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `Cluster.mu sync.RWMutex` | Protects the nodes map; reads are concurrent; writes on register/unregister |
| One goroutine per KeepConnected stream | Handles one client connection; reads requests and sends responses |

## 10. Configuration

```go
// KeepConnected has no separate configuration; it uses the master's existing gRPC server.
```

## 11. Observability

- `obs.ClusterRegistryNodeCount.WithLabelValues(nodeType).Set(count)` updated on register/unregister
- Node connect/disconnect logged at INFO with address and type

## 12. Testing Strategy

- **Unit tests**:
  - `TestClusterRegister`: register two nodes, assert both in GetNodes result
  - `TestClusterUnregister`: register then unregister, assert GetNodes returns empty
  - `TestClusterGetNodesByType`: register filer and S3 nodes, assert GetNodes("filer") returns only filer
  - `TestClusterOnNodeChange`: register + unregister, assert callback invoked correctly

- **Integration tests**:
  - `TestKeepConnectedLifecycle`: start master gRPC server, connect filer client, assert registered; disconnect, assert unregistered
  - `TestVolumeLocationStreaming`: register client, trigger topology change, assert client receives updated VolumeLocations

## 13. Open Questions

None.
