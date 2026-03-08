# gRPC and Protobuf Layer

## Assumptions

- All inter-service communication uses gRPC with Protocol Buffers for serialization.
- gRPC ports default to HTTP port + 10000 (e.g., Master 9333 -> gRPC 19333).
- TLS for gRPC is configured separately from HTTPS in `security.toml`.

## Code Files / Modules Referenced

- `goblob/pb/master_pb/` - Master service protobuf definitions and generated code
- `goblob/pb/volume_server_pb/` - Volume server service definitions
- `goblob/pb/filer_pb/` - Filer service definitions
- `goblob/pb/iam_pb/` - IAM service definitions
- `goblob/pb/s3_pb/` - S3 IAM cache service definitions
- `goblob/pb/server_address.go` - `ServerAddress` type and helpers
- `goblob/pb/server_discovery.go` - `ServerDiscovery` for SRV-based discovery
- `goblob/pb/grpc_client_server.go` - gRPC client/server helper functions
- `goblob/server/master_grpc_server.go` - Master gRPC service implementation
- `goblob/server/volume_grpc_*.go` - Volume gRPC service implementations
- `goblob/server/filer_grpc_server*.go` - Filer gRPC service implementations
- `goblob/wdclient/masterclient.go` - `MasterClient` gRPC streaming client
- `goblob/security/tls.go` - gRPC TLS credential loading

## Overview

The gRPC and Protobuf layer provides the backbone for all inter-service communication in GoBlob. Each server component (Master, Volume, Filer) exposes a gRPC service alongside its HTTP API. gRPC is used for heartbeats, metadata streaming, volume operations, file operations, and cluster coordination. Streaming RPCs are heavily used for real-time data like heartbeats and metadata change subscriptions.

## Responsibilities

- **Service definitions**: Define RPC methods and message types for all services
- **Streaming RPCs**: Bidirectional streaming for heartbeats, metadata subscriptions
- **Client helpers**: Reusable connection management, retry, and failover
- **Service discovery**: Master-based discovery of volume servers and filers
- **TLS transport**: Encrypted gRPC channels with optional mutual authentication

## Architecture Role

```
+------------------------------------------------------------------+
|                    gRPC Service Map                                |
+------------------------------------------------------------------+
|                                                                   |
|  +-------------------+     +-------------------+                  |
|  | master_pb         |     | volume_server_pb  |                  |
|  | .MasterService    |     | .VolumeServer     |                  |
|  +-------------------+     +-------------------+                  |
|  | SendHeartbeat     |     | VolumeStatus      |                  |
|  | KeepConnected     |     | AllocateVolume    |                  |
|  | LookupVolume      |     | WriteNeedle..     |                  |
|  | Assign            |     | ReadNeedle..      |                  |
|  | GetMasterConfig   |     | VolumeDelete      |                  |
|  | VolumeList        |     | VolumeCopy        |                  |
|  | ...               |     | VolumeCompact     |                  |
|  +-------------------+     | VolumeSync*       |                  |
|                            | VolumeFollow      |                  |
|  +-------------------+     | ReadAllNeedles    |                  |
|  | filer_pb          |     | ...               |                  |
|  | .FilerService     |     +-------------------+                  |
|  +-------------------+                                            |
|  | LookupDirectoryEntry|   +-------------------+                  |
|  | ListEntries       |     | iam_pb            |                  |
|  | CreateEntry       |     | .IAMService       |                  |
|  | UpdateEntry       |     +-------------------+                  |
|  | AppendToEntry     |     | GetCredentials    |                  |
|  | DeleteEntry       |     | ...               |                  |
|  | AtomicRenameEntry |     +-------------------+                  |
|  | StreamRenameEntry |                                            |
|  | AssignVolume      |     +-------------------+                  |
|  | LookupVolume      |     | s3_pb             |                  |
|  | SubscribeMetadata |     | .S3IamCache       |                  |
|  | SubscribeLocalMeta|     +-------------------+                  |
|  | KvGet / KvPut     |     | GetS3Config       |                  |
|  | ...               |     | ...               |                  |
|  +-------------------+     +-------------------+                  |
+------------------------------------------------------------------+
```

## Component Structure Diagram

```
gRPC Connection Patterns:

Master <-- Streaming Heartbeat -- Volume Server
  |
  +--- KeepConnected (bidirectional stream) --- Filer
  |
  +--- KeepConnected (bidirectional stream) --- S3 Gateway

Filer <-- SubscribeMetadata (server stream) -- S3 Gateway
  |
  +--- SubscribeLocalMetadata (server stream)- Peer Filer

Volume <-- Unary RPCs -- Filer (assign, read, write)
  |
  +--- Streaming RPCs -- Master (heartbeat, volume copy)


ServerAddress Type:
+---------------------------------------------------------------+
| type ServerAddress string                                      |
| format: "host:port" or "host:port.grpcPort"                   |
|                                                                |
| Methods:                                                       |
|   ToGrpcAddress() string   # extract/compute gRPC address     |
|   ToHttpAddress() string   # extract HTTP address              |
+---------------------------------------------------------------+

ServerDiscovery:
+---------------------------------------------------------------+
| type ServerDiscovery struct                                     |
|   instances []ServerAddress                                     |
|   srvRecord string          # DNS SRV record for discovery     |
+---------------------------------------------------------------+
| GetInstances() []ServerAddress                                  |
| RefreshBySrvIfAvailable()   # resolve SRV records              |
+---------------------------------------------------------------+
```

