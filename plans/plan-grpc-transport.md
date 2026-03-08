# Feature: gRPC & Protobuf Transport Layer

## 1. Purpose

The gRPC and Protobuf Transport Layer defines all inter-service communication in GoBlob. Every internal service interaction—heartbeats, metadata operations, volume allocation, file assignments—travels over gRPC with Protocol Buffers for serialization.

This subsystem provides:
- The `.proto` service and message definitions for each server role
- Generated Go code (stubs and server interfaces)
- Client helper functions that manage connection lifecycle, TLS, retry
- A `ServerAddress` type and `ServerDiscovery` for locating services
- Streaming RPC patterns for long-lived connections (heartbeats, metadata subscriptions)

## 2. Responsibilities

- Define gRPC service interfaces for Master, Volume, Filer, IAM, S3
- Generate Go stubs from `.proto` files
- Provide `WithXxxClient()` helper functions for short-lived unary RPCs
- Provide `WithXxxStreamingClient()` helpers for long-lived streaming connections
- Handle TLS credential loading for gRPC channels
- Derive gRPC port from HTTP port (port + 10000) by default
- Provide `ServerDiscovery` for SRV-record-based master discovery
- Implement the `MasterClient` which maintains a persistent bidirectional stream to the master

## 3. Non-Responsibilities

- Does not implement business logic (that lives in the server implementations)
- Does not implement HTTP REST endpoints (those are separate)
- Does not handle authentication logic (JWT verification is in the security subsystem)
- Does not implement service mesh or load balancing at the gRPC level

## 4. Architecture Design

```
+--------------------------------------------------------------------+
|                       gRPC Service Map                              |
+--------------------------------------------------------------------+
|                                                                     |
|  master_pb.MasterService          volume_pb.VolumeServer            |
|    SendHeartbeat (bidi stream)      AllocateVolume (unary)          |
|    KeepConnected (bidi stream)      WriteNeedle (unary)             |
|    Assign (unary)                   ReadNeedle (unary)              |
|    LookupVolume (unary)             VolumeDelete (unary)            |
|    GetMasterConfig (unary)          VolumeCopy (server stream)      |
|    VolumeList (unary)               VolumeCompact (unary)           |
|    ...                              ReadAllNeedles (server stream)  |
|                                     VolumeFollow (bidi stream)      |
|                                     ...                             |
|  filer_pb.FilerService            iam_pb.IAMService                 |
|    LookupDirectoryEntry (unary)     GetCredentials (unary)          |
|    ListEntries (server stream)      ...                             |
|    CreateEntry (unary)                                              |
|    UpdateEntry (unary)                                              |
|    DeleteEntry (unary)                                              |
|    AtomicRenameEntry (unary)                                        |
|    AssignVolume (unary)                                             |
|    LookupVolume (unary)                                             |
|    SubscribeMetadata (server stream)                                |
|    SubscribeLocalMetadata (server stream)                           |
|    KvGet / KvPut / KvDelete (unary)                                 |
|    ...                                                              |
+--------------------------------------------------------------------+

Port Convention:
  Service  HTTP   gRPC   = HTTP + 10000
  Master   9333   19333
  Volume   8080   18080
  Filer    8888   18888
  S3       8333   18333
```

## 5. Core Data Structures (Go)

