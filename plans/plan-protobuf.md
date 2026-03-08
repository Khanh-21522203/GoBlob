# Feature: Protobuf Schemas & gRPC Service Definitions

## 1. Purpose

This plan defines every `.proto` file in the GoBlob project. These files are the source of truth for all inter-service communication. The generated Go code (`*_pb/` packages) is imported by every other subsystem — master, volume, filer, S3 gateway, admin shell, and client SDK.

Without compiling these proto files first, no other package can be compiled.

## 2. Responsibilities

- Define all gRPC service interfaces and their RPC method signatures
- Define all protobuf message types used across service boundaries
- Generate Go stubs via `protoc --go_out --go-grpc_out`
- Provide a single canonical schema for each cross-service data type (Entry, FileChunk, Volume, etc.)

## 3. Non-Responsibilities

- Does not implement any business logic
- Does not manage connections or retries (that is `plan-grpc-transport`)
- Does not define in-process Go types (those are in `plan-core-types`, `plan-filer-store`, etc.)

## 4. Architecture Design

```
proto/
  master.proto          -> pb/master_pb/
  volume_server.proto   -> pb/volume_server_pb/
  filer.proto           -> pb/filer_pb/
  iam.proto             -> pb/iam_pb/
  s3.proto              -> pb/s3_pb/
```

Each `.proto` file declares `option go_package = "goblob/pb/<name>_pb";`.

Generation command:
```bash
protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  proto/*.proto
```

## 5. Core Data Structures (Go)

The following sections show each proto file in full.

---

### master.proto