## Control Flow

### Master KeepConnected (Heartbeat)

```
Volume Server                      Master Server
    |                                   |
    |== SendHeartbeat (bidi stream) ===>|
    |                                   |
    |-- Heartbeat{                      |
    |     ip, port, publicUrl,          |
    |     maxCounts[], volumes[],       |
    |     deletedVids[],                |
    |     newVids[], state{}            |
    |   } =============================>|
    |                                   |
    |                                   |--> ProcessJoinMessage()
    |                                   |--> update topology
    |                                   |
    |<== HeartbeatResponse{            |
    |      volumeSizeLimit,             |
    |      leader,                      |
    |      metricsAddress               |
    |    } =============================|
    |                                   |
    |   (repeat every pulsePeriod)      |
    |                                   |
```

### Filer SubscribeMetadata (Server Stream)

```
Subscriber (S3/Peer)               Filer Server
    |                                   |
    |-- SubscribeMetadata{              |
    |     pathPrefix,                   |
    |     sinceNs,                      |
    |     clientName,                   |
    |     clientId                      |
    |   } =============================>|
    |                                   |
    |                                   |--> register listener
    |                                   |    (knownListeners map)
    |                                   |
    |<== EventNotification{             |
    |      oldEntry, newEntry,          |
    |      deleteChunks,                |
    |      newParentPath,               |
    |      isTruncate                   |
    |    } =============================|
    |                                   |
    |<== EventNotification{...} ========|
    |   (continuous stream of changes)  |
    |                                   |
```

### gRPC Client Helper Pattern

```
pb.WithMasterServerClient(streamingMode, master, grpcDialOption, fn)
    |
    +--> grpc.Dial(master.ToGrpcAddress(), grpcDialOption)
    |    (with TLS if configured)
    |
    +--> fn(masterClient)
    |    (caller executes RPC calls)
    |
    +--> Close connection (or reuse from pool)

pb.WithVolumeServerClient(streamingMode, volumeServer, grpcDialOption, fn)
    |
    +--> same pattern for volume server connections

pb.WithFilerClient(streamingMode, clientId, filer, grpcDialOption, fn)
    |
    +--> same pattern for filer connections
```

## Data Flow Diagram

```
Protobuf Message Flow:

  +--------+  proto  +--------+  proto  +--------+
  | Client | ------> | gRPC   | ------> | Server |
  | Stub   |         | Frame  |         | Impl   |
  +--------+         +--------+         +--------+
  | Marshal|         | Header |         |Unmarshal|
  | to     |         | + body |         | from    |
  | bytes  |         | (HTTP/2|         | bytes   |
  +--------+         |  frame)|         +--------+
                     +--------+

gRPC Port Mapping:

  Service      HTTP Port    gRPC Port     Derivation
  ---------    ---------    ---------     ----------
  Master       9333         19333         HTTP + 10000
  Volume       8080         18080         HTTP + 10000
  Filer        8888         18888         HTTP + 10000
  S3           8333         18333         HTTP + 10000
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `google.golang.org/grpc` | gRPC framework |
| `google.golang.org/protobuf` | Protocol Buffers runtime |
| `goblob/pb/*_pb` | Generated protobuf code |
| `goblob/security` | TLS credential loading for gRPC |

## Error Handling

- **Connection failure**: `WithXxxClient` helpers return error; callers retry with backoff
- **Stream disconnection**: Heartbeat streams reconnect automatically; metadata subscriptions retry
- **TLS mismatch**: Connection refused; error logged with TLS details
- **Deadline exceeded**: gRPC default deadlines; streaming RPCs have configurable keepalive

## Async / Background Behavior

| Goroutine | Purpose |
|---|---|
| `MasterClient.KeepConnectedToMaster()` | Persistent bidirectional stream to master |
| `SubscribeMetadata` stream handler | Persistent server-stream for metadata changes |
| Volume heartbeat | Persistent bidirectional stream for volume status |
| Connection pool management | gRPC connections reused via `grpc.Dial` |

## Configuration

- **gRPC TLS**: `grpc.{master,volume,filer}.{cert,key,ca}` in `security.toml`
- **Port override**: `-port.grpc` flag to set explicit gRPC port
- **Max message size**: gRPC default (~4MB); some calls use streaming for large payloads
- **Keepalive**: gRPC keepalive settings for long-lived streams

## Edge Cases

- **Port derivation**: If gRPC port not explicitly set, computed as HTTP port + 10000
- **SRV discovery**: `ServerDiscovery` can resolve DNS SRV records for master addresses
- **Streaming reconnection**: Heartbeat and metadata streams auto-reconnect on disconnect
- **Mixed TLS/non-TLS**: Each service can independently configure TLS
- **Local gRPC**: Unix socket for co-located services (filer + S3 on same host)