```go
package pb

// ServerAddress is a typed "host:port" or "host:httpPort.grpcPort" string.
// It provides helpers to derive gRPC and HTTP addresses.
// (see core/types package for implementation)
type ServerAddress = types.ServerAddress

// ServerDiscovery holds a set of server addresses that can be refreshed
// via DNS SRV records.
type ServerDiscovery struct {
    instances  []ServerAddress
    srvRecord  string
    mu         sync.RWMutex
}

func NewServerDiscovery(instances []ServerAddress, srvRecord string) *ServerDiscovery
func (sd *ServerDiscovery) GetInstances() []ServerAddress
func (sd *ServerDiscovery) RefreshBySrvIfAvailable() error

// MasterClient maintains a persistent gRPC connection to the master server.
// It tracks the current leader and reconnects on failure.
type MasterClient struct {
    masters     *ServerDiscovery
    clientType  ClusterNodeType  // volume, filer, broker, etc.
    clientAddr  ServerAddress
    currentMaster ServerAddress

    grpcDialOption grpc.DialOption

    // onMasterChange is called when the leader changes.
    onMasterChange func(newMaster ServerAddress)
    // onPeerUpdate is called when cluster membership changes (for brokers).
    onPeerUpdate   func(updates []*PeerUpdate)

    mu sync.RWMutex
    logger *slog.Logger
}

// ClusterNodeType identifies what kind of node is connecting to the master.
type ClusterNodeType string
const (
    VolumeServerType ClusterNodeType = "volume"
    FilerType        ClusterNodeType = "filer"
    S3GatewayType    ClusterNodeType = "s3"
)

// PeerUpdate is sent to onPeerUpdate when a peer node joins or leaves.
type PeerUpdate struct {
    Address ServerAddress
    NodeType ClusterNodeType
    IsAdd    bool // true = joined, false = left
}
```

### Proto Service Definitions

The following describes the key service methods. Full `.proto` files are generated separately.

**master_pb/master.proto**:
```protobuf
service MasterService {
  // Volume server -> master: bidirectional heartbeat stream
  rpc SendHeartbeat(stream Heartbeat) returns (stream HeartbeatResponse);
  // Any client -> master: keep-connected bidirectional stream
  rpc KeepConnected(stream ClientListenRequest) returns (stream VolumeLocation);
  // File ID assignment
  rpc Assign(AssignRequest) returns (AssignResult);
  // Volume location lookup
  rpc LookupVolume(LookupVolumeRequest) returns (LookupVolumeResponse);
  // Master config
  rpc GetMasterConfiguration(GetMasterConfigurationRequest) returns (GetMasterConfigurationResponse);
  // Volume operations
  rpc VolumeList(VolumeListRequest) returns (VolumeListResponse);
}
```

**volume_server_pb/volume_server.proto**:
```protobuf
service VolumeServer {
  rpc AllocateVolume(AllocateVolumeRequest) returns (AllocateVolumeResponse);
  rpc VolumeDelete(VolumeDeleteRequest) returns (VolumeDeleteResponse);
  rpc VolumeMarkReadonly(VolumeMarkReadonlyRequest) returns (VolumeMarkReadonlyResponse);
  rpc VolumeCopy(VolumeCopyRequest) returns (stream VolumeCopyResponse);
  rpc VolumeCompact(VolumeCompactRequest) returns (VolumeCompactResponse);
  rpc VolumeCommitCompact(VolumeCommitCompactRequest) returns (VolumeCommitCompactResponse);
  rpc ReadAllNeedles(ReadAllNeedlesRequest) returns (stream ReadAllNeedlesResponse);
  rpc VolumeFollow(stream VolumeFollowRequest) returns (stream VolumeFollowResponse);
  rpc FetchAndWriteNeedle(FetchAndWriteNeedleRequest) returns (FetchAndWriteNeedleResponse);
}
```