```protobuf
syntax = "proto3";
package master_pb;
option go_package = "goblob/pb/master_pb";

// MasterService is the master gRPC service.
service MasterService {
    // Volume server heartbeat (bidirectional stream).
    rpc SendHeartbeat(stream Heartbeat) returns (stream HeartbeatResponse) {}

    // Filer/S3 gateway registration and topology updates (bidirectional stream).
    rpc KeepConnected(stream KeepConnectedRequest) returns (stream KeepConnectedResponse) {}

    // Assign a new file ID for a client upload.
    rpc Assign(AssignRequest) returns (AssignResponse) {}

    // Look up volume server addresses for a VolumeId.
    rpc LookupVolume(LookupVolumeRequest) returns (LookupVolumeResponse) {}

    // Get current cluster status.
    rpc GetMasterConfiguration(GetMasterConfigurationRequest) returns (GetMasterConfigurationResponse) {}

    // List all volumes with their locations.
    rpc VolumeList(VolumeListRequest) returns (VolumeListResponse) {}
}

// Heartbeat is sent by volume servers to the master on each heartbeat tick.
message Heartbeat {
    string ip          = 1;
    uint32 port        = 2;
    string public_url  = 3;
    uint32 grpc_port   = 4;
    uint64 max_file_key = 5; // highest needle id ever written on this server
    string data_center = 6;
    string rack        = 7;
    uint32 admin_port  = 8;

    // Volumes currently on this server (full list on first heartbeat; delta thereafter).
    repeated VolumeInformationMessage volumes = 9;

    // Volume IDs that have been deleted since last heartbeat.
    repeated uint32 deleted_vids = 11;

    // Volume IDs that have been newly created since last heartbeat.
    repeated uint32 new_vids = 12;

    // Max volume counts per disk type.
    repeated MaxVolumeCounts max_volume_counts = 13;

    bool has_no_volumes   = 14; // true when this server has zero volumes
}

message MaxVolumeCounts {
    string disk_type  = 1;
    uint32 max_count  = 2;
}

message HeartbeatResponse {
    uint64 volume_size_limit = 1; // max bytes per volume
    string leader            = 2; // current Raft leader address
    string metrics_address   = 3; // Prometheus metrics push target
    repeated uint32 deleted_volume_ids = 4;
}

// VolumeInformationMessage describes a single volume on a volume server.
message VolumeInformationMessage {
    uint32 id                = 1;
    uint64 size              = 2; // bytes used
    string collection        = 3;
    uint64 file_count        = 4;
    uint64 delete_count      = 5;
    uint64 deleted_byte_count = 6;
    bool   read_only         = 7;
    uint32 replica_placement = 8; // encoded ReplicaPlacement byte
    string ttl               = 9;
    uint32 compaction_revision = 10;
    string disk_type         = 11;
}

message KeepConnectedRequest {
    string client_type    = 1; // "filer", "s3"
    string client_address = 2; // host:port of the connecting client
    string version        = 3;
    string filer_group    = 4; // optional grouping for filer instances
    string data_center    = 5;
    string rack           = 6;
    string grpc_port      = 7;
}

message KeepConnectedResponse {
    // Volume location updates streamed to connected clients.
    repeated VolumeLocation volume_locations = 1;
    string metrics_address = 2;
}

message VolumeLocation {
    string url         = 1;
    string public_url  = 2;
    repeated uint32 new_vids     = 3;
    repeated uint32 deleted_vids = 4;
    string leader      = 5; // current master leader
    string data_center = 6;
    string rack        = 7;
}

message AssignRequest {
    uint64 count        = 1;
    string replication  = 2;
    string collection   = 3;
    string ttl          = 4;
    string data_center  = 5;
    string rack         = 6;
    string disk_type    = 7;
    int64  preallocate  = 8;
    string writable_volume_count = 9;
}

message AssignResponse {
    string fid        = 1;
    string url        = 2;
    string public_url = 3;
    uint64 count      = 4;
    string error      = 5;
    string auth       = 6; // write JWT
}

message LookupVolumeRequest {
    repeated string volume_or_file_ids = 1;
    string collection                  = 2;
}

message LookupVolumeResponse {
    map<string, VolumeIdLocations> locations_map = 1;
}

message VolumeIdLocations {
    repeated Location locations = 1;
    string error               = 2;
}

message Location {
    string url        = 1;
    string public_url = 2;
    string data_center = 3;
}

message GetMasterConfigurationRequest {}
message GetMasterConfigurationResponse {
    string metrics_address      = 1;
    string leader               = 2;
    uint64 volume_size_limit    = 3;
    string default_replication  = 4;
}

message VolumeListRequest  {}
message VolumeListResponse {
    TopologyInfo topology_info = 1;
    uint64 volume_size_limit   = 2;
}

message TopologyInfo {
    string id              = 1;
    repeated DataCenterInfo data_center_infos = 2;
}

message DataCenterInfo {
    string id            = 1;
    repeated RackInfo rack_infos = 2;
}

message RackInfo {
    string id                = 1;
    repeated DataNodeInfo data_node_infos = 2;
}

message DataNodeInfo {
    string id            = 1;
    string public_url    = 2;
    uint64 free_volume_count = 3;
    uint64 active_volume_count = 4;
    uint64 remote_volume_count = 5;
    repeated DiskInfo disk_infos = 6;
}

message DiskInfo {
    string type            = 1;
    repeated VolumeInformationMessage volume_infos = 2;
    uint64 max_volume_count = 3;
    uint64 free_volume_count = 4;
}
```

---

### volume_server.proto