**filer_pb/filer.proto**:
```protobuf
service FilerService {
  rpc LookupDirectoryEntry(LookupDirectoryEntryRequest) returns (LookupDirectoryEntryResponse);
  rpc ListEntries(ListEntriesRequest) returns (stream ListEntriesResponse);
  rpc CreateEntry(CreateEntryRequest) returns (CreateEntryResponse);
  rpc UpdateEntry(UpdateEntryRequest) returns (UpdateEntryResponse);
  rpc AppendToEntry(AppendToEntryRequest) returns (AppendToEntryResponse);
  rpc DeleteEntry(DeleteEntryRequest) returns (DeleteEntryResponse);
  rpc AtomicRenameEntry(AtomicRenameEntryRequest) returns (AtomicRenameEntryResponse);
  rpc StreamRenameEntry(StreamRenameEntryRequest) returns (stream StreamRenameEntryResponse);
  rpc AssignVolume(AssignVolumeRequest) returns (AssignVolumeResponse);
  rpc LookupVolume(LookupVolumeRequest) returns (LookupVolumeResponse);
  rpc SubscribeMetadata(SubscribeMetadataRequest) returns (stream SubscribeMetadataResponse);
  rpc SubscribeLocalMetadata(SubscribeMetadataRequest) returns (stream SubscribeMetadataResponse);
  rpc KvGet(KvGetRequest) returns (KvGetResponse);
  rpc KvPut(KvPutRequest) returns (KvPutResponse);
  rpc KvDelete(KvDeleteRequest) returns (KvDeleteResponse);
  rpc GetFilerConfiguration(GetFilerConfigurationRequest) returns (GetFilerConfigurationResponse);
}
```

## 6. Public Interfaces

```go
package pb

// Connection helper functions

// WithMasterServerClient opens a gRPC connection to the master, calls fn, then closes.
// Use streamingMode=true for long-lived connections (avoids connection reuse timeouts).
func WithMasterServerClient(
    streamingMode bool,
    master ServerAddress,
    grpcDialOption grpc.DialOption,
    fn func(client master_pb.MasterServiceClient) error,
) error

// WithVolumeServerClient opens a gRPC connection to a volume server.
func WithVolumeServerClient(
    streamingMode bool,
    volumeServer ServerAddress,
    grpcDialOption grpc.DialOption,
    fn func(client volume_pb.VolumeServerClient) error,
) error

// WithFilerClient opens a gRPC connection to a filer server.
func WithFilerClient(
    streamingMode bool,
    clientId int32,
    filer ServerAddress,
    grpcDialOption grpc.DialOption,
    fn func(client filer_pb.FilerServiceClient) error,
) error

// MasterClient API
func NewMasterClient(
    masters *ServerDiscovery,
    clientType ClusterNodeType,
    clientAddr ServerAddress,
    dataCenter string,
    rack string,
    grpcDialOption grpc.DialOption,
    onMasterChange func(ServerAddress),
) *MasterClient

// KeepConnectedToMaster maintains a bidirectional stream to the master,
// receiving volume location updates and cluster membership changes.
// Runs until ctx is cancelled.
func (mc *MasterClient) KeepConnectedToMaster(ctx context.Context)

// WithMaster executes fn with a gRPC client connected to the current master leader.
func (mc *MasterClient) WithMaster(fn func(client master_pb.MasterServiceClient) error) error

// GetMaster returns the current master leader address.
func (mc *MasterClient) GetMaster() ServerAddress
```

## 7. Internal Algorithms

### WithXxxClient Pattern
```
WithMasterServerClient(streamingMode, master, dialOpt, fn):
  conn, err = grpcDial(master.ToGrpcAddress(), dialOpt)
  if err: return err
  defer conn.Close()
  client = master_pb.NewMasterServiceClient(conn)
  return fn(client)
```

For streaming connections, a connection pool or long-lived `grpc.ClientConn` is used to avoid the overhead of repeated TLS handshakes.

### MasterClient.KeepConnectedToMaster
```
KeepConnectedToMaster(ctx):
  for:
    // Try each master in round-robin until connected
    for each masterAddr in mc.masters.GetInstances():
      err = tryConnectToMaster(ctx, masterAddr)
      if err == nil: break

    sleep(1s)  // backoff before retry

tryConnectToMaster(ctx, masterAddr):
  conn = grpcDial(masterAddr.ToGrpcAddress(), mc.grpcDialOption)
  client = master_pb.NewMasterServiceClient(conn)

  stream, err = client.KeepConnected(ctx)
  if err: return err

  // Send initial registration
  stream.Send(&master_pb.ClientListenRequest{
    Name: string(mc.clientType),
    ClientAddress: string(mc.clientAddr),
    Version: version.Version,
  })

  // Receive volume location updates
  for:
    resp, err = stream.Recv()
    if err: return err  // reconnect

    // Update local state
    mc.mu.Lock()
    mc.currentMaster = masterAddr
    mc.mu.Unlock()
    mc.onMasterChange(masterAddr)

    // Propagate peer updates if applicable
    if mc.onPeerUpdate != nil && len(resp.ClusterNodeUpdates) > 0:
      mc.onPeerUpdate(resp.ClusterNodeUpdates)
```