```protobuf
syntax = "proto3";
package volume_server_pb;
option go_package = "goblob/pb/volume_server_pb";

service VolumeServer {
    // Volume management
    rpc AllocateVolume(AllocateVolumeRequest) returns (AllocateVolumeResponse) {}
    rpc VolumeDelete(VolumeDeleteRequest) returns (VolumeDeleteResponse) {}
    rpc VolumeCopy(VolumeCopyRequest) returns (stream VolumeCopyResponse) {}
    rpc VolumeMarkReadonly(VolumeMarkReadonlyRequest) returns (VolumeMarkReadonlyResponse) {}
    rpc VolumeStatus(VolumeStatusRequest) returns (VolumeStatusResponse) {}

    // Compaction
    rpc VolumeCompact(VolumeCompactRequest) returns (VolumeCompactResponse) {}
    rpc VolumeCommitCompact(VolumeCommitCompactRequest) returns (VolumeCommitCompactResponse) {}

    // Needle iteration (for admin tools)
    rpc ReadAllNeedles(ReadAllNeedlesRequest) returns (stream ReadAllNeedlesResponse) {}
}

message AllocateVolumeRequest {
    uint32 volume_id        = 1;
    string collection       = 2;
    string replication      = 3;
    string ttl              = 4;
    string disk_type        = 5;
    int64  preallocate      = 6;
    uint32 memory_map_size_mb = 7;
}
message AllocateVolumeResponse { string error = 1; }

message VolumeDeleteRequest  { uint32 volume_id = 1; string collection = 2; }
message VolumeDeleteResponse { string error = 1; }

message VolumeCopyRequest {
    uint32 volume_id        = 1;
    string collection       = 2;
    string replication      = 3;
    string ttl              = 4;
    string source_data_node = 5;
    string disk_type        = 6;
}
message VolumeCopyResponse {
    uint64 processed_bytes = 1;
    float  process_percent = 2;
    string last_append_at_ns = 3;
}

message VolumeMarkReadonlyRequest  { uint32 volume_id = 1; bool is_readonly = 2; }
message VolumeMarkReadonlyResponse { string error = 1; }

message VolumeStatusRequest  { uint32 volume_id = 1; }
message VolumeStatusResponse {
    bool is_readonly    = 1;
    uint64 volume_size  = 2;
    string error        = 3;
}

message VolumeCompactRequest  { uint32 volume_id = 1; float garbage_threshold = 2; }
message VolumeCompactResponse { string error = 1; }

message VolumeCommitCompactRequest  { uint32 volume_id = 1; }
message VolumeCommitCompactResponse { string error = 1; }

message ReadAllNeedlesRequest  { uint32 volume_id = 1; }
message ReadAllNeedlesResponse {
    uint64 needle_id  = 1;
    uint32 cookie     = 2;
    uint64 offset     = 3;
    uint32 size       = 4;
    bytes  data       = 5;
    string mime       = 6;
    string file_name  = 7;
    bool   is_deleted = 8;
}
```

---

### filer.proto

```protobuf
syntax = "proto3";
package filer_pb;
option go_package = "goblob/pb/filer_pb";

service FilerService {
    rpc LookupDirectoryEntry(LookupDirectoryEntryRequest) returns (LookupDirectoryEntryResponse) {}
    rpc ListEntries(ListEntriesRequest) returns (stream ListEntriesResponse) {}
    rpc CreateEntry(CreateEntryRequest) returns (CreateEntryResponse) {}
    rpc UpdateEntry(UpdateEntryRequest) returns (UpdateEntryResponse) {}
    rpc AppendToEntry(AppendToEntryRequest) returns (AppendToEntryResponse) {}
    rpc DeleteEntry(DeleteEntryRequest) returns (DeleteEntryResponse) {}
    rpc AtomicRenameEntry(AtomicRenameEntryRequest) returns (AtomicRenameEntryResponse) {}
    rpc StreamRenameEntry(StreamRenameEntryRequest) returns (stream StreamRenameEntryResponse) {}

    rpc AssignVolume(AssignVolumeRequest) returns (AssignVolumeResponse) {}
    rpc LookupVolume(LookupVolumeRequest) returns (LookupVolumeResponse) {}

    rpc SubscribeMetadata(SubscribeMetadataRequest) returns (stream SubscribeMetadataResponse) {}
    rpc SubscribeLocalMetadata(SubscribeMetadataRequest) returns (stream SubscribeMetadataResponse) {}

    rpc KvGet(KvGetRequest) returns (KvGetResponse) {}
    rpc KvPut(KvPutRequest) returns (KvPutResponse) {}
    rpc KvDelete(KvDeleteRequest) returns (KvDeleteResponse) {}

    rpc GetFilerConfiguration(GetFilerConfigurationRequest) returns (GetFilerConfigurationResponse) {}

    // Distributed lock
    rpc DistributedLock(DistributedLockRequest) returns (DistributedLockResponse) {}
    rpc DistributedUnlock(DistributedUnlockRequest) returns (DistributedUnlockResponse) {}
}

// Entry is the core filer metadata type representing a file or directory.
message Entry {
    string name = 1;
    bool   is_directory = 2;
    repeated FileChunk chunks = 3;
    FuseAttributes attributes = 4;
    map<string, bytes> extended = 5;
    bytes  content              = 6; // inlined file data for small files
    bytes  hard_link_id         = 7;
    int32  hard_link_counter    = 8;
    RemoteEntry remote          = 9;
}

message FileChunk {
    string file_id        = 1;
    int64  offset         = 2;
    uint64 size           = 3;
    int64  modified_ts_ns = 4;
    string e_tag          = 5;
    bytes  cipher_key     = 6;
    bool   is_compressed  = 7;
    bool   is_chunk_manifest = 8;
    string fid            = 9; // alternative to file_id: pre-parsed vid,key,cookie
}

message FuseAttributes {
    uint64 file_size  = 1;
    int64  mtime      = 2; // unix seconds
    uint32 file_mode  = 3;
    uint32 uid        = 4;
    uint32 gid        = 5;
    int64  crtime     = 6; // creation time unix seconds
    string mime       = 7;
    string replication = 8;
    string collection  = 9;
    int32  ttl_sec     = 10;
    string user_name   = 11;
    repeated string group_names = 12;
    string symlink_target = 13;
    string md5         = 14;
    string disk_type   = 15;
    bool   is_remote   = 16;
    uint64 inode       = 17;
}

message RemoteEntry {
    string storage_name = 1;
    string remote_path  = 2;
    int64  remote_size  = 3;
    string remote_e_tag = 4;
    int64  last_local_sync_ts_ns = 5;
}

message LookupDirectoryEntryRequest  { string directory = 1; string name = 2; }
message LookupDirectoryEntryResponse { Entry entry = 1; string error = 2; }

message ListEntriesRequest {
    string directory          = 1;
    string prefix             = 2;
    string start_from_file_name = 3;
    bool   inclusive_start_from = 4;
    uint32 limit              = 5;
}
message ListEntriesResponse { Entry entry = 1; }

message CreateEntryRequest {
    string directory     = 1;
    Entry  entry         = 2;
    bool   o_excl        = 3; // fail if exists
    bool   is_from_other_cluster = 4;
    repeated string signatures = 5;
    bool   skip_check_parent_dir = 6;
}
message CreateEntryResponse { string error = 1; }

message UpdateEntryRequest {
    string directory = 1;
    Entry  entry     = 2;
    bool   is_from_other_cluster = 3;
    repeated string signatures = 4;
}
message UpdateEntryResponse { string error = 1; }

message AppendToEntryRequest {
    string directory = 1;
    string entry_name = 2;
    repeated FileChunk chunks = 3;
}
message AppendToEntryResponse { string error = 1; }

message DeleteEntryRequest {
    string directory          = 1;
    string name               = 2;
    bool   is_delete_data     = 3;
    bool   is_recursive       = 4;
    bool   ignore_recursive_error = 5;
    bool   is_from_other_cluster  = 6;
    repeated string signatures = 7;
    repeated FileChunk chunks  = 8;
}
message DeleteEntryResponse { string error = 1; }

message AtomicRenameEntryRequest {
    string old_directory = 1;
    string old_name      = 2;
    string new_directory = 3;
    string new_name      = 4;
}
message AtomicRenameEntryResponse { string error = 1; }

message StreamRenameEntryRequest {
    string old_directory = 1;
    string old_name      = 2;
    string new_directory = 3;
    string new_name      = 4;
    repeated string signatures = 5;
}
message StreamRenameEntryResponse {
    Entry  entry          = 1;
    string event_notification = 2;
    string new_parent_path = 3;
}

message AssignVolumeRequest {
    uint32 count        = 1;
    string collection   = 2;
    string replication  = 3;
    string ttl          = 4;
    string disk_type    = 5;
    string data_center  = 6;
    string rack         = 7;
    int32  replication_as_min = 8;
}
message AssignVolumeResponse {
    string file_id     = 1;
    string url         = 2;
    string public_url  = 3;
    int32  count       = 4;
    string auth        = 5;
    string collection  = 6;
    string replication = 7;
    string ttl         = 8;
    string disk_type   = 9;
    string error       = 10;
}

message LookupVolumeRequest  { repeated string volume_ids = 1; }
message LookupVolumeResponse { map<string, Locations> locations_map = 1; }
message Locations { repeated Location locations = 1; string error = 2; }
message Location  { string url = 1; string public_url = 2; string data_center = 3; }

message SubscribeMetadataRequest {
    string path_prefix  = 1;
    int64  since_ns     = 2;
    int32  client_id    = 3;
    string client_name  = 4;
    repeated string watched_events = 5;
}

message SubscribeMetadataResponse {
    string directory            = 1;
    EventNotification event_notification = 2;
    int64  ts_ns                = 3;
}

message EventNotification {
    Entry old_entry          = 1;
    Entry new_entry          = 2;
    bool  delete_chunks      = 3;
    string new_parent_path   = 4;
    bool  is_from_other_cluster = 5;
    repeated string signatures = 6;
    bool  is_truncate        = 7;
}

message KvGetRequest    { bytes key = 1; }
message KvGetResponse   { bytes value = 1; string error = 2; }
message KvPutRequest    { bytes key = 1; bytes value = 2; }
message KvPutResponse   { string error = 1; }
message KvDeleteRequest { bytes key = 1; }
message KvDeleteResponse { string error = 1; }

message GetFilerConfigurationRequest {}
message GetFilerConfigurationResponse {
    string masters           = 1;
    string replication       = 2;
    string collection        = 3;
    uint32 max_mb            = 4;
    string dir_buckets       = 5;
    bool   cipher            = 6;
    repeated string signatures = 7;
    string metrics_address   = 8;
    string filer_group       = 9;
    string data_center       = 10;
    string rack              = 11;
}

message DistributedLockRequest {
    string name        = 1;
    int64  expire_ns   = 2;
    string renew_token = 3;
    string owner       = 4;
}
message DistributedLockResponse {
    string renew_token = 1;
    string error       = 2;
}

message DistributedUnlockRequest {
    string name        = 1;
    string renew_token = 2;
}
message DistributedUnlockResponse { string error = 1; }
```