### gRPC Dial Options
```
func LoadClientTLSDialOption(cfg TLSCertConfig) (grpc.DialOption, error):
  if cfg.Cert == "" && cfg.Key == "":
    return grpc.WithTransportCredentials(insecure.NewCredentials()), nil

  cert, err = tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
  if cfg.CA != "":
    // mTLS: load CA for server verification
    caCert = readFile(cfg.CA)
    caCertPool = x509.NewCertPool()
    caCertPool.AppendCertsFromPEM(caCert)
    tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caCertPool}
  else:
    tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true}

  return grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)), nil
```

### Port Derivation
```
func DeriveGrpcPort(httpPort int) int:
  return httpPort + 10000

func (a ServerAddress) ToGrpcAddress() string:
  // parse "host:port" or "host:httpPort.grpcPort"
  // return "host:grpcPort"
```

## 8. Persistence Model

No persistence. gRPC connections are transient. The `MasterClient` maintains in-memory state (current master address) that is re-established on reconnect.

## 9. Concurrency Model

- `grpc.ClientConn` is safe for concurrent use by multiple goroutines
- `MasterClient.currentMaster` is protected by `sync.RWMutex`
- `WithXxxClient` creates a new connection per call (for unary) or reuses a long-lived connection (for streaming)
- The `KeepConnectedToMaster` goroutine is the only writer of `currentMaster`; all callers are readers

## 10. Configuration

```go
type GRPCConfig struct {
    // Cert, Key, CA for client-side TLS when connecting to each service.
    // Configured per-service via security.toml [grpc.{master,volume,filer}].
    Master TLSCertConfig
    Volume TLSCertConfig
    Filer  TLSCertConfig
    // MaxRecvMsgSize is the maximum gRPC message size for receives. Default: 4MB.
    MaxRecvMsgSize int `default:"4194304"`
    // MaxSendMsgSize is the maximum gRPC message size for sends. Default: 4MB.
    MaxSendMsgSize int `default:"4194304"`
    // KeepaliveTime is how often to send keepalives on idle connections. Default: 30s.
    KeepaliveTime time.Duration `default:"30s"`
    // KeepaliveTimeout is the keepalive response timeout. Default: 10s.
    KeepaliveTimeout time.Duration `default:"10s"`
}
```

## 11. Observability

- gRPC connection attempts and failures logged at DEBUG
- Master leader changes logged at INFO: `"connected to master leader %s (was %s)"`
- `MasterClient.currentMaster` exposed via `expvar`
- gRPC interceptors record request latency and error codes for all unary RPCs:
  - `obs.GRPCRequestDuration.WithLabelValues(service, method, code).Observe(duration)`

## 12. Testing Strategy

- **Unit tests**:
  - `TestServerAddressGrpcDerivation`: verify port arithmetic for various formats
  - `TestServerDiscoveryRoundRobin`: verify instances returned in rotation
  - `TestMasterClientReconnect`: mock master, disconnect it, assert client reconnects to next master
- **Integration tests** (in-process gRPC servers):
  - `TestWithMasterServerClient`: start test master gRPC server, call `WithMasterServerClient`, assert handler invoked
  - `TestKeepConnectedToMasterStream`: start test master with KeepConnected handler, assert client receives updates
- **Proto-level tests**:
  - `TestHeartbeatMessageSerialization`: marshal/unmarshal Heartbeat proto, assert round-trip equality

## 13. Open Questions

None.