---

### iam.proto

```protobuf
syntax = "proto3";
package iam_pb;
option go_package = "goblob/pb/iam_pb";

// IAMService is co-hosted on the filer gRPC port.
service IAMService {
    rpc GetS3ApiConfiguration(GetS3ApiConfigurationRequest) returns (GetS3ApiConfigurationResponse) {}
    rpc PutS3ApiConfiguration(PutS3ApiConfigurationRequest) returns (PutS3ApiConfigurationResponse) {}
    rpc GetS3ApiConfigurations(GetS3ApiConfigurationsRequest) returns (stream GetS3ApiConfigurationsResponse) {}
}

message GetS3ApiConfigurationRequest  {}
message GetS3ApiConfigurationResponse { S3ApiConfiguration s3_api_configuration = 1; }

message PutS3ApiConfigurationRequest  { S3ApiConfiguration s3_api_configuration = 1; }
message PutS3ApiConfigurationResponse { string error = 1; }

message GetS3ApiConfigurationsRequest  {}
message GetS3ApiConfigurationsResponse { S3ApiConfiguration s3_api_configuration = 1; }

message S3ApiConfiguration {
    repeated Identity identities = 1;
}

message Identity {
    string name                   = 1;
    repeated Credential credentials = 2;
    repeated string actions        = 3;
}

message Credential {
    string access_key = 1;
    string secret_key = 2;
}
```

## 6. Public Interfaces

The generated packages expose:
- `*_pb.NewXxxServerClient(cc grpc.ClientConnInterface) XxxServerClient` — client stubs
- `RegisterXxxServerServer(s *grpc.Server, srv XxxServerServer)` — server registration
- All message types as Go structs with JSON and proto tags

## 7. Internal Algorithms

Proto compilation is a build step, not runtime logic. The `Makefile` target:

```makefile
proto:
	find proto/ -name '*.proto' | xargs protoc \
	  --go_out=. --go_opt=paths=source_relative \
	  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	  -I proto/
```

## 8. Persistence Model

None. Protobuf is a serialization format; persistence is handled by the consuming subsystems.

## 9. Concurrency Model

Generated stubs are safe for concurrent use. All serialization/deserialization is stateless.

## 10. Configuration

```toml
# No runtime configuration. Generation requires:
# - protoc >= 3.21
# - protoc-gen-go >= 1.28
# - protoc-gen-go-grpc >= 1.2
```

## 11. Observability

None — this is a code generation artifact.

## 12. Testing Strategy

- **Roundtrip tests**: For each message type, marshal to bytes then unmarshal, assert field equality
- **Compatibility tests**: Confirm that adding new optional fields does not break existing parsers (backwards compatibility)
- **`go generate`**: Run `go generate ./proto/...` in CI and assert no diff in generated files (detect schema drift)

## 13. Open Questions

None.
