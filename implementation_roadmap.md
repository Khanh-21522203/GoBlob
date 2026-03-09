# Implementation Roadmap

This roadmap provides a step-by-step guide to building GoBlob, a distributed blob storage system with S3 compatibility. The implementation is organized into logical phases that respect component dependencies and enable incremental system construction.

> **LLM Implementation Note**: Each task below is self-contained. Read the **Implementation Steps** and **Critical Details** sections before writing any code. The **Pitfalls** section lists the most common mistakes — read it before starting. The **Verification** section tells you exactly how to confirm the task is complete. Do not move to the next task until verification passes.

---

## Phase 0: Project Bootstrap

**Purpose**: Initialize the repository, module system, and tooling before any code is written.

### Task: Initialize Go Module and Repository

**Goal**
Create the Go module, establish the directory skeleton, configure code quality tooling, and set up a minimal CI pipeline so every subsequent task lands in a clean, lintable, testable repo.

**Implementation Steps**

1. Run `go mod init github.com/yourusername/goblob` in the repo root to create `go.mod`
2. Create the full directory skeleton (create each as an empty directory — add `.gitkeep` if needed):
   ```
   goblob/          # all Go packages live here
   proto/           # .proto schema files
   plans/           # feature design docs
   docs/            # architecture docs
   test/            # integration tests
   benchmark/       # performance benchmarks
   k8s/             # Kubernetes manifests
   helm/            # Helm chart
   ```
3. Create `.gitignore` with this exact content:
   ```
   # Go binaries
   blob
   *.exe

   # Generated protobuf
   **/*.pb.go
   **/*_grpc.pb.go

   # Dependencies
   vendor/

   # IDE
   .idea/
   .vscode/
   *.swp

   # Test artifacts
   *.test
   coverage.out
   ```
4. Create `Makefile` with these exact targets:
   ```makefile
   .PHONY: build test lint proto clean

   build:
   	go build -o blob ./goblob/

   test:
   	go test ./goblob/... -race -count=1

   lint:
   	golangci-lint run ./goblob/...

   proto:
   	find proto/ -name '*.proto' | xargs protoc \
   	  --go_out=. --go_opt=paths=source_relative \
   	  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
   	  -I proto/

   clean:
   	rm -f blob
   	find . -name '*.pb.go' -delete
   ```
5. Create `.golangci.yml`:
   ```yaml
   linters:
     enable:
       - vet
       - staticcheck
       - errcheck
       - gocritic
       - ineffassign
       - unused
   linters-settings:
     errcheck:
       check-blank: true
   issues:
     exclude-rules:
       - path: ".*\\.pb\\.go"
         linters: [all]
   ```
6. Add initial dependencies by running:
   ```
   go get google.golang.org/grpc@latest
   go get google.golang.org/protobuf@latest
   go get github.com/spf13/viper@latest
   go get github.com/gorilla/mux@latest
   go.uber.org/zap@latest
   go get github.com/prometheus/client_golang@latest
   go get github.com/hashicorp/raft@latest
   go get github.com/boltdb/bolt@latest
   go get github.com/syndtr/goleveldb@latest
   go mod tidy
   ```
7. Create `.github/workflows/ci.yml`:
   ```yaml
   name: CI
   on: [push, pull_request]
   jobs:
     build-and-test:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v3
         - uses: actions/setup-go@v4
           with: { go-version: '1.21' }
         - run: go build ./...
         - run: go test ./... -race
         - uses: golangci/golangci-lint-action@v3
   ```
8. Create the minimal entry point `goblob/blob.go`:
   ```go
   package main

   func main() {}
   ```
9. Create `CONTRIBUTING.md` noting:
   - Branch naming: `feature/<name>`, `fix/<name>`
   - Commit format: `<type>: <description>` (types: feat, fix, test, refactor, docs)

**Critical Details**
- The module name `github.com/yourusername/goblob` must exactly match throughout all `package` declarations and `import` paths. Decide on the real module name before any other packages are created — changing it later requires a global find-replace.
- The Makefile `proto` target must include `-I proto/` so protoc can find imports within the `proto/` directory.
- The `.golangci.yml` must exclude `*.pb.go` files from linting — generated code will always have lint errors.

**Pitfalls**
- Do not use `go mod init` with a path that does not match your actual GitHub repository path — this causes import errors later.
- Do not commit generated `*.pb.go` files (covered by `.gitignore`); they must be regenerated from `.proto` files.

**Verification**
```bash
go build ./...          # must succeed with zero errors
make lint               # must succeed with zero lint warnings
go test ./...           # passes (only blob.go exists, trivially passes)
```

**Expected Output**
- `go.mod` and `go.sum`
- `Makefile`
- `.gitignore`
- `.golangci.yml`
- `.github/workflows/ci.yml`
- `goblob/blob.go`
- Full directory skeleton

### Phase 0 Checkpoint

Run `make build && make lint` and confirm zero errors before proceeding to Phase 1.

---

## Phase 1: Foundation Layer

**Purpose**: Establish core types, configuration infrastructure, and code generation tooling that all other components depend on.

### Task: Define Core Types Package

**Goal**
Create the foundational type system used across all GoBlob components. This package has zero external dependencies and is imported by every other package — get it right before touching anything else.

**Package path**: `goblob/core/types` (not `goblob/types`)

**Implementation Steps**

1. Create `goblob/core/types/` package directory
2. Create `goblob/core/types/types.go` with all types in a single file (or split logically as shown below)
3. Define these exact types with the exact underlying types shown:
   ```go
   type VolumeId uint32    // identifies a logical volume; max ~4 billion
   type NeedleId uint64    // blob identity within a volume; globally monotonic
   type Cookie   uint32    // random 4-byte anti-brute-force value
   type Offset   uint32    // encoded byte offset = actualOffset / 8
   type Size     uint32    // on-disk byte size; 0 = tombstone (deleted)
   type DiskType string    // "hdd", "ssd", or custom tag; "" = default
   type NeedleVersion uint8
   type ServerAddress string  // typed "host:port" string
   ```
4. Define constants:
   ```go
   const NeedleAlignmentSize uint64 = 8
   const TombstoneFileSize   Size   = 0
   const NeedleIndexSize            = 16   // NeedleId(8)+Offset(4)+Size(4)
   const (
       NeedleVersionV1 NeedleVersion = 1
       NeedleVersionV2 NeedleVersion = 2
       NeedleVersionV3 NeedleVersion = 3
       CurrentNeedleVersion = NeedleVersionV3
   )
   const (
       DefaultMasterHTTPPort = 9333
       DefaultVolumeHTTPPort = 8080
       DefaultFilerHTTPPort  = 8888
       DefaultS3HTTPPort     = 8333
       GRPCPortOffset        = 10000
   )
   const (
       HardDriveType   DiskType = "hdd"
       SolidStateType  DiskType = "ssd"
       DefaultDiskType DiskType = ""
   )
   ```
5. Implement `Offset` helpers:
   ```go
   func (o Offset) ToActualOffset() int64 { return int64(o) * int64(NeedleAlignmentSize) }
   func ToEncoded(actualOffset int64) Offset { return Offset(actualOffset / int64(NeedleAlignmentSize)) }
   ```
6. Define `NeedleValue` (one entry in the `.idx` file):
   ```go
   type NeedleValue struct {
       Key    NeedleId
       Offset Offset
       Size   Size
   }
   func (nv NeedleValue) IsDeleted() bool { return nv.Size == TombstoneFileSize }
   ```
7. Implement `FileId` with **exact** string encoding:
   - Format: `"<VolumeId>,<NeedleId><Cookie>"` where NeedleId is 16 hex chars and Cookie is 8 hex chars
   - Example: VolumeId=3, NeedleId=0x01637037, Cookie=0xd6 → `"3,0000000001637037000000d6"`
   ```go
   func (f FileId) String() string {
       return fmt.Sprintf("%d,%016x%08x", f.VolumeId, f.NeedleId, f.Cookie)
   }
   ```
   - `ParseFileId` algorithm:
     1. Find comma index
     2. Parse VolumeId as decimal uint32
     3. `rest` = everything after comma; must be ≥ 9 chars
     4. Last 8 hex chars of `rest` = Cookie (uint32)
     5. Remaining hex chars = NeedleId (uint64)
8. Implement `ReplicaPlacement` with **exact** 1-byte encoding (bit layout `DDDRRRCC`):
   ```go
   type ReplicaPlacement struct {
       DifferentDataCenterCount uint8  // bits 7-5 (3 bits)
       DifferentRackCount       uint8  // bits 4-2 (3 bits)
       SameRackCount            uint8  // bits 1-0 (2 bits)
   }
   func (rp ReplicaPlacement) Byte() byte {
       return (rp.DifferentDataCenterCount&0x07)<<5 |
              (rp.DifferentRackCount&0x07)<<2 |
              (rp.SameRackCount & 0x03)
   }
   func ParseReplicaPlacement(b byte) ReplicaPlacement {
       return ReplicaPlacement{
           DifferentDataCenterCount: (b >> 5) & 0x07,
           DifferentRackCount:       (b >> 2) & 0x07,
           SameRackCount:            b & 0x03,
       }
   }
   func (rp ReplicaPlacement) TotalCopies() int {
       return 1 + int(rp.DifferentDataCenterCount) + int(rp.DifferentRackCount) + int(rp.SameRackCount)
   }
   ```
9. Implement `TTL` with **exact** 2-byte wire format:
   ```go
   type TTLUnit byte
   const (
       TTLUnitEmpty  TTLUnit = 0
       TTLUnitMinute TTLUnit = 'm'
       TTLUnitHour   TTLUnit = 'h'
       TTLUnitDay    TTLUnit = 'd'
       TTLUnitWeek   TTLUnit = 'w'
       TTLUnitMonth  TTLUnit = 'M'
       TTLUnitYear   TTLUnit = 'y'
   )
   type TTL struct { Count uint8; Unit TTLUnit }
   func (t TTL) IsNeverExpire() bool { return t.Count == 0 }
   func (t TTL) Bytes() [2]byte { return [2]byte{t.Count, byte(t.Unit)} }
   // ParseTTL("3d") → TTL{Count:3, Unit:'d'}
   // ParseTTL("") → TTL{} (no expiry)
   ```
10. Implement `ServerAddress` helpers:
    - `ToGrpcAddress()`: if address is `"host:httpPort"`, return `"host:httpPort+10000"`. If address is `"host:httpPort.grpcPort"`, return `"host:grpcPort"`.
    - `ToHttpAddress()`: strip grpcPort suffix if present
    - `Host()`: return hostname only
11. Write unit tests using table-driven tests covering:
    - `TestParseFileId`: round-trip `FileId.String()` → `ParseFileId()`, invalid inputs (no comma, short hex, bad chars)
    - `TestOffsetEncoding`: `ToEncoded(ToActualOffset(n)) == n` for various values
    - `TestReplicaPlacement`: all D/R/C combinations, round-trip `Byte()` → `ParseReplicaPlacement()`
    - `TestParseTTL`: each unit letter, empty string, count=0, count=255
    - `TestServerAddressGrpcDerivation`: port+10000 formula, explicit grpc override, malformed

**Critical Details**
- `NeedleId` in the FileId string is always **16 hex chars** (zero-padded uint64). `Cookie` is always **8 hex chars** (zero-padded uint32). If the total hex string after the comma is `N` chars, Cookie = last 8, NeedleId = chars `[0:N-8]`.
- `Offset` encodes `actualByteOffset / 8` — not the raw offset. This limits volume files to 32 GB (2^32 × 8).
- `TombstoneFileSize = 0` — a Size of zero means deleted. A real needle always has Size > 0.
- `ReplicaPlacement` string representation is `"DDR"` decimal digits, e.g., `"001"` means 1 copy same rack; `"010"` means 1 copy different rack; `"100"` means 1 copy different DC.

**Pitfalls**
- Do NOT place the package at `goblob/types/` — use `goblob/core/types/` to match the module structure used in all plan files.
- The `Cookie` is 4 bytes (uint32) but 8 hex chars. The `NeedleId` is 8 bytes (uint64) but 16 hex chars. Mixing these up breaks FileId parsing.
- `TTLUnit` values are ASCII character codes (`'m'=109`, `'h'=104`), not integers 1–6.

**Verification**
```bash
go test ./goblob/core/types/... -v -run TestParseFileId   # all cases pass
go test ./goblob/core/types/... -v -run TestReplicaPlacement
go test ./goblob/core/types/... -race                     # no race conditions
```

**Expected Output**
- `goblob/core/types/types.go` (or split into logical files)
- `goblob/core/types/types_test.go`

---

### Task: Setup Protobuf Code Generation

**Goal**
Define all gRPC service interfaces and message types in `.proto` files, then generate Go stubs. All other packages depend on these generated stubs — no other Go code can be written until this step is complete.

**Prerequisites**: Install `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc`:
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# protoc must be installed via system package manager
```

**Implementation Steps**

1. Create these proto files with the exact content below. Each file declares `option go_package` to control where generated Go code lands.

2. Create `proto/master.proto`:
   ```protobuf
   syntax = "proto3";
   package master_pb;
   option go_package = "goblob/pb/master_pb";

   service MasterService {
       rpc SendHeartbeat(stream Heartbeat) returns (stream HeartbeatResponse) {}
       rpc KeepConnected(stream KeepConnectedRequest) returns (stream KeepConnectedResponse) {}
       rpc Assign(AssignRequest) returns (AssignResponse) {}
       rpc LookupVolume(LookupVolumeRequest) returns (LookupVolumeResponse) {}
       rpc GetMasterConfiguration(GetMasterConfigurationRequest) returns (GetMasterConfigurationResponse) {}
       rpc VolumeList(VolumeListRequest) returns (VolumeListResponse) {}
   }

   message Heartbeat {
       string ip = 1; uint32 port = 2; string public_url = 3; uint32 grpc_port = 4;
       uint64 max_file_key = 5; string data_center = 6; string rack = 7; uint32 admin_port = 8;
       repeated VolumeInformationMessage volumes = 9;
       repeated uint32 deleted_vids = 11; repeated uint32 new_vids = 12;
       repeated MaxVolumeCounts max_volume_counts = 13; bool has_no_volumes = 14;
   }
   message MaxVolumeCounts { string disk_type = 1; uint32 max_count = 2; }
   message HeartbeatResponse {
       uint64 volume_size_limit = 1; string leader = 2; string metrics_address = 3;
       repeated uint32 deleted_volume_ids = 4;
   }
   message VolumeInformationMessage {
       uint32 id = 1; uint64 size = 2; string collection = 3;
       uint64 file_count = 4; uint64 delete_count = 5; uint64 deleted_byte_count = 6;
       bool read_only = 7; uint32 replica_placement = 8; string ttl = 9;
       uint32 compaction_revision = 10; string disk_type = 11;
   }
   message KeepConnectedRequest {
       string client_type = 1; string client_address = 2; string version = 3;
       string filer_group = 4; string data_center = 5; string rack = 6; string grpc_port = 7;
   }
   message KeepConnectedResponse {
       repeated VolumeLocation volume_locations = 1; string metrics_address = 2;
   }
   message VolumeLocation {
       string url = 1; string public_url = 2;
       repeated uint32 new_vids = 3; repeated uint32 deleted_vids = 4;
       string leader = 5; string data_center = 6; string rack = 7;
   }
   message AssignRequest {
       uint64 count = 1; string replication = 2; string collection = 3; string ttl = 4;
       string data_center = 5; string rack = 6; string disk_type = 7;
       int64 preallocate = 8; string writable_volume_count = 9;
   }
   message AssignResponse {
       string fid = 1; string url = 2; string public_url = 3;
       uint64 count = 4; string error = 5; string auth = 6;
   }
   message LookupVolumeRequest { repeated string volume_or_file_ids = 1; string collection = 2; }
   message LookupVolumeResponse { map<string, VolumeIdLocations> locations_map = 1; }
   message VolumeIdLocations { repeated Location locations = 1; string error = 2; }
   message Location { string url = 1; string public_url = 2; string data_center = 3; }
   message GetMasterConfigurationRequest {}
   message GetMasterConfigurationResponse {
       string metrics_address = 1; string leader = 2;
       uint64 volume_size_limit = 3; string default_replication = 4;
   }
   message VolumeListRequest {}
   message VolumeListResponse { TopologyInfo topology_info = 1; uint64 volume_size_limit = 2; }
   message TopologyInfo { string id = 1; repeated DataCenterInfo data_center_infos = 2; }
   message DataCenterInfo { string id = 1; repeated RackInfo rack_infos = 2; }
   message RackInfo { string id = 1; repeated DataNodeInfo data_node_infos = 2; }
   message DataNodeInfo {
       string id = 1; string public_url = 2;
       uint64 free_volume_count = 3; uint64 active_volume_count = 4; uint64 remote_volume_count = 5;
       repeated DiskInfo disk_infos = 6;
   }
   message DiskInfo {
       string type = 1; repeated VolumeInformationMessage volume_infos = 2;
       uint64 max_volume_count = 3; uint64 free_volume_count = 4;
   }
   ```

3. Create `proto/volume_server.proto`:
   ```protobuf
   syntax = "proto3";
   package volume_server_pb;
   option go_package = "goblob/pb/volume_server_pb";

   service VolumeServer {
       rpc AllocateVolume(AllocateVolumeRequest) returns (AllocateVolumeResponse) {}
       rpc VolumeDelete(VolumeDeleteRequest) returns (VolumeDeleteResponse) {}
       rpc VolumeCopy(VolumeCopyRequest) returns (stream VolumeCopyResponse) {}
       rpc VolumeMarkReadonly(VolumeMarkReadonlyRequest) returns (VolumeMarkReadonlyResponse) {}
       rpc VolumeStatus(VolumeStatusRequest) returns (VolumeStatusResponse) {}
       rpc VolumeCompact(VolumeCompactRequest) returns (VolumeCompactResponse) {}
       rpc VolumeCommitCompact(VolumeCommitCompactRequest) returns (VolumeCommitCompactResponse) {}
       rpc ReadAllNeedles(ReadAllNeedlesRequest) returns (stream ReadAllNeedlesResponse) {}
   }
   message AllocateVolumeRequest {
       uint32 volume_id = 1; string collection = 2; string replication = 3; string ttl = 4;
       string disk_type = 5; int64 preallocate = 6; uint32 memory_map_size_mb = 7;
   }
   message AllocateVolumeResponse { string error = 1; }
   message VolumeDeleteRequest { uint32 volume_id = 1; string collection = 2; }
   message VolumeDeleteResponse { string error = 1; }
   message VolumeCopyRequest {
       uint32 volume_id = 1; string collection = 2; string replication = 3; string ttl = 4;
       string source_data_node = 5; string disk_type = 6;
   }
   message VolumeCopyResponse { uint64 processed_bytes = 1; float process_percent = 2; string last_append_at_ns = 3; }
   message VolumeMarkReadonlyRequest { uint32 volume_id = 1; bool is_readonly = 2; }
   message VolumeMarkReadonlyResponse { string error = 1; }
   message VolumeStatusRequest { uint32 volume_id = 1; }
   message VolumeStatusResponse { bool is_readonly = 1; uint64 volume_size = 2; string error = 3; }
   message VolumeCompactRequest { uint32 volume_id = 1; float garbage_threshold = 2; }
   message VolumeCompactResponse { string error = 1; }
   message VolumeCommitCompactRequest { uint32 volume_id = 1; }
   message VolumeCommitCompactResponse { string error = 1; }
   message ReadAllNeedlesRequest { uint32 volume_id = 1; }
   message ReadAllNeedlesResponse {
       uint64 needle_id = 1; uint32 cookie = 2; uint64 offset = 3; uint32 size = 4;
       bytes data = 5; string mime = 6; string file_name = 7; bool is_deleted = 8;
   }
   ```

4. Create `proto/filer.proto` with `FilerService` (see `plans/plan-protobuf.md` for full content). Key RPCs:
   - `LookupDirectoryEntry`, `ListEntries` (streaming), `CreateEntry`, `UpdateEntry`, `AppendToEntry`, `DeleteEntry`, `AtomicRenameEntry`, `StreamRenameEntry`
   - `AssignVolume`, `LookupVolume`
   - `SubscribeMetadata`, `SubscribeLocalMetadata` (streaming)
   - `KvGet`, `KvPut`, `KvDelete`
   - `GetFilerConfiguration`
   - `DistributedLock`, `DistributedUnlock`

5. Create `proto/iam.proto`:
   ```protobuf
   syntax = "proto3";
   package iam_pb;
   option go_package = "goblob/pb/iam_pb";

   service IAMService {
       rpc GetS3ApiConfiguration(GetS3ApiConfigurationRequest) returns (GetS3ApiConfigurationResponse) {}
       rpc PutS3ApiConfiguration(PutS3ApiConfigurationRequest) returns (PutS3ApiConfigurationResponse) {}
       rpc GetS3ApiConfigurations(GetS3ApiConfigurationsRequest) returns (stream GetS3ApiConfigurationsResponse) {}
   }
   message S3ApiConfiguration { repeated Identity identities = 1; }
   message Identity { string name = 1; repeated Credential credentials = 2; repeated string actions = 3; }
   message Credential { string access_key = 1; string secret_key = 2; }
   message GetS3ApiConfigurationRequest {}
   message GetS3ApiConfigurationResponse { S3ApiConfiguration s3_api_configuration = 1; }
   message PutS3ApiConfigurationRequest { S3ApiConfiguration s3_api_configuration = 1; }
   message PutS3ApiConfigurationResponse { string error = 1; }
   message GetS3ApiConfigurationsRequest {}
   message GetS3ApiConfigurationsResponse { S3ApiConfiguration s3_api_configuration = 1; }
   ```

6. Run `make proto` to generate Go stubs. Generated files land in `goblob/pb/master_pb/`, `goblob/pb/volume_server_pb/`, `goblob/pb/filer_pb/`, `goblob/pb/iam_pb/`.

7. Add these to `.gitignore` (already covered by `**/*.pb.go`).

8. Add round-trip marshaling tests for key message types.

**Critical Details**
- The `go_package` option controls where generated code lands. It must match the import path used by consuming packages exactly.
- The `SendHeartbeat` and `SubscribeMetadata` RPCs use **bidirectional** or **server-side** streaming — the `stream` keyword placement matters:
  - Bidirectional: `rpc Foo(stream A) returns (stream B)`
  - Server streaming: `rpc Foo(A) returns (stream B)`
- Generated `*_grpc.pb.go` files contain both the client stub and server interface. Implement the server interface in Phase 4, not here.

**Pitfalls**
- Forgetting `-I proto/` in the `protoc` command causes import errors if proto files reference each other.
- Do not manually edit generated `*.pb.go` files — they are overwritten on each `make proto`.
- The field numbers in proto messages are permanent once data is written to disk or network. Never renumber them.

**Verification**
```bash
make proto                    # generates without errors
go build ./goblob/pb/...      # all generated packages compile
# Confirm generated files exist:
ls goblob/pb/master_pb/
ls goblob/pb/volume_server_pb/
ls goblob/pb/filer_pb/
ls goblob/pb/iam_pb/
```

**Expected Output**
- `proto/master.proto`, `proto/volume_server.proto`, `proto/filer.proto`, `proto/iam.proto`
- `goblob/pb/master_pb/*.pb.go` (generated)
- `goblob/pb/volume_server_pb/*.pb.go` (generated)
- `goblob/pb/filer_pb/*.pb.go` (generated)
- `goblob/pb/iam_pb/*.pb.go` (generated)

---

### Task: Implement Configuration System

**Goal**
Build unified configuration loading using TOML files and CLI flag overrides via Viper. Every server role reads its config through this package.

**Package path**: `goblob/config`

**Implementation Steps**

1. Create `goblob/config/config.go` with the `Loader` interface and `NewViperLoader` constructor:
   ```go
   type Loader interface {
       LoadMasterConfig() (*MasterConfig, error)
       LoadVolumeConfig() (*VolumeServerConfig, error)
       LoadFilerConfig() (*FilerConfig, error)
       LoadSecurityConfig() (*SecurityConfig, error)
   }
   // NewViperLoader creates a Loader using Viper, searching standard config paths.
   // cliFlags overrides file values: key = mapstructure tag name, value = CLI arg value.
   func NewViperLoader(cliFlags map[string]interface{}) Loader
   func ConfigSearchPaths() []string  // returns [., $HOME/.goblob/, /usr/local/etc/goblob/, /etc/goblob/]
   ```

2. Implement the **config loading algorithm** (same for every role):
   ```
   1. Create a new viper.Viper instance (NOT the global singleton — test isolation)
   2. Set all defaults (see defaults table below)
   3. Set config name (e.g., "master") and type "toml"
   4. Add search paths in order: ".", "$HOME/.goblob/", "/usr/local/etc/goblob/", "/etc/goblob/"
   5. Call viper.ReadInConfig() — silently ignore ConfigFileNotFoundError
   6. For each key in cliFlags: call viper.Set(key, value)   ← CLI overrides file
   7. Call viper.Unmarshal(&cfg) into the typed Config struct
   8. Run validate(cfg) — return error on constraint violations
   ```

3. Define `MasterConfig` struct in `goblob/config/master.go`:
   ```go
   type MasterConfig struct {
       Port              int             `mapstructure:"port"`
       GRPCPort          int             `mapstructure:"grpc_port"`
       MetaDir           string          `mapstructure:"meta_dir"`
       Peers             []string        `mapstructure:"peers"`
       VolumeSizeLimitMB uint32          `mapstructure:"volume_size_limit_mb"`
       DefaultReplication string         `mapstructure:"default_replication"`
       GarbageThreshold  float64         `mapstructure:"garbage_threshold"`
       VolumeGrowth      VolumeGrowthConfig `mapstructure:"volume_growth"`
       Maintenance       MaintenanceConfig  `mapstructure:"maintenance"`
       DataCenter        string          `mapstructure:"data_center"`
       Rack              string          `mapstructure:"rack"`
       ReplicationAsMin  bool            `mapstructure:"replication_as_min"`
   }
   type VolumeGrowthConfig struct {
       Copy1Count     int     `mapstructure:"copy_1"`
       Copy2Count     int     `mapstructure:"copy_2"`
       Copy3Count     int     `mapstructure:"copy_3"`
       CopyOtherCount int     `mapstructure:"copy_other"`
       Threshold      float64 `mapstructure:"threshold"`
   }
   type MaintenanceConfig struct {
       Scripts      string `mapstructure:"scripts"`
       SleepMinutes int    `mapstructure:"sleep_minutes"`
   }
   ```

4. Define `VolumeServerConfig` struct in `goblob/config/volume.go`:
   ```go
   type VolumeServerConfig struct {
       Port                       int                  `mapstructure:"port"`
       GRPCPort                   int                  `mapstructure:"grpc_port"`
       Masters                    []string             `mapstructure:"masters"`
       DataCenter                 string               `mapstructure:"data_center"`
       Rack                       string               `mapstructure:"rack"`
       Directories                []DiskDirectoryConfig `mapstructure:"directories"`
       IndexType                  string               `mapstructure:"index_type"`
       ReadMode                   string               `mapstructure:"read_mode"`
       CompactionBytesPerSecond   int64                `mapstructure:"compaction_bytes_per_second"`
       ConcurrentUploadLimitMB    int64                `mapstructure:"concurrent_upload_limit_mb"`
       ConcurrentDownloadLimitMB  int64                `mapstructure:"concurrent_download_limit_mb"`
       FileSizeLimitMB            int64                `mapstructure:"file_size_limit_mb"`
       HeartbeatInterval          time.Duration        `mapstructure:"heartbeat_interval"`
       PreStopSeconds             int                  `mapstructure:"pre_stop_seconds"`
       PublicPort                 int                  `mapstructure:"public_port"`
   }
   type DiskDirectoryConfig struct {
       Path               string          `mapstructure:"path"`
       IdxPath            string          `mapstructure:"idx_path"`
       MaxVolumeCount     int32           `mapstructure:"max_volume_count"`
       DiskType           types.DiskType  `mapstructure:"disk_type"`
       MinFreeSpacePercent float32        `mapstructure:"min_free_space_percent"`
   }
   ```

5. Define `FilerConfig` in `goblob/config/filer.go` (key fields):
   ```go
   type FilerConfig struct {
       Port                    int      `mapstructure:"port"`
       GRPCPort                int      `mapstructure:"grpc_port"`
       Masters                 []string `mapstructure:"masters"`
       DefaultStoreDir         string   `mapstructure:"default_store_dir"`
       MaxFileSizeMB           int      `mapstructure:"max_file_size_mb"`
       EncryptVolumeData       bool     `mapstructure:"encrypt_volume_data"`
       MaxFilenameLength       uint32   `mapstructure:"max_filename_length"`
       BucketsFolder           string   `mapstructure:"buckets_folder"`
       DefaultReplication      string   `mapstructure:"default_replication"`
       DefaultCollection       string   `mapstructure:"default_collection"`
       LogFlushIntervalSeconds int      `mapstructure:"log_flush_interval_seconds"`
       ConcurrentUploadLimitMB int64    `mapstructure:"concurrent_upload_limit_mb"`
   }
   ```

6. Define `SecurityConfig` in `goblob/config/security.go` (loaded from `security.toml`):
   ```go
   type SecurityConfig struct {
       JWT   JWTConfig    `mapstructure:"jwt"`
       Guard GuardConfig  `mapstructure:"guard"`
       HTTPS HTTPSConfig  `mapstructure:"https"`
       GRPC  GRPCTLSConfig `mapstructure:"grpc"`
       CORS  CORSConfig   `mapstructure:"cors"`
   }
   type JWTConfig struct {
       Signing      JWTKeyConfig `mapstructure:"signing"`
       FilerSigning JWTKeyConfig `mapstructure:"filer_signing"`
   }
   type JWTKeyConfig struct {
       Key                 string         `mapstructure:"key"`
       ExpiresAfterSeconds int            `mapstructure:"expires_after_seconds"`
       Read                JWTKeyLeafConfig `mapstructure:"read"`
   }
   type JWTKeyLeafConfig struct { Key string `mapstructure:"key"`; ExpiresAfterSeconds int `mapstructure:"expires_after_seconds"` }
   type GuardConfig struct { WhiteList string `mapstructure:"white_list"` }
   type TLSCertConfig struct { Cert string `mapstructure:"cert"`; Key string `mapstructure:"key"`; CA string `mapstructure:"ca"` }
   type HTTPSConfig struct { Master TLSCertConfig `mapstructure:"master"`; Volume TLSCertConfig `mapstructure:"volume"`; Filer TLSCertConfig `mapstructure:"filer"` }
   type GRPCTLSConfig struct { Master TLSCertConfig `mapstructure:"master"`; Volume TLSCertConfig `mapstructure:"volume"`; Filer TLSCertConfig `mapstructure:"filer"` }
   type CORSConfig struct { AllowedOrigins string `mapstructure:"allowed_origins"` }
   ```

7. Implement **validation rules** in `goblob/config/validation.go`:
   - `MasterConfig.Peers`: count must be 0 or odd (1, 3, 5); empty/`["none"]` means single-master
   - `MasterConfig.MetaDir`: must be writable (call `os.MkdirAll` + attempt temp file write)
   - `VolumeServerConfig.Directories`: at least 1 entry required
   - `VolumeServerConfig.IndexType`: must be one of `memory|leveldb|leveldbMedium|leveldbLarge`
   - `VolumeServerConfig.ReadMode`: must be one of `local|proxy|redirect`
   - `FilerConfig.Masters`: at least 1 master address required

8. Set **defaults** via `viper.SetDefault`:

   | Config key | Default value |
   |---|---|
   | `port` (master) | `9333` |
   | `port` (volume) | `8080` |
   | `port` (filer) | `8888` |
   | `volume_size_limit_mb` | `30000` |
   | `default_replication` | `"000"` |
   | `garbage_threshold` | `0.3` |
   | `volume_growth.copy_1` | `7` |
   | `volume_growth.copy_2` | `6` |
   | `volume_growth.copy_3` | `3` |
   | `volume_growth.copy_other` | `1` |
   | `volume_growth.threshold` | `0.9` |
   | `maintenance.sleep_minutes` | `17` |
   | `index_type` | `"leveldb"` |
   | `read_mode` | `"redirect"` |
   | `heartbeat_interval` | `"5s"` |
   | `pre_stop_seconds` | `10` |
   | `max_file_size_mb` | `4` |
   | `max_filename_length` | `255` |
   | `buckets_folder` | `"/buckets"` |
   | `log_flush_interval_seconds` | `60` |
   | `jwt.signing.expires_after_seconds` | `10` |

9. Write unit tests:
   - No config file: only defaults returned
   - Config file with all fields: full unmarshal
   - CLI override takes precedence over file value
   - Invalid field values return validation error
   - Each test creates its own `viper.Viper` (not global)
   - Tests write TOML to `t.TempDir()`

**Critical Details**
- Use `viper.New()` (not `viper.GetViper()`) in every test to prevent state leakage between tests.
- The search path precedence is: CLI flags > env vars > config file > defaults.
- Config files are read-only at runtime; never write back to them.

**Pitfalls**
- Using the global `viper` singleton in tests causes race conditions when tests run in parallel.
- `mapstructure:"heartbeat_interval"` must be a `time.Duration` field — Viper parses duration strings like `"5s"` correctly.

**Verification**
```bash
go test ./goblob/config/... -v       # all loading and validation tests pass
go vet ./goblob/config/...           # no vet errors
```

**Expected Output**
- `goblob/config/config.go`
- `goblob/config/master.go`
- `goblob/config/volume.go`
- `goblob/config/filer.go`
- `goblob/config/security.go`
- `goblob/config/validation.go`
- `goblob/config/config_test.go`

---

### Task: Setup Observability Infrastructure

**Goal**
Establish structured logging (zap) and Prometheus metrics. Other packages import `goblob/stats` to record metrics — they do not import the full observability package directly.

**Implementation Steps**

1. Create `goblob/observability/logger.go`:
   ```go
   // NewLogger creates a zap.Logger.
   // If json=true, uses JSON encoding (production). Otherwise uses console (dev).
   func NewLogger(level zapcore.Level, json bool) *zap.Logger
   // ReplaceGlobal sets the package-level logger used by zap.L() and zap.S()
   func ReplaceGlobal(l *zap.Logger)
   ```

2. Create `goblob/observability/metrics.go`:
   ```go
   // NewRegistry creates a Prometheus registry (not the default global registry).
   // Use a non-default registry so tests can create isolated registries.
   func NewRegistry() *prometheus.Registry
   // MustRegister registers collectors with the registry; panics on error.
   func MustRegister(r *prometheus.Registry, cs ...prometheus.Collector)
   ```

3. Create `goblob/observability/handlers.go`:
   ```go
   // MetricsHandler returns an HTTP handler for /metrics using the given registry.
   func MetricsHandler(r *prometheus.Registry) http.Handler
   // PProfHandlers returns a map of path → handler for /debug/pprof/* endpoints.
   func PProfHandlers() map[string]http.Handler
   ```

4. Create `goblob/stats/stats.go` — this is the package all server code imports:
   ```go
   package stats

   import "github.com/prometheus/client_golang/prometheus"

   var (
       // Master metrics
       MasterAssignRequests = prometheus.NewCounter(prometheus.CounterOpts{
           Name: "goblob_master_assign_requests_total",
           Help: "Total number of file ID assign requests handled by master",
       })
       MasterLookupRequests = prometheus.NewCounter(prometheus.CounterOpts{
           Name: "goblob_master_lookup_requests_total",
       })

       // Volume server metrics
       VolumeServerReadBytes = prometheus.NewCounter(prometheus.CounterOpts{
           Name: "goblob_volume_read_bytes_total",
           Help: "Total bytes read from volume files",
       })
       VolumeServerWriteBytes = prometheus.NewCounter(prometheus.CounterOpts{
           Name: "goblob_volume_write_bytes_total",
       })
       VolumeServerNeedleCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
           Name: "goblob_volume_needle_count",
       }, []string{"volume_id"})

       // Filer metrics
       FilerMetadataOps = prometheus.NewCounterVec(prometheus.CounterOpts{
           Name: "goblob_filer_metadata_ops_total",
       }, []string{"op"})  // op: create, update, delete, list

       // Request latencies
       RequestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
           Name:    "goblob_request_duration_seconds",
           Buckets: prometheus.DefBuckets,
       }, []string{"server", "op"})
   )

   // Register adds all declared metrics to the given registry.
   func Register(r *prometheus.Registry) { r.MustRegister(MasterAssignRequests, ...) }
   ```

5. Write a simple usage example showing how server code records a metric:
   ```go
   stats.MasterAssignRequests.Inc()
   stats.VolumeServerReadBytes.Add(float64(n.DataSize))
   timer := prometheus.NewTimer(stats.RequestLatency.WithLabelValues("volume", "read"))
   defer timer.ObserveDuration()
   ```

**Critical Details**
- Use `go.uber.org/zap` for logging (not `logrus`). The plan specifies zap.
- The `goblob/stats` package must NOT import `goblob/observability` — it only imports `prometheus/client_golang`. This prevents circular imports.
- Use a non-default Prometheus registry in all production code. The default global registry is not safe for test isolation.

**Pitfalls**
- If `goblob/stats` imports `goblob/observability`, circular imports will occur because `observability` imports `stats` for metric registration.
- Do not use `prometheus.MustRegister()` (global) — use `registry.MustRegister()`.

**Verification**
```bash
go build ./goblob/observability/... ./goblob/stats/...
go test ./goblob/observability/...
```

**Expected Output**
- `goblob/observability/logger.go`
- `goblob/observability/metrics.go`
- `goblob/observability/handlers.go`
- `goblob/stats/stats.go`

---

### Task: Implement Security Primitives

**Goal**
Build JWT token generation/verification, TLS configuration loading, and an IP whitelist Guard middleware that protects HTTP and gRPC endpoints.

**Implementation Steps**

1. Create `goblob/security/jwt.go`:
   ```go
   // SignJWT creates a signed JWT with the given signing key and expiry.
   // Uses HS256 (HMAC-SHA256) algorithm.
   // Returns signed token string or error.
   func SignJWT(key string, expiresAfterSec int) (string, error)

   // VerifyJWT verifies a JWT token string against the given key.
   // Returns claims map or error if invalid/expired.
   func VerifyJWT(tokenString, key string) (jwt.MapClaims, error)
   ```
   Use `github.com/golang-jwt/jwt/v4` (add to go.mod). Sign with `jwt.SigningMethodHS256`.

2. Create `goblob/security/guard.go`:
   ```go
   // Guard enforces access control: IP whitelist + optional JWT verification.
   type Guard struct {
       whiteList    []string // CIDR ranges and/or exact IPs
       signingKey   string   // HS256 key; empty = no JWT required
       filerKey     string   // separate key for filer access
   }
   func NewGuard(whiteList, signingKey, filerKey string) *Guard
   // Allowed returns true if the request passes whitelist and JWT checks.
   func (g *Guard) Allowed(r *http.Request, requiresJWT bool) bool
   // ParseWhiteList parses "192.168.0.0/24,10.0.0.1" into a list of CIDRs/IPs.
   func ParseWhiteList(s string) []string
   ```

3. Create `goblob/security/middleware.go`:
   ```go
   // HTTPMiddleware wraps an HTTP handler with Guard enforcement.
   func HTTPMiddleware(guard *Guard, next http.Handler) http.Handler
   // GRPCUnaryInterceptor creates a gRPC unary interceptor for Guard.
   func GRPCUnaryInterceptor(guard *Guard) grpc.UnaryServerInterceptor
   ```

4. Create `goblob/security/tls.go`:
   ```go
   // LoadTLSConfig loads TLS config from cert/key/ca file paths.
   // ca is optional; if non-empty, enables mutual TLS verification.
   func LoadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error)
   // LoadClientTLSConfig loads TLS config for a gRPC client.
   func LoadClientTLSConfig(certFile, keyFile, caFile string) (credentials.TransportCredentials, error)
   ```
   Note: TLS cert file watching (hot-reload) is NOT implemented here — that is a future enhancement.

5. Write unit tests:
   - `TestJWTRoundTrip`: sign then verify, assert claims match
   - `TestJWTExpired`: sign with 1-second expiry, sleep 2s, verify returns error
   - `TestJWTInvalidKey`: verify with wrong key returns error
   - `TestGuardIPWhitelist`: request from allowed IP passes; from non-listed IP blocked
   - `TestGuardJWT`: request without JWT blocked when key configured; with valid JWT passes

**Critical Details**
- Use `github.com/golang-jwt/jwt/v4` — not the deprecated `dgrijalva/jwt-go`.
- The Guard's IP check must handle both exact IPs and CIDR ranges using `net.ParseCIDR` and `net.IP.Mask`.
- When `signingKey` is empty, JWT verification is skipped (system runs open).

**Pitfalls**
- Do not use `jwt.Parse` without specifying `jwt.WithValidMethods([]string{"HS256"})` — this creates a security vulnerability (algorithm confusion attacks).

**Verification**
```bash
go test ./goblob/security/... -v -race
```

**Expected Output**
- `goblob/security/jwt.go`
- `goblob/security/guard.go`
- `goblob/security/tls.go`
- `goblob/security/middleware.go`
- `goblob/security/security_test.go`

---

### Task: Implement Shared Utilities Package

**Goal**
Build `goblob/util/` with thread-safe data structures used across topology, filer, storage, and server packages.

**Implementation Steps**

1. Create `goblob/util/concurrent_read_map.go`:
   ```go
   // ConcurrentReadMap is a thread-safe map optimized for concurrent reads.
   // Write operations take an exclusive lock; reads take a shared lock.
   type ConcurrentReadMap[K comparable, V any] struct {
       mu sync.RWMutex
       m  map[K]V
   }
   func NewConcurrentReadMap[K comparable, V any]() *ConcurrentReadMap[K, V]
   func (m *ConcurrentReadMap[K, V]) Get(key K) (V, bool)
   func (m *ConcurrentReadMap[K, V]) Set(key K, value V)
   func (m *ConcurrentReadMap[K, V]) Delete(key K)
   func (m *ConcurrentReadMap[K, V]) Range(fn func(K, V) bool) // calls fn for each entry
   ```
   If not using generics (Go < 1.18), use `interface{}` values with type assertions.

2. Create `goblob/util/unbounded_queue.go`:
   ```go
   // UnboundedQueue is a goroutine-safe, unbounded FIFO queue backed by a linked list.
   // Used by the Filer for background blob deletion.
   type UnboundedQueue[T any] struct {
       mu   sync.Mutex
       head *node[T]
       tail *node[T]
       cond *sync.Cond
   }
   type node[T any] struct { val T; next *node[T] }
   func NewUnboundedQueue[T any]() *UnboundedQueue[T]
   func (q *UnboundedQueue[T]) Enqueue(v T)
   // Dequeue blocks until an item is available.
   func (q *UnboundedQueue[T]) Dequeue() T
   // TryDequeue returns (value, true) if an item is ready, else (zero, false).
   func (q *UnboundedQueue[T]) TryDequeue() (T, bool)
   ```

3. Create `goblob/util/full_path.go`:
   ```go
   // FullPath is an absolute filer path like "/foo/bar/baz.txt".
   type FullPath string
   // Dir returns the parent directory path, e.g., "/foo/bar".
   func (p FullPath) Dir() FullPath
   // Name returns the base filename, e.g., "baz.txt".
   func (p FullPath) Name() string
   // Child appends a name to the path, e.g., p.Child("x") = "/foo/bar/x".
   func (p FullPath) Child(name string) FullPath
   ```
   Implement using `path.Dir`, `path.Base`, `path.Join` from the standard library.

4. Add numeric helpers in `goblob/util/util.go`:
   ```go
   func MinUint64(a, b uint64) uint64 { if a < b { return a }; return b }
   func MaxUint64(a, b uint64) uint64 { if a > b { return a }; return b }
   ```

5. Write unit tests covering concurrent access (`-race` flag) for `ConcurrentReadMap` and `UnboundedQueue`.

**Pitfalls**
- `UnboundedQueue.Dequeue()` must call `q.cond.Wait()` inside a `for` loop (not `if`) to handle spurious wakeups.
- `FullPath.Dir()` on `"/"` must return `"/"`, not `"."`.

**Verification**
```bash
go test ./goblob/util/... -race -count=1
```

**Expected Output**
- `goblob/util/concurrent_read_map.go`
- `goblob/util/unbounded_queue.go`
- `goblob/util/full_path.go`
- `goblob/util/util.go`
- `goblob/util/util_test.go`

---

### Task: Implement Graceful Shutdown Package

**Goal**
Build `goblob/grace/` providing signal-based shutdown hook execution. Every server registers shutdown hooks here; they run in reverse order on SIGINT or SIGTERM.

**Implementation Steps**

1. Create `goblob/grace/grace.go` with this exact behavior:
   ```go
   var hooks []func()
   var mu   sync.Mutex

   // OnInterrupt registers fn to be called on SIGINT or SIGTERM.
   // Hooks are called in LIFO order (last registered, first called).
   func OnInterrupt(fn func()) {
       mu.Lock()
       hooks = append(hooks, fn)
       mu.Unlock()
   }

   // init starts the signal listener goroutine once.
   func init() {
       go func() {
           c := make(chan os.Signal, 1)
           signal.Notify(c, os.Interrupt, syscall.SIGTERM)
           <-c
           runHooks()
           os.Exit(0)
       }()
   }

   func runHooks() {
       mu.Lock()
       h := make([]func(), len(hooks))
       copy(h, hooks)
       mu.Unlock()
       // Call in LIFO order
       for i := len(h) - 1; i >= 0; i-- {
           h[i]()
       }
   }

   // NotifyExit triggers shutdown programmatically (for tests and controlled shutdown).
   func NotifyExit() {
       runHooks()
   }
   ```

2. Write unit tests using `NotifyExit()`:
   ```go
   func TestHooksRunInLIFOOrder(t *testing.T) {
       var order []int
       OnInterrupt(func() { order = append(order, 1) })
       OnInterrupt(func() { order = append(order, 2) })
       OnInterrupt(func() { order = append(order, 3) })
       NotifyExit()
       // expect [3, 2, 1]
       assert.Equal(t, []int{3, 2, 1}, order)
   }
   ```

**Critical Details**
- The `init()` goroutine must run once. Since `grace` is a package, `init()` runs automatically on import.
- Hooks must complete before `os.Exit(0)` — there is no timeout by design.
- `NotifyExit()` does NOT call `os.Exit` — only the signal handler does. This allows tests to call it without killing the test process.

**Pitfalls**
- Do NOT call `os.Exit(0)` inside `NotifyExit()`. Only the signal handler goroutine calls `os.Exit`.
- Hooks registered after `NotifyExit()` is called will NOT run (the list was already snapshotted).

**Verification**
```bash
go test ./goblob/grace/... -v
```

**Expected Output**
- `goblob/grace/grace.go`
- `goblob/grace/grace_test.go`

### Phase 1 Checkpoint

Run `go test ./goblob/...` across all Phase 1 packages and confirm all unit tests pass. Run `go test ./goblob/... -race` to confirm no race conditions. The following must be true before proceeding to Phase 2:
- `goblob/core/types` tests pass including all table-driven edge cases
- `goblob/pb/...` packages compile (generated stubs present)
- `goblob/config` loads defaults correctly with no config file present
- `goblob/security` JWT round-trip and Guard whitelist tests pass
- `goblob/util` concurrent tests pass under `-race`
- `goblob/grace` LIFO hook order test passes

---

## Phase 2: Storage Engine

**Purpose**: Build the core blob storage layer including needle binary format, volume management, and persistence mechanisms. All blob I/O passes through this layer.

### Task: Implement Needle Binary Format

**Goal**
Define the binary serialization format for storing blobs as "needles" inside `.dat` volume files. Get the exact binary layout right — it is the most critical persistence concern in the entire system.

**Package path**: `goblob/storage/needle`

**Implementation Steps**

1. Create `goblob/storage/needle/needle.go` with the `Needle` struct:
   ```go
   package needle

   import (
       "goblob/core/types"
       "hash/crc32"
       "encoding/binary"
   )

   type Needle struct {
       // Identity
       Cookie   types.Cookie
       Id       types.NeedleId
       Size     types.Size    // total on-disk size including header+body+footer+padding

       // Data
       DataSize uint32
       Data     []byte

       // Optional metadata (presence controlled by Flags byte)
       Flags        uint8
       Name         []byte   // max 255 bytes
       Mime         []byte   // max 255 bytes
       Pairs        []byte   // JSON key=value, max 64KB
       LastModified uint64   // unix seconds, stored as 5-byte uint40
       Ttl          types.TTL

       // Footer
       Checksum   CRC32
       AppendAtNs uint64  // v3 only: nanoseconds since epoch
   }

   // Flag bitmask constants for the Flags byte
   const (
       FlagHasName         uint8 = 0x01
       FlagHasMime         uint8 = 0x02
       FlagHasLastModified uint8 = 0x04
       FlagHasTtl          uint8 = 0x08
       FlagHasPairs        uint8 = 0x10
       FlagIsCompressed    uint8 = 0x20
   )

   // Size limits
   const (
       MaxNeedleDataSize  = 1<<32 - 1  // 4GB
       MaxNeedleNameSize  = 255
       MaxNeedleMimeSize  = 255
       MaxNeedlePairsSize = 65535
   )

   type CRC32 uint32
   ```

2. Implement the **exact binary layout** (Version 3):
   ```
   HEADER (16 bytes, fixed):
     Cookie     uint32  4B  — random anti-brute-force
     NeedleId   uint64  8B  — from sequencer
     BodySize   uint32  4B  — total bytes in body + footer (NOT including header)

   BODY (BodySize - footerSize bytes):
     DataSize   uint32  4B  — length of Data field
     Data       []byte  DataSize bytes
     Flags      uint8   1B  — bitmask
     [NameSize] uint8   1B  — present only if FlagHasName set
     [Name]     []byte  NameSize bytes
     [MimeSize] uint8   1B  — present only if FlagHasMime set
     [Mime]     []byte  MimeSize bytes
     [PairsSize]uint16  2B  — present only if FlagHasPairs set (big-endian)
     [Pairs]    []byte  PairsSize bytes
     [LastMod]  uint40  5B  — present only if FlagHasLastModified set (big-endian)
     [Ttl]      uint16  2B  — present only if FlagHasTtl set ([Count, Unit])

   FOOTER (12 bytes):
     Checksum   uint32  4B  — CRC32 IEEE over (Cookie + NeedleId + body_bytes)
     AppendAtNs uint64  8B  — v3 only: unix nanoseconds of append time

   PADDING: 0-7 zero bytes to align total needle to 8-byte boundary
   Total = 16 + len(body) + 12 + padding; always a multiple of 8.
   ```

3. Implement CRC32 calculation:
   ```go
   type CRC32 uint32
   func NewCRC32(cookie types.Cookie, id types.NeedleId, body []byte) CRC32 {
       h := crc32.NewIEEE()
       var buf [12]byte
       binary.BigEndian.PutUint32(buf[0:4], uint32(cookie))
       binary.BigEndian.PutUint64(buf[4:12], uint64(id))
       h.Write(buf[:])
       h.Write(body)
       return CRC32(h.Sum32())
   }
   ```

4. Implement `WriteTo(w io.Writer, v types.NeedleVersion) (int64, error)` in `needle_write.go`:
   - Step 1: Build body bytes: DataSize(4B big-endian) + Data + Flags(1B) + optional fields in flag order
   - Step 2: Compute CRC32 over `cookie_bytes(4B) + id_bytes(8B) + body_bytes`
   - Step 3: Build footer: Checksum(4B big-endian) + AppendAtNs(8B big-endian, v3 only)
   - Step 4: BodySize = len(body) + len(footer)
   - Step 5: Write header: Cookie(4B) + NeedleId(8B) + BodySize(4B) — all big-endian
   - Step 6: Write body bytes
   - Step 7: Write footer bytes
   - Step 8: Write padding: `NeedleAlignPadding(16 + len(body) + len(footer))` zero bytes
   - Return total bytes written

5. Implement `ReadFrom(data []byte, offset int64, size types.Size, v types.NeedleVersion) error` in `needle_read.go`:
   - Parse header: Cookie(4B), NeedleId(8B), BodySize(4B)
   - If BodySize == 0: return nil (empty/tombstone needle)
   - Parse body bytes sequentially:
     - DataSize(4B), Data[0:DataSize]
     - Flags(1B)
     - If FlagHasName: NameSize(1B), Name[0:NameSize]
     - If FlagHasMime: MimeSize(1B), Mime[0:MimeSize]
     - If FlagHasPairs: PairsSize(2B big-endian), Pairs[0:PairsSize]
     - If FlagHasLastModified: read 5 bytes big-endian into uint64
     - If FlagHasTtl: [Count(1B), Unit(1B)]
   - Parse footer: Checksum(4B); if v3: AppendAtNs(8B)
   - Call `VerifyChecksum()` — return error on mismatch

6. Implement `VerifyChecksum() error`:
   ```go
   func (n *Needle) VerifyChecksum() error {
       // Recompute body bytes from current fields, then compute CRC32
       computed := NewCRC32(n.Cookie, n.Id, n.bodyBytes())
       if computed != n.Checksum {
           return fmt.Errorf("needle checksum mismatch: computed %d, stored %d", computed, n.Checksum)
       }
       return nil
   }
   ```

7. Implement `IsExpired(writeTimeUnixSec uint64) bool`:
   ```go
   secondsPerUnit := map[types.TTLUnit]uint64{
       'm': 60, 'h': 3600, 'd': 86400, 'w': 604800, 'M': 2592000, 'y': 31536000,
   }
   expireAt := writeTimeUnixSec + uint64(n.Ttl.Count)*secondsPerUnit[n.Ttl.Unit]
   return uint64(time.Now().Unix()) > expireAt
   ```

8. Implement `NeedleAlignPadding(n int) int`:
   ```go
   func NeedleAlignPadding(n int) int {
       rem := n % int(types.NeedleAlignmentSize)
       if rem == 0 { return 0 }
       return int(types.NeedleAlignmentSize) - rem
   }
   ```

9. Write unit tests:
   - `TestNeedleRoundTrip`: create needle with all fields set, WriteTo a buffer, ReadFrom that buffer, assert all fields equal — for v1, v2, v3
   - `TestNeedleChecksum`: after writing, corrupt one byte of data, ReadFrom should return checksum error
   - `TestNeedlePadding`: assert `(16 + bodySize + footerSize + padding) % 8 == 0` for various data sizes
   - `TestNeedleMinimal`: needle with DataSize=0, no optional fields — verify empty needle encoding
   - `TestNeedleTTLExpiry`: `IsExpired` returns false for future TTL, true for past TTL

**Critical Details**
- All multi-byte integers in the needle are **big-endian**. Use `binary.BigEndian.PutUint32`, etc.
- The `LastModified` field is a **5-byte (uint40)** big-endian integer — Go has no uint40 type; read/write it as 5 bytes manually.
- `BodySize` in the header is `len(body) + len(footer)`, NOT just the data length. The footer IS counted in BodySize.
- CRC32 uses the **IEEE** polynomial (`crc32.NewIEEE()`), not Castagnoli.
- Padding bytes must be **zero bytes** (not random).
- The `Flags` byte controls which optional fields follow the `Data` field. Fields MUST be written/read in the exact order: Name, Mime, Pairs, LastModified, Ttl.

**Pitfalls**
- Forgetting to include the footer in `BodySize` causes read failures — `ReadFrom` will try to read past the actual footer.
- Writing optional fields in the wrong order breaks deserialization.
- The 5-byte LastModified encoding: `buf[0]=(v>>32)&0xFF, buf[1]=(v>>24)&0xFF, ...` — do not truncate to 4 bytes.

**Verification**
```bash
go test ./goblob/storage/needle/... -v -run TestNeedleRoundTrip
go test ./goblob/storage/needle/... -v -run TestNeedleChecksum
go test ./goblob/storage/needle/... -race
```

**Expected Output**
- `goblob/storage/needle/needle.go`
- `goblob/storage/needle/needle_read.go`
- `goblob/storage/needle/needle_write.go`
- `goblob/storage/needle/needle_test.go`

---

### Task: Implement SuperBlock and Volume Metadata

**Goal**
Define the 8-byte header written at the start of every `.dat` volume file, encoding volume-level metadata.

**Package path**: `goblob/storage/super_block`

**Implementation Steps**

1. Create `goblob/storage/super_block/super_block.go` with this exact binary layout:
   ```
   SuperBlock binary layout (8 bytes minimum):
   Byte 0:   Version            uint8   — NeedleVersion (1, 2, or 3)
   Byte 1:   ReplicaPlacement   uint8   — encoded as DDDRRRCC
   Byte 2-3: TTL                uint16  — [Count, Unit], big-endian
   Byte 4-5: CompactionRevision uint16  — incremented each vacuum, big-endian
   Byte 6-7: ExtraSize          uint16  — bytes of protobuf SuperBlockExtra, big-endian
   [ExtraSize bytes: protobuf SuperBlockExtra (collection, version, etc.)]
   ```

2. Define the `SuperBlock` struct:
   ```go
   type SuperBlock struct {
       Version            types.NeedleVersion
       ReplicaPlacement   types.ReplicaPlacement
       Ttl                types.TTL
       CompactionRevision uint16
       Extra              *SuperBlockExtra  // nil if ExtraSize==0
   }
   type SuperBlockExtra struct {
       VolumeId   uint32
       Collection string
   }
   ```

3. Implement `SuperBlock.Bytes() []byte`:
   ```go
   buf := make([]byte, 8)
   buf[0] = byte(sb.Version)
   buf[1] = sb.ReplicaPlacement.Byte()
   ttl := sb.Ttl.Bytes()
   buf[2] = ttl[0]; buf[3] = ttl[1]
   binary.BigEndian.PutUint16(buf[4:6], sb.CompactionRevision)
   // ExtraSize = 0 for now (no protobuf extra)
   binary.BigEndian.PutUint16(buf[6:8], 0)
   return buf
   ```

4. Implement `ParseSuperBlock(data []byte) (SuperBlock, error)`:
   - Verify `len(data) >= 8`
   - Parse each field from bytes 0-7
   - If ExtraSize > 0: unmarshal protobuf from `data[8:8+ExtraSize]`

5. Implement file I/O helpers:
   ```go
   // ReadSuperBlock reads and parses the SuperBlock from the start of the .dat file.
   func ReadSuperBlock(f *os.File) (SuperBlock, error)
   // WriteSuperBlock writes the SuperBlock to the start of the .dat file.
   func WriteSuperBlock(f *os.File, sb SuperBlock) error
   ```

6. Add version compatibility check:
   ```go
   func (sb SuperBlock) IsCompatible() bool {
       return sb.Version >= types.NeedleVersionV1 && sb.Version <= types.CurrentNeedleVersion
   }
   ```

7. Write unit tests: encode then decode, assert all fields equal.

**Critical Details**
- The SuperBlock is always at **byte offset 0** in the `.dat` file. Needle data starts at offset `8 + ExtraSize` (minimum 8).
- `CompactionRevision` starts at 0 for new volumes and increments by 1 each time `CommitCompact()` is called.

**Pitfalls**
- Reading a SuperBlock from an empty file returns an error, not a zero SuperBlock. Handle `io.EOF` explicitly.

**Verification**
```bash
go test ./goblob/storage/super_block/... -v
```

**Expected Output**
- `goblob/storage/super_block/super_block.go`
- `goblob/storage/super_block/super_block_test.go`

---

### Task: Implement NeedleMap Interface and Implementations

**Goal**
Build the in-memory and LevelDB-backed needle index mapping `NeedleId → (Offset, Size)` for fast O(1) lookups.

**Package path**: `goblob/storage/needle_map`

**Implementation Steps**

1. Create `goblob/storage/needle_map/needle_map.go` with the interface:
   ```go
   // NeedleMapper is the interface for the needle index.
   type NeedleMapper interface {
       // Put records or updates the location of a needle.
       Put(key types.NeedleId, offset types.Offset, size types.Size) error
       // Get returns the NeedleValue for the given key, or (NeedleValue{}, false) if not found.
       Get(key types.NeedleId) (types.NeedleValue, bool)
       // Delete marks a needle as deleted (sets Size = TombstoneFileSize).
       Delete(key types.NeedleId) error
       // Close flushes pending writes and releases resources.
       Close() error
       // ContentSize returns total bytes of live needle data (not counting deleted).
       ContentSize() uint64
       // DeletedSize returns total bytes of deleted needle data.
       DeletedSize() uint64
       // FileCount returns number of live needles.
       FileCount() int64
       // DeletedCount returns number of deleted needles.
       DeletedCount() int64
   }
   ```

2. Implement `MemoryNeedleMap` in `memory_map.go`:
   ```go
   type MemoryNeedleMap struct {
       mu          sync.RWMutex
       m           map[types.NeedleId]types.NeedleValue
       fileCount   int64
       deletedCount int64
       contentSize  uint64
       deletedSize  uint64
   }
   // Put: acquire write lock, update map and counters, write index entry to .idx file
   // Get: acquire read lock, return from map
   // Delete: acquire write lock, set Size = TombstoneFileSize in map, update counters
   ```

3. Implement the **index file format** (`.idx`). Each entry is exactly 16 bytes:
   ```
   NeedleId  uint64  8B  big-endian
   Offset    uint32  4B  big-endian (encoded: actualOffset / 8)
   Size      uint32  4B  big-endian
   ```
   The `.idx` file is the `NeedleId → (Offset, Size)` index. It is append-only during writes, exactly like the `.dat` file.

4. Implement `LoadIndexFile(idxFile string) (NeedleMapper, error)`:
   - Read the `.idx` file in 16-byte chunks
   - For each entry: if Size == TombstoneFileSize, mark as deleted; else add to map
   - Return a fully-loaded `MemoryNeedleMap`

5. Implement `LevelDbNeedleMap` in `leveldb_map.go`:
   ```go
   type LevelDbNeedleMap struct {
       db          *leveldb.DB
       fileCount   int64
       deletedCount int64
       contentSize  uint64
       deletedSize  uint64
   }
   // Key: NeedleId as big-endian 8 bytes
   // Value: Offset(4B) + Size(4B) = 8 bytes, big-endian
   ```

6. Write unit tests and benchmarks:
   - `TestMemoryNeedleMapPutGet`: put 1000 entries, get each by key
   - `TestNeedleMapDelete`: put then delete, get returns `IsDeleted() == true`
   - `TestIndexFileLoadSave`: write to idx file, reload into new map, assert equal
   - `BenchmarkMemoryNeedleMapGet`: benchmark concurrent Gets

**Critical Details**
- The index file is NOT a database — it is a flat binary file of fixed-size 16-byte records, appended sequentially (one record per needle write or delete).
- On startup, the index file is replayed from beginning to end to rebuild the in-memory map. The LAST entry for a given NeedleId wins.
- `TombstoneFileSize = 0`. When `Size == 0`, the needle is deleted.

**Pitfalls**
- The `.idx` file encodes `Offset` as the **encoded** value (actualOffset / 8), not the raw byte offset. Do not convert it when writing — the Volume layer stores the encoded offset.
- LevelDB is NOT thread-safe for concurrent iteration + modification. Use batch writes.

**Verification**
```bash
go test ./goblob/storage/needle_map/... -race -bench=.
```

**Expected Output**
- `goblob/storage/needle_map/needle_map.go`
- `goblob/storage/needle_map/memory_map.go`
- `goblob/storage/needle_map/leveldb_map.go`
- `goblob/storage/needle_map/needle_map_test.go`

---

### Task: Implement Volume Management

**Goal**
Build the `Volume` type that manages one `.dat` + `.idx` file pair. All needle reads, writes, and deletes go through Volume methods.

**Implementation Steps**

1. Create `goblob/storage/volume.go` with the `Volume` struct:
   ```go
   type Volume struct {
       Id         types.VolumeId
       dir        string            // directory path
       Collection string
       dataFile   *os.File          // the .dat file (append-only writes)
       nm         needle_map.NeedleMapper
       SuperBlock super_block.SuperBlock
       ReadOnly   bool
       noWriteOrDelete bool         // set during vacuum freeze
       dataFileAccessLock sync.RWMutex
       lastModifiedTsNs   uint64
   }
   ```

2. Implement `NewVolume(dirname string, id types.VolumeId, collection string, replication types.ReplicaPlacement, ttl types.TTL, mapKind NeedleMapKind) (*Volume, error)`:
   - Create `.dat` file: write SuperBlock (8 bytes) to it
   - Create index: either load existing `.idx` file or create new `NeedleMapper`
   - Return initialized `*Volume`

3. Implement **needle write** `WriteNeedle(n *needle.Needle) (offset types.Offset, size uint32, isUnchanged bool, err error)`:
   ```
   1. Acquire dataFileAccessLock.Lock()
   2. Seek to end of .dat file to get current offset
   3. If currentOffset % 8 != 0: add padding to align
   4. Compute encodedOffset = ToEncoded(currentOffset)
   5. Write needle to file: n.WriteTo(dataFile, v.SuperBlock.Version)
   6. Get written size from WriteTo return value
   7. Update index: nm.Put(n.Id, encodedOffset, types.Size(writtenSize))
   8. Release lock
   9. Return (encodedOffset, writtenSize, false, nil)
   ```

4. Implement **needle read** `ReadNeedleBody(offset types.Offset, size types.Size, readNeedleData bool) (*needle.Needle, error)`:
   ```
   1. Acquire dataFileAccessLock.RLock()
   2. actualOffset = offset.ToActualOffset()
   3. Read size bytes from dataFile at actualOffset using ReadAt
   4. Parse needle from those bytes: n.ReadFrom(data, actualOffset, size, version)
   5. Release lock
   6. Return needle
   ```

5. Implement `ReadNeedleByFileId(fid types.FileId) (*needle.Needle, error)`:
   ```
   1. nv, found := nm.Get(fid.NeedleId)
   2. If not found: return nil, ErrNotFound
   3. If nv.IsDeleted(): return nil, ErrDeleted
   4. If nv.Offset == 0 && nv.Size == 0: return nil, ErrNotFound
   5. n = ReadNeedleBody(nv.Offset, nv.Size, true)
   6. Verify n.Cookie == fid.Cookie; if not: return nil, ErrCookieMismatch
   7. Return n
   ```

6. Implement **needle delete** `DeleteNeedle(fid types.FileId) error`:
   - Acquire write lock
   - Write a tombstone entry to `.dat`: a needle with the same NeedleId but DataSize=0
   - Update index: `nm.Delete(fid.NeedleId)`
   - Release lock

7. Add **volume statistics**:
   ```go
   func (v *Volume) FileCount() int64        { return v.nm.FileCount() }
   func (v *Volume) DeletedCount() int64     { return v.nm.DeletedCount() }
   func (v *Volume) ContentSize() uint64     { return v.nm.ContentSize() }
   func (v *Volume) DeletedSize() uint64     { return v.nm.DeletedSize() }
   func (v *Volume) DataFileSize() (int64, error) // stat the .dat file
   ```

8. Implement `Close() error`: close dataFile, close NeedleMapper, set to nil.

9. Write unit tests for the full volume lifecycle (create → write → read → delete → verify deleted).

**Critical Details**
- Use `dataFile.ReadAt(buf, offset)` (not `Seek + Read`) for reads — `ReadAt` is safe for concurrent callers.
- The needle index tracks the encoded offset (actualOffset / 8), not the raw byte offset.
- After writing a needle, the next needle must start at an 8-byte aligned offset. The writer handles its own padding.
- `Cookie` verification in read is a security check — an attacker who guesses a NeedleId cannot read without the correct Cookie.

**Pitfalls**
- Do not use `Seek + Read` for reads. Use `ReadAt` for concurrent safety.
- The `.dat` file must be opened with `O_RDWR | O_CREATE` (not just `O_WRONLY`) since reads and writes both occur.

**Verification**
```bash
go test ./goblob/storage/... -run TestVolumeLifecycle -v
go test ./goblob/storage/... -race
```

**Expected Output**
- `goblob/storage/volume.go`
- `goblob/storage/volume_read.go`
- `goblob/storage/volume_write.go`
- `goblob/storage/volume_test.go`

---

### Task: Implement DiskLocation Management

**Goal**
Build `DiskLocation`, which manages all volumes in a single on-disk directory.

**Implementation Steps**

1. Create `goblob/storage/disk_location.go`:
   ```go
   type DiskLocation struct {
       Directory      string
       IdxDirectory   string          // separate idx dir (optional)
       MaxVolumeCount int32
       MinFreeSpacePercent float32
       DiskType       types.DiskType
       volumes        map[types.VolumeId]*Volume
       mu             sync.RWMutex
   }
   ```

2. Implement `LoadExistingVolumes(needleMapKind NeedleMapKind)`:
   - Scan directory for files ending in `.dat`
   - For each `.dat` file, parse the VolumeId from the filename (format: `{id}.dat`)
   - Call `NewVolume(dir, id, ...)` to load each volume
   - Store in `volumes` map

3. Implement `DeleteVolumeById(id types.VolumeId) error`:
   - Remove from `volumes` map
   - Delete the `.dat` and `.idx` files from disk

4. Implement `GetVolume(id types.VolumeId) (*Volume, bool)` with RLock.

5. Implement `AddVolume(id types.VolumeId, ...) error` with Lock.

6. Implement `AvailableVolumeCount() int32`:
   - Returns `MaxVolumeCount - int32(len(volumes))`

7. Add disk free space check using `syscall.Statfs` (Linux) or `golang.org/x/sys/unix.Statfs`:
   ```go
   func (dl *DiskLocation) CheckFreeSpace() bool  // true if above MinFreeSpacePercent
   ```

8. Write unit tests using `t.TempDir()` as the directory.

**Critical Details**
- Volume filenames follow the pattern `{volumeId}.dat` and `{volumeId}.idx`. Parse `VolumeId` from filename with `strconv.ParseUint(name[:len(name)-4], 10, 32)`.

**Verification**
```bash
go test ./goblob/storage/... -run TestDiskLocation -v
```

**Expected Output**
- `goblob/storage/disk_location.go`
- Tests in `goblob/storage/disk_location_test.go`

---

### Task: Implement Store (Top-Level Storage Manager)

**Goal**
Build `Store`, the top-level storage manager for a volume server. It owns multiple `DiskLocation` instances and dispatches all I/O.

**Implementation Steps**

1. Create `goblob/storage/store.go`:
   ```go
   type Store struct {
       Ip          string
       Port        int
       PublicUrl   string
       Locations   []*DiskLocation
       dataCenter  string
       rack        string
       needleMapKind NeedleMapKind
       // Concurrency limits for upload/download
       concurrentUploadLimitMB   int64
       concurrentDownloadLimitMB int64
       uploadCond   *sync.Cond
       downloadCond *sync.Cond
   }
   ```

2. Implement `NewStore(ip string, port int, dirs []DiskDirectoryConfig, mapKind NeedleMapKind) *Store`:
   - Create one `DiskLocation` per directory config
   - Call `dl.LoadExistingVolumes(mapKind)` for each
   - Initialize concurrency limit sync.Cond variables

3. Implement volume lookup:
   ```go
   func (s *Store) GetVolume(id types.VolumeId) (*Volume, bool) {
       for _, dl := range s.Locations {
           if v, ok := dl.GetVolume(id); ok { return v, true }
       }
       return nil, false
   }
   ```

4. Implement `WriteVolumeNeedle(vid types.VolumeId, n *needle.Needle) (offset types.Offset, size uint32, err error)`:
   - Look up volume → call `v.WriteNeedle(n)`

5. Implement `ReadVolumeNeedle(vid types.VolumeId, fid types.FileId) (*needle.Needle, error)`:
   - Look up volume → call `v.ReadNeedleByFileId(fid)`

6. Implement `DeleteVolumeNeedle(vid types.VolumeId, fid types.FileId) error`

7. Implement `AllocateVolume(vid types.VolumeId, preallocate int64, ...) error`:
   - Find the DiskLocation with the most free volume capacity
   - Create new volume there

8. Implement concurrent upload/download limiting:
   ```go
   // Before upload: check if inflight bytes < limit; if over limit, wait on uploadCond
   // After upload: decrement inflight bytes, broadcast on uploadCond
   ```

9. Write unit tests for the full store lifecycle.

**Verification**
```bash
go test ./goblob/storage/... -run TestStore -race
```

**Expected Output**
- `goblob/storage/store.go`
- `goblob/storage/store_test.go`

---

### Task: Implement Volume Compaction (Local)

**Goal**
Build the per-volume garbage collection mechanism. Volumes accumulate tombstone (deleted) needles over time; compaction rewrites the volume with only live needles and atomically swaps the files.

**Implementation Steps**

1. Create `goblob/storage/compaction.go` with these exact behaviors:

2. Implement garbage ratio calculation:
   ```go
   func (v *Volume) garbageRatio() float64 {
       if v.ContentSize() == 0 { return 0 }
       return float64(v.DeletedSize()) / float64(v.ContentSize()+v.DeletedSize())
   }
   ```

3. Implement `Volume.Compact(preallocate int64, compactThreshold float64, bytesPerSecond int64) error`:
   ```
   1. If garbageRatio() < compactThreshold: return nil (skip)
   2. Create temp data file: datFile.cpd in same directory
   3. Create temp index file: datFile.cpx in same directory
   4. Write new SuperBlock (with CompactionRevision+1) to .cpd
   5. Iterate all NeedleValues in the index:
      a. Skip if nv.IsDeleted() (tombstone)
      b. Skip if needle.IsExpired(appendAtNs)
      c. Read needle from .dat using nv.Offset, nv.Size
      d. Write needle to .cpd
      e. Write index entry to .cpx
      f. If bytesPerSecond > 0: throttle with time.Sleep
   6. Sync and close .cpd and .cpx temp files
   ```

4. Implement `Volume.CommitCompact() error`:
   ```
   1. Acquire dataFileAccessLock.Lock() (freeze all reads and writes)
   2. Set v.noWriteOrDelete = true
   3. Close current dataFile
   4. Rename .cpd → .dat atomically (os.Rename is atomic on same filesystem)
   5. Rename .cpx → .idx atomically
   6. Reload index from new .idx file
   7. Open new .dat file
   8. Set v.noWriteOrDelete = false
   9. Increment v.SuperBlock.CompactionRevision
   10. Release lock
   ```

5. Add `noWriteOrDelete` guard in `WriteNeedle` and `DeleteNeedle`:
   ```go
   if v.noWriteOrDelete { return ErrVolumeReadOnly }
   ```

6. Implement I/O throttling in Compact:
   ```go
   if bytesPerSecond > 0 {
       sleepNs := int64(float64(writtenBytes) / float64(bytesPerSecond) * 1e9)
       time.Sleep(time.Duration(sleepNs - elapsed))
   }
   ```

7. Write unit tests:
   - Write 100 needles, delete 40, confirm garbage ratio ≈ 0.40
   - Call Compact, confirm .cpd and .cpx files are created
   - Call CommitCompact, confirm .dat contains only 60 live needles
   - Confirm deleted needle IDs return ErrNotFound after compact

**Critical Details**
- `os.Rename` is atomic on the same filesystem (POSIX guarantee). Never use copy-then-delete.
- The `noWriteOrDelete` flag must be set while holding the lock. Reads are allowed during compaction (only blocked during the brief CommitCompact swap).
- `.cpd` and `.cpx` extensions are the standard temp file names for compaction data and index.

**Pitfalls**
- If the process crashes during Compact (before CommitCompact), the `.cpd`/`.cpx` files are orphaned. On restart, detect and delete orphaned temp files.
- Do NOT rename `.cpd` → `.dat` without first closing the `.dat` file descriptor — on Windows this fails; on Linux it may leave stale file descriptors.

**Verification**
```bash
go test ./goblob/storage/... -run TestCompaction -v
# Verify: before compact: fileCount=100, deletedCount=40
# After CommitCompact: fileCount=60, deletedCount=0, data file is smaller
```

**Expected Output**
- `goblob/storage/compaction.go`
- `goblob/storage/compaction_test.go`

---

### Task: Implement Master-Coordinated Vacuum

**Goal**
Build the master-side vacuum orchestration and the corresponding volume server HTTP endpoints. The 3-step vacuum protocol ensures all replicas are compacted before committing.

**Implementation Steps**

1. Add these 3 HTTP endpoints to the volume server (Phase 4 adds the server, but stub these handlers in `goblob/storage/`):

   **Step 1** — `GET /vol/vacuum/check?garbageThreshold=X`:
   - Scan all volumes, compute `garbageRatio()` for each
   - Return JSON array of VolumeIds where ratio > threshold

   **Step 2** — `GET /vol/vacuum/needle?vid=N`:
   - Find volume by VolumeId
   - Set `noWriteOrDelete = true`
   - Run `v.Compact()` in a goroutine
   - Block until `.cpd` file is ready (or use a status endpoint)
   - Return 200 OK

   **Step 3** — `GET /vol/vacuum/commit?vid=N`:
   - Call `v.CommitCompact()`
   - Clear `noWriteOrDelete`
   - Return 200 OK

2. Create `goblob/topology/topology_vacuum.go` (skeleton — full implementation in Phase 4):
   ```go
   // Vacuum coordinates compaction across all DataNodes.
   // For each DataNode: call /vol/vacuum/check, then for each candidate volume:
   //   for each replica: call /vol/vacuum/needle, then /vol/vacuum/commit
   func (t *Topology) Vacuum(garbageThreshold float64, preallocate int64) int
   ```

3. Add `POST /vol/vacuum` on the master server (Phase 4 adds the server) that calls `Topology.Vacuum()`.

4. Write an integration test:
   - Start in-process volume server
   - Write 100 needles, delete 40
   - Call `/vol/vacuum/check` — confirm volume ID appears
   - Call `/vol/vacuum/needle` — confirm .cpd created
   - Call `/vol/vacuum/commit` — confirm volume shrinks

**Critical Details**
- The 3-step protocol (check → compact → commit) is ordered: you cannot commit without first compacting.
- Between steps 2 and 3, writes to that volume return `ErrVolumeReadOnly`. This is expected and intentional.

**Verification**
```bash
go test ./goblob/storage/... -run TestVacuumProtocol -v
```

**Expected Output**
- Vacuum HTTP handler stubs in `goblob/storage/`
- `goblob/topology/topology_vacuum.go` (skeleton)
- Integration test in `goblob/storage/vacuum_test.go`

### Phase 2 Checkpoint

Run `go test ./goblob/storage/...` — all unit tests must pass. Then manually verify the vacuum cycle:
1. Create a volume, write 100 needles, delete 40
2. Confirm `garbageRatio() > 0.3`
3. Call `Compact()` — confirm `.cpd` and `.cpx` files exist in the volume directory
4. Call `CommitCompact()` — confirm:
   - `.dat` file is smaller than before
   - The 40 deleted needle IDs now return `ErrNotFound`
   - `FileCount() == 60`, `DeletedCount() == 0`

Run `go test ./goblob/storage/... -race` before proceeding to Phase 3.

---

## Phase 3: Cluster Coordination

**Purpose**: Implement consensus, topology management, and distributed coordination primitives for cluster orchestration.

### Task: Implement Raft Consensus Layer

**Goal**
Integrate Raft consensus for master server high availability and leader election. The master cluster requires an odd number of nodes (1, 3, or 5) to form quorum. This package provides the `RaftServer` abstraction over the `hashicorp/raft` library.

**Package path**: `goblob/raft`

**Implementation Steps**

1. Create `goblob/raft/raft_server.go` with the `RaftServer` interface that the entire master server depends on:
   ```go
   package raft

   import (
       "time"
       hraft "github.com/hashicorp/raft"
   )

   // RaftServer is the interface the rest of the system uses to interact with Raft.
   type RaftServer interface {
       IsLeader() bool
       // LeaderAddress returns the gRPC address of the current leader, or "" if unknown.
       LeaderAddress() string
       // Apply submits a command to the Raft log and waits for consensus.
       // Returns error if not leader or consensus times out.
       Apply(cmd RaftCommand, timeout time.Duration) error
       // Barrier waits until all log entries are applied to the FSM.
       Barrier(timeout time.Duration) error
       AddPeer(addr string) error
       RemovePeer(addr string) error
       Stats() map[string]string
       Shutdown() error
   }

   // RaftCommand is a command that can be submitted to the Raft log.
   type RaftCommand interface {
       Type() string
       Encode() ([]byte, error)
   }
   ```

2. Define the **three Raft log commands** used by GoBlob:
   ```go
   // MaxFileIdCommand advances the sequencer's max file ID.
   // Applied whenever the master issues a batch of file IDs.
   type MaxFileIdCommand struct { MaxFileId uint64 `json:"max_file_id"` }
   func (c MaxFileIdCommand) Type() string          { return "max_file_id" }
   func (c MaxFileIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

   // MaxVolumeIdCommand records the highest VolumeId ever allocated.
   type MaxVolumeIdCommand struct { MaxVolumeId uint32 `json:"max_volume_id"` }
   func (c MaxVolumeIdCommand) Type() string          { return "max_volume_id" }
   func (c MaxVolumeIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

   // TopologyIdCommand sets the cluster's unique identity (UUID).
   type TopologyIdCommand struct { TopologyId string `json:"topology_id"` }
   func (c TopologyIdCommand) Type() string          { return "topology_id" }
   func (c TopologyIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

   // LogEntry wraps a command with type info for the FSM.
   type LogEntry struct {
       Type    string          `json:"type"`
       Payload json.RawMessage `json:"payload"`
   }
   ```

3. Create `goblob/raft/fsm.go` implementing `hraft.FSM`:
   ```go
   // MasterFSM is the Raft finite state machine for the GoBlob master.
   type MasterFSM struct {
       mu          sync.Mutex
       maxFileId   uint64
       maxVolumeId uint32
       topologyId  string
       // Callbacks invoked after each Apply (on leader and followers alike)
       onMaxFileIdUpdate   func(uint64)
       onMaxVolumeIdUpdate func(uint32)
       onTopologyIdSet     func(string)
   }

   func NewMasterFSM(
       onMaxFileIdUpdate func(uint64),
       onMaxVolumeIdUpdate func(uint32),
       onTopologyIdSet func(string),
   ) *MasterFSM

   // Apply is called by the Raft library for EVERY committed log entry.
   func (f *MasterFSM) Apply(log *hraft.Log) interface{} {
       var entry LogEntry
       json.Unmarshal(log.Data, &entry)
       switch entry.Type {
       case "max_file_id":
           var cmd MaxFileIdCommand; json.Unmarshal(entry.Payload, &cmd)
           f.mu.Lock()
           if cmd.MaxFileId > f.maxFileId { f.maxFileId = cmd.MaxFileId; f.onMaxFileIdUpdate(f.maxFileId) }
           f.mu.Unlock()
       case "max_volume_id":
           var cmd MaxVolumeIdCommand; json.Unmarshal(entry.Payload, &cmd)
           f.mu.Lock()
           if cmd.MaxVolumeId > f.maxVolumeId { f.maxVolumeId = cmd.MaxVolumeId; f.onMaxVolumeIdUpdate(f.maxVolumeId) }
           f.mu.Unlock()
       case "topology_id":
           var cmd TopologyIdCommand; json.Unmarshal(entry.Payload, &cmd)
           f.mu.Lock(); f.topologyId = cmd.TopologyId; f.mu.Unlock()
           f.onTopologyIdSet(cmd.TopologyId)
       }
       return nil
   }

   // Snapshot creates a point-in-time snapshot of FSM state.
   func (f *MasterFSM) Snapshot() (hraft.FSMSnapshot, error) {
       f.mu.Lock()
       state := struct{ MaxFileId uint64; MaxVolumeId uint32; TopologyId string }{
           f.maxFileId, f.maxVolumeId, f.topologyId,
       }
       f.mu.Unlock()
       data, _ := json.Marshal(state)
       return &fsmSnapshot{data: data}, nil
   }

   // Restore applies a snapshot, replacing all current FSM state.
   func (f *MasterFSM) Restore(snapshot io.ReadCloser) error {
       data, _ := io.ReadAll(snapshot)
       var state struct{ MaxFileId uint64; MaxVolumeId uint32; TopologyId string }
       json.Unmarshal(data, &state)
       f.mu.Lock()
       f.maxFileId = state.MaxFileId; f.maxVolumeId = state.MaxVolumeId; f.topologyId = state.TopologyId
       f.mu.Unlock()
       f.onMaxFileIdUpdate(state.MaxFileId)
       f.onMaxVolumeIdUpdate(state.MaxVolumeId)
       if state.TopologyId != "" { f.onTopologyIdSet(state.TopologyId) }
       return nil
   }
   ```

4. Define `RaftConfig` and implement `NewRaftServer`:
   ```go
   type RaftConfig struct {
       NodeId            string         // "ip:port" of this node
       BindAddr          string         // TCP bind address for Raft transport
       MetaDir           string         // directory for log, stable store, snapshots
       Peers             []string       // all master addresses (including self)
       HeartbeatTimeout  time.Duration  // default: 1s
       ElectionTimeout   time.Duration  // default: 1s
       CommitTimeout     time.Duration  // default: 50ms
       MaxAppendEntries  int            // default: 64
       SnapshotInterval  time.Duration  // default: 120s
       SnapshotThreshold uint64         // default: 8192
       SingleMode        bool           // skip quorum; for -peers=none
   }

   func NewRaftServer(cfg RaftConfig, fsm hraft.FSM, onLeaderChange func(bool)) (RaftServer, error) {
       // 1. Open BoltDB log store: cfg.MetaDir + "/raft-log.bolt"
       // 2. Open BoltDB stable store: cfg.MetaDir + "/raft-stable.bolt"
       // 3. Create FileSnapshotStore in cfg.MetaDir + "/snapshots", retain 3 snaps
       // 4. Create TCP transport: hraft.NewTCPTransport(cfg.BindAddr, ...)
       // 5. Create hraft.Config with defaults + overrides from cfg
       // 6. Bootstrap if no existing state:
       //    - SingleMode: bootstrap with just this node as voter
       //    - Multi-node: bootstrap with all cfg.Peers as voters
       // 7. Create hraft.NewRaft(raftCfg, fsm, log, stable, snap, transport)
       // 8. Start goroutine: for isLeader := range r.LeaderCh() { onLeaderChange(isLeader) }
       // 9. Return &raftServerImpl{r: r}
   }
   ```

5. Implement **follower redirect** helper:
   ```go
   // If not leader, redirect HTTP requests to the leader.
   func RedirectToLeader(rs RaftServer, w http.ResponseWriter, r *http.Request) bool {
       if rs.IsLeader() { return false }
       leaderAddr := rs.LeaderAddress()
       if leaderAddr == "" {
           http.Error(w, "no leader elected", http.StatusServiceUnavailable)
           return true
       }
       http.Redirect(w, r, "http://"+leaderAddr+r.RequestURI, http.StatusTemporaryRedirect)
       return true
   }
   ```

6. Implement **leader change handler** logic (to be called from `onLeaderChange` in the master server):
   ```
   On becoming leader:
     1. Call raftServer.Barrier(10s) — ensure all log entries are applied
     2. If topologyId == "": apply TopologyIdCommand{uuid.New()} to set cluster identity
     3. Start ProcessGrowRequest() goroutine
     4. Start startAdminScripts() goroutine

   On losing leadership:
     1. Stop ProcessGrowRequest() goroutine (via context cancel)
     2. Stop startAdminScripts() goroutine
     3. Log: "lost leadership, stopping volume growth and maintenance"
   ```

7. Write tests:
   - `TestSingleModeStartup`: start with `SingleMode=true`, assert immediately becomes leader
   - `TestFSMApplyMaxFileId`: apply command, assert callback invoked with correct value
   - `TestFSMSnapshotRestore`: snapshot FSM, restore to new FSM, assert state equal
   - `TestFollowerRedirect`: call `RedirectToLeader` when not leader, assert 307 redirect

**Critical Details**
- The `MetaDir` directory structure created by Raft:
  ```
  $meta_dir/
    raft-log.bolt      # BoltDB: Raft log entries
    raft-stable.bolt   # BoltDB: current term, voted for
    snapshots/         # FSM snapshots
  ```
- `Barrier()` must be called after winning leadership to ensure all previous log entries are applied before serving requests.
- The `onLeaderChange` callback is invoked from Raft's internal goroutine — it must NOT block.
- `SingleMode = true` (set when `-peers=none`) bootstraps a single-node cluster that immediately becomes leader without needing quorum.

**Pitfalls**
- Calling `hraft.BootstrapCluster` on a node that already has existing state causes an error. Check `hasExistingState(logStore, stableStore, snapshotStore)` first.
- Do NOT use in-memory transport for production code — only for integration tests.
- `Apply()` returns an error if called on a non-leader. The master server must always check `IsLeader()` before calling `Apply()`.

**Verification**
```bash
go test ./goblob/raft/... -v -run TestSingleMode
go test ./goblob/raft/... -v -run TestFSM
go test ./goblob/raft/... -race
```

**Expected Output**
- `goblob/raft/raft_server.go`
- `goblob/raft/fsm.go`
- `goblob/raft/raft_test.go`

---

### Task: Implement File ID Sequencer

**Goal**
Build globally unique, monotonically increasing `NeedleId` generation. The sequencer runs inside the master and must survive restarts without ever reusing an ID.

**Package path**: `goblob/sequence`

**Implementation Steps**

1. Create `goblob/sequence/sequencer.go` with the `Sequencer` interface:
   ```go
   // Sequencer generates globally unique, monotonically increasing NeedleIds.
   type Sequencer interface {
       // NextFileId returns the start of a batch of count unique IDs.
       // Caller receives IDs [start, start+count-1] inclusive.
       NextFileId(count uint64) uint64
       // SetMax advances the counter to at least maxId (used during recovery).
       SetMax(maxId uint64)
       // GetMax returns the current maximum issued ID.
       GetMax() uint64
   }
   ```

2. Implement `FileSequencer` in `goblob/sequence/file_sequencer.go`:
   ```go
   // FileSequencer is the default implementation for single-master mode.
   // It persists state to a file to survive restarts.
   type FileSequencer struct {
       dir     string    // directory containing the max_needle_id file
       step    uint64    // pre-allocation batch size (default: 10000)
       current uint64    // last issued ID (in memory)
       saved   uint64    // last value written to disk (= current + step)
       mu      sync.Mutex
   }

   // NextFileId algorithm:
   // 1. Lock
   // 2. if current >= saved: write (current+step) to disk, set saved = current+step
   // 3. start = current + 1
   // 4. current += count
   // 5. Unlock
   // 6. return start

   // File format: a single ASCII decimal number followed by newline.
   // File path: filepath.Join(dir, "max_needle_id")
   // On startup: read file, set current = saved = fileValue
   ```

   The step pre-allocation means: if the server crashes, at most `step` IDs are wasted. This is acceptable because IDs are just monotonically increasing integers — gaps are fine.

3. Implement `RaftSequencer` in `goblob/sequence/raft_sequencer.go`:
   ```go
   // RaftSequencer wraps FileSequencer but submits increments via Raft log.
   // This ensures all master replicas have the same view of issued IDs.
   type RaftSequencer struct {
       wrapped *FileSequencer
       raft    RaftApplier  // raft.RaftServer
   }

   // RaftApplier is the subset of RaftServer used by the sequencer.
   type RaftApplier interface {
       Apply(cmd raft.RaftCommand, timeout time.Duration) error
   }

   // NextFileId: submit MaxFileIdCommand{current+count} to Raft log, then call wrapped.NextFileId
   func (rs *RaftSequencer) NextFileId(count uint64) uint64 {
       start := rs.wrapped.current + 1
       newMax := start + count - 1
       rs.raft.Apply(raft.MaxFileIdCommand{MaxFileId: newMax}, 5*time.Second)
       return rs.wrapped.NextFileId(count)
   }
   ```

4. Implement `SnowflakeSequencer` in `goblob/sequence/snowflake_sequencer.go`:
   ```go
   // SnowflakeSequencer generates IDs without coordination using:
   // 41 bits timestamp (ms since epoch) | 10 bits machineId | 12 bits sequence
   // IDs are not strictly sequential across machines.
   type SnowflakeSequencer struct {
       machineId    uint64
       lastMs       int64
       sequenceBits int64
       mu           sync.Mutex
   }
   ```

5. Write unit tests:
   - `TestFileSequencerMonotonic`: call `NextFileId(1)` 10000 times, assert each result > previous
   - `TestFileSequencerBatch`: `NextFileId(100)` returns correct batch start; no overlap with next call
   - `TestFileSequencerRestart`: write file, create new sequencer from same dir, assert GetMax == saved value
   - `TestFileSequencerSetMax`: call `SetMax(99999)`, assert `NextFileId(1) > 99999`
   - `BenchmarkFileSequencer`: measure IDs/second under concurrent load

**Critical Details**
- The default step size is **10000**. This means after a crash and restart, the sequencer jumps forward by up to 10000. This wastes IDs but prevents reuse.
- `SetMax` is called during master startup when heartbeats from volume servers report higher NeedleIds than the sequencer's current state — this handles recovery after the sequencer's persistence file is lost.
- The `RaftSequencer` wraps `FileSequencer` — the Raft log entry records the high-water mark, but local state still uses the file-based fast path.

**Pitfalls**
- `NextFileId(count)` must be atomic (under mutex). Two concurrent callers must receive non-overlapping ranges.
- The file write in `FileSequencer` must be atomic (write to temp file then rename) or use `O_SYNC` to avoid partial writes on crash.

**Verification**
```bash
go test ./goblob/sequence/... -race -count=1
go test ./goblob/sequence/... -bench=. -benchtime=5s
```

**Expected Output**
- `goblob/sequence/sequencer.go`
- `goblob/sequence/file_sequencer.go`
- `goblob/sequence/raft_sequencer.go`
- `goblob/sequence/snowflake_sequencer.go`
- `goblob/sequence/sequencer_test.go`

---

### Task: Implement Topology Manager

**Goal**
Build the cluster topology tree: `Topology → DataCenter → Rack → DataNode → DiskInfo → [Volumes]`. The topology tracks which volumes live on which servers and selects volume servers for reads and writes.

**Package path**: `goblob/topology`

**Implementation Steps**

1. Create `goblob/topology/node.go` with the `Node` interface:
   ```go
   // Node is the common interface for all topology levels.
   type Node interface {
       Id() string
       // FreeSpace returns the number of volumes that can still be created.
       FreeSpace() int64
       // ReserveOneVolume attempts to reserve capacity for one new volume.
       // Returns the DataNode that was reserved, or error if no capacity.
       ReserveOneVolume(r *VolumeGrowRequest) (*DataNode, error)
       Children() []Node
       Parent() Node
       SetParent(Node)
       // IsDataNode returns true if this node is a DataNode (leaf).
       IsDataNode() bool
   }
   ```

2. Create `goblob/topology/topology.go` with the `Topology` root:
   ```go
   type Topology struct {
       id          string         // cluster identity UUID (set via Raft)
       NodeImpl                   // embedded base node
       collectionMap map[string]*Collection  // key: collection name
       volumeSizeLimit uint64     // max bytes per volume (from master config)
       mu          sync.RWMutex
       // Channels for volume server notification
       chanFullVolumes     chan types.VolumeId
       chanCrowdedVolumes  chan types.VolumeId
   }

   // LookupVolume returns all DataNode locations for a given VolumeId.
   func (t *Topology) LookupVolume(vid types.VolumeId) []*VolumeLocation

   // GetOrCreateDataCenter finds or creates a DataCenter by name.
   func (t *Topology) GetOrCreateDataCenter(dcName string) *DataCenter

   // ProcessJoinMessage handles a heartbeat from a volume server:
   // - Creates DataCenter/Rack/DataNode if not existing
   // - Updates the DataNode's volume list
   // - Notifies VolumeLayouts of new/deleted volumes
   func (t *Topology) ProcessJoinMessage(joinMessage *master_pb.Heartbeat) (newVolumes []*master_pb.VolumeShortInformationMessage, err error)
   ```

3. Create `goblob/topology/data_center.go`:
   ```go
   type DataCenter struct {
       NodeImpl
       racks map[string]*Rack
   }
   func (dc *DataCenter) GetOrCreateRack(rackName string) *Rack
   ```

4. Create `goblob/topology/rack.go`:
   ```go
   type Rack struct {
       NodeImpl
       dataNodes map[string]*DataNode
   }
   func (r *Rack) GetOrCreateDataNode(addr string, ip string, port int, publicUrl string, maxVolumeCount int64) *DataNode
   ```

5. Create `goblob/topology/data_node.go`:
   ```go
   type DataNode struct {
       NodeImpl
       Ip          string
       Port        int
       PublicUrl   string
       DataCenter  string
       Rack        string
       lastSeen    time.Time
       diskInfos   map[types.DiskType]*DiskInfo
       mu          sync.RWMutex
   }

   type DiskInfo struct {
       DiskType       types.DiskType
       MaxVolumeCount int64
       volumes        map[types.VolumeId]*master_pb.VolumeInformationMessage
   }

   // UpdateVolumes processes a heartbeat's volume list, returns added/deleted volume IDs.
   func (dn *DataNode) UpdateVolumes(msgs []*master_pb.VolumeInformationMessage) (newVids, deletedVids []types.VolumeId)

   // GetAddress returns "ip:port".
   func (dn *DataNode) GetAddress() types.ServerAddress

   // IsAlive returns true if lastSeen within the last 30 seconds.
   func (dn *DataNode) IsAlive() bool
   ```

6. Create `goblob/topology/volume_layout.go`:
   ```go
   // VolumeLayout tracks writable volumes for a specific (collection, replication, TTL, diskType) tuple.
   type VolumeLayout struct {
       replication  types.ReplicaPlacement
       ttl          types.TTL
       diskType     types.DiskType
       writableVolumes   util.ConcurrentReadMap[types.VolumeId, []*VolumeLocation]
       readonlyVolumes   util.ConcurrentReadMap[types.VolumeId, []*VolumeLocation]
       crowdedVolumes    util.ConcurrentReadMap[types.VolumeId, []*VolumeLocation]
       mu           sync.RWMutex
   }

   type VolumeLocation struct {
       Url       string
       PublicUrl string
       DataCenter string
   }

   // PickForWrite selects a writable volume for a new upload.
   // Returns a VolumeId and the list of replica locations.
   func (vl *VolumeLayout) PickForWrite(option *VolumeGrowOption) (types.VolumeId, []*VolumeLocation, error)

   // SetVolumeWritable moves a volume from readOnly to writable.
   func (vl *VolumeLayout) SetVolumeWritable(vid types.VolumeId)

   // SetVolumeReadOnly moves a volume from writable to readOnly.
   func (vl *VolumeLayout) SetVolumeReadOnly(vid types.VolumeId)

   // HasWritableVolume returns true if there is at least one writable volume.
   func (vl *VolumeLayout) HasWritableVolume() bool
   ```

7. Implement `ReplicaPlacement`-aware node selection in `volume_growth.go` (next task references this):
   - When selecting DataNodes for a new volume, the topology must satisfy:
     - `DifferentDataCenterCount` replicas in different DCs
     - `DifferentRackCount` replicas in same DC, different racks
     - `SameRackCount` replicas in same rack, different servers

8. Write unit tests:
   - `TestTopologyProcessJoinMessage`: send a simulated heartbeat, assert DataNode registered, volumes tracked
   - `TestVolumeLayoutPickForWrite`: add 3 writable volumes, pick one, assert it's returned
   - `TestTopologyLookupVolume`: register volume on DataNode, lookup by VolumeId, assert location returned
   - `TestReplicaPlacementSelection`: with ReplicaPlacement "010" (1 copy different rack), assert selected nodes are on different racks

**Critical Details**
- The topology tree is rebuilt from heartbeats after every restart. It is NOT persisted to disk directly (only the max VolumeId is persisted via Raft).
- `ProcessJoinMessage` is called for every heartbeat from every volume server. It must be fast and idempotent.
- `VolumeLayout.PickForWrite` uses a round-robin or random selection among writable volumes.

**Pitfalls**
- Volume servers send their full volume list on the first heartbeat and only deltas (new_vids, deleted_vids) on subsequent heartbeats. Handle both cases in `UpdateVolumes`.
- A DataNode is considered dead if `lastSeen` is older than 30 seconds. Remove dead nodes from writable volume selections.

**Verification**
```bash
go test ./goblob/topology/... -v -run TestTopology
go test ./goblob/topology/... -race
```

**Expected Output**
- `goblob/topology/topology.go`
- `goblob/topology/node.go`
- `goblob/topology/data_center.go`
- `goblob/topology/rack.go`
- `goblob/topology/data_node.go`
- `goblob/topology/volume_layout.go`
- `goblob/topology/topology_test.go`

---

### Task: Implement Volume Growth Strategy

**Goal**
Build automated volume creation when the cluster runs low on writable capacity. Implements a reservation pattern to prevent double-allocation under concurrent growth requests.

**Implementation Steps**

1. Create `goblob/topology/volume_growth.go`:
   ```go
   type VolumeGrowOption struct {
       Collection       string
       ReplicaPlacement types.ReplicaPlacement
       Ttl              types.TTL
       DiskType         types.DiskType
       Preallocate      int64
   }

   type VolumeGrowRequest struct {
       Option VolumeGrowOption
       Count  int  // number of volumes to create
   }

   type VolumeGrowth struct {
       cfg config.VolumeGrowthConfig
   }
   ```

2. Implement `AutomaticGrowByType(option *VolumeGrowOption, grpcOptions []grpc.DialOption, topo *Topology) (int, error)`:
   ```
   1. Determine count from config:
      - ReplicaPlacement "000" (no replication): copy_1 = 7
      - ReplicaPlacement with 1 extra copy: copy_2 = 6
      - ReplicaPlacement with 2 extra copies: copy_3 = 3
      - Other: copy_other = 1
   2. Call findEmptySlots(option, topo, count) to get candidate DataNode groups
      Each group has TotalCopies() DataNodes satisfying the ReplicaPlacement
   3. For each group:
      a. For each DataNode in group: call node.ReserveOneVolume(option)
         This atomically reserves capacity to prevent double-allocation
      b. Select a new VolumeId: topo.NextVolumeId()
      c. For each DataNode: call AllocateVolume gRPC with the selected VolumeId
      d. On gRPC success: release reservation, register volume in topology
      e. On gRPC failure: release reservation, return error
   4. Return number of volumes successfully created
   ```

3. Implement `CapacityReservation` on `NodeImpl`:
   ```go
   type CapacityReservation struct {
       DiskType types.DiskType
       Count    int
       Id       int64  // unique reservation ID (atomic counter)
   }

   // AddReservation temporarily reduces FreeSpace by count.
   func (n *NodeImpl) AddReservation(dt types.DiskType, count int) int64

   // RemoveReservation releases a reservation, restoring FreeSpace.
   func (n *NodeImpl) RemoveReservation(id int64)

   // FreeSpace must subtract the sum of all active reservations.
   func (n *NodeImpl) FreeSpace() int64
   ```

4. Implement capacity threshold monitoring. In `VolumeLayout`, after each volume assignment:
   ```go
   // If writableCount < threshold * totalCount, trigger growth
   if float64(vl.writableVolumeCount()) < vl.growThreshold * float64(vl.totalVolumeCount()) {
       growthChan <- &VolumeGrowRequest{Option: ..., Count: 1}
   }
   ```

5. Implement `ProcessGrowRequest(ctx context.Context, growthChan <-chan *VolumeGrowRequest)` goroutine:
   ```go
   // On master server (Phase 4):
   for {
       select {
       case req := <-growthChan:
           // Deduplicate: skip if same option is already in-flight
           AutomaticGrowByType(req.Option, ...)
       case <-ctx.Done():
           return
       }
   }
   ```

6. Write unit tests:
   - `TestGrowthThresholdTrigger`: set 3 writable volumes out of 5, confirm growth triggered
   - `TestCapacityReservationPreventsDoubleAlloc`: concurrent growth requests, assert each volume allocated to distinct node
   - `TestGrowthWithInsufficientNodes`: topology with 1 DataNode but replication needs 2, assert error returned
   - `TestReservationRollbackOnFailure`: AllocateVolume gRPC returns error, assert reservation released, FreeSpace restored

**Critical Details**
- **Reservation pattern is mandatory**: without it, two concurrent growth requests can both select the same DataNode and allocate the same VolumeId.
- The `growThreshold` default is **0.9** (trigger growth when fewer than 90% of total volumes are writable).
- `findEmptySlots` must satisfy the `ReplicaPlacement` constraint: for `"010"` (1 copy in different rack), the two selected nodes must be in different racks.

**Pitfalls**
- Without the `CapacityReservation` pattern, two goroutines calling `AutomaticGrowByType` concurrently will both call `FreeSpace() > 0` on the same node, both get `true`, and try to create volumes on the same node.

**Verification**
```bash
go test ./goblob/topology/... -v -run TestGrowth -race
```

**Expected Output**
- `goblob/topology/volume_growth.go`
- Tests added to `goblob/topology/topology_test.go`

---

### Task: Implement Cluster Registry

**Goal**
Build the registry that tracks non-volume-server nodes (filers, S3 gateways) via the `KeepConnected` gRPC bidirectional stream.

**Package path**: `goblob/cluster`

**Implementation Steps**

1. Create `goblob/cluster/registry.go`:
   ```go
   // ClusterNode represents a registered non-volume node (filer, s3, etc.)
   type ClusterNode struct {
       ClientType    string  // "filer", "s3"
       ClientAddress string  // "host:port"
       Version       string
       DataCenter    string
       Rack          string
       GrpcPort      string
       lastSeen      time.Time
   }

   // ClusterRegistry tracks all connected non-volume nodes.
   type ClusterRegistry struct {
       nodes map[string]*ClusterNode  // key: clientAddress
       mu    sync.RWMutex
   }

   func NewClusterRegistry() *ClusterRegistry

   // Register adds or updates a node registration.
   func (r *ClusterRegistry) Register(req *master_pb.KeepConnectedRequest)

   // Unregister removes a node by address.
   func (r *ClusterRegistry) Unregister(addr string)

   // ListNodes returns all currently registered nodes, optionally filtered by clientType.
   func (r *ClusterRegistry) ListNodes(clientType string) []*ClusterNode

   // ExpireDeadNodes removes nodes whose lastSeen > 30s ago. Call periodically.
   func (r *ClusterRegistry) ExpireDeadNodes()
   ```

2. Implement the `KeepConnected` gRPC handler (the actual gRPC server implementation goes in Phase 4, but define the handler logic here):
   ```go
   // HandleKeepConnected processes a KeepConnected stream from a filer/S3 gateway.
   // The stream sends heartbeat requests periodically; the server sends back volume location updates.
   func HandleKeepConnected(
       stream master_pb.MasterService_KeepConnectedServer,
       registry *ClusterRegistry,
       topo *Topology,
   ) error {
       for {
           req, err := stream.Recv()
           if err != nil { registry.Unregister(req.ClientAddress); return err }
           registry.Register(req)
           // Send back current volume locations for any changes since last heartbeat
           // (implementation details in Phase 4)
       }
   }
   ```

3. Add a background goroutine to call `ExpireDeadNodes()` every 30 seconds.

4. Write unit tests:
   - `TestRegistryRegisterAndList`: register 2 filers and 1 S3, list by type
   - `TestRegistryExpireDeadNodes`: register node, advance time past 30s, expire, assert removed

**Verification**
```bash
go test ./goblob/cluster/... -v -race
```

**Expected Output**
- `goblob/cluster/registry.go`
- `goblob/cluster/registry_test.go`

### Phase 3 Checkpoint

Before proceeding to Phase 4, verify ALL of the following:

1. **Raft single-mode**: Start Raft with `SingleMode=true`. Assert `IsLeader()` returns `true` within 2 seconds. Apply all 3 command types, assert FSM callbacks invoked. Run snapshot+restore cycle, assert state equal.

2. **Sequencer**: Run `go test -race ./goblob/sequence/...`. Under concurrent load (100 goroutines each calling `NextFileId(1)`), assert all 100 returned IDs are unique and monotonically increasing. Restart sequencer from disk, assert `GetMax() >= previous max`.

3. **Topology**: Call `ProcessJoinMessage` with a simulated heartbeat reporting 5 volumes. Assert topology tree has 1 DataCenter, 1 Rack, 1 DataNode, 5 volumes. Call `LookupVolume` for each volume ID and get the correct server address.

4. **Volume growth**: With 1 writable volume out of 5 capacity, assert `HasWritableVolume()` triggers growth request. With insufficient nodes for replication, assert error returned.

5. **Full test suite**: `go test ./goblob/topology/... ./goblob/sequence/... ./goblob/raft/... ./goblob/cluster/... -race` — all pass.

---

## Phase 4: Core Servers

**Purpose**: Implement the three primary server roles (Master, Volume, Filer) that form the distributed storage system.

### Task: Implement gRPC Transport Layer

**Goal**
Build gRPC client/server helpers with connection pooling, TLS support, and service discovery.

**Critical Details**
- Package: `goblob/wdclient/` (master client) and `goblob/pb/` (RPC helpers)
- Helper pattern: `pb.WithMasterServerClient(masterAddr, grpcDialOption, fn func(pb.MasterClient) error) error` — dials, calls fn, closes; never caches the connection (callers manage their own pools)
- `pb.WithVolumeServerClient(addr, grpcDialOption, fn func(pb.VolumeServerClient) error) error` — same pattern for volume gRPC
- `pb.WithFilerClient(addr, grpcDialOption, fn func(filer_pb.FilerClient) error) error` — same pattern for filer gRPC
- `MasterClient` struct (in `goblob/wdclient/`): `masterAddresses []string`, `currentMaster types.ServerAddress`, `grpcDialOption grpc.DialOption`, `vidCache *VidCache`; method `KeepConnectedToMaster(ctx, filerGrpcAddress, filerMux, dataCenter, rack, ip, port, maxCpu, fn)` — maintains a bidirectional `SendHeartbeat`-style stream with the master leader; reconnects on error with 1s backoff
- TLS dial option: if `SecurityConfig.Grpc.CertFile == ""` → `grpc.WithTransportCredentials(insecure.NewCredentials())`; otherwise load mTLS credentials: `security.LoadGrpcClientTLS(cfg)` → `grpc.WithTransportCredentials(creds)`
- gRPC port convention: HTTP port + 10000; use `types.ServerAddress.ToGrpcAddress()` which appends +10000 to HTTP port
- `VidCache` in `goblob/wdclient/vidmap.go`: `map[types.VolumeId][]pb.Location` with TTL; used by filer to cache volume locations

**Implementation Steps**

1. Create `goblob/wdclient/` package
2. Implement `WithMasterServerClient`, `WithVolumeServerClient`, `WithFilerClient` helper functions in `goblob/pb/grpc_client.go`; each: `grpc.DialContext(ctx, addr, opt)` → defer close → call fn
3. Implement `MasterClient` struct in `goblob/wdclient/master_client.go`; `KeepConnectedToMaster` runs a goroutine that dials the current leader, calls `SendHeartbeat` stream (for volume servers) or `KeepConnected` stream (for filer/S3), processes responses, updates `currentMaster` on leader change; retries with `time.Sleep(1 * time.Second)` on error
4. Implement `VidCache` in `goblob/wdclient/vidmap.go` with `sync.RWMutex`; `Get(vid) ([]pb.Location, bool)`, `Set(vid, locs)`, `Invalidate(vid)`; TTL-based lazy expiry on Get
5. Add TLS dial option loader: `func GetDialOption(secCfg *security.SecurityConfig) (grpc.DialOption, error)` — return insecure or mTLS credentials
6. Implement `ServerDiscovery` for SRV-based discovery (optional, used when masters = ["_goblob-master._tcp.example.com"])
7. Write unit tests: mock gRPC server, test WithXxxClient helpers, test VidCache TTL behavior

**Pitfalls**
- Do NOT cache grpc.ClientConn inside the helper functions — each `WithXxxClient` call creates a new connection; connection reuse is handled by the caller if needed
- gRPC port is NOT the same as HTTP port; always use `types.ServerAddress.ToGrpcAddress()` which adds 10000, not direct string concatenation
- TLS credentials must be loaded before dialing; passing `nil` `grpc.DialOption` will panic — always pass at least `grpc.WithTransportCredentials(insecure.NewCredentials())`
- `KeepConnectedToMaster` is for filer/S3 to register with master; volume servers use their own `SendHeartbeat` bidirectional stream — don't confuse the two

**Verification**
```bash
go test -race ./goblob/wdclient/... ./goblob/pb/...
# Smoke test: confirm TLS dial option loading works with empty config (insecure)
go test -run TestGetDialOptionInsecure ./goblob/wdclient/...
```

**Dependencies**
Protobuf, Security, Configuration

**Related Plan**
`plans/plan-grpc-transport.md`

**Expected Output**
- `goblob/pb/grpc_client.go`
- `goblob/wdclient/master_client.go`
- `goblob/wdclient/vidmap.go`
- Unit tests

### Task: Implement Master Server Core

**Goal**
Build central coordinator handling file ID assignment, volume lookup, and topology management.

**Critical Details**
- `MasterServer` struct (in `goblob/server/master_server.go`):
  ```go
  type MasterServer struct {
      Topo      *topology.Topology
      vg        *topology.VolumeGrowth
      Sequencer sequence.Sequencer
      Raft      RaftServer
      Cluster   *cluster.Cluster
      MasterClient *wdclient.MasterClient
      guard     *security.Guard
      volumeGrowthRequestChan chan *VolumeGrowRequest
      option    *MasterOption
      ctx       context.Context
      cancel    context.CancelFunc
      logger    *slog.Logger
  }
  ```
- `handleAssign` algorithm:
  1. Check `ms.Raft.IsLeader()` — if not leader: `proxyToLeader(w, r)` then return
  2. Parse params: `collection`, `replication`, `ttl`, `count`, `dataCenter`, `rack`, `diskType`
  3. Call `ms.Topo.PickForWrite(count, option)` → returns `(fid types.FileId, volumeUrl string, err error)`
  4. `fid.NeedleId = ms.Sequencer.NextFileId(count)` (Sequencer assigns the actual NeedleId)
  5. Generate JWT: `jwt = security.GenJwtForVolumeServer(ms.guard.SigningKey, ms.option.JwtExpireSeconds, fid.String())`
  6. Write JSON: `{"fid": fid.String(), "url": volumeUrl, "publicUrl": publicUrl, "count": count, "auth": jwt}`
- `handleLookup` algorithm: followers CAN answer (no leader proxy needed); parse `volumeId`; call `ms.Topo.LookupVolumeId(vid)` → return JSON array of `{url, publicUrl}` locations
- `proxyToLeader`: uses `httputil.NewSingleHostReverseProxy(leaderUrl)` where `leaderUrl = "http://" + ms.Raft.LeaderAddress()`
- gRPC `SendHeartbeat(stream)` handler (in `master_grpc_server.go`): receive `Heartbeat` message → call `ms.Topo.ProcessJoinMessage(hb)` → send back `HeartbeatResponse{Leader: ms.Raft.LeaderAddress(), VolumeSizeLimit: ms.option.VolumeSizeLimitMB * 1024 * 1024}`
- `startAdminScripts()` in `master_server.go`: ticker every `ms.option.MaintenanceConfig.SleepMinutes` minutes; only runs if `ms.Raft.IsLeader()`; creates a `shell.Shell` and runs the configured maintenance script
- `ProcessGrowRequest()`: goroutine draining `volumeGrowthRequestChan`; calls `ms.vg.AutomaticGrowByType(...)` for each request

**Implementation Steps**

1. Create `goblob/server/master_server.go` with `MasterServer` struct and `NewMasterServer(httpMux, opt, peers) (*MasterServer, error)`
2. In `NewMasterServer`: create Topology, VolumeGrowth, Sequencer, guard; register HTTP routes on httpMux; start background goroutines (ProcessGrowRequest, StartRefreshWritableVolumes)
3. Implement `handleAssign` in `master_server_handlers.go`: leader check → PickForWrite → NextFileId → JWT → JSON response; return 503 if no writable volumes
4. Implement `handleLookup`: no leader check; parse volumeId from query; call Topo.LookupVolumeId; return JSON
5. Implement `handleStatus`: return JSON with topology tree, writable volume count, DataCenter/Rack/DataNode breakdown
6. Implement `handleVolGrow`: leader check; parse collection/replication; enqueue to `volumeGrowthRequestChan`
7. Implement `handleVacuum`: leader check; call `topology.Vacuum(garbageThreshold, collection)`
8. Implement `proxyToLeader(w, r)`: get current leader address from Raft; build `httputil.ReverseProxy` targeting leader; call ServeHTTP
9. Create `goblob/server/master_grpc_server.go`: implement `pb.MasterServer` interface; `SendHeartbeat(stream)` reads heartbeats, calls `Topo.ProcessJoinMessage`, sends back response with leader and volume size limit
10. Implement `startAdminScripts()` background goroutine with ticker
11. Implement `ProcessGrowRequest()` background goroutine
12. Add graceful shutdown: stop heartbeat processing, stop admin scripts, close Raft

**Pitfalls**
- `/dir/assign` MUST check `IsLeader()` and proxy to leader — non-leader masters do NOT have the sequencer that issues monotonically increasing IDs
- `/dir/lookup` does NOT need a leader check — topology is replicated via heartbeats to all masters
- `handleAssign` must return 503 (not 500) when no writable volumes exist — the client uses 503 to know when to retry
- `ProcessJoinMessage` must be called under a topology write lock — use the lock from `topology.Topology.TopoLock` or the dataNode's own lock
- The gRPC `SendHeartbeat` handler is bidirectional streaming — it must call `stream.Recv()` in a loop, NOT just once

**Verification**
```bash
go test -race -run TestHandleAssign ./goblob/server/...
go test -race -run TestHandleAssignFollowerProxy ./goblob/server/...
# Start master, verify assign returns fid:
curl -X POST "http://localhost:9333/dir/assign"
# Verify lookup:
curl "http://localhost:9333/dir/lookup?volumeId=1"
```

**Dependencies**
Raft, Topology, Sequencer, Volume Growth, Cluster Registry, gRPC Transport, Security

**Related Plan**
`plans/plan-master-server.md`

**Expected Output**
- `goblob/server/master_server.go`
- `goblob/server/master_grpc_server.go`
- `goblob/server/master_server_handlers.go`
- Integration tests

### Task: Implement Operation Primitives (Assign + Upload)

**Goal**
Build the minimal client-side `goblob/operation/` helpers that the Replication Engine and Volume Server need to forward writes to replica nodes. The full Client SDK (with caching, retries, download) is completed in Phase 5; this task only covers what is strictly needed for replication.

**Critical Details**
- `AssignedFileId` struct:
  ```go
  type AssignedFileId struct {
      Fid       string  // e.g. "3,abc123ef01234567"
      Url       string  // volume server internal URL
      PublicUrl string  // volume server public URL
      Count     uint64
      Auth      string  // write JWT, empty if security disabled
  }
  ```
- `UploadResult` struct: `Name string`, `Size uint32`, `ETag string`, `Mime string`, `Fid string`, `Error string`
- `Assign`: `POST http://masterAddr/dir/assign?collection=X&replication=Y&ttl=Z&count=N`; decode JSON into `AssignedFileId`; return `ErrNoWritableVolumes` on HTTP 503
- `Upload`: `PUT http://uploadUrl` with body as multipart form OR raw binary; set `Content-Type` header; set `Authorization: Bearer jwt` if jwt non-empty; check status == 201; decode JSON response into `UploadResult`
- `LookupVolumeId`: `GET http://masterAddr/dir/lookup?volumeId=N`; decode JSON `{volumeOrFileId, locations: [{url, publicUrl}]}` array; return slice of url strings
- HTTP timeout: 30 seconds; use `http.Client{Timeout: 30 * time.Second}` (no retry logic in Phase 4)

**Implementation Steps**

1. Create `goblob/operation/` package
2. Implement `Assign(ctx context.Context, masterAddr string, opt *AssignOption) (*AssignedFileId, error)` in `assign.go`; build query params from opt; POST; decode JSON; return ErrNoWritableVolumes on 503
3. Implement `Upload(ctx context.Context, uploadUrl, filename string, data io.Reader, size int64, isGzip bool, mimeType string, pairMap map[string]string, jwt string) (*UploadResult, error)` in `upload.go`; build PUT request; set headers; check 201; decode response
4. Implement `LookupVolumeId(ctx context.Context, masterAddr string, vid types.VolumeId) ([]VolumeLocation, error)` in `lookup.go`; GET /dir/lookup; decode JSON `LookupResult` struct
5. Define `VolumeLocation` struct: `Url string`, `PublicUrl string`; define `LookupResult` struct: `VolumeOrFileId string`, `Locations []VolumeLocation`, `Error string`
6. Write unit tests using `httptest.NewServer` for mock master and volume servers

**Pitfalls**
- Upload body must be re-readable for the multipart encoder — do NOT pass a non-seekable `io.Reader` and expect it to work with retries (retry comes in Phase 5; in Phase 4, just pass the reader once)
- `Upload` URL format is `http://volumeServerAddr/fid` (NOT `http://volumeServerAddr/dir/assign` — that's the master)
- Always check `UploadResult.Error` field even when HTTP status is 201 — the volume server may return 201 with an error JSON body for certain logic errors

**Verification**
```bash
go test -race ./goblob/operation/...
go test -run TestAssignSuccess ./goblob/operation/...
go test -run TestUploadSuccess ./goblob/operation/...
```

**Dependencies**
Core Types

**Related Plan**
`plans/plan-client-sdk.md`

**Expected Output**
- `goblob/operation/assign.go`
- `goblob/operation/upload.go`
- `goblob/operation/lookup.go`
- Unit tests

### Task: Implement Replication Engine

**Goal**
Build synchronous replication ensuring strong consistency across replica volume servers.

**Critical Details**
- `ReplicaLocations` struct (in `goblob/replication/`):
  ```go
  type ReplicaLocations struct {
      mu        sync.RWMutex
      locations map[types.VolumeId][]types.ServerAddress
  }
  ```
  Methods: `Update(vid, addrs)`, `Get(vid) []types.ServerAddress`; updated from master heartbeat responses
- `ReplicateRequest` struct: `VolumeId types.VolumeId`, `Needle *needle.Needle`, `NeedleBytes []byte` (pre-serialized to avoid per-replica re-encoding), `JWTToken string`
- `Replicator` interface: `ReplicatedWrite(ctx, req ReplicateRequest, replicaAddrs []types.ServerAddress) error`
- `HTTPReplicator.ReplicatedWrite` algorithm:
  1. If `len(replicaAddrs) == 0`: return nil
  2. Launch one goroutine per replica; each calls `writeToReplica(ctx, addr, req)`
  3. `writeToReplica`: build URL as `http://addr/vid,needleIdHex`; create PUT request with `bytes.NewReader(req.NeedleBytes)` as body; set `Authorization: Bearer jwt` header; set `X-Replication: true` header; check resp.StatusCode == 201
  4. Collect all goroutine results; return combined error if any failed
- Loop prevention: Volume server `handleWrite` checks `r.Header.Get("X-Replication") == "true"` → if true, write locally only and skip replication
- Error handling: if ANY replica fails → return error to caller → caller returns 500 to client → client retries; partial replication is repaired by `volume.fix.replication`
- JWT forwarding: pass the client's JWT token (from `Authorization: Bearer` header) directly to replicas — the JWT is bound to the FileId, valid on all servers

**Implementation Steps**

1. Create `goblob/replication/` package
2. Define `ReplicaLocations`, `ReplicateRequest`, `ReplicateResult` structs in `replica_locations.go`
3. Implement `Replicator` interface and `HTTPReplicator` struct in `replicator.go`
4. Implement `ReplicatedWrite`: parallel goroutines per replica, buffered result channel, collect all results
5. Implement `writeToReplica`: build URL `http://{addr}/{vid},{needleIdHex}`, PUT with NeedleBytes, `X-Replication: true` header, auth header; return error if status != 201
6. Implement `NewReplicaLocations()`, `Update()`, `Get()` with `sync.RWMutex` (RLock for Get, Lock for Update)
7. Write unit tests: TestReplicatedWriteSuccess (2 mock servers), TestReplicatedWriteOneFailure (1 server returns 500), TestReplicatedWriteNoReplicas (empty list → nil)

**Pitfalls**
- `NeedleBytes` must be the full serialized needle (header + body + footer) — NOT just the raw data; use `needle.Needle.Bytes()` or equivalent to serialize before passing
- The `X-Replication: true` header is the ONLY mechanism preventing infinite replication loops — never omit it on replica writes
- The URL for replica writes is `/{vid},{needleId}` (no cookie in URL); the cookie is inside the needle bytes; parse carefully to avoid double-encoding
- Use buffered channel `results := make(chan replicateResult, len(replicaAddrs))` to prevent goroutine leaks if the caller cancels

**Verification**
```bash
go test -race -run TestReplicatedWriteSuccess ./goblob/replication/...
go test -race -run TestReplicatedWriteOneFailure ./goblob/replication/...
go test -race -run TestReplicaLoopPrevention ./goblob/replication/...
```

**Dependencies**
Core Types, Storage Engine (needle package)

**Related Plan**
`plans/plan-replication-engine.md`

**Expected Output**
- `goblob/replication/replicator.go`
- `goblob/replication/replica_locations.go`
- Unit tests

### Task: Implement Volume Server Core

**Goal**
Build data plane server storing blobs and serving read/write requests with replication.

**Critical Details**
- `VolumeServer` struct (in `goblob/server/volume_server.go`):
  ```go
  type VolumeServer struct {
      SeedMasterNodes []types.ServerAddress
      currentMaster   types.ServerAddress
      masterMu        sync.RWMutex
      store           *storage.Store
      guard           *security.Guard
      grpcDialOption  grpc.DialOption
      replicator      replication.Replicator
      replicaLocations *replication.ReplicaLocations
      option          *VolumeServerOption
      inFlightUploadBytes   int64  // atomic
      inFlightDownloadBytes int64  // atomic
      uploadLimitCond       *sync.Cond
      downloadLimitCond     *sync.Cond
      isHeartbeating bool
      stopChan       chan struct{}
      ctx    context.Context
      cancel context.CancelFunc
      logger *slog.Logger
  }
  ```
- `handleWrite` full algorithm:
  1. If `ConcurrentUploadLimitMB > 0`: `uploadLimitCond.L.Lock(); for atomic.LoadInt64(&inFlightUploadBytes) > limit { uploadLimitCond.Wait() }; atomic.AddInt64(&inFlightUploadBytes, r.ContentLength); uploadLimitCond.L.Unlock()`; defer release + Signal
  2. `vs.guard.CheckRequest(r)` → 401 if unauthorized
  3. Parse FileId from URL: `types.ParseFileId(r.URL.Path[1:])`
  4. `needle.CreateNeedleFromRequest(r, fixJpgOrientation, fileSizeLimit)` → returns `(n, originalSize, contentMD5, err)`; 413 if oversized
  5. `isReplication = r.Header.Get("X-Replication") == "true"`
  6. `vs.store.WriteVolumeNeedle(WriteRequest{VolumeId: fid.VolumeId, Needle: n, IsReplication: isReplication})`
  7. If NOT isReplication: get replica addrs from `vs.replicaLocations.Get(fid.VolumeId)`; filter out self; call `vs.replicator.ReplicatedWrite(...)`
  8. Set `ETag: contentMD5`; write 201; JSON body `{"size": result.Size, "eTag": contentMD5}`
- `handleRead` algorithm:
  1. Download throttle (same pattern as upload)
  2. Parse FileId
  3. `vol = vs.store.FindVolume(fid.VolumeId)` — if nil: check ReadMode (local→404, redirect→302, proxy→proxy)
  4. `vs.store.ReadVolumeNeedle(fid.VolumeId, n, ReadOption{})` → check for deleted needle (return 404)
  5. Set Content-Type, ETag, Last-Modified headers; handle If-None-Match → 304; handle Range requests
  6. `http.ServeContent(w, r, n.Name, time.Unix(n.LastModified, 0), bytes.NewReader(n.Data))`
- `ReadMode` constants: `ReadModeLocal ReadMode = "local"`, `ReadModeProxy = "proxy"`, `ReadModeRedirect = "redirect"`
- Heartbeat loop: dial current master gRPC; call `client.SendHeartbeat(ctx)` bidirectional stream; send `buildFullHeartbeat()` once; then ticker at HeartbeatInterval → send `buildDeltaHeartbeat()` → recv response → update `currentMaster` from `resp.Leader`; update `replicaLocations` from response; reconnect on error with 1s sleep
- Graceful shutdown sequence:
  1. `close(vs.stopChan)` — stops heartbeat goroutine
  2. `time.Sleep(time.Duration(vs.option.PreStopSeconds) * time.Second)` — allows master to reroute traffic
  3. `vs.httpServer.Shutdown(context.Background())`
  4. `vs.grpcServer.GracefulStop()`
  5. `vs.store.Close()`

**Implementation Steps**

1. Create `goblob/server/volume_server.go`; define `VolumeServer` struct and `VolumeServerOption` struct
2. Implement `NewVolumeServer(adminMux, publicMux *http.ServeMux, opt *VolumeServerOption) (*VolumeServer, error)`: create Store, Guard, Replicator, ReplicaLocations, sync.Cond objects; register routes; start heartbeat goroutine
3. Register routes on adminMux: `PUT /{fid}` → handleWrite; `GET /{fid}` → handleRead; `DELETE /{fid}` → handleDelete; `GET /status` → handleStatus; `GET /vol/vacuum/check` → handleVacuumCheck; `GET /vol/vacuum/needle` → handleVacuumNeedle; `GET /vol/vacuum/commit` → handleVacuumCommit
4. Implement `handleWrite` with all steps above; handle errors with appropriate HTTP status codes (400 bad FileId, 413 oversized, 500 write failure, 500 replication failure)
5. Implement `handleRead`: throttle → parse → find volume → ReadVolumeNeedle → serve; for deleted needles return 404
6. Implement `handleDelete`: parse FileId → store.DeleteVolumeNeedle; return 204
7. Implement `heartbeat()` goroutine: dial master gRPC → SendHeartbeat stream → sendFullHeartbeat → loop: ticker + recv response + sendDeltaHeartbeat; reconnect with 1s backoff
8. Implement `buildFullHeartbeat()`: populate `master_pb.Heartbeat` with Ip, Port, GrpcPort, PublicUrl, DataCenter, Rack, MaxVolumeCounts, all volume info
9. Implement `Shutdown()` with the 5-step sequence above
10. Create `goblob/server/volume_grpc_server.go`: implement `pb.VolumeServerServer` interface for AllocateVolume, VolumeDelete, VolumeCopy, VolumeCompact, ReadAllNeedles
11. Write integration test: TestVolumeServerEndToEnd (start master + 2 volumes, PUT blob, confirm replicated on both)

**Pitfalls**
- NEVER skip the `X-Replication: true` check — if omitted, replica writes will re-replicate infinitely
- The `uploadLimitCond` and `downloadLimitCond` must share a mutex with the atomic counter check — use `sync.Mutex` (not `sync.RWMutex`) as the `sync.Cond` base
- `handleWrite` must call `n.ParsePath(r.URL.Path[1:])` correctly — the path is `/{vid},{16hexNeedleId}{8hexCookie}`; use `types.ParseFileId(path)` exactly
- In heartbeat, update `currentMaster` under `masterMu.Lock()` — multiple goroutines may race on this field
- ReadMode "redirect" must return 302 with Location header pointing to the correct volume server URL; do NOT issue a redirect to the master

**Verification**
```bash
go test -race -run TestHandleWrite ./goblob/server/...
go test -race -run TestHandleRead ./goblob/server/...
go test -race -run TestHandleWriteReplication ./goblob/server/...
go test -race -run TestGracefulShutdown ./goblob/server/...
```

**Dependencies**
Storage Engine, Replication Engine, Security, gRPC Transport

**Related Plan**
`plans/plan-volume-server.md`

**Expected Output**
- `goblob/server/volume_server.go`
- `goblob/server/volume_grpc_server.go`
- `goblob/server/volume_server_handlers.go`
- Integration tests

### Task: Implement Filer Store Interface and LevelDB Backend

**Goal**
Build pluggable metadata persistence layer with default LevelDB implementation.

**Critical Details**
- `Entry` struct (package `goblob/filer`):
  ```go
  type Entry struct {
      FullPath FullPath
      Attr     Attr          // Mode, Mtime, Crtime, Uid, Gid, Mime, Replication, Collection, TtlSec, DiskType, FileSize, INode, SymlinkTarget
      Chunks   []*FileChunk  // empty for dirs or inline-stored files
      Content  []byte        // inline data (for small files)
      Extended map[string][]byte
      HardLinkId      []byte
      HardLinkCounter int32
      Remote   *RemoteEntry
  }
  ```
- `FileChunk` struct: `FileId string`, `Offset int64`, `Size int64`, `ModifiedTsNs int64`, `ETag string`, `CipherKey []byte`, `IsCompressed bool`; method `Fid() (types.FileId, error)` parses and caches
- `FullPath` type: `type FullPath string`; method `DirAndName() (dir FullPath, name string)` — split at last "/"
- `FilerStore` interface: `GetName() string`, `Initialize(config, prefix) error`, `Shutdown()`, `InsertEntry(ctx, *Entry) error`, `UpdateEntry(ctx, *Entry) error`, `FindEntry(ctx, FullPath) (*Entry, error)`, `DeleteEntry(ctx, FullPath) error`, `DeleteFolderChildren(ctx, FullPath) error`, `ListDirectoryEntries(ctx, dirPath, startFileName, includeStartFile bool, limit int64, fn func(*Entry) bool) (string, error)`, `ListDirectoryPrefixedEntries(ctx, dirPath, startFileName, includeStartFile bool, limit int64, prefix string, fn func(*Entry) bool) (string, error)`, `BeginTransaction(ctx) (context.Context, error)`, `CommitTransaction(ctx) error`, `RollbackTransaction(ctx) error`, `KvPut(ctx, key, value []byte) error`, `KvGet(ctx, key []byte) ([]byte, error)`, `KvDelete(ctx, key []byte) error`
- LevelDB2 key encoding: `key = string(dir) + "\x00" + name` (dir = parent directory path, name = filename)
  - Example: entry at `/photos/vacation.jpg` → key = `/photos\x00vacation.jpg`
  - Root directory entries: `/\x00photos` (dir = "/", name = "photos")
  - Directory listing: `LevelDB.Iterator.Seek(prefix)` where `prefix = string(dirPath) + "\x00" + startFileName`; iterate while key has prefix `string(dirPath) + "\x00"`
- Entry serialization: use `proto.Marshal(filer_pb.Entry{...})` for the value; key is the LevelDB key; `FullPath` is NOT stored in the value (it IS the key)
- `VirtualFilerStore` wrapper: normalizes paths and delegates to underlying `FilerStore`; `pathTranslations []PathTranslation` maps source prefixes to target prefixes (used for path-specific store routing)
- `LevelDB2Store` persistence path: `{defaultStoreDir}/filer.db/`
- Inline storage threshold: files < `saveToFilerLimit` bytes (typically 64KB) stored in `Entry.Content`; no chunks

**Implementation Steps**

1. Create `goblob/filer/` package; define `FullPath`, `Entry`, `Attr`, `FileChunk`, `RemoteEntry` in `entry.go`
2. Define `FilerStore` interface in `filer_store.go`; define `VirtualFilerStore` struct
3. Create `goblob/filer/leveldb2/leveldb2_store.go`; implement `LevelDB2Store` with `dir string`, `db *leveldb.DB`
4. Implement LevelDB2 key encoding in `encodeKey(dir FullPath, name string) []byte` and `decodeKey(key []byte) (dir FullPath, name string)`
5. Implement `InsertEntry`: encode entry to proto → `db.Put(encodeKey(dir, name), protoBytes, nil)`
6. Implement `FindEntry`: `db.Get(encodeKey(dir, name), nil)` → decode proto → return Entry
7. Implement `DeleteEntry`: `db.Delete(encodeKey(dir, name), nil)`
8. Implement `ListDirectoryEntries`: create iterator with `util.BytesPrefix([]byte(string(dirPath)+"\x00"))`; seek to startFileName; iterate calling fn; return lastFileName for pagination
9. Implement `BeginTransaction` / `CommitTransaction` / `RollbackTransaction` as no-ops (LevelDB doesn't support transactions; return nil)
10. Implement `KvPut`, `KvGet`, `KvDelete` using a separate LevelDB key namespace (e.g., prefix `"\xff"` to avoid collisions with path keys)
11. Write unit tests: TestInsertFindDelete, TestListDirectoryEntries (pagination), TestKvPutGetDelete

**Pitfalls**
- The `\x00` separator in LevelDB keys MUST be a null byte, not the literal characters `\x00` — use `[]byte{0}` in code
- Directory entries (directories themselves) are stored with `dir="/"` and `name="photos"` for `/photos/` — the path of the directory entry itself is `/photos`, stored as key `/\x00photos`
- `ListDirectoryEntries` returns `lastFileName` (not `lastFullPath`) as the pagination cursor — the caller appends it to `dirPath` for the next request
- Do NOT store `Entry.FullPath` in the protobuf value — it's redundant with the key and causes inconsistency if keys are moved
- `FindEntry` on a non-existent path should return `filer_pb.ErrNotFound` (not `sql.ErrNoRows` or LevelDB's `leveldb.ErrNotFound` directly) — wrap with a canonical error

**Verification**
```bash
go test -race ./goblob/filer/... ./goblob/filer/leveldb2/...
go test -run TestInsertFindDelete ./goblob/filer/leveldb2/...
go test -run TestListDirectoryEntries ./goblob/filer/leveldb2/...
go test -run TestListPaginationCursor ./goblob/filer/leveldb2/...
```

**Dependencies**
Core Types, Protobuf

**Related Plan**
`plans/plan-filer-store.md`

**Expected Output**
- `goblob/filer/entry.go`
- `goblob/filer/filer_store.go`
- `goblob/filer/leveldb2/leveldb2_store.go`
- Unit tests

### Task: Implement Distributed Lock Manager

**Goal**
Build named distributed locks for cross-process coordination using filer KV store.

**Critical Details**
- `DistributedLockManager` struct (package `goblob/filer`):
  ```go
  type DistributedLockManager struct {
      filerStore FilerStore
      localLocks map[string]*lockEntry
      localMu    sync.Mutex
      logger     *slog.Logger
  }
  type lockEntry struct {
      Owner      string
      ExpireAtNs int64
      RenewToken string
  }
  ```
- KV key format: `[]byte("__lock__:" + lockName)` — prefix `__lock__:` distinguishes locks from other KV entries
- Value format: JSON-encoded `lockEntry`
- `TryLock(ctx, name, owner, expireNs int64, renewToken string) (LockResult, error)` algorithm:
  1. `key = []byte("__lock__:" + name)`; `now = time.Now().UnixNano()`
  2. `existing, err = dlm.filerStore.KvGet(ctx, key)` — if nil/ErrNotFound: proceed to step 5
  3. `lock = parseLockEntry(existing)` — if `lock.ExpireAtNs > now` (still valid):
     - If `lock.Owner != owner && lock.RenewToken != renewToken`: return `LockResult{Acquired: false, CurrentOwner: lock.Owner}`, nil
     - If renewal (`renewToken != ""`): if `lock.RenewToken != renewToken`: return `{}`, ErrNotLockOwner
  4. (Expired or our lock — fall through to write)
  5. `newToken = hex.EncodeToString(randomBytes(32))`; write `lockEntry{Owner, ExpireAtNs: now+expireNs, RenewToken: newToken}` to KV
  6. Return `LockResult{Acquired: true, RenewToken: newToken}`, nil
- `Unlock(ctx, name, renewToken string) error`: get existing → check token → `KvDelete`
- `IsLocked(ctx, name string) (bool, string, error)`: get existing → check `ExpireAtNs > now` → return (held, owner)
- Error vars: `ErrLockHeldByOther`, `ErrNotLockOwner`, `ErrLockNotFound`
- Background cleanup goroutine: scan KV prefix `"__lock__:"` every hour; delete entries where `ExpireAtNs < now`
- gRPC handlers on filer gRPC port: `DistributedLock(req)` → calls `TryLock`; `DistributedUnlock(req)` → calls `Unlock`
- Known race: no CAS in KV — two concurrent acquirers may both read "no lock" and both write; for high-contention locks, callers should add retry with jitter

**Implementation Steps**

1. Define `DistributedLockManager`, `lockEntry`, `LockResult` structs in `goblob/filer/lock_manager.go`
2. Implement `NewDistributedLockManager(store FilerStore, logger) *DistributedLockManager`
3. Implement `TryLock` following the algorithm above; use `encoding/json` to marshal/unmarshal lockEntry
4. Implement `Unlock`: KvGet → check token → KvDelete; return ErrLockNotFound if entry doesn't exist
5. Implement `IsLocked`: KvGet → check ExpireAtNs; return (false, "", nil) if expired or not found
6. Define error variables: `var ErrLockHeldByOther = errors.New("lock held by another owner")`; `var ErrNotLockOwner = errors.New("renewToken mismatch")`; `var ErrLockNotFound = errors.New("lock not found")`
7. Add `cleanExpiredLocks(ctx, interval)` background goroutine using KV prefix scan
8. Implement gRPC `DistributedLock` and `DistributedUnlock` handlers in `goblob/server/filer_grpc_server.go`
9. Write unit tests: TestTryLockFresh, TestTryLockRenewal, TestTryLockContention, TestTryLockExpiry, TestUnlockSuccess, TestUnlockWrongToken

**Pitfalls**
- The KV key must use exact prefix `"__lock__:"` — do not change this; other subsystems (admin shell) use `"__admin_shell_lock__"` as their lock name, which becomes key `"__lock__:__admin_shell_lock__"`
- `ExpireAtNs` is Unix nanoseconds, NOT seconds — use `time.Now().UnixNano()`, NOT `time.Now().Unix()`
- The RenewToken must be cryptographically random — use `crypto/rand`, NOT `math/rand`, to generate the 32-byte token
- Lock entries are NEVER automatically deleted on expiry — they remain in KV until explicitly unlocked or cleaned by the background goroutine; always check `ExpireAtNs` when determining if a lock is held

**Verification**
```bash
go test -race ./goblob/filer/...
go test -run TestTryLockFresh ./goblob/filer/...
go test -run TestTryLockContention ./goblob/filer/...
go test -run TestTryLockExpiry ./goblob/filer/...
go test -race -count=10 -run TestDLMConcurrent ./goblob/filer/...
```

**Dependencies**
Filer Store

**Related Plan**
`plans/plan-distributed-lock.md`

**Expected Output**
- `goblob/filer/lock_manager.go`
- gRPC handlers in `goblob/server/filer_grpc_server.go`
- Unit tests

### Task: Implement Log Buffer for Metadata Events

**Goal**
Build in-memory ring buffer event bus for filer metadata change subscriptions.

**Critical Details**
- `LogBuffer` struct (package `goblob/log_buffer`):
  ```go
  type LogBuffer struct {
      segments            []*LogSegment
      segmentsMu          sync.RWMutex
      lastSegmentIdx      int
      maxSegmentCount     int       // default: 3
      maxSegmentSizeBytes int       // default: 2 MB
      flushInterval       time.Duration
      notifyFn            func()    // called after AppendEntry to wake subscribers
      flushFn             FlushFn   // uploads segment to volume server
      stopCh              chan struct{}
      logger              *slog.Logger
  }
  type LogSegment struct {
      buf       []byte  // length-prefixed protobuf frames
      startTsNs int64
      stopTsNs  int64
      isFlushed bool
  }
  type FlushFn func(ctx context.Context, data []byte, startTsNs, stopTsNs int64) error
  ```
- Frame format in `LogSegment.buf`: `[4-byte big-endian uint32 length][proto bytes of SubscribeMetadataResponse]` — repeated for each event
- `AppendEntry(event *filer_pb.SubscribeMetadataResponse)` algorithm:
  1. Set `event.TsNs = time.Now().UnixNano()`
  2. `data = proto.Marshal(event)` → frame = `uint32ToBytes(len(data)) + data`
  3. Lock segmentsMu (write); if `len(seg.buf) + len(frame) > maxSegmentSizeBytes`: call `rotate()`
  4. Append frame to current segment; update startTsNs/stopTsNs; unlock
  5. Call `notifyFn()` (outside lock)
- `rotate()` (called under write lock): if full: evict oldest segment (flush sync first if !isFlushed); advance `lastSegmentIdx = (lastSegmentIdx + 1) % maxSegmentCount`; initialize new empty segment
- `ReadFromBuffer(sinceNs int64) ReadResult`: read lock; scan segments in chronological order; parse frames; collect events with `TsNs > sinceNs`; return events + nextTs
- `IsInBuffer(sinceNs int64) bool`: check if `sinceNs >= segments[oldest].startTsNs`
- Flush daemon: ticker at `flushInterval`; for each non-active, non-flushed segment: call `flushFn(ctx, seg.buf, seg.startTsNs, seg.stopTsNs)` → mark `isFlushed = true`
- `FlushFn` implementation (provided by FilerServer): Assign file ID → PUT bytes to volume server → `KvPut(ctx, []byte("__log_seg__:"+zeroPad(startTsNs, 20)), marshalLocation(...))`
- Volume log segment KV key: `"__log_seg__:<20-digit-zero-padded-startTsNs>"` — zero-padded for lexicographic sort order
- `ReadFromVolume(ctx, sinceNs, pathPrefix, fn)`: scan Filer KV prefix `"__log_seg__:"`; for each location: GET needle from volume → parse frames → filter by pathPrefix and TsNs > sinceNs → call fn

**Implementation Steps**

1. Create `goblob/log_buffer/` package; define `LogBuffer`, `LogSegment`, `LogSegmentLocation`, `FlushFn`, `ReadResult` types
2. Implement `NewLogBuffer(maxSegmentCount, maxSegmentSizeBytes int, flushInterval time.Duration, notifyFn func(), flushFn FlushFn, logger) *LogBuffer`; initialize `segments` slice with maxSegmentCount empty LogSegment pointers
3. Implement `AppendEntry`: timestamp event → marshal to proto → length-prefix → lock → maybe rotate → append → unlock → notify
4. Implement `rotate()` (internal, called under write lock): flush oldest if needed → evict → init new segment at next ring position
5. Implement `ReadFromBuffer(sinceNs int64) ReadResult`: read lock → iterate segments in order → parse frames → filter by TsNs → return ReadResult
6. Implement `IsInBuffer(sinceNs int64) bool`: compare against oldest segment's startTsNs
7. Implement `StartFlushDaemon(ctx context.Context)`: goroutine with ticker; call `flushFullSegments(ctx)` each tick; on `ctx.Done()` flush all pending synchronously
8. Implement `Stop()`: close stopCh; flush any remaining segments synchronously
9. Write unit tests: TestAppendReadAfter, TestReadAfterFiltered, TestSegmentRotation, TestSegmentEviction, TestFlushDaemon (mock flushFn), TestConcurrentAppendRead (-race)

**Pitfalls**
- The frame format is `[4-byte length][proto bytes]` — the 4 bytes are big-endian uint32; use `binary.BigEndian.PutUint32` to encode and `binary.BigEndian.Uint32` to decode — do NOT use varint encoding
- `notifyFn()` must be called OUTSIDE the segmentsMu lock to avoid deadlock — the cond's mutex may be the same as the listeners' mutex
- The KV key for log segments uses 20-digit zero-padded decimal nanoseconds so that lexicographic scan order equals chronological order — do NOT use hex or unpadded decimal
- Do NOT call `rotate()` outside the write lock — it modifies the ring buffer index
- The `flushFn` is injected by FilerServer to avoid circular imports between `log_buffer` and `filer` packages

**Verification**
```bash
go test -race ./goblob/log_buffer/...
go test -run TestAppendReadAfter ./goblob/log_buffer/...
go test -run TestSegmentRotation ./goblob/log_buffer/...
go test -race -run TestConcurrentAppendRead ./goblob/log_buffer/...
```

**Dependencies**
Protobuf (filer_pb)

**Related Plan**
`plans/plan-log-buffer.md`

**Expected Output**
- `goblob/log_buffer/log_buffer.go`
- Unit tests

### Task: Implement Filer Server Core

**Goal**
Build filesystem metadata layer mapping paths to chunks with cross-filer synchronization.

**Critical Details**
- `FilerServer` struct (package `goblob/server`):
  ```go
  type FilerServer struct {
      option          *FilerOption
      filer           *filer.Filer
      filerGuard      *security.Guard
      volumeGuard     *security.Guard
      grpcDialOption  grpc.DialOption
      secret          security.SigningKey
      knownListeners  map[int32]int32
      listenersMu     sync.Mutex
      listenersCond   *sync.Cond
      inFlightDataSize int64  // atomic
      inFlightDataLimitCond *sync.Cond
      credentialManager *credential.CredentialManager
      ctx    context.Context
      cancel context.CancelFunc
      logger *slog.Logger
  }
  ```
- `Filer` struct (package `goblob/filer`): `UniqueFilerId int32`, `Store filer.VirtualFilerStore`, `MasterClient *pb.MasterClient`, `fileIdDeletionQueue *util.UnboundedQueue`, `DirBucketsPath string` (default: "/buckets"), `LocalMetaLogBuffer *log_buffer.LogBuffer`, `MetaAggregator *MetaAggregator`, `FilerConf *FilerConf`, `Dlm *DistributedLockManager`, `DeletionRetryQueue *DeletionRetryQueue`
- HTTP routes: `POST /{path+}` → handleFileUpload; `GET /{path+}` → handleFileDownload; `DELETE /{path+}` → handleDeleteEntry; `GET /healthz` → return "OK"
- `handleFileUpload` algorithm:
  1. Throttle in-flight upload data (same sync.Cond pattern as volume server)
  2. `rule = filerConf.MatchRule(r.URL.Path)` → get collection, replication, ttl, maxMB for this path
  3. Chunk large files: for each `chunkSizeBytes` chunk of r.Body: call `Filer.AssignVolume(ctx, req)` → PUT chunk to volume server → build `filer.FileChunk{FileId, Offset, Size, ModifiedTsNs}`
  4. Create `filer.Entry{FullPath, Attr{Mode:0644, Mtime:now, Mime, FileSize}, Chunks}` and call `Filer.CreateEntry(ctx, entry, ...)`
  5. Append event to `filer.LocalMetaLogBuffer`; notify `listenersCond.Broadcast()`; write 201
- `handleFileDownload` algorithm:
  1. `entry, err = filer.FindEntry(ctx, path)` → 404 if ErrNotFound
  2. If `len(entry.Content) > 0`: serve inline bytes
  3. For each chunk: lookup volume server from `filer.MasterClient.LookupVolumeId(chunkVid)` → generate read JWT → proxy chunk bytes to client via HTTP GET
- `handleDeleteEntry`: `filer.DeleteEntry(ctx, path, isDeleteData=true, ...)` → enqueues chunk FileIds to `fileIdDeletionQueue`
- `loopProcessingDeletion`: goroutine consuming `fileIdDeletionQueue`; DELETE each FileId from volume server; retry with exponential backoff up to `maxRetry` times
- `MetaAggregator.AggregateFromPeers()`: for each peer filer: goroutine that subscribes to peer's `SubscribeLocalMetadata` gRPC stream; replays events to local filer store via `applyRemoteEvent(event)`; reconnects on error with 1s sleep
- `SubscribeMetadata` gRPC handler: register listener → loop: wait on `listenersCond` with 1s timeout → drain `LocalMetaLogBuffer.ReadFromBuffer(sinceNs)` → send events matching `pathPrefix`; for very old `sinceNs` (outside buffer): read from volume log segments first via `ReadFromVolume`
- FilerConf per-path rules: `FilerConfRule{PathPrefix, Collection, Replication, Ttl, DiskType, MaxMB, Fsync}`; stored as an entry in filer at `/.filer_conf`; `MatchRule(path) *FilerConfRule` finds longest matching PathPrefix

**Implementation Steps**

1. Create `goblob/filer/filer.go` defining `Filer`, `MetaAggregator`, `FilerConf`, `FilerConfRule`, `DeletionRetryQueue` structs
2. Implement `Filer.CreateEntry`, `UpdateEntry`, `FindEntry`, `DeleteEntry`, `ListDirectoryEntries`, `DoRename`, `AssignVolume` methods
3. Implement `loopProcessingDeletion` background goroutine: dequeue from `fileIdDeletionQueue` → DELETE from volume server → retry with backoff
4. Implement `MetaAggregator`: `AggregateFromPeers()` starts per-peer goroutines; `subscribeToFiler(peer)` uses `pb.WithFilerClient` + `SubscribeLocalMetadata` stream; `applyRemoteEvent(event)` writes metadata to local store
5. Create `goblob/server/filer_server.go` with `FilerServer` struct; implement `NewFilerServer(defaultMux, readonlyMux, opt) (*FilerServer, error)`: create Filer, guards, log buffer, DLM; register HTTP routes; start background goroutines
6. Register HTTP routes: POST/GET/DELETE on `/{path...}`; GET `/healthz`
7. Implement `handleFileUpload` with chunking, throttling, and metadata creation
8. Implement `handleFileDownload` with inline content check and chunk proxying
9. Implement `handleDeleteEntry` with async deletion queue
10. Create `goblob/server/filer_grpc_server.go`; implement `filer_pb.FilerServer` interface: `LookupDirectoryEntry`, `ListEntries(stream)`, `CreateEntry`, `UpdateEntry`, `DeleteEntry`, `AtomicRenameEntry`, `SubscribeMetadata(stream)`, `AssignVolume`, `LookupVolume`, `KvGet`, `KvPut`, `KvDelete`
11. Implement `SubscribeMetadata` handler: register listener → wait on `listenersCond` → drain log buffer → send events
12. Write integration test: TestFilerServerEndToEnd (start filer + mock volume + mock master, POST file, GET file, DELETE file)

**Pitfalls**
- `SubscribeMetadata` handler must NOT hold `listenersMu` during `stream.Send()` — lock only for the cond wait, release before sending
- The `listenersCond` must use `listenersMu` as its base lock — `listenersCond = sync.NewCond(&filerServer.listenersMu)`
- `MetaAggregator` uses `UniqueFilerId` to identify events it generated itself — it must skip events with `EventNotification.ClientId == f.UniqueFilerId` to prevent replay loops when subscribing to peers
- Chunk upload in `handleFileUpload` must handle `io.ErrUnexpectedEOF` (last chunk smaller than chunkSize) — always upload the partial chunk
- `FilerConf.MatchRule` should find the LONGEST matching prefix, not the first — store rules sorted by prefix length descending

**Verification**
```bash
go test -race ./goblob/filer/... ./goblob/server/...
go test -run TestHandleFileUploadSmall ./goblob/server/...
go test -run TestHandleFileUploadLarge ./goblob/server/...
go test -run TestHandleDeleteEntry ./goblob/server/...
go test -run TestMetadataSubscription ./goblob/server/...
go test -run TestMetaAggregatorSync ./goblob/server/...
```

**Dependencies**
Filer Store, Log Buffer, Distributed Lock, gRPC Transport, Client SDK (operation primitives)

**Related Plan**
`plans/plan-filer-server.md`

**Expected Output**
- `goblob/filer/filer.go`
- `goblob/server/filer_server.go`
- `goblob/server/filer_grpc_server.go`
- `goblob/server/filer_server_handlers.go`
- `goblob/filer/meta_aggregator.go`
- Integration tests


### Phase 4 Checkpoint

Bring up a 1-master + 2-volume-server cluster. Run an end-to-end write: call `/dir/assign` on master → `PUT /{fid}` on volume server → confirm replication on the second volume server by reading directly. Run a read: `/dir/lookup` → `GET /{fid}`. Run a filer test: POST a file, GET it back, DELETE it, confirm deletion. Confirm all three servers start, heartbeat successfully, and shut down cleanly via Ctrl-C. Run `go test -race ./goblob/...` before proceeding to Phase 5.

---

## Phase 5: Client Interfaces

**Purpose**: Build client libraries and gateway services for accessing the storage system.

### Task: Implement Client SDK

**Goal**  
Create a production-ready Go client SDK for file operations with automatic retries, connection pooling, and distributed upload support.

**Critical Details**
- Package: `goblob/operation/`
- `AssignedFileId` struct: Fid types.VolumeId, Url string, PublicUrl string, Count int, Auth string (JWT token)
- `UploadOption` struct: Master string, Collection string, Replication string, Ttl int32, DataCenter string, Rack string, DiskType string, Count int, Preallocate bool
- `UploadResult` struct: Name string, Size int64, ETag string, Mime string, Fid types.VolumeId, Error string
- `VolumeLocationCache` struct: locations map[types.VolumeId]*locationEntry, mu sync.RWMutex, ttl time.Duration (default 10min)
- Chunk upload default: 8MB chunks, 3 concurrent workers
- JWT format: `Bearer {token}` in Authorization header

**Implementation Steps**

1. Create `operation/assignedfileid.go` with struct and JSON unmarshaling
2. Create `operation/uploadoptions.go` with functional option pattern
3. Create `operation/uploadresult.go` with response structs
4. Create `operation/volumelocationcache.go` with TTL-based cache
5. Implement `Assign(masterAddr string, opt UploadOption) (*AssignedFileId, error)` with POST to `/dir/assign`
6. Implement `Upload(uploadUrl, filename, data, size, isGzip, mimeType, pairMap, jwt)` with PUT request and Authorization header
7. Implement `LookupVolumeId(masterAddr, vid, cache)` with GET to `/dir/lookup?volumeId=N`
8. Implement `UploadWithRetry` with exponential backoff starting at 500ms, max 30s
9. Implement `ChunkUpload` with worker pool for parallel chunk uploads
10. Add comprehensive error wrapping with context

**Pitfalls**
- Forgetting to set Content-Type header causes incorrect MIME types
- Not checking HTTP 200-299 status range before reading body
- Race condition in cache when updating map without holding write lock
- JWT token not included in upload request causes 401 errors
- Chunk upload fails to handle last chunk differently (< chunk size)
- Retry loop must check for context cancellation

**Verification**
```bash
# Test client SDK operations
go test -v -run TestClientSDK ./operation/

# Test volume location cache TTL
go test -v -run TestVolumeLocationCache ./operation/

# Test chunk upload with retry
go test -v -run TestChunkUpload ./operation/

# Integration test with real server
go test -v -run TestClientUploadIntegration ./operation/
```

**Dependencies**  
- github.com/gorilla/mux (for URL parsing)
- golang.org/x/net/publicsuffix (for URL validation)

**Related Plan**  
`plans/plan-client-sdk.md`

**Expected Output**  
- `goblob/operation/assignedfileid.go`
- `goblob/operation/uploadoptions.go`
- `goblob/operation/uploadresult.go`
- `goblob/operation/volumelocationcache.go`
- `goblob/operation/client.go`
- Unit tests and examples

---

### Task: Implement IAM System

**Goal**  
Build a complete identity and access management system for S3 API authentication and authorization with hot-reloadable configuration.

**Critical Details**
- Package: `goblob/s3api/iam/`
- `Identity` struct: Name string, Credentials []*Credential, Actions []string
- Supported actions: "Read", "Write", "Admin", "*", "s3:GetObject", "s3:PutObject", etc.
- `Credential` struct: AccessKey string, SecretKey string
- `IdentityAccessManagement` struct: identities []*Identity, mu sync.RWMutex, hashes map[string]*sync.Pool
- HMAC key pools prevent constant-time comparison timing attacks
- Filer KV key: `[]byte("__iam_config__")`, value: JSON S3ApiConfiguration
- `LookupByAccessKey(accessKey)`: O(n) linear scan through identities
- `IsAuthorized(identity, action, bucket, object)`: wildcard matching, bucket-scoped policies
- gRPC service: register on Filer server at startup

**Implementation Steps**

1. Create `iam/identity.go` with Identity and Credential structs
2. Create `iam/iam.go` with IdentityAccessManagement struct and methods
3. Implement `LookupByAccessKey(accessKey string) (*Identity, string, bool)` with linear scan
4. Implement `IsAuthorized(identity, action, bucket, object string) bool` with pattern matching
5. Implement `deriveSigningKey(secretKey, date, region, service string) []byte` with HMAC chain
6. Implement `Reload(cfg *iampb.S3ApiConfiguration) error` with atomic swap
7. Implement HMAC key pool creation in `reloadHashes()`
8. Create gRPC service handlers `GetS3ApiConfiguration` and `PutS3ApiConfiguration`
9. Add unit tests for authorization logic with wildcards
10. Add integration tests with Filer KV store

**Pitfalls**
- Linear scan doesn't scale beyond hundreds of identities (use map for production)
- HMAC key pool not synced with identities causes authorization failures
- Wildcard matching must check "Admin" before bucket-scoped permissions
- Filer KV read/write must use []byte key, not string
- Race condition when reloading config while requests are in-flight
- Not validating action strings allows arbitrary permissions

**Verification**
```bash
# Test IAM lookup and authorization
go test -v -run TestIAM ./iam/

# Test wildcard authorization
go test -v -run TestWildcardAuth ./iam/

# Test config reload
go test -v -run TestConfigReload ./iam/

# Test HMAC derivation
go test -v -run TestSigningKey ./iam/

# Integration with filer KV
go test -v -run TestFilerKV ./iam/
```

**Dependencies**  
Filer Store, Security

**Related Plan**  
`plans/plan-iam.md`

**Expected Output**  
- `goblob/s3api/iam/identity.go`
- `goblob/s3api/iam/iam.go`
- Unit tests

---

### Task: Implement S3 API Gateway Core

**Goal**  
Create a full-featured S3-compatible API gateway with routing, authentication, and integration with the GoBlob storage layer.

**Critical Details**
- Package: `goblob/s3api/`
- `S3ApiServer` struct: iam *iam.IdentityAccessManagement, cb *CircuitBreaker, filerGuard *security.Guard, filerClient *FilerClient
- `S3ApiServerOption`: Filers []types.ServerAddress, Port int (default 8333), DomainName string, BucketsPath string (default "/buckets")
- gorilla/mux routes: query-param routes must be registered BEFORE bare routes
- Route precedence: specific methods before catch-all
- Bucket mapping: `s3://my-bucket/dir/obj` → `/buckets/my-bucket/dir/obj`
- XML responses for all S3 operations
- Error responses must match S3 error format

**Implementation Steps**

1. Create `s3api/server.go` with S3ApiServer struct and options
2. Create `s3api/router.go` with route registration using gorilla/mux
3. Implement route handlers for all S3 operations (listBuckets, createBucket, deleteBucket, listObjects, putObject, getObject, headObject, deleteObject)
4. Implement deleteObjects batch operation with XML parsing
5. Implement bucket path mapping to filer directory structure
6. Add request logging middleware
7. Add metrics middleware for operation tracking
8. Create XML response builders for each operation
9. Add error handling with S3-compatible error codes
10. Register routes with query parameters before bare routes

**Pitfalls**
- Query-param routes registered after bare routes never match
- Not handling trailing slashes causes 404 on bucket URLs
- XML namespace must be `xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`
- Empty listObjects responses must return empty XML, not 404
- putObject must handle both Content-Length and chunked encoding
- HeadObject must return same headers as GetObject but no body
- deleteObjects batch limited to 1000 keys per request

**Verification**
```bash
# Test S3 server startup
go test -v -run TestS3ServerStartup ./s3api/

# Test routing order
go test -v -run TestRoutePrecedence ./s3api/

# Test bucket operations
go test -v -run TestBucketOperations ./s3api/

# Test object operations
go test -v -run TestObjectOperations ./s3api/

# Integration with real S3 client
go test -v -run TestS3ClientIntegration ./s3api/
```

**Dependencies**  
Filer Server, IAM, Security, Client SDK

**Related Plan**  
`plans/plan-s3-gateway.md`

**Expected Output**  
- `goblob/s3api/s3api_server.go`
- `goblob/s3api/s3api_bucket_handlers.go`
- `goblob/s3api/s3api_object_handlers.go`
- Integration tests

---

### Task: Implement S3 SigV4 Authentication

**Goal**  
Implement AWS Signature Version 4 authentication for both Authorization header and presigned URL formats with full signature verification.

**Critical Details**
- Auth types: `AuthTypeSigV4` (Authorization header), `AuthTypePresigned` (query params), `AuthTypeAnonymous`
- SigV4 header format: `AWS4-HMAC-SHA256 Credential=<accessKeyId>/<date>/<region>/s3/aws4_request, SignedHeaders=..., Signature=...`
- `deriveSigningKey(secretKey, date, region, service)`: HMAC chain with specific AWS4 prefix
- Canonical request: HTTPMethod + CanonicalURI + CanonicalQueryString + CanonicalHeaders + SignedHeaders + HexEncode(Hash(RequestPayload))
- String to sign: "AWS4-HMAC-SHA256" + Timestamp + CredentialScope + HexEncode(Hash(CanonicalRequest))
- Region always "us-east-1" for compatibility (not validated)
- Presigned URL expiration: X-Amz-Expires query parameter (seconds)

**Implementation Steps**

1. Create `s3api/auth/auth.go` with auth types and AuthRequest function
2. Implement Authorization header parsing with regex
3. Implement presigned URL parameter parsing
4. Implement `deriveSigningKey(secretKey, date, region, service string) []byte`
5. Implement canonical request building from HTTP request
6. Implement string-to-sign construction
7. Implement signature calculation with HMAC-SHA256
8. Implement signature comparison with constant-time equality
9. Add caching for derived signing keys (per access key + date)
10. Add comprehensive logging for auth failures

**Pitfalls**
- Not including "AWS4" prefix in HMAC chain causes signature mismatch
- URI encoding in canonical URI must use %XX uppercase
- Query parameters must be sorted by key name in canonical query string
- Headers must be lowercase in canonical headers
- Not handling X-Amz-Security-Token (session token) breaks temporary credentials
- Presigned URL expiration must be checked against current time
- Empty payload hash uses "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

**Verification**
```bash
# Test SigV4 signature verification
go test -v -run TestSigV4Auth ./s3api/auth/

# Test presigned URL generation and verification
go test -v -run TestPresignedURL ./s3api/auth/

# Test header parsing
go test -v -run TestHeaderParsing ./s3api/auth/

# Integration with AWS SDK
go test -v -run TestAWSSDKAuth ./s3api/auth/
```

**Dependencies**  
IAM, Security

**Related Plan**  
`plans/plan-s3-gateway.md`

**Expected Output**  
- `goblob/s3api/auth/auth.go`
- `goblob/s3api/auth/signing.go`
- Unit tests

---

### Task: Implement S3 Multipart Upload

**Goal**  
Implement S3 multipart upload functionality for large files with parallel part uploads and atomic completion.

**Critical Details**
- `MultipartUpload` struct: UploadId string, Bucket string, Key string, Parts []*UploadedPart
- `UploadedPart` struct: PartNumber int, ETag string, Size int64
- Staging directory: `/buckets/{bucket}/.uploads/{uploadId}/`
- Part files: `/buckets/{bucket}/.uploads/{uploadId}/{partNumber}`
- Minimum part size: 5MB (except last part)
- Maximum parts: 10,000
- ETag calculation: MD5 hash of part data
- Completion concatenates chunk lists from filer entries

**Implementation Steps**

1. Create `s3api/multipart/types.go` with multipart structs
2. Create `s3api/multipart/initiate.go` with initiateMultipart handler
3. Create `s3api/multipart/upload_part.go` with uploadPart handler
4. Create `s3api/multipart/complete.go` with completeMultipart handler
5. Create `s3api/multipart/abort.go` with abortMultipart handler
6. Implement uploadId generation (UUID or random)
7. Implement part validation (size ≥ 5MB except last)
8. Implement ETag calculation using MD5
9. Implement atomic completion with temp file and rename
10. Add cleanup for abandoned uploads (older than 7 days)

**Pitfalls**
- Not checking minimum part size causes upload to fail at completion
- PartNumber must be 1-10000, validation required
- ETag must be quoted string in XML response
- Completion must validate all parts are present and ordered
- Staging directory cleanup must not delete active uploads
- Concurrent uploadPart requests to same part number must be serialized
- Final entry must concatenate chunk lists, not copy data

**Verification**
```bash
# Test multipart upload flow
go test -v -run TestMultipartUpload ./s3api/multipart/

# Test part size validation
go test -v -run TestPartSizeValidation ./s3api/multipart/

# Test atomic completion
go test -v -run TestAtomicCompletion ./s3api/multipart/

# Test abort and cleanup
go test -v -run TestAbortAndCleanup ./s3api/multipart/

# Integration with large file upload
go test -v -run TestLargeFileUpload ./s3api/multipart/
```

**Dependencies**  
S3 API Gateway Core

**Related Plan**  
`plans/plan-s3-gateway.md`

**Expected Output**  
- `goblob/s3api/s3api_multipart.go`
- Integration tests

---

### Task: Implement S3 Advanced Features

**Goal**  
Implement advanced S3 features including server-side encryption, versioning, bucket policies, CORS, and object tagging.

**Critical Details**
- SSE-S3: AES-256 key per object, stored in metadata as `x-amz-server-side-encryption: AES256`
- Versioning: per-bucket VersioningState (Unset/Enabled/Suspended), versionId generation
- Bucket policies: JSON policy document with Effect/Action/Resource/Principal
- CORS: BucketConfig.CORS []CORSRule with AllowedOrigins/AllowedMethods/AllowedHeaders
- Object tagging: `/buckets/{bucket}/{key}/.tags` entry stores map[string]string
- Policy evaluation: deny overrides, explicit allow required
- CORS headers: Access-Control-Allow-Origin, Access-Control-Allow-Methods, Access-Control-Allow-Headers

**Implementation Steps**

1. Create `s3api/sse/sse.go` with server-side encryption
2. Create `s3api/versioning/versioning.go` with versioning state management
3. Create `s3api/policy/policy.go` with policy parsing and evaluation
4. Create `s3api/cors/cors.go` with CORS handling
5. Create `s3api/tagging/tagging.go` with object tag operations
6. Implement AES-256 key generation and storage for SSE-S3
7. Implement versionId generation (UUID or timestamp-based)
8. Implement JSON policy document parsing
9. Implement policy evaluation logic (deny override)
10. Implement OPTIONS request handler for CORS preflight

**Pitfalls**
- SSE key must be stored securely, never logged
- VersionId must be returned in response headers for versioned buckets
- Policy evaluation order matters (deny before allow)
- CORS wildcard "*" must be only value if used
- Object tagging has limit of 10 tags per object
- Tag keys must be case-sensitive
- Not handling suspended versioning causes writes to fail

**Verification**
```bash
# Test server-side encryption
go test -v -run TestSSE ./s3api/sse/

# Test versioning operations
go test -v -run TestVersioning ./s3api/versioning/

# Test bucket policy evaluation
go test -v -run TestPolicyEval ./s3api/policy/

# Test CORS handling
go test -v -run TestCORS ./s3api/cors/

# Test object tagging
go test -v -run TestTagging ./s3api/tagging/

# Integration test
go test -v -run TestAdvancedFeatures ./s3api/
```

**Dependencies**  
S3 API Gateway Core

**Related Plan**  
`plans/plan-s3-gateway.md`

**Expected Output**  
- `goblob/s3api/s3api_encryption.go`
- `goblob/s3api/s3api_versioning.go`
- `goblob/s3api/s3api_policy.go`
- Integration tests


### Phase 5 Checkpoint

Run `aws s3 mb s3://test-bucket` and `aws s3 cp testfile.txt s3://test-bucket/` against the running S3 gateway. Confirm the object is retrievable with `aws s3 cp s3://test-bucket/testfile.txt -`. Run `go test ./goblob/s3api/...` and `go test ./goblob/operation/...` before proceeding to Phase 6.

---

## Phase 6: Command Line Interface

**Purpose**: Build CLI binary with subcommands for launching servers and administrative operations.

### Task: Implement Command Framework + Main Entry Point

**Goal**  
Create a unified command-line interface with subcommands for all server types and administrative operations.

**Critical Details**
- Package: `goblob/command/` (main package is `goblob/blob.go`)
- `Command` interface: `Name() string`, `Synopsis() string`, `Usage() string`, `SetFlags(fs *flag.FlagSet)`, `Run(ctx context.Context, args []string) error`
- Command registration: package-level `Commands map[string]Command` populated via `init()` functions
- `main()`: parse os.Args[1] as command name → lookup → SetFlags → fs.Parse → signal.NotifyContext → cmd.Run → handle SIGHUP
- SIGHUP handler: reload security config, volumeServer.LoadNewVolumes(), filerServer.ReloadConfig()
- Command lookup must handle unknown commands gracefully
- Context cancellation triggers graceful shutdown

**Implementation Steps**

1. Create `command/command.go` with Command interface and Commands map
2. Create `blob.go` main package with main() function
3. Implement command parsing and lookup logic
4. Implement signal handling for SIGTERM and SIGINT
5. Implement SIGHUP handler for config reload
6. Add help command that lists all available commands
7. Add version command with build info
8. Implement error handling with appropriate exit codes
9. Add command-specific flag validation
10. Add logging configuration setup

**Pitfalls**
- Forgetting to call SetFlags before Parse causes flag parsing to fail
- Not checking for nil commands causes panic on unknown command
- SIGHUP must be handled in all goroutines, not just main
- Context cancellation must propagate to all servers
- Flag validation must happen after Parse, not before
- Help command must handle both -h and --help flags
- Exit codes: 0 for success, 1 for errors, 2 for usage

**Verification**
```bash
# Test command framework
go test -v -run TestCommandFramework ./command/

# Test unknown command handling
go test -v -run TestUnknownCommand ./command/

# Test flag parsing
go test -v -run TestFlagParsing ./command/

# Test signal handling
go test -v -run TestSignalHandling ./command/

# Build and test help
go build -o blob && ./blob -h
```

**Dependencies**  
Configuration

**Related Plan**  
`plans/plan-cmd.md`

**Expected Output**  
- `goblob/command/command.go`
- `goblob/command/version.go`
- Unit tests

---

### Task: Implement Server Subcommands

**Goal**  
Implement individual subcommands for master, volume, filer, and s3 servers with proper flag configuration and lifecycle management.

**Critical Details**
- `blob master`: -port 9333, -mdir, -peers, -volumeSizeLimitMB, -replication, -garbageThreshold
- `blob volume`: -port 8080, -masters (comma-separated), -dir (repeatable), -max, -readMode, -fileSizeLimitMB
- `blob filer`: -port 8888, -masters, -defaultReplication, -defaultCollection, -maxMB
- `blob s3`: -port 8333, -filer, -filer.path, -domainName
- Each subcommand: load security config → create option struct → NewXxxServer → start HTTP/gRPC → block on ctx.Done → graceful shutdown
- Security config path: default to `/etc/goblob/security.json`
- Server startup order matters for dependencies

**Implementation Steps**

1. Create `command/master.go` with master subcommand
2. Create `command/volume.go` with volume subcommand
3. Create `command/filer.go` with filer subcommand
4. Create `command/s3.go` with s3 subcommand
5. Implement flag registration for each subcommand
6. Implement security config loading
7. Implement server option struct creation
8. Implement server startup and shutdown logic
9. Add health check endpoints for each server
10. Add pid file creation for daemon mode

**Pitfalls**
- Masters flag must be parsed as comma-separated list
- Dir flag is repeatable, use flag.StringSlice
- ReadMode must be validated (local/proxy/redirect)
- Security config load failure must be fatal
- Server startup must return error on bind failure
- Graceful shutdown must wait for active connections
- Health check must not require authentication

**Verification**
```bash
# Test master subcommand
go test -v -run TestMasterCommand ./command/

# Test volume subcommand
go test -v -run TestVolumeCommand ./command/

# Test filer subcommand
go test -v -run TestFilerCommand ./command/

# Test s3 subcommand
go test -v -run TestS3Command ./command/

# Integration test
go build -o blob && ./blob master -port 9333 &
./blob volume -masters localhost:9333 -dir /tmp/data &
./blob filer -masters localhost:9333 &
./blob s3 -filer localhost:8888 &
```

**Dependencies**  
Master Server, Volume Server, Filer Server, S3 Gateway, Configuration

**Related Plan**  
`plans/plan-cmd.md`

**Expected Output**  
- `goblob/command/master.go`
- `goblob/command/volume.go`
- `goblob/command/filer.go`
- `goblob/command/s3.go`
- Integration tests

---

### Task: Implement All-in-One Server

**Goal**  
Create a unified server mode that starts all components in the correct order with proper dependency management and graceful shutdown.

**Critical Details**
- `runServer()`: start master → sleep 1s → start volume → sleep 1s → start filer → sleep 2s → start s3 → block on ctx.Done() → shutdown
- Shutdown order: s3 → filer → volume → master
- Use `startXxxInProcess(opt, secCfg)` helper functions
- Sleep durations allow for Raft election, volume registration, and filer sync
- Each server gets its own context from parent
- Errors during startup must stop the entire process
- Shutdown must wait for each server to complete

**Implementation Steps**

1. Create `command/server.go` with server subcommand
2. Implement `runServer()` with startup sequence
3. Implement `startMasterInProcess(opt, secCfg)` helper
4. Implement `startVolumeInProcess(opt, secCfg)` helper
5. Implement `startFilerInProcess(opt, secCfg)` helper
6. Implement `startS3InProcess(opt, secCfg)` helper
7. Implement shutdown sequence with timeout
8. Add configuration flags for each component
9. Add component enable/disable flags
10. Add health aggregation endpoint

**Pitfalls**
- Sleep durations are too short causes race conditions
- Not waiting for Raft election causes volume registration to fail
- Shutdown must be in reverse order of dependencies
- Context cancellation must propagate to all servers
- Startup errors must stop entire process
- Health checks must verify all dependencies
- S3 server must wait for filer to be ready

**Verification**
```bash
# Test server startup sequence
go test -v -run TestServerStartup ./command/

# Test graceful shutdown
go test -v -run TestServerShutdown ./command/

# Test component dependencies
go test -v -run TestDependencies ./command/

# Integration test
go build -o blob && ./blob server -port.master 9333 -port.volume 8080 -port.filer 8888 -port.s3 8333
```

**Dependencies**  
All Command implementations

**Related Plan**  
`plans/plan-cmd.md`

**Expected Output**  
- `goblob/command/server.go`
- Integration tests

---

### Task: Implement Admin Shell

**Goal**  
Create an interactive admin shell for cluster management with command history, tab completion, and remote execution capabilities.

**Critical Details**
- Package: `goblob/shell/`
- `Command` interface: `Name() string`, `Help() string`, `Do(args []string, env *CommandEnv, writer io.Writer) error`
- `CommandEnv` struct: MasterClient *pb.MasterClient, FilerAddress types.ServerAddress, GrpcDialOption grpc.DialOption, option *ShellOption, isLocked bool, topology *topology.Topology
- Built-in commands: lock, unlock, volume.vacuum, volume.balance, volume.move, s3.clean.uploads
- `REPL`: print "goblob> " prompt → shellSplit(line) → lookup command → cmd.Do
- `shellSplit`: handles quoted strings, ignores empty strings
- Lock mechanism uses filer KV for distributed coordination

**Implementation Steps**

1. Create `shell/command.go` with Command interface
2. Create `shell/env.go` with CommandEnv struct
3. Create `shell/shell.go` with REPL implementation
4. Implement `shellSplit(line []string) []string` with quote handling
5. Create built-in commands (lock, unlock, volume.*, s3.*)
6. Implement command registry with lookup
7. Add command history with readline
8. Add tab completion for commands
9. Implement remote execution via gRPC
10. Add color output for better UX

**Pitfalls**
- shellSplit must handle escaped quotes
- Empty strings from multiple spaces must be filtered
- Lock timeout must be reasonable (default 5 minutes)
- Remote execution must handle connection failures
- Tab completion must handle partial matches
- History file permissions must be 0600
- Output buffering must be flushed after each command

**Verification**
```bash
# Test shell command parsing
go test -v -run TestShellParsing ./shell/

# Test command registry
go test -v -run TestCommandRegistry ./shell/

# Test lock mechanism
go test -v -run TestLockMechanism ./shell/

# Test built-in commands
go test -v -run TestBuiltInCommands ./shell/

# Integration test
go build -o blob && ./blob shell
```

**Dependencies**  
Distributed Lock, gRPC Transport, Client SDK

**Related Plan**  
`plans/plan-admin-shell.md`

**Expected Output**  
- `goblob/shell/shell.go`
- `goblob/shell/command_volume.go`
- `goblob/shell/command_cluster.go`
- `goblob/shell/command_lock.go`
- `goblob/shell/command_fs.go`
- Integration tests

---

### Task: Implement Automated Maintenance + Utility Subcommands

**Goal**  
Implement automated maintenance scripts and utility subcommands for cluster health management and optimization.

**Critical Details**
- `startAdminScripts(ms)`: runs on master leader only, ticker every ms.option.MaintenanceConfig.SleepMinutes minutes
- Default script: "volume.balance\nvolume.fix.replication\nvolume.vacuum\ns3.clean.uploads -olderThan=24h"
- `volume.balance`: fetch topology → find overloaded/underloaded nodes → pick volumes to move → optionally execute
- `volume.vacuum`: GET /vol/vacuum/check → if ratio > threshold → GET /vol/vacuum/needle → GET /vol/vacuum/commit
- `volume.fix.replication`: find volumes with insufficient replicas → assign new replicas → copy data
- `s3.clean.uploads`: list /buckets/*/.uploads → delete folders older than threshold
- GarbageThreshold default: 0.3 (30% free space)
- Balance threshold: 20% difference between nodes

**Implementation Steps**

1. Create `command/maintenance.go` with maintenance scripts
2. Create `shell/commands/balance.go` with volume.balance command
3. Create `shell/commands/vacuum.go` with volume.vacuum command
4. Create `shell/commands/fix_replication.go` with volume.fix.replication command
5. Create `shell/commands/clean_uploads.go` with s3.clean.uploads command
6. Implement topology analysis for balance
7. Implement vacuum check and commit logic
8. Implement replication fix logic
9. Implement upload cleanup with age filtering
10. Add dry-run mode for all maintenance commands

**Pitfalls**
- Balance must check available space before moving
- Vacuum commit must be atomic to prevent data loss
- Replication fix must respect rack and data center constraints
- Upload cleanup must not delete active uploads
- Dry-run mode must not make any changes
- Maintenance scripts must run on leader only
- Error handling must continue on individual failures

**Verification**
```bash
# Test maintenance scheduler
go test -v -run TestMaintenanceScheduler ./command/

# Test volume balance
go test -v -run TestVolumeBalance ./shell/commands/

# Test volume vacuum
go test -v -run TestVolumeVacuum ./shell/commands/

# Test replication fix
go test -v -run TestReplicationFix ./shell/commands/

# Test upload cleanup
go test -v -run TestUploadCleanup ./shell/commands/

# Integration test
go build -o blob && ./blob server &
./blob shell -c "volume.balance -dryRun"
```

**Dependencies**  
Storage Engine, Client SDK

**Related Plan**  
`plans/plan-admin-shell.md`

**Expected Output**  
- `goblob/command/backup.go`
- `goblob/command/export.go`
- `goblob/command/compact.go`
- `goblob/command/fix.go`
- `goblob/command/benchmark.go`
- Integration tests


### Phase 6 Checkpoint

Run `blob server` (all-in-one mode) and confirm all subcommands start without errors. Run `blob shell` and execute `cluster.status`, `volume.list`, `fs.ls /`. Run `go test ./goblob/command/... ./goblob/shell/...` before proceeding to Phase 7.
## Phase 7: Production Readiness

**Purpose**: Add monitoring, testing, documentation, and deployment tooling for production use.

### Task: Enhance Observability

**Goal**
Add comprehensive metrics, structured logging, health checks, and debug endpoints across all components using the `goblob/obs/` package.

**Critical Details**
- Package: `goblob/obs/`
- `obs.New("subsystem")` returns `*slog.Logger` with "subsystem" attribute pre-attached
- All Prometheus metrics registered in `init()` on package import
- Metric naming: `goblob_<subsystem>_<metric>` (namespace="goblob")
- Existing metrics: MasterLeadershipGauge, MasterVolumeCount, MasterAssignRequests, VolumeServerNeedleWriteBytes, VolumeServerNeedleReadBytes, VolumeServerNeedleWriteLatency, VolumeServerNeedleReadLatency, VolumeServerDiskFreeBytes, FilerRequestsTotal, FilerStoreLatency
- `MetricsServer`: serves /metrics (promhttp.Handler()), /debug/vars (expvar.Handler()), /debug/pprof/ (net/http/pprof)
- `StartMetricsPusher(cfg PushConfig)`: pushes every cfg.Interval (default 15s) to Pushgateway
- LogFormat: "json" (slog.NewJSONHandler) or "text" (slog.NewTextHandler)
- `SetLevel(level)` uses slog.LevelVar (atomic)
- Startup: -metricsPort flag; if >0: obs.NewMetricsServer(addr).Start()
- pprof on DefaultServeMux; use separate mux for metrics to avoid exposing pprof on metrics port unless intended

**Implementation Steps**

1. Create `goblob/obs/` package with logger wrapper:
   ```go
   func New(subsystem string) *slog.Logger {
       return slog.Default().With("subsystem", subsystem)
   }
   func SetLevel(level LogLevel)
   func SetOutput(w io.Writer)
   ```
2. Declare Prometheus metrics as package variables:
   ```go
   var (
       MasterLeadershipGauge = prometheus.NewGauge(prometheus.GaugeOpts{
           Namespace: "goblob", Subsystem: "master", Name: "is_leader",
           Help: "1 if this master is the Raft leader, 0 otherwise.",
       })
       // ... other metrics
   )
   ```
3. Register all metrics in `init()`:
   ```go
   func init() {
       prometheus.MustRegister(MasterLeadershipGauge, MasterVolumeCount, ...)
   }
   ```
4. Implement MetricsServer:
   ```go
   type MetricsServer struct {
       addr   string
       server *http.Server
   }
   func NewMetricsServer(addr string) *MetricsServer
   func (m *MetricsServer) Start()
   func (m *MetricsServer) Stop(ctx context.Context) error
   ```
5. Add metrics push to Pushgateway:
   ```go
   type PushConfig struct {
       URL      string
       Job      string
       Interval time.Duration // default 15s
   }
   func StartMetricsPusher(cfg PushConfig) (cancel func())
   ```
6. Initialize logging in package init():
   ```go
   func init() {
       opts := &slog.HandlerOptions{Level: LevelInfo}
       handler := slog.NewJSONHandler(os.Stderr, opts)
       slog.SetDefault(slog.New(handler))
   }
   ```
7. Add -metricsPort flag to main binary; if >0, start metrics server
8. Use obs.New() in all servers: master, volume, filer, s3
9. Increment metrics in request handlers: `obs.VolumeServerNeedleWriteBytes.WithLabelValues(vid).Add(float64(n))`
10. Add health check endpoints: GET /health (returns 200 if OK), GET /ready (returns 200 if ready to serve traffic)
11. Register pprof handlers on DefaultServeMux; use separate mux for metrics port

**Pitfalls**
- Forgetting to call prometheus.MustRegister() in init() causes panic on metric access
- Using pprof on the same mux as /metrics exposes profiling publicly; use separate mux
- Logging before slog.SetDefault() loses structured fields; always init logging first
- Not using "subsystem" attribute makes logs unfilterable; always use obs.New()
- Metrics push without error handling silently fails; log push failures

**Verification**
```bash
# Build and start master with metrics
./blob master -metricsPort 9090 &
MASTER_PID=$!

# Check metrics endpoint
curl http://localhost:9090/metrics | grep goblob_master_is_leader

# Check debug vars
curl http://localhost:9090/debug/vars

# Check pprof (should be on separate port if configured)
curl http://localhost:9090/debug/pprof/heap

# Test log output
LOGGER_LEVEL=debug ./blob master 2>&1 | grep -q '"subsystem":"master"'

# Test metrics push (optional)
./blob master -pushgatewayURL http://pushgateway:9091 -pushgatewayJob goblob-test

# Kill master
kill $MASTER_PID
```

**Dependencies**
All servers, Observability infrastructure

**Related Plan**
`plans/plan-observability.md`

**Expected Output**
- `goblob/obs/obs.go`
- `goblob/obs/metrics.go`
- `goblob/obs/metrics_server.go`
- Enhanced metrics in all server components
- `docs/observability/metrics.md`
- `docs/observability/dashboards/`
- Grafana dashboard JSON files

---

### Task: Implement Additional Filer Store Backends

**Goal**
Add support for production-grade metadata stores beyond LevelDB: Redis3, Postgres2, MySQL2, and Cassandra.

**Critical Details**
- All backends implement `FilerStore` interface: InsertEntry, UpdateEntry, FindEntry, DeleteEntry, DeleteFolderChildren, ListDirectoryEntries, ListDirectoryPrefixedEntries, BeginTransaction, CommitTransaction, RollbackTransaction, KvPut, KvGet, KvDelete
- Redis3Store: HSET {dir} {name} {proto bytes}; prefix scan via HSCAN; KV uses flat keys
- Postgres2Store: table `filer_meta(dir_hash BIGINT, dir TEXT, name TEXT, meta BYTEA, PRIMARY KEY(dir_hash, name))`; dir_hash = FNV hash of dir; transactions via sql.Tx stored in context
- MySQL2Store: same schema as Postgres but MySQL syntax
- CassandraStore: table with dir as partition key, name as clustering key
- Backend selection: LoadFilerStoreFromConfig reads filer.toml [backend_name] section; default = leveldb2
- filer.toml format: `[redis3]` section with address/password/database; `[postgres2]` with hostname/port/database/username/password/connection_max_*; `[mysql2]` similar
- Use testcontainers-go for PostgreSQL/Redis/MySQL integration tests

**Implementation Steps**

1. Create `goblob/filer/redis3/redis_store.go`:
   ```go
   type Redis3Store struct {
       client *redis.Client
   }
   func (s *Redis3Store) InsertEntry(ctx context.Context, entry *filer.Entry) error {
       key := fmt.Sprintf("%s\x00%s", entry.FullPath.DirAndName())
       data, _ := proto.Marshal(entry)
       return s.client.HSet(ctx, key[0], key[1], data).Err()
   }
   func (s *Redis3Store) FindEntry(ctx context.Context, fp filer.FullPath) (*filer.Entry, error) {
       dir, name := fp.DirAndName()
       data, err := s.client.HGet(ctx, dir, name).Bytes()
       // unmarshal proto
   }
   ```
2. Create `goblob/filer/postgres2/postgres_store.go`:
   ```go
   type Postgres2Store struct {
       db *sql.DB
   }
   const createTableSQL = `
       CREATE TABLE IF NOT EXISTS filer_meta (
           dir_hash BIGINT,
           dir TEXT NOT NULL,
           name TEXT NOT NULL,
           meta BYTEA,
           PRIMARY KEY (dir_hash, name)
       )`
   func (s *Postgres2Store) InsertEntry(ctx context.Context, entry *filer.Entry) error {
       dirHash := fnv.New64a()
       dirHash.Write([]byte(entry.FullPath.DirAndName().Dir))
       _, err := s.db.ExecContext(ctx,
           "INSERT INTO filer_meta (dir_hash, dir, name, meta) VALUES ($1, $2, $3, $4)",
           dirHash.Sum64(), dir, name, protoBytes)
   }
   ```
3. Create `goblob/filer/mysql2/mysql_store.go` with same schema but MySQL syntax:
   ```sql
   CREATE TABLE IF NOT EXISTS filer_meta (
       dir_hash BIGINT,
       dir VARCHAR(512) NOT NULL,
       name VARCHAR(255) NOT NULL,
       meta LONGBLOB,
       PRIMARY KEY (dir_hash, name)
   )
   ```
4. Create `goblob/filer/cassandra/cassandra_store.go`:
   ```go
   const createTableSQL = `
       CREATE TABLE IF NOT EXISTS filer_meta (
           dir TEXT,
           name TEXT,
           meta BLOB,
           PRIMARY KEY (dir, name)
       ) WITH CLUSTERING ORDER BY (name ASC)`
   ```
5. Implement transaction support for Postgres2/MySQL2:
   ```go
   func (s *Postgres2Store) BeginTransaction(ctx context.Context) (context.Context, error) {
       tx, err := s.db.BeginTx(ctx, nil)
       return context.WithValue(ctx, txKey, tx), err
   }
   func (s *Postgres2Store) CommitTransaction(ctx context.Context) error {
       tx := ctx.Value(txKey).(*sql.Tx)
       return tx.Commit()
   }
   ```
6. Add config loading in `goblob/filer/store_loader.go`:
   ```go
   func LoadFilerStoreFromConfig(config util.Configuration) (filer.FilerStore, error) {
       backend := config.GetString("backend") // default "leveldb2"
       switch backend {
       case "redis3":
           return redis3.NewRedis3Store(config)
       case "postgres2":
           return postgres2.NewPostgres2Store(config)
       case "mysql2":
           return mysql2.NewMySQL2Store(config)
       case "cassandra":
           return cassandra.NewCassandraStore(config)
       default:
           return leveldb2.NewLevelDB2Store(config)
       }
   }
   ```
7. Add integration tests using testcontainers-go:
   ```go
   //go:build integration
   func TestPostgres2Store(t *testing.T) {
       ctx := context.Background()
       pgContainer, err := postgres.Run(ctx, "postgres:16-alpine")
       require.NoError(t, err)
       connStr, _ := pgContainer.ConnectionString(ctx)
       // test store operations
   }
   ```
8. Add backend configuration docs in `docs/filer/store-backends.md`

**Pitfalls**
- Not hashing dir in Postgres/MySQL causes poor performance on large directories; always use dir_hash
- Forgetting to close Redis client leaks connections; defer client.Close()
- Using TEXT instead of BYTEA for meta column in Postgres causes encoding issues
- Not handling context cancellation in transactions leaves connections open
- Using HSCAN without cursor in Redis causes infinite loop on large directories
- Cassandra clustering key must include name for efficient queries

**Verification**
```bash
# Test Redis3 backend
echo "[redis3]
address = localhost:6379
database = 0" > filer.toml
./blob filer -config filer.toml &
FILER_PID=$!
# upload file, check metadata
curl -X PUT http://localhost:8888/buckets/test/file.txt -d "test data"
redis-cli HGET "/buckets/test" "file.txt" | grep -q test
kill $FILER_PID

# Test Postgres2 backend
docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=test postgres:16-alpine
echo "[postgres2]
hostname = localhost
port = 5432
database = filer
username = postgres
password = test
connection_max_idle = 10
connection_max_open = 100" > filer.toml
./blob filer -config filer.toml &
# verify table created
psql -h localhost -U postgres -c "\d filer_meta"

# Run integration tests
go test -tags=integration ./goblob/filer/postgres2/... -v
go test -tags=integration ./goblob/filer/redis3/... -v
```

**Dependencies**
Filer Store interface

**Related Plan**
`plans/plan-filer-store.md`

**Expected Output**
- `goblob/filer/redis3/redis_store.go`
- `goblob/filer/postgres2/postgres_store.go`
- `goblob/filer/mysql2/mysql_store.go`
- `goblob/filer/cassandra/cassandra_store.go`
- `goblob/filer/store_loader.go`
- Integration tests in each backend package
- `docs/filer/store-backends.md`

---

### Task: Create Integration Test Suite

**Goal**
Build comprehensive integration tests covering multi-server scenarios: cluster startup, failover, replication, compaction, and S3 API compatibility.

**Critical Details**
- `test/integration/` package with build tag `//go:build integration`
- Test cluster launcher: `startCluster(t *testing.T, opt ClusterOpt) *TestCluster`; creates temp dirs, starts master + volume(s) + filer in-process with random ports; cleanup via t.Cleanup
- Key test scenarios:
  - TestEndToEndBlobUploadDownload: assign → PUT /fid → GET /fid, assert bytes match
  - TestReplicationVerification: 2 volumes, write to primary, read from replica
  - TestMasterFailover: 3 masters, kill leader, assert new leader elected within 5s, assign still works
  - TestVolumeCompaction: write 100 blobs, delete 70, vacuum, assert volume size reduced
  - TestFilerMetadataOps: create dir, upload file, list dir, rename, delete
  - TestS3ApiCompatible: use aws-sdk-go client, PutObject/GetObject/ListObjects/DeleteObject
- Run integration tests with: `go test -tags=integration -race ./test/integration/... -timeout=5m`
- Use `net.Listen("tcp", "127.0.0.1:0")` to allocate random ports, avoid port conflicts
- GitHub Actions workflow: `.github/workflows/ci.yml`; matrix: go version 1.22 + 1.23; jobs: lint (golangci-lint), test (unit), test-integration

**Implementation Steps**

1. Create `test/integration/` package with build tag:
   ```go
   //go:build integration
   package integration
   ```
2. Implement TestCluster struct:
   ```go
   type TestCluster struct {
       tempDir string
       masters []*MasterServer
       volumes []*VolumeServer
       filer   *FilerServer
       s3      *S3Gateway
   }
   type ClusterOpt struct {
       NumMasters int
       NumVolumes int
       EnableFiler bool
       EnableS3    bool
   }
   func startCluster(t *testing.T, opt ClusterOpt) *TestCluster {
       tempDir := t.TempDir()
       // allocate random ports via net.Listen("tcp", "127.0.0.1:0")
       // start servers in goroutines
       t.Cleanup(func() {
           // stop all servers
       })
       return cluster
   }
   ```
3. Write TestEndToEndBlobUploadDownload:
   ```go
   func TestEndToEndBlobUploadDownload(t *testing.T) {
       cluster := startCluster(t, ClusterOpt{NumMasters: 1, NumVolumes: 2, EnableFiler: true})
       client := cluster.FilerClient()
       testData := []byte("test data for e2e")
       // assign fid
       fid, err := client.Assign("")
       require.NoError(t, err)
       // upload via volume server
       resp, err := http.Put(fmt.Sprintf("http://localhost:%s/%s", volumePort, fid), testData)
       require.Equal(t, 201, resp.StatusCode)
       // download and verify
       downloaded, err := http.Get(fmt.Sprintf("http://localhost:%s/%s", volumePort, fid))
       require.Equal(t, testData, downloaded)
   }
   ```
4. Write TestReplicationVerification:
   ```go
   func TestReplicationVerification(t *testing.T) {
       cluster := startCluster(t, ClusterOpt{NumMasters: 1, NumVolumes: 2, EnableFiler: true})
       // upload with replication=002
       fid := uploadWithReplication(t, cluster, "data", "002")
       // kill primary volume
       cluster.volumes[0].Stop()
       // read from replica, assert success
       data := downloadFromFiler(t, cluster, fid)
       require.Equal(t, "data", data)
   }
   ```
5. Write TestMasterFailover:
   ```go
   func TestMasterFailover(t *testing.T) {
       cluster := startCluster(t, ClusterOpt{NumMasters: 3, NumVolumes: 1})
       leaderIdx := findLeader(t, cluster)
       // kill leader
       cluster.masters[leaderIdx].Stop()
       // wait for new leader (max 5s)
       ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
       defer cancel()
       for {
           if findLeader(t, cluster) >= 0 {
               break
           }
           select {
           case <-ctx.Done():
               t.Fatal("no new leader elected within 5s")
           case <-time.After(100 * time.Millisecond):
           }
       }
       // assign should still work
       fid, err := cluster.masters[findLeader(t, cluster)].Assign("")
       require.NoError(t, err)
   }
   ```
6. Write TestVolumeCompaction:
   ```go
   func TestVolumeCompaction(t *testing.T) {
       cluster := startCluster(t, ClusterOpt{NumMasters: 1, NumVolumes: 1})
       // upload 100 blobs
       for i := 0; i < 100; i++ {
           uploadBlob(t, cluster, []byte(fmt.Sprintf("blob-%d", i)))
       }
       volSizeBefore := getVolumeSize(t, cluster.volumes[0].DataDir())
       // delete 70 blobs
       for i := 0; i < 70; i++ {
           deleteBlob(t, cluster, i)
       }
       // run vacuum
       cluster.volumes[0].Vacuum()
       volSizeAfter := getVolumeSize(t, cluster.volumes[0].DataDir())
       require.Less(t, volSizeAfter, volSizeBefore)
   }
   ```
7. Write TestFilerMetadataOps:
   ```go
   func TestFilerMetadataOps(t *testing.T) {
       cluster := startCluster(t, ClusterOpt{NumMasters: 1, NumVolumes: 1, EnableFiler: true})
       client := cluster.FilerClient()
       // create directory
       err := client.CreateDirectory("/test", 0755)
       require.NoError(t, err)
       // upload file
       err = client.UploadFile("/test/file.txt", []byte("content"))
       require.NoError(t, err)
       // list directory
       entries, err := client.ListDirectory("/test")
       require.Len(t, entries, 1)
       require.Equal(t, "file.txt", entries[0].Name())
       // rename
       err = client.Rename("/test/file.txt", "/test/renamed.txt")
       require.NoError(t, err)
       // delete
       err = client.Delete("/test/renamed.txt")
       require.NoError(t, err)
   }
   ```
8. Write TestS3ApiCompatible:
   ```go
   func TestS3ApiCompatible(t *testing.T) {
       cluster := startCluster(t, ClusterOpt{NumMasters: 1, NumVolumes: 1, EnableFiler: true, EnableS3: true})
       cfg, _ := aws.LoadDefaultConfig(context.Background(),
           aws.WithRegion("us-east-1"),
           aws.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
               func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
                   return aws.Endpoint{URL: fmt.Sprintf("http://localhost:%d", cluster.s3Port)}, nil
               })),
       )
       client := s3.NewFromConfig(cfg)
       // PutObject
       _, err := client.PutObject(context.Background(), &s3.PutObjectInput{
           Bucket: aws.String("test-bucket"),
           Key:    aws.String("test-key"),
           Body:   bytes.NewReader([]byte("test data")),
       })
       require.NoError(t, err)
       // GetObject
       resp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
           Bucket: aws.String("test-bucket"),
           Key:    aws.String("test-key"),
       })
       require.NoError(t, err)
       data, _ := io.ReadAll(resp.Body)
       require.Equal(t, []byte("test data"), data)
       // ListObjects
       list, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
           Bucket: aws.String("test-bucket"),
       })
       require.NoError(t, err)
       require.Len(t, list.Contents, 1)
       // DeleteObject
       _, err = client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
           Bucket: aws.String("test-bucket"),
           Key:    aws.String("test-key"),
       })
       require.NoError(t, err)
   }
   ```
9. Create GitHub Actions workflow `.github/workflows/ci.yml`:
   ```yaml
   name: CI
   on: [push, pull_request]
   jobs:
     lint:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - uses: actions/setup-go@v5
           with:
             go-version: '1.22'
         - run: golangci-lint run ./goblob/...
     test:
       runs-on: ubuntu-latest
       strategy:
         matrix:
           go-version: ['1.22', '1.23']
       steps:
         - uses: actions/checkout@v4
         - uses: actions/setup-go@v5
           with:
             go-version: ${{ matrix.go-version }}
         - run: go test ./goblob/... -race -count=1
     test-integration:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - uses: actions/setup-go@v5
           with:
             go-version: '1.22'
         - run: go test -tags=integration -race ./test/integration/... -timeout=5m
   ```

**Pitfalls**
- Hardcoding port numbers causes conflicts in CI; always use random ports via net.Listen("tcp", "127.0.0.1:0")
- Not cleaning up temp dirs leaves artifacts; use t.TempDir() and t.Cleanup()
- Race conditions in failover tests; add retries with context timeout
- Not waiting for cluster readiness causes flaky tests; add health check loop
- Using shared global state between tests causes interference; each test gets its own cluster
- Long-running tests without timeout hang CI; use -timeout=5m

**Verification**
```bash
# Run unit tests
go test ./goblob/... -race -count=1

# Run integration tests
go test -tags=integration -race ./test/integration/... -timeout=5m -v

# Run specific test
go test -tags=integration -run TestEndToEndBlobUploadDownload ./test/integration/...

# Run with coverage
go test -tags=integration -coverprofile=coverage.out ./test/integration/...
go tool cover -html=coverage.out

# Run linter
golangci-lint run ./goblob/...

# Run CI locally (act)
act -j test-integration
```

**Dependencies**
All servers

**Related Plan**
N/A (testing infrastructure)

**Expected Output**
- `test/integration/cluster_test.go`
- `test/integration/replication_test.go`
- `test/integration/failover_test.go`
- `test/integration/compaction_test.go`
- `test/integration/filer_test.go`
- `test/integration/s3_test.go`
- `.github/workflows/ci.yml`

---

### Task: Create Deployment Configurations

**Goal**
Provide deployment templates for various platforms: Docker, docker-compose, Kubernetes, Helm, and systemd.

**Critical Details**
- Multi-stage Dockerfile: `FROM golang:1.22 AS builder` → `go build -ldflags "..." -o /blob` → `FROM gcr.io/distroless/base-debian12` → `COPY --from=builder /blob /blob`
- docker-compose.yml: services: master (3 replicas if HA, or 1 for dev), volume (2+), filer, s3; volumes for data dirs; depends_on with health checks
- Kubernetes: master as StatefulSet (stable hostname needed for Raft peer discovery); volume as StatefulSet (stable storage); filer + s3 as Deployment
- Helm chart: `helm/goblob/` with Chart.yaml, values.yaml (port overrides, replica counts, storage class), templates/
- systemd: `/etc/systemd/system/goblob-master.service` with ExecStart, Restart=always, User=goblob

**Implementation Steps**

1. Create multi-stage Dockerfile:
   ```dockerfile
   # Stage 1: Build
   FROM golang:1.22 AS builder
   WORKDIR /src
   COPY . .
   RUN go build -ldflags "-s -w" -o /blob ./goblob/

   # Stage 2: Runtime
   FROM gcr.io/distroless/base-debian12
   COPY --from=builder /blob /blob
   EXPOSE 8333 8334 8888 8336 9090
   ENTRYPOINT ["/blob"]
   ```
2. Create docker-compose.yml:
   ```yaml
   version: '3.8'
   services:
     master:
       image: goblob:latest
       command: master -mdir /data/master
       ports:
         - "8333:8333"
         - "9333:9333"  # Raft
       volumes:
         - master-data:/data/master
       deploy:
         replicas: 3  # HA setup
       healthcheck:
         test: ["CMD", "curl", "-f", "http://localhost:8333/health"]
         interval: 10s
         timeout: 5s
         retries: 3

     volume:
       image: goblob:latest
       command: volume -dir /data/volume -mserver master:9333
       ports:
         - "8334:8334"
         - "9090:9090"  # metrics
       volumes:
         - volume-data:/data/volume
       deploy:
         replicas: 2
       depends_on:
         master:
           condition: service_healthy

     filer:
       image: goblob:latest
       command: filer -mdir /data/filer
       ports:
         - "8888:8888"
         - "9091:9091"  # metrics
       volumes:
         - filer-data:/data/filer
       depends_on:
         master:
           condition: service_healthy

     s3:
       image: goblob:latest
       command: s3 -filer localhost:8888
       ports:
         - "8333:8333"  # S3 API
       depends_on:
         - filer

   volumes:
     master-data:
     volume-data:
     filer-data:
   ```
3. Create Kubernetes manifests:
   - `k8s/master-statefulset.yaml`:
   ```yaml
   apiVersion: apps/v1
   kind: StatefulSet
   metadata:
     name: goblob-master
   spec:
     serviceName: goblob-master
     replicas: 3
     selector:
       matchLabels:
         app: goblob-master
     template:
       metadata:
         labels:
           app: goblob-master
       spec:
         containers:
         - name: master
           image: goblob:latest
           command: ["/blob", "master"]
           args:
             - "-mdir=/data"
             - "-peers=goblob-master-0.goblob-master:9333,goblob-master-1.goblob-master:9333,goblob-master-2.goblob-master:9333"
           ports:
           - containerPort: 8333
           - containerPort: 9333
           volumeMounts:
           - name: data
             mountPath: /data
     volumeClaimTemplates:
     - metadata:
         name: data
       spec:
         accessModes: ["ReadWriteOnce"]
         storageClassName: "standard"
         resources:
           requests:
             storage: 10Gi
   ```
   - `k8s/volume-statefulset.yaml`:
   ```yaml
   apiVersion: apps/v1
   kind: StatefulSet
   metadata:
     name: goblob-volume
   spec:
     serviceName: goblob-volume
     replicas: 2
     selector:
       matchLabels:
         app: goblob-volume
     template:
       metadata:
         labels:
           app: goblob-volume
       spec:
         containers:
         - name: volume
           image: goblob:latest
           command: ["/blob", "volume"]
           args:
             - "-dir=/data"
             - "-mserver=goblob-master-0.goblob-master:9333"
           ports:
           - containerPort: 8334
           volumeMounts:
           - name: data
             mountPath: /data
     volumeClaimTemplates:
     - metadata:
         name: data
       spec:
         accessModes: ["ReadWriteOnce"]
         resources:
           requests:
             storage: 100Gi
   ```
   - `k8s/filer-deployment.yaml`:
   ```yaml
   apiVersion: apps/v1
   kind: Deployment
   metadata:
     name: goblob-filer
   spec:
     replicas: 2
     selector:
       matchLabels:
         app: goblob-filer
     template:
       metadata:
         labels:
           app: goblob-filer
       spec:
         containers:
         - name: filer
           image: goblob:latest
           command: ["/blob", "filer"]
           args:
             - "-mdir=/data"
           ports:
           - containerPort: 8888
           volumeMounts:
           - name: data
             mountPath: /data
         volumes:
         - name: data
           emptyDir: {}
   ```
   - `k8s/s3-deployment.yaml`:
   ```yaml
   apiVersion: apps/v1
   kind: Deployment
   metadata:
     name: goblob-s3
   spec:
     replicas: 2
     selector:
       matchLabels:
         app: goblob-s3
     template:
       metadata:
         labels:
           app: goblob-s3
       spec:
         containers:
         - name: s3
           image: goblob:latest
           command: ["/blob", "s3"]
           args:
             - "-filer=goblob-filer:8888"
           ports:
           - containerPort: 8333
   ```
4. Create Helm chart `helm/goblob/`:
   - `Chart.yaml`:
   ```yaml
   apiVersion: v2
   name: goblob
   description: A distributed blob storage system
   type: application
   version: 1.0.0
   appVersion: "1.0"
   ```
   - `values.yaml`:
   ```yaml
   master:
     replicas: 3
     image:
       repository: goblob
       tag: latest
     storage:
       class: standard
       size: 10Gi
     ports:
       http: 8333
       raft: 9333

   volume:
     replicas: 2
     storage:
       size: 100Gi
     ports:
       http: 8334

   filer:
     replicas: 2
     ports:
       http: 8888

   s3:
     replicas: 2
     ports:
       http: 8333
   ```
   - `templates/master-statefulset.yaml`, `templates/volume-statefulset.yaml`, etc. using Helm templates
5. Create systemd service files:
   - `/etc/systemd/system/goblob-master.service`:
   ```ini
   [Unit]
   Description=GoBlob Master Server
   After=network.target

   [Service]
   Type=simple
   User=goblob
   Group=goblob
   ExecStart=/usr/local/bin/blob master -mdir /var/lib/goblob/master
   Restart=always
   RestartSec=5
   StandardOutput=journal
   StandardError=journal
   SyslogIdentifier=goblob-master

   [Install]
   WantedBy=multi-user.target
   ```
   - Similar files for volume, filer, s3
6. Create deployment documentation in `docs/deployment/`

**Pitfalls**
- Using Deployment instead of StatefulSet for master/volume causes issues with Raft peer discovery (requires stable hostnames)
- Not setting health checks causes Kubernetes to route traffic to not-ready pods
- Forgetting to expose Raft port (9333) prevents master cluster communication
- Using ephemeral storage for volumes causes data loss on pod restart
- Not setting resource limits causes resource exhaustion
- Systemd service without Restart=always leaves server down after crash

**Verification**
```bash
# Build Docker image
docker build -t goblob:latest .

# Run docker-compose
docker-compose up -d
docker-compose ps
docker-compose logs -f

# Test Kubernetes deployment
kubectl apply -f k8s/
kubectl get statefulsets
kubectl get pods
kubectl port-forward svc/goblob-master 8333:8333
curl http://localhost:8333/health

# Test Helm chart
helm install goblob ./helm/goblob/
helm list
helm status goblob
helm test goblob

# Test systemd
sudo cp systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start goblob-master
sudo systemctl status goblob-master
```

**Dependencies**
All servers, Configuration

**Related Plan**
N/A (deployment infrastructure)

**Expected Output**
- `Dockerfile`
- `docker-compose.yml`
- `k8s/master-statefulset.yaml`
- `k8s/volume-statefulset.yaml`
- `k8s/filer-deployment.yaml`
- `k8s/s3-deployment.yaml`
- `helm/goblob/Chart.yaml`
- `helm/goblob/values.yaml`
- `helm/goblob/templates/*.yaml`
- `systemd/goblob-master.service`
- `docs/deployment/docker.md`
- `docs/deployment/kubernetes.md`
- `docs/deployment/systemd.md`

---

### Task: Write User Documentation

**Goal**
Create comprehensive documentation for users and operators covering getting started, configuration, API reference, S3 compatibility, operations, troubleshooting, performance, and contributing.

**Critical Details**
- docs/ structure: getting-started.md, configuration.md, api-reference.md, s3-compatibility.md, operations/backup.md, operations/scaling.md, operations/restore.md, troubleshooting.md, performance.md, contributing.md
- S3 compatibility matrix: list supported/unsupported S3 API operations (PutObject ✓, GetObject ✓, Multipart ✓, Versioning ✓, Object Lock ✓, Replication ✗, Analytics ✗ etc.)
- MkDocs config: mkdocs.yml with site_name, nav tree, theme: material

**Implementation Steps**

1. Create `docs/getting-started.md`:
   - Quick start: docker-compose up
   - Building from source: go build
   - First upload: curl, AWS CLI
   - Verify: download and checksum

2. Create `docs/configuration.md`:
   - master.toml reference
   - volume.toml reference
   - filer.toml reference
   - security.toml reference
   - Environment variables override

3. Create `docs/api-reference.md`:
   - HTTP API endpoints
   - gRPC services
   - Request/response examples

4. Create `docs/s3-compatibility.md`:
   - Supported operations matrix:
     ```markdown
     | Operation | Status | Notes |
     |-----------|--------|-------|
     | PutObject | ✓ | Full support |
     | GetObject | ✓ | Full support |
     | DeleteObject | ✓ | Full support |
     | ListObjectsV2 | ✓ | Full support |
     | CreateMultipartUpload | ✓ | Full support |
     | UploadPart | ✓ | Full support |
     | CompleteMultipartUpload | ✓ | Full support |
     | AbortMultipartUpload | ✓ | Full support |
     | HeadObject | ✓ | Full support |
     | CopyObject | ✓ | Full support |
     | PutObjectTagging | ✓ | Full support |
     | GetObjectTagging | ✓ | Full support |
     | DeleteObjectTagging | ✓ | Full support |
     | Versioning | Partial | Marker versioning only |
     | Object Lock | ✓ | Full support |
     | Replication | ✗ | Not supported |
     | Analytics | ✗ | Not supported |
     ```
   - Known limitations
   - Client compatibility (AWS SDK, boto3, MinIO client)

5. Create `docs/operations/backup.md`:
   - Backup master snapshots (Raft)
   - Backup volume data
   - Backup filer metadata
   - Automation scripts

6. Create `docs/operations/scaling.md`:
   - Add volume servers
   - Add master nodes
   - Grow volumes
   - Rebalance data

7. Create `docs/operations/restore.md`:
   - Restore from master snapshot
   - Restore volume data
   - Restore filer metadata
   - Disaster recovery

8. Create `docs/troubleshooting.md`:
   - Common errors and solutions
   - Debug modes
   - Log analysis
   - Metrics interpretation

9. Create `docs/performance.md`:
   - Tuning parameters
   - Hardware recommendations
   - Benchmarking guide
   - Bottleneck identification

10. Create `docs/contributing.md`:
    - Development setup
    - Code style
    - Testing requirements
    - PR process

11. Create `mkdocs.yml`:
    ```yaml
    site_name: GoBlob Documentation
    theme:
      name: material
    nav:
      - Home: index.md
      - Getting Started: getting-started.md
      - Configuration: configuration.md
      - API Reference: api-reference.md
      - S3 Compatibility: s3-compatibility.md
      - Operations:
          - Backup: operations/backup.md
          - Scaling: operations/scaling.md
          - Restore: operations/restore.md
      - Troubleshooting: troubleshooting.md
      - Performance: performance.md
      - Contributing: contributing.md
    ```

**Pitfalls**
- Documentation drift from code causes user confusion; update docs with every API change
- Missing S3 operation status causes user frustration; clearly mark unsupported ops
- Not including example commands makes docs hard to follow; always show examples
- Outdated config reference causes misconfiguration; auto-generate from code structs

**Verification**
```bash
# Build MkDocs site
pip install mkdocs-material
mkdocs build
mkdocs serve

# Check for broken links
mkdocs build --strict

# Test all example commands in docs
# (manual verification)
```

**Dependencies**
All components

**Related Plan**
N/A (documentation)

**Expected Output**
- `docs/getting-started.md`
- `docs/configuration.md`
- `docs/api-reference.md`
- `docs/s3-compatibility.md`
- `docs/operations/backup.md`
- `docs/operations/scaling.md`
- `docs/operations/restore.md`
- `docs/troubleshooting.md`
- `docs/performance.md`
- `docs/contributing.md`
- `mkdocs.yml`

---

### Task: Implement Performance Benchmarks

**Goal**
Create benchmark suite for measuring system performance across different scenarios: upload, download, concurrent operations, large files, metadata operations, with regression detection.

**Critical Details**
- Use Go's testing.B framework: `func BenchmarkUpload(b *testing.B)` in `benchmark/` package
- Scenarios: single blob upload (4KB, 64KB, 1MB, 16MB), concurrent uploads (10/100/1000 goroutines), download, list metadata (1000 entries), filer operations
- Report: throughput (MB/s), latency (p50/p99), ops/sec
- Regression detection: compare against baseline stored in benchmark/baseline.json using benchstat

**Implementation Steps**

1. Create `benchmark/` package:
   ```go
   package benchmark
   ```
2. Implement upload benchmarks:
   ```go
   func BenchmarkUpload4KB(b *testing.B) {
       benchmarkUpload(b, 4*1024)
   }
   func BenchmarkUpload64KB(b *testing.B) {
       benchmarkUpload(b, 64*1024)
   }
   func BenchmarkUpload1MB(b *testing.B) {
       benchmarkUpload(b, 1024*1024)
   }
   func BenchmarkUpload16MB(b *testing.B) {
       benchmarkUpload(b, 16*1024*1024)
   }
   func benchmarkUpload(b *testing.B, size int64) {
       cluster := startBenchmarkCluster(b)
       data := make([]byte, size)
       rand.Read(data)
       b.ResetTimer()
       for i := 0; i < b.N; i++ {
           fid, err := cluster.master.Assign("")
           if err != nil {
               b.Fatal(err)
           }
           err = uploadToVolume(cluster, fid, data)
           if err != nil {
               b.Fatal(err)
           }
       }
   }
   ```
3. Implement concurrent upload benchmarks:
   ```go
   func BenchmarkUploadConcurrent10(b *testing.B) {
       benchmarkUploadConcurrent(b, 10)
   }
   func BenchmarkUploadConcurrent100(b *testing.B) {
       benchmarkUploadConcurrent(b, 100)
   }
   func BenchmarkUploadConcurrent1000(b *testing.B) {
       benchmarkUploadConcurrent(b, 1000)
   }
   func benchmarkUploadConcurrent(b *testing.B, concurrency int) {
       cluster := startBenchmarkCluster(b)
       data := make([]byte, 1024*1024) // 1MB
       rand.Read(data)
       b.ResetTimer()
       b.RunParallel(func(pb *testing.PB) {
           for pb.Next() {
               fid, err := cluster.master.Assign("")
               if err != nil {
                   b.Error(err)
                   continue
               }
               uploadToVolume(cluster, fid, data)
           }
       })
   }
   ```
4. Implement download benchmarks:
   ```go
   func BenchmarkDownload1MB(b *testing.B) {
       cluster := startBenchmarkCluster(b)
       data := make([]byte, 1024*1024)
       fid := uploadBenchmarkBlob(b, cluster, data)
       b.ResetTimer()
       for i := 0; i < b.N; i++ {
           downloaded, err := downloadFromVolume(cluster, fid)
           if err != nil {
               b.Fatal(err)
           }
           if len(downloaded) != len(data) {
               b.Fatalf("size mismatch: %d != %d", len(downloaded), len(data))
           }
       }
   }
   ```
5. Implement metadata listing benchmark:
   ```go
   func BenchmarkList1000Entries(b *testing.B) {
       cluster := startBenchmarkCluster(b)
       // upload 1000 files
       for i := 0; i < 1000; i++ {
           uploadToFiler(b, cluster, fmt.Sprintf("file-%d.txt", i), []byte("data"))
       }
       b.ResetTimer()
       for i := 0; i < b.N; i++ {
           entries, err := cluster.filer.ListDirectory("/")
           if err != nil {
               b.Fatal(err)
           }
           if len(entries) != 1000 {
               b.Fatalf("expected 1000 entries, got %d", len(entries))
           }
       }
   }
   ```
6. Implement filer operation benchmarks:
   ```go
   func BenchmarkFilerCreateFile(b *testing.B) {
       cluster := startBenchmarkCluster(b)
       b.ResetTimer()
       for i := 0; i < b.N; i++ {
           err := cluster.filer.CreateFile(fmt.Sprintf("/file-%d.txt", i), []byte("data"))
           if err != nil {
               b.Fatal(err)
           }
       }
   }
   ```
7. Add benchmark result reporting:
   ```go
   // Use b.ReportMetric() to report custom metrics
   func BenchmarkUploadWithMetrics(b *testing.B) {
       start := time.Now()
       var totalBytes int64
       for i := 0; i < b.N; i++ {
           totalBytes += uploadSize
       }
       elapsed := time.Since(start)
       b.ReportMetric(float64(totalBytes)/elapsed.Seconds()/1024/1024, "MB/s")
   }
   ```
8. Create baseline file `benchmark/baseline.json`:
   ```json
   {
       "BenchmarkUpload4KB": {"ns/op": 1000000, "allocs/op": 100},
       "BenchmarkUpload64KB": {"ns/op": 2000000, "allocs/op": 500}
   }
   ```
9. Add regression detection script:
   ```bash
   #!/bin/bash
   # benchmark/regression_check.sh
   go test -bench=. -benchmem ./benchmark/... > new.txt
   benchstat baseline.txt new.txt
   ```

**Pitfalls**
- Not resetting timer causes benchmark to include setup time; always call b.ResetTimer()
- Forgetting to run GC before benchmark causes inconsistent results; add runtime.GC()
- Not using b.RunParallel() for concurrent benchmarks underutilizes CPU
- Small benchmarks without -benchtime produce noisy results; use -benchtime=10s for stable measurements
- Not warming up the system causes first-run overhead in measurements

**Verification**
```bash
# Run all benchmarks
go test -bench=. -benchmem ./benchmark/...

# Run specific benchmark
go test -bench=BenchmarkUpload1MB -benchtime=10s ./benchmark/...

# Run with CPU profiling
go test -bench=. -cpuprofile=cpu.prof ./benchmark/...
go tool pprof cpu.prof

# Compare with baseline
go test -bench=. -benchmem ./benchmark/... > new.txt
benchstat baseline.txt new.txt

# Run benchmarks on different Go versions
go1.22 test -bench=. ./benchmark/...
go1.23 test -bench=. ./benchmark/...
```

**Dependencies**
All servers, Client SDK

**Related Plan**
N/A (benchmarking infrastructure)

**Expected Output**
- `benchmark/upload_bench.go`
- `benchmark/download_bench.go`
- `benchmark/concurrent_bench.go`
- `benchmark/metadata_bench.go`
- `benchmark/baseline.txt`
- `benchmark/regression_check.sh`
- `docs/benchmarks.md`

---

### Task: Security Hardening

**Goal**
Implement additional security features: rate limiting, request size limits, audit logging, security headers, input validation, TLS cert rotation, and fuzz testing.

**Critical Details**
- Rate limiting per client IP: implement as http.Handler middleware using token bucket (golang.org/x/time/rate)
- Request size limits: http.MaxBytesReader(w, r.Body, maxBytes) at handler entry
- Audit logging: log authentication events (auth success/failure), admin operations, data mutations at WARN/INFO with source IP and user identity
- Security headers: X-Content-Type-Options: nosniff, X-Frame-Options: DENY, X-XSS-Protection: 1; mode=block on all HTTP responses
- Input validation: validate VolumeId range (uint32), file path traversal prevention (reject ".." in path), check filename length <= MaxFilenameLength
- certWatcher: uses `crypto/tls` GetCertificate callback for zero-downtime TLS rotation
- FuzzVerifyJwt: feed arbitrary strings to VerifyJwt, assert no panic

**Implementation Steps**

1. Implement rate limiting middleware:
   ```go
   // goblob/security/rate_limit.go
   package security

   import (
       "golang.org/x/time/rate"
       "net/http"
       "sync"
   )

   type RateLimiter struct {
       limiters map[string]*rate.Limiter
       mu       sync.RWMutex
       r        rate.Limit // requests per second
       b        int        // burst size
   }

   func NewRateLimiter(r rate.Limit, b int) *RateLimiter {
       return &RateLimiter{
           limiters: make(map[string]*rate.Limiter),
           r:        r,
           b:        b,
       }
   }

   func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
       rl.mu.Lock()
       defer rl.mu.Unlock()
       if limiter, ok := rl.limiters[ip]; ok {
           return limiter
       }
       limiter := rate.NewLimiter(rl.r, rl.b)
       rl.limiters[ip] = limiter
       return limiter
   }

   func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
       return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
           ip := GetActualRemoteHost(r)
           if !rl.getLimiter(ip).Allow() {
               http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
               return
           }
           next.ServeHTTP(w, r)
       })
   }
   ```
2. Implement request size limit middleware:
   ```go
   // goblob/security/size_limit.go
   func MaxBytesSizeLimit(maxBytes int64) func(http.Handler) http.Handler {
       return func(next http.Handler) http.Handler {
           return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
               r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
               next.ServeHTTP(w, r)
           })
       }
   }
   ```
3. Implement audit logging:
   ```go
   // goblob/security/audit.go
   package security

   import (
       "goblob/obs"
       "net/http"
       "time"
   )

   func AuditLog(next http.Handler) http.Handler {
       return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
           start := time.Now()
           // capture response status
           ww := &responseWriter{ResponseWriter: w, status: 200}
           next.ServeHTTP(ww, r)

           // log audit events
           if ww.status >= 400 || isMutation(r.Method) || isAuthenticated(r) {
               obs.Warn("audit log",
                   "method", r.Method,
                   "path", r.URL.Path,
                   "status", ww.status,
                   "remote_addr", GetActualRemoteHost(r),
                   "user_id", GetUserID(r),
                   "duration_ms", time.Since(start).Milliseconds(),
               )
           }
       })
   }

   type responseWriter struct {
       http.ResponseWriter
       status int
   }

   func (rw *responseWriter) WriteHeader(status int) {
       rw.status = status
       rw.ResponseWriter.WriteHeader(status)
   }
   ```
4. Implement security headers middleware:
   ```go
   // goblob/security/headers.go
   func SecurityHeaders(next http.Handler) http.Handler {
       return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
           w.Header().Set("X-Content-Type-Options", "nosniff")
           w.Header().Set("X-Frame-Options", "DENY")
           w.Header().Set("X-XSS-Protection", "1; mode=block")
           w.Header().Set("Content-Security-Policy", "default-src 'self'")
           w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
           next.ServeHTTP(w, r)
       })
   }
   ```
5. Implement input validation:
   ```go
   // goblob/security/validation.go
   package security

   import (
       "strconv"
       "strings"
   )

   const MaxFilenameLength = 255

   func ValidateVolumeId(vid string) error {
       id, err := strconv.ParseUint(vid, 10, 32)
       if err != nil {
           return ErrInvalidVolumeId
       }
       if id > uint32(0xFFFFFFFF) {
           return ErrVolumeIdOutOfRange
       }
       return nil
   }

   func ValidatePath(path string) error {
       if strings.Contains(path, "..") {
           return ErrPathTraversalDetected
       }
       if strings.HasPrefix(path, "/") {
           return ErrAbsolutePathNotAllowed
       }
       return nil
   }

   func ValidateFilename(name string) error {
       if len(name) > MaxFilenameLength {
           return ErrFilenameTooLong
       }
       if name == "." || name == ".." {
           return ErrInvalidFilename
       }
       return nil
   }
   ```
6. Implement certWatcher (already in security plan):
   ```go
   // goblob/security/cert_watcher.go
   type certWatcher struct {
       certFile string
       keyFile  string
       mu       sync.RWMutex
       cert     *tls.Certificate
   }

   func NewCertWatcher(certFile, keyFile string) (*certWatcher, error) {
       cw := &certWatcher{certFile: certFile, keyFile: keyFile}
       if err := cw.reload(); err != nil {
           return nil, err
       }
       go cw.watchLoop()
       return cw, nil
   }

   func (cw *certWatcher) GetTLSConfig() *tls.Config {
       return &tls.Config{
           GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
               cw.mu.RLock()
               defer cw.mu.RUnlock()
               return cw.cert, nil
           },
       }
   }
   ```
7. Add fuzz test for JWT verification:
   ```go
   // goblob/security/jwt_fuzz_test.go
   func FuzzVerifyJwt(f *testing.F) {
       // Add seed corpus
       f.Add("valid.token.here")
       f.Add("")
       f.Add("invalid")

       signingKey := security.SigningKey("test-key")

       f.Fuzz(func(t *testing.T, token string) {
           // Should never panic, only return error
           defer func() {
               if r := recover(); r != nil {
                   t.Errorf("VerifyJwt panicked with: %v", r)
               }
           }()
           security.VerifyJwt(signingKey, token)
       })
   }
   ```
8. Apply all security middleware to servers:
   ```go
   // In volume server setup
   handler = security.RateLimit(100, 10).Middleware(handler)
   handler = security.MaxBytesSizeLimit(100 * 1024 * 1024)(handler) // 100MB
   handler = security.AuditLog(handler)
   handler = security.SecurityHeaders(handler)
   ```
9. Add security scanning to CI:
   ```yaml
   # .github/workflows/security.yml
   jobs:
     security:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - name: Run Gosec
           uses: securego/gosec@master
           with:
             args: ./goblob/...
         - name: Run go-vulncheck
           run: |
             go install golang.org/x/vuln/cmd/govulncheck@latest
             govulncheck ./goblob/...
   ```

**Pitfalls**
- Rate limiting without IP cleanup causes memory leak; add periodic cleanup of old limiters
- Request size limit applied after body read causes DoS; apply at handler entry
- Not masking sensitive data in audit logs leaks credentials; redact passwords, tokens
- Security headers missing on error responses; apply headers before any write
- Path validation not rejecting encoded paths allows bypass; decode URL before validation
- Fuzz tests without seed corpus miss edge cases; add known attack vectors

**Verification**
```bash
# Test rate limiting
for i in {1..150}; do curl http://localhost:8334/health; done | grep -c "429"

# Test request size limit
curl -X POST http://localhost:8334/ -d @largefile.bin # should return 413

# Test audit logging
./blob volume 2>&1 | grep "audit log"

# Test security headers
curl -I http://localhost:8334/health | grep -E "X-Content-Type-Options|X-Frame-Options"

# Test input validation
curl "http://localhost:8334/../etc/passwd" # should return 400

# Test cert rotation
# update cert file, verify new cert used without restart

# Run fuzz tests
go test -fuzz=FuzzVerifyJwt ./goblob/security/...

# Run security scanner
gosec ./goblob/...
govulncheck ./goblob/...
```

**Dependencies**
Security infrastructure, All servers

**Related Plan**
`plans/plan-security.md`

**Expected Output**
- `goblob/security/rate_limit.go`
- `goblob/security/size_limit.go`
- `goblob/security/audit.go`
- `goblob/security/headers.go`
- `goblob/security/validation.go`
- `goblob/security/jwt_fuzz_test.go`
- Security middleware applied in all servers
- `.github/workflows/security.yml`
- `docs/security/best-practices.md`
- `docs/security/audit-logging.md`

---

### Phase 7 Checkpoint

Run `go test -tags=integration -race ./test/integration/... -timeout=5m` and confirm all integration tests pass. Start the system with `-metricsPort 9090` and confirm `curl http://localhost:9090/metrics` returns GoBlob metrics. Build the Docker image and start via docker-compose. Run a security scan (gosec ./...). Verify all documentation pages render in MkDocs. Before proceeding to Phase 8, confirm CI pipeline passes on the main branch.

---

## Phase 8: Advanced Features

**Purpose**: Implement optional advanced features for enhanced functionality and performance. These are post-v1 extensions.

### Task: Implement Erasure Coding

**Goal**
Add Reed-Solomon erasure coding as an alternative to replication for improved space efficiency with fault tolerance.

**Critical Details**
- Use github.com/klauspost/reedsolomon library (standard Reed-Solomon)
- Default configuration: 10 data shards, 3 parity shards (10+3)
- EC volumes have special volume type: DiskType="ec"
- Shard storage: each shard stored as a separate volume on a different data node
- Volume layout: ecVolume.Shards[i] = (volumeId, shardIndex, dataNode)
- Write path: encode data → distribute shards to N+M nodes in parallel
- Read path: request data shards; if any fail, reconstruct via parity shards
- Admin command: `volume.ec.encode -vid 5` to convert a normal volume to EC
- Package: `goblob/storage/erasure_coding/`

**Implementation Steps**

1. Create `goblob/storage/erasure_coding/` package:
   ```go
   package erasure_coding

   import (
       "github.com/klauspost/reedsolomon"
   )
   ```
2. Define EC volume layout:
   ```go
   type ECVolume struct {
       VolumeId     uint32
       DataShards   int // e.g., 10
       ParityShards int // e.g., 3
       ShardSize    int64
       Shards       []ECShard
   }

   type ECShard struct {
       VolumeId   uint32
       ShardIndex int
       DataNode   string
   }

   func (v *ECVolume) TotalShards() int {
       return v.DataShards + v.ParityShards
   }
   ```
3. Implement encoder:
   ```go
   type Encoder struct {
       dataShards   int
       parityShards int
       enc          reedsolomon.Encoder
   }

   func NewEncoder(dataShards, parityShards int) (*Encoder, error) {
       enc, err := reedsolomon.New(dataShards, parityShards)
       if err != nil {
           return nil, err
       }
       return &Encoder{
           dataShards:   dataShards,
           parityShards: parityShards,
           enc:          enc,
       }, nil
   }

   func (e *Encoder) Encode(data []byte) ([][]byte, error) {
       // Split data into dataShards pieces
       shardSize := (len(data) + e.dataShards - 1) / e.dataShards
       shards := make([][]byte, e.TotalShards())

       // Fill data shards
       for i := 0; i < e.dataShards; i++ {
           start := i * shardSize
           end := start + shardSize
           if end > len(data) {
               end = len(data)
           }
           shards[i] = data[start:end]
       }

       // Pad last shard if needed
       if len(shards[e.dataShards-1]) < shardSize {
           shards[e.dataShards-1] = append(shards[e.dataShards-1], make([]byte, shardSize-len(shards[e.dataShards-1]))...)
       }

       // Initialize parity shards
       for i := e.dataShards; i < e.TotalShards(); i++ {
           shards[i] = make([]byte, shardSize)
       }

       // Encode parity
       err := e.enc.Encode(shards)
       return shards, err
   }
   ```
4. Implement decoder:
   ```go
   func (e *Encoder) Decode(shards [][]byte) ([]byte, error) {
       // Check if reconstruction needed
       err := e.enc.Reconstruct(shards)
       if err != nil {
           return nil, err
       }

       // Verify data integrity
       ok, err := e.enc.Verify(shards)
       if !ok || err != nil {
           return nil, fmt.Errorf("shard verification failed: %w", err)
       }

       // Concatenate data shards
       var data []byte
       for i := 0; i < e.dataShards; i++ {
           data = append(data, shards[i]...)
       }

       return data, nil
   }
   ```
5. Implement EC volume server integration:
   ```go
   type ECWriteRequest struct {
       VolumeId uint32
       Data     []byte
   }

   func (s *VolumeServer) ECPut(req *ECWriteRequest) error {
       encoder, err := NewEncoder(10, 3) // default 10+3
       if err != nil {
           return err
       }

       shards, err := encoder.Encode(req.Data)
       if err != nil {
           return err
       }

       // Distribute shards to N+M volume servers in parallel
       var wg sync.WaitGroup
       errCh := make(chan error, len(shards))

       for i, shard := range shards {
           wg.Add(1)
           go func(shardIndex int, shardData []byte) {
               defer wg.Done()
               node := s.getShardNode(req.VolumeId, shardIndex)
               err := s.uploadShard(node, req.VolumeId, shardIndex, shardData)
               if err != nil {
                   errCh <- err
               }
           }(i, shard)
       }

       wg.Wait()
       close(errCh)

       for err := range errCh {
           if err != nil {
               return fmt.Errorf("shard upload failed: %w", err)
           }
       }

       return nil
   }
   ```
6. Implement EC read path:
   ```go
   func (s *VolumeServer) ECGet(volumeId uint32) ([]byte, error) {
       ecVol := s.getECVolume(volumeId)
       shards := make([][]byte, ecVol.TotalShards())

       // Fetch all data shards in parallel
       var wg sync.WaitGroup
       for i := 0; i < ecVol.TotalShards(); i++ {
           wg.Add(1)
           go func(shardIndex int) {
               defer wg.Done()
               node := ecVol.Shards[shardIndex].DataNode
               data, err := s.downloadShard(node, volumeId, shardIndex)
               if err == nil {
                   shards[shardIndex] = data
               }
           }(i)
       }
       wg.Wait()

       // Decode (will reconstruct missing shards if needed)
       encoder, _ := NewEncoder(ecVol.DataShards, ecVol.ParityShards)
       return encoder.Decode(shards)
   }
   ```
7. Add admin command for EC conversion:
   ```go
   // goblob/command/volume_ec_encode.go
   func init() {
       cmd := &Command{
           Name: "volume.ec.encode",
           Help: "Convert a normal volume to erasure coding",
           Flags: []Flag{
               {Name: "vid", Usage: "Volume ID to convert"},
               {Name: "dataShards", Usage: "Number of data shards (default 10)"},
               {Name: "parityShards", Usage: "Number of parity shards (default 3)"},
           },
           Run: runVolumeEcEncode,
       }
       Register(cmd)
   }

   func runVolumeEcEncode(args []string) {
       vid := getFlagInt("vid")
       dataShards := getFlagIntDefault("dataShards", 10)
       parityShards := getFlagIntDefault("parityShards", 3)

       // Read all needles from volume
       // Encode each needle with EC
       // Write shards to N+M new volumes
       // Update volume metadata to EC type
   }
   ```
8. Add EC metrics:
   ```go
   var (
       VolumeServerECEncodeBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "ec",
           Name:      "encode_bytes_total",
           Help:      "Total bytes encoded with erasure coding",
       }, []string{"volume_id"})
       VolumeServerECDecodeBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "ec",
           Name:      "decode_bytes_total",
           Help:      "Total bytes decoded with erasure coding",
       }, []string{"volume_id"})
       VolumeServerECReconstructShards = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "ec",
           Name:      "reconstruct_shards_total",
           Help:      "Total shards reconstructed from parity",
       }, []string{"volume_id"})
   )
   ```

**Pitfalls**
- Not validating shard count before encoding causes panic; always check dataShards > 0 and parityShards >= 0
- Shard size mismatch causes decode failures; pad all shards to same size
- Not handling node failures during write causes data loss; implement quorum write
- Concurrent EC operations without locking corrupts shard metadata; use volume-level lock
- Forgetting to update DiskType causes normal volume server to try reading EC shards; set DiskType="ec"

**Verification**
```bash
# Create EC volume
./blob master -mdir /data/master
./blob volume -dir /data/ec1 -mserver localhost:9333 -dataShards 10 -parityShards 3

# Upload data
curl -X PUT http://localhost:8334/ec/1 -d "test data"

# Verify shards created
ls -la /data/ec1/
# Should show shard files: 1.shard.0, 1.shard.1, ..., 1.shard.12

# Test reconstruction: delete one data shard
rm /data/ec1/1.shard.3
curl http://localhost:8334/ec/1
# Should return reconstructed data

# Test EC conversion
./blob volume.ec.encode -vid 5 -dataShards 10 -parityShards 3

# Check EC metrics
curl http://localhost:9090/metrics | grep goblob_ec_
```

**Dependencies**
Storage Engine, Volume Server, Topology

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/storage/erasure_coding/ec.go`
- `goblob/storage/erasure_coding/encoder.go`
- `goblob/storage/erasure_coding/decoder.go`
- `goblob/command/volume_ec_encode.go`
- Configuration updates
- `docs/advanced/erasure-coding.md`

---

### Task: Implement Tiered Storage

**Goal**
Add support for hot/warm/cold storage tiers with automatic data migration based on access patterns.

**Critical Details**
- DiskType string constants: "ssd", "hdd", "" (default)
- Tiering policy: after accessAge > threshold → migrate to slower tier (hdd) → after archiveAge → migrate to S3 (remote)
- RemoteEntry struct on filer Entry: StorageName, RemotePath, RemoteSize, RemoteETag
- Admin commands: `volume.tier.upload -source.dir /data/hdd -cloud s3 -bucket archive`
- Background scanner: every N hours, scan filer entries older than threshold, move chunks to remote storage

**Implementation Steps**

1. Define storage tier types:
   ```go
   // goblob/storage/tiering/types.go
   package tiering

   const (
       DiskTypeSSD   = "ssd"
       DiskTypeHDD   = "hdd"
       DiskTypeDefault = ""
   )

   type Tier struct {
       DiskType    string
       AccessAge   time.Duration // age before tiering
       ArchiveAge  time.Duration // age before archiving
       RemoteStore string        // "s3", "azure", "gcs"
   }
   ```
2. Extend filer Entry with remote storage:
   ```go
   // goblob/filer/entry.go
   type RemoteEntry struct {
       StorageName string // "s3", "azure", "gcs"
       RemotePath  string // e.g., "s3://archive-bucket/chunks/5,abc123"
       RemoteSize  int64
       RemoteETag  string
   }

   type Entry struct {
       // ... existing fields
       Remote *RemoteEntry
   }
   ```
3. Implement tier scanner:
   ```go
   // goblob/storage/tiering/scanner.go
   type Scanner struct {
       filer    filer.FilerClient
       tier     *Tier
       interval time.Duration
   }

   func NewScanner(filer filer.FilerClient, tier *Tier) *Scanner {
       return &Scanner{
           filer:    filer,
           tier:     tier,
           interval: time.Hour,
       }
   }

   func (s *Scanner) Start(ctx context.Context) {
       ticker := time.NewTicker(s.interval)
       for {
           select {
           case <-ctx.Done():
               return
           case <-ticker.C:
               s.ScanAndTier(ctx)
           }
       }
   }

   func (s *Scanner) ScanAndTier(ctx context.Context) {
       // List all entries recursively
       // For each entry:
       //   if Remote == nil && time.Since(Attr.Mtime) > s.tier.ArchiveAge:
       //     s.Archive(ctx, entry)
       //   elif DiskType == "ssd" && time.Since(Attr.Mtime) > s.tier.AccessAge:
       //     s.Migrate(ctx, entry, "hdd")
   }
   ```
4. Implement migration:
   ```go
   func (s *Scanner) Migrate(ctx context.Context, entry *filer.Entry, targetDiskType string) error {
       // Find volume server with target disk type
       // Copy chunks to new volume
       // Update entry chunks
       // Delete old chunks
       return nil
   }
   ```
5. Implement archival to remote storage:
   ```go
   func (s *Scanner) Archive(ctx context.Context, entry *filer.Entry) error {
       for _, chunk := range entry.Chunks {
           // Read chunk data
           data, err := s.readChunk(ctx, chunk)
           if err != nil {
               return err
           }

           // Upload to remote storage
           remotePath, err := s.uploadToS3(ctx, chunk.FileId, data)
           if err != nil {
               return err
           }

           // Update entry with remote metadata
           chunk.Remote = &filer.RemoteEntry{
               StorageName: "s3",
               RemotePath:  remotePath,
               RemoteSize:  int64(len(data)),
               RemoteETag:  computeETag(data),
           }
       }

       // Delete local chunks
       for _, chunk := range entry.Chunks {
           s.deleteChunk(ctx, chunk)
       }

       // Update entry in filer
       return s.filer.UpdateEntry(ctx, entry)
   }
   ```
6. Add admin commands:
   ```go
   // goblob/command/volume_tier_upload.go
   func init() {
       cmd := &Command{
           Name: "volume.tier.upload",
           Help: "Upload local chunks to remote storage",
           Flags: []Flag{
               {Name: "source.dir", Usage: "Source directory"},
               {Name: "cloud", Usage: "Remote storage (s3, azure, gcs)"},
               {Name: "bucket", Usage: "Remote bucket name"},
           },
           Run: runTierUpload,
       }
       Register(cmd)
   }

   func runTierUpload(args []string) {
       sourceDir := getFlag("source.dir")
       cloud := getFlag("cloud")
       bucket := getFlag("bucket")

       // List all chunks in sourceDir
       // Upload each to remote storage
       // Update filer metadata
   }
   ```
7. Add tier configuration:
   ```toml
   # tiering.toml
   [tiers.hot]
   disk_type = "ssd"
   access_age = "7d"

   [tiers.warm]
   disk_type = "hdd"
   access_age = "30d"

   [tiers.cold]
   remote_store = "s3"
   archive_age = "90d"
   ```
8. Add tiering metrics:
   ```go
   var (
       TieredMigrationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "tiering",
           Name:      "migrations_total",
           Help:      "Total tiered storage migrations",
       }, []string{"from_tier", "to_tier"})
       TieredArchiveSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
           Namespace: "goblob",
           Subsystem: "tiering",
           Name:      "archive_size_bytes",
           Help:      "Total data archived to remote storage",
       }, []string{"storage"})
   )
   ```

**Pitfalls**
- Not checking remote storage availability before archival causes data loss; verify connectivity
- Tiering without proper locking causes race conditions; use entry-level lock
- Forgetting to update Chunk.Remote after migration causes data unavailability
- Archiving without backup loses data if remote upload fails; upload first, delete later
- Not handling remote storage errors causes scan to stop; log errors and continue

**Verification**
```bash
# Configure tiered storage
echo "[tiers.hot]
disk_type = ssd
access_age = 1h
[tiers.cold]
remote_store = s3
archive_age = 24h" > tiering.toml

# Start volume with tiering
./blob volume -dir /data/ssd -tiering.config tiering.toml

# Upload data
curl -X PUT http://localhost:8334/1 -d "test data"

# Wait for access_age threshold
sleep 3601

# Check if migrated to HDD
curl http://localhost:8334/1
# Check logs for migration event

# Test archival
./blob volume.tier.upload -source.dir /data/hdd -cloud s3 -bucket archive

# Verify remote entry in filer
./blob filer.ls / | grep RemotePath
```

**Dependencies**
Storage Engine, Volume Server, Topology

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/storage/tiering/types.go`
- `goblob/storage/tiering/scanner.go`
- `goblob/command/volume_tier_upload.go`
- Configuration updates
- `docs/advanced/tiered-storage.md`

---

### Task: Implement Cross-Region Replication

**Goal**
Add asynchronous replication across geographically distributed clusters with conflict resolution.

**Critical Details**
- Uses Filer's SubscribeMetadata gRPC stream to replicate metadata events to remote clusters
- For each filer write event: replicate the blob data first (copy chunk from source volume to target volume), then replicate the metadata entry
- Conflict resolution: last-write-wins by TsNs (nanosecond timestamp)
- Replication lag tracking: `goblob_replication_lag_seconds` Prometheus gauge
- Package: `goblob/replication/async/`

**Implementation Steps**

1. Create `goblob/replication/async/` package:
   ```go
   package async

   import (
       "goblob/pb"
   )
   ```
2. Define replication config:
   ```go
   type Config struct {
       SourceCluster string
       TargetCluster string
       TargetFiler   string
       BatchSize     int
       FlushInterval time.Duration
   }

   type Replicator struct {
       config      Config
       sourceFiler filer.FilerClient
       targetFiler filer.FilerClient
       lagMetric   prometheus.Gauge
   }
   ```
3. Implement metadata stream subscriber:
   ```go
   func (r *Replicator) Start(ctx context.Context) error {
       // Subscribe to metadata changes
       stream, err := r.sourceFiler.SubscribeMetadata(ctx, &pb.SubscribeRequest{
           ClientId: "replicator-" + r.config.SourceCluster,
       })
       if err != nil {
           return err
       }

       for {
           event, err := stream.Recv()
           if err != nil {
               return err
           }

           switch event.Type {
           case pb.EventType_CREATE, pb.EventType_UPDATE:
               if err := r.replicateEntry(ctx, event.Entry); err != nil {
                   log.Error("failed to replicate entry", "error", err)
               }
           case pb.EventType_DELETE:
               if err := r.replicateDelete(ctx, event.Entry); err != nil {
                   log.Error("failed to replicate delete", "error", err)
               }
           }

           // Update lag metric
           lag := time.Since(time.Unix(0, event.TimestampNs))
           r.lagMetric.Set(lag.Seconds())
       }
   }
   ```
4. Implement blob data replication:
   ```go
   func (r *Replicator) replicateEntry(ctx context.Context, entry *filer.Entry) error {
       // For each chunk, replicate blob data
       for _, chunk := range entry.Chunks {
           if err := r.replicateChunk(ctx, chunk); err != nil {
               return err
           }
       }

       // Check if entry exists on target
       existing, err := r.targetFiler.FindEntry(ctx, entry.FullPath)
       if err == nil && existing != nil {
           // Conflict resolution: last-write-wins
           if existing.Attr.Mtime.After(entry.Attr.Mtime) {
               log.Info("skipping stale entry", "path", entry.FullPath)
               return nil
           }
       }

       // Replicate metadata
       return r.targetFiler.InsertEntry(ctx, entry)
   }

   func (r *Replicator) replicateChunk(ctx context.Context, chunk *filer.FileChunk) error {
       // Read chunk from source
       fid, err := types.ParseFileId(chunk.FileId)
       if err != nil {
           return err
       }

       sourceData, err := r.readFromSource(ctx, fid)
       if err != nil {
           return err
       }

       // Assign new FileId on target
       newFid, err := r.assignOnTarget(ctx)
       if err != nil {
           return err
       }

       // Write to target volume
       if err := r.writeToTarget(ctx, newFid, sourceData); err != nil {
           return err
       }

       // Update chunk FileId for target metadata
       chunk.FileId = newFid.String()
       return nil
   }
   ```
5. Implement delete replication:
   ```go
   func (r *Replicator) replicateDelete(ctx context.Context, entry *filer.Entry) error {
       // Check existing entry timestamp
       existing, err := r.targetFiler.FindEntry(ctx, entry.FullPath)
       if err != nil {
           return err
       }

       // Conflict resolution: don't delete if target is newer
       if existing.Attr.Mtime.After(entry.Attr.Mtime) {
           log.Info("skipping stale delete", "path", entry.FullPath)
           return nil
       }

       return r.targetFiler.DeleteEntry(ctx, entry.FullPath)
   }
   ```
6. Add replication lag metrics:
   ```go
   var (
       ReplicationLagSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
           Namespace: "goblob",
           Subsystem: "replication",
           Name:      "lag_seconds",
           Help:      "Replication lag in seconds",
       }, []string{"target_cluster"})
       ReplicatedEntriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "replication",
           Name:      "replicated_entries_total",
           Help:      "Total entries replicated",
       }, []string{"target_cluster", "status"})
   )
   ```
7. Add replication configuration:
   ```toml
   # replication.toml
   [[replication.targets]]
   name = "region-b"
   filer = "filer.region-b.example.com:8888"
   enabled = true
   ```
8. Add admin commands:
   ```go
   // goblob/command/replication_status.go
   func init() {
       cmd := &Command{
           Name: "replication.status",
           Help: "Show replication status",
           Run:  runReplicationStatus,
       }
       Register(cmd)
   }

   func runReplicationStatus(args []string) {
       // Show lag, throughput, errors for each target
   }
   ```

**Pitfalls**
- Not replicating blob data before metadata causes broken references; always replicate chunks first
- Replication without backpressure on slow targets causes memory buildup; use bounded channel
- Conflict resolution without timestamp comparison causes data loss; always compare TsNs
- Not handling network partitions causes split-brain; implement quorum reads
- Skipping deleted entries causes orphaned blobs on target; replicate deletes

**Verification**
```bash
# Configure replication on source cluster
echo "[[replication.targets]]
name = region-b
filer = filer.region-b:8888
enabled = true" > replication.toml

# Start replicator
./blob replicator -config replication.toml &

# Upload data on source
curl -X PUT http://localhost:8888/buckets/test/file.txt -d "test data"

# Verify replicated on target
curl http://filer.region-b:8888/buckets/test/file.txt

# Check replication lag
curl http://localhost:9090/metrics | grep goblob_replication_lag_seconds

# Test conflict resolution: update same file on both clusters
# Verify last-write-wins by timestamp

# Test delete replication
curl -X DELETE http://localhost:8888/buckets/test/file.txt
curl http://filer.region-b:8888/buckets/test/file.txt
# Should return 404
```

**Dependencies**
Filer Server, Log Buffer, Replication Engine

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/replication/async/replicator.go`
- `goblob/command/replication_status.go`
- Configuration updates
- `docs/advanced/cross-region-replication.md`

---

### Task: Implement Caching Layer

**Goal**
Add distributed caching for frequently accessed blobs using LRU in-memory cache and Redis.

**Critical Details**
- LRU in-memory cache: use golang.org/x/sys or implement LRU manually with doubly-linked list + map
- Cache key: FileId string
- Cache value: `[]byte` (needle data)
- Redis-based distributed cache: store blobs as Redis strings with TTL
- Invalidation: on DELETE, remove from cache
- Cache-aside pattern: on GET, check cache first; on miss, read from volume, populate cache

**Implementation Steps**

1. Create `goblob/cache/` package:
   ```go
   package cache
   ```
2. Implement LRU cache:
   ```go
   // goblob/cache/lru.go
   type LRUCache struct {
       capacity int64
       used     int64
       ll       *list.List
       cache    map[string]*list.Element
   }

   type entry struct {
       key   string
       value []byte
   }

   func NewLRUCache(capacity int64) *LRUCache {
       return &LRUCache{
           capacity: capacity,
           ll:       list.New(),
           cache:    make(map[string]*list.Element),
       }
   }

   func (c *LRUCache) Get(key string) ([]byte, bool) {
       if elem, hit := c.cache[key]; hit {
           c.ll.MoveToFront(elem)
           return elem.Value.(*entry).value, true
       }
       return nil, false
   }

   func (c *LRUCache) Put(key string, value []byte) {
       // Evict if necessary
       if c.capacity < c.used+int64(len(value)) {
           c.removeOldest()
       }

       // Add to cache
       elem := c.ll.PushFront(&entry{key, value})
       c.cache[key] = elem
       c.used += int64(len(value))
   }

   func (c *LRUCache) Invalidate(key string) {
       if elem, hit := c.cache[key]; hit {
           c.ll.Remove(elem)
           delete(c.cache, key)
           c.used -= int64(len(elem.Value.(*entry).value))
       }
   }
   ```
3. Implement Redis cache:
   ```go
   // goblob/cache/redis.go
   import (
       "github.com/redis/go-redis/v9"
   )

   type RedisCache struct {
       client *redis.Client
       ttl    time.Duration
   }

   func NewRedisCache(addr string, ttl time.Duration) (*RedisCache, error) {
       client := redis.NewClient(&redis.Options{
           Addr: addr,
       })
       return &RedisCache{client: client, ttl: ttl}, nil
   }

   func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
       data, err := c.client.Get(ctx, "cache:"+key).Bytes()
       if err == redis.Nil {
           return nil, ErrCacheMiss
       }
       return data, err
   }

   func (c *RedisCache) Put(ctx context.Context, key string, value []byte) error {
       return c.client.Set(ctx, "cache:"+key, value, c.ttl).Err()
   }

   func (c *RedisCache) Invalidate(ctx context.Context, key string) error {
       return c.client.Del(ctx, "cache:"+key).Err()
   }
   ```
4. Implement cache-aside in volume server:
   ```go
   // goblob/volume/server.go
   type VolumeServer struct {
       // ... existing fields
       cache cache.Cache
   }

   func (s *VolumeServer) GetNeedle(w http.ResponseWriter, r *http.Request) {
       fid := parseFileId(r.URL.Path)

       // Check cache
       if data, hit := s.cache.Get(fid.String()); hit {
           obs.Info("cache hit", "fid", fid.String())
           w.Write(data)
           return
       }

       // Cache miss: read from volume
       needle, err := s.store.Read(fid)
       if err != nil {
           http.Error(w, err.Error(), http.StatusNotFound)
           return
       }

       // Populate cache
       s.cache.Put(fid.String(), needle.Data)

       w.Write(needle.Data)
   }

   func (s *VolumeServer) DeleteNeedle(w http.ResponseWriter, r *http.Request) {
       fid := parseFileId(r.URL.Path)

       // Delete from volume
       if err := s.store.Delete(fid); err != nil {
           http.Error(w, err.Error(), http.StatusInternalServerError)
           return
       }

       // Invalidate cache
       s.cache.Invalidate(fid.String())

       w.WriteHeader(http.StatusNoContent)
   }
   ```
5. Add cache configuration:
   ```toml
   # cache.toml
   [cache.lru]
   enabled = true
   max_size_mb = 1024

   [cache.redis]
   enabled = false
   address = localhost:6379
   ttl = 3600
   ```
6. Add cache metrics:
   ```go
   var (
       CacheHitsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "cache",
           Name:      "hits_total",
           Help:      "Total cache hits",
       }, []string{"layer"})
       CacheMissesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "cache",
           Name:      "misses_total",
           Help:      "Total cache misses",
       }, []string{"layer"})
       CacheSizeBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
           Namespace: "goblob",
           Subsystem: "cache",
           Name:      "size_bytes",
           Help:      "Current cache size in bytes",
       }, []string{"layer"})
   )
   ```

**Pitfalls**
- LRU without eviction policy causes OOM; enforce capacity limit
- Cache without size limit causes memory leak; use max_size_mb
- Redis connection without connection pooling causes performance degradation; configure pool
- Cache invalidation race condition causes stale data; use atomic delete
- Not handling cache errors causes volume read failures; fallback to volume on cache error

**Verification**
```bash
# Configure LRU cache
echo "[cache.lru]
enabled = true
max_size_mb = 100" > cache.toml

# Start volume with cache
./blob volume -dir /data/volume -cache.config cache.toml &

# Upload data
curl -X PUT http://localhost:8334/1 -d "test data"

# First read: cache miss
curl http://localhost:8334/1
# Check metrics: goblob_cache_misses_total should increase

# Second read: cache hit
curl http://localhost:8334/1
# Check metrics: goblob_cache_hits_total should increase

# Test invalidation
curl -X DELETE http://localhost:8334/1
# Verify cache entry removed

# Check cache size
curl http://localhost:9090/metrics | grep goblob_cache_size_bytes
```

**Dependencies**
Volume Server, Client SDK

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/cache/lru.go`
- `goblob/cache/redis.go`
- `goblob/cache/cache.go` (interface)
- Configuration updates
- `docs/advanced/caching.md`

---

### Task: Implement Data Deduplication

**Goal**
Add content-based deduplication to reduce storage usage for identical data blocks.

**Critical Details**
- Content hash: SHA-256 of raw blob data
- Hash-to-FileId mapping stored in Filer KV: key="dedup:<sha256hex>", value=existing FileId
- Reference counting: stored in Filer KV: key="dedup_ref:<fileId>", value=count
- On upload: compute SHA-256 → lookup KV → if hit: increment refcount, return existing FileId → if miss: store normally, create KV entries
- On delete: decrement refcount → if 0: actually delete the blob

**Implementation Steps**

1. Create `goblob/storage/dedup/` package:
   ```go
   package dedup

   import (
       "crypto/sha256"
       "encoding/hex"
   )
   ```
2. Implement deduplicator:
   ```go
   type Deduplicator struct {
       filer filer.FilerStore
   }

   func NewDeduplicator(filer filer.FilerStore) *Deduplicator {
       return &Deduplicator{filer: filer}
   }

   func (d *Deduplicator) ComputeHash(data []byte) string {
       hash := sha256.Sum256(data)
       return hex.EncodeToString(hash[:])
   }

   func (d *Deduplicator) LookupOrCreate(ctx context.Context, data []byte) (string, error) {
       hash := d.ComputeHash(data)

       // Check if hash exists
       existingFid, err := d.filer.KvGet(ctx, "dedup:"+hash)
       if err == nil && existingFid != nil {
           // Dedup hit: increment refcount
           d.incrementRefCount(ctx, string(existingFid))
           return string(existingFid), nil
       }

       // Dedup miss: return empty, caller will store
       return "", nil
   }

   func (d *Deduplicator) RecordStored(ctx context.Context, fid string, data []byte) error {
       hash := d.ComputeHash(data)

       // Store hash -> fid mapping
       if err := d.filer.KvPut(ctx, "dedup:"+hash, []byte(fid)); err != nil {
           return err
       }

       // Initialize refcount
       return d.filer.KvPut(ctx, "dedup_ref:"+fid, []byte("1"))
   }

   func (d *Deduplicator) incrementRefCount(ctx context.Context, fid string) error {
       refKey := "dedup_ref:" + fid
       refBytes, err := d.filer.KvGet(ctx, refKey)
       if err != nil {
           return err
       }

       refCount, _ := strconv.Atoi(string(refBytes))
       refCount++

       return d.filer.KvPut(ctx, refKey, []byte(strconv.Itoa(refCount)))
   }

   func (d *Deduplicator) DecrementRefCount(ctx context.Context, fid string) (int, error) {
       refKey := "dedup_ref:" + fid
       refBytes, err := d.filer.KvGet(ctx, refKey)
       if err != nil {
           return 0, err
       }

       refCount, _ := strconv.Atoi(string(refBytes))
       refCount--

       if refCount <= 0 {
           // Delete refcount entry
           d.filer.KvDelete(ctx, refKey)
           return 0, nil
       }

       if err := d.filer.KvPut(ctx, refKey, []byte(strconv.Itoa(refCount))); err != nil {
           return 0, err
       }

       return refCount, nil
   }
   ```
3. Integrate with volume server write path:
   ```go
   func (s *VolumeServer) PutNeedle(w http.ResponseWriter, r *http.Request) {
       data, _ := io.ReadAll(r.Body)

       // Try dedup
       existingFid, err := s.dedup.LookupOrCreate(r.Context(), data)
       if err != nil {
           http.Error(w, err.Error(), http.StatusInternalServerError)
           return
       }

       if existingFid != "" {
           // Dedup hit: return existing FileId
           w.Header().Set("Content-Type", "text/plain")
           w.Write([]byte(existingFid))
           return
       }

       // Dedup miss: store normally
       fid := s.assignFileId()
       if err := s.store.Write(fid, data); err != nil {
           http.Error(w, err.Error(), http.StatusInternalServerError)
           return
       }

       // Record in dedup index
       if err := s.dedup.RecordStored(r.Context(), fid.String(), data); err != nil {
           log.Error("failed to record dedup", "error", err)
       }

       w.Header().Set("Content-Type", "text/plain")
       w.Write([]byte(fid.String()))
   }
   ```
4. Integrate with volume server delete path:
   ```go
   func (s *VolumeServer) DeleteNeedle(w http.ResponseWriter, r *http.Request) {
       fid := parseFileId(r.URL.Path)

       // Decrement refcount
       refCount, err := s.dedup.DecrementRefCount(r.Context(), fid.String())
       if err != nil {
           http.Error(w, err.Error(), http.StatusInternalServerError)
           return
       }

       if refCount > 0 {
           // Still referenced elsewhere: don't delete
           w.WriteHeader(http.StatusAccepted)
           return
       }

       // No more references: actually delete
       if err := s.store.Delete(fid); err != nil {
           http.Error(w, err.Error(), http.StatusInternalServerError)
           return
       }

       // Remove dedup hash entry
       // (need to look up hash by fid first, or maintain reverse index)

       w.WriteHeader(http.StatusNoContent)
   }
   ```
5. Add dedup metrics:
   ```go
   var (
       DedupHitBytes = prometheus.NewCounter(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "dedup",
           Name:      "hit_bytes_total",
           Help:      "Total bytes saved via deduplication",
       })
       DedupMissBytes = prometheus.NewCounter(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "dedup",
           Name:      "miss_bytes_total",
           Help:      "Total bytes not deduplicated",
       })
       DedupRefCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
           Namespace: "goblob",
           Subsystem: "dedup",
           Name:      "ref_count",
           Help:      "Number of references per deduplicated blob",
       }, []string{"file_id"})
   )
   ```

**Pitfalls**
- Not using atomic operations on refcount causes race conditions; use Filer's transaction support
- Forgetting to delete hash mapping on final delete causes KV bloat; cleanup hash entry
- Dedup without locking on concurrent uploads causes duplicate storage; use write lock on hash key
- SHA-256 computation on large data is slow; compute hash in streaming fashion
- Not handling dedup errors causes upload failures; fallback to non-dedup path on error

**Verification**
```bash
# Upload same data twice
DATA="test data for dedup"
curl -X PUT http://localhost:8334/1 -d "$DATA"
FID1=$(curl -s http://localhost:8334/1)
curl -X PUT http://localhost:8334/2 -d "$DATA"
FID2=$(curl -s http://localhost:8334/2)

# Verify same FileId returned (dedup hit)
echo "FID1: $FID1"
echo "FID2: $FID2"
[ "$FID1" = "$FID2" ] && echo "Dedup works!" || echo "Dedup failed!"

# Check refcount
./blob filer.kv.get "dedup_ref:$FID1"
# Should output "2"

# Delete one reference
curl -X DELETE http://localhost:8334/1

# Verify blob still exists (refcount > 0)
curl http://localhost:8334/2

# Delete second reference
curl -X DELETE http://localhost:8334/2

# Verify blob deleted
curl http://localhost:8334/2
# Should return 404

# Check dedup metrics
curl http://localhost:9090/metrics | grep goblob_dedup_
```

**Dependencies**
Storage Engine, Filer Server

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/storage/dedup/dedup.go`
- Filer store schema updates
- `docs/advanced/deduplication.md`

---

### Task: Implement Quota Management

**Goal**
Add per-user and per-bucket storage quotas with enforcement and reporting.

**Critical Details**
- Quota stored in Filer KV: key="quota:user:<userId>", value=JSON{MaxBytes, UsedBytes}
- Per-bucket quota: key="quota:bucket:<bucketName>", value=JSON{MaxBytes, UsedBytes}
- Enforcement: check quota before upload; reject with 507 Insufficient Storage if exceeded
- Update UsedBytes after successful upload/delete (best-effort, not transactional)

**Implementation Steps**

1. Create `goblob/quota/` package:
   ```go
   package quota

   import (
       "encoding/json"
   )
   ```
2. Define quota structures:
   ```go
   type Quota struct {
       MaxBytes int64 `json:"max_bytes"`
       UsedBytes int64 `json:"used_bytes"`
   }

   type Manager struct {
       filer filer.FilerStore
   }

   func NewManager(filer filer.FilerStore) *Manager {
       return &Manager{filer: filer}
   }
   ```
3. Implement quota checking:
   ```go
   func (m *Manager) CheckUserQuota(ctx context.Context, userId string, requiredBytes int64) error {
       quota, err := m.GetUserQuota(ctx, userId)
       if err != nil {
           return err // fail open on error
       }

       if quota.MaxBytes > 0 && quota.UsedBytes+requiredBytes > quota.MaxBytes {
           return &QuotaExceededError{
               UserId:      userId,
               MaxBytes:    quota.MaxBytes,
               UsedBytes:   quota.UsedBytes,
               Required:    requiredBytes,
           }
       }

       return nil
   }

   func (m *Manager) CheckBucketQuota(ctx context.Context, bucketName string, requiredBytes int64) error {
       quota, err := m.GetBucketQuota(ctx, bucketName)
       if err != nil {
           return err
       }

       if quota.MaxBytes > 0 && quota.UsedBytes+requiredBytes > quota.MaxBytes {
           return &QuotaExceededError{
               BucketName:  bucketName,
               MaxBytes:    quota.MaxBytes,
               UsedBytes:   quota.UsedBytes,
               Required:    requiredBytes,
           }
       }

       return nil
   }
   ```
4. Implement quota update:
   ```go
   func (m *Manager) AddUserUsage(ctx context.Context, userId string, bytes int64) error {
       quota, err := m.GetUserQuota(ctx, userId)
       if err != nil {
           return err
       }

       quota.UsedBytes += bytes
       return m.SetUserQuota(ctx, userId, quota)
   }

   func (m *Manager) SubtractUserUsage(ctx context.Context, userId string, bytes int64) error {
       quota, err := m.GetUserQuota(ctx, userId)
       if err != nil {
           return err
       }

       quota.UsedBytes -= bytes
       if quota.UsedBytes < 0 {
           quota.UsedBytes = 0
       }
       return m.SetUserQuota(ctx, userId, quota)
   }
   ```
5. Implement quota storage:
   ```go
   func (m *Manager) GetUserQuota(ctx context.Context, userId string) (*Quota, error) {
       key := "quota:user:" + userId
       data, err := m.filer.KvGet(ctx, key)
       if err != nil {
           return &Quota{MaxBytes: 0, UsedBytes: 0}, nil // No quota set
       }

       var quota Quota
       if err := json.Unmarshal(data, &quota); err != nil {
           return nil, err
       }
       return &quota, nil
   }

   func (m *Manager) SetUserQuota(ctx context.Context, userId string, quota *Quota) error {
       key := "quota:user:" + userId
       data, err := json.Marshal(quota)
       if err != nil {
           return err
       }
       return m.filer.KvPut(ctx, key, data)
   }

   func (m *Manager) SetBucketQuota(ctx context.Context, bucketName string, quota *Quota) error {
       key := "quota:bucket:" + bucketName
       data, err := json.Marshal(quota)
       if err != nil {
           return err
       }
       return m.filer.KvPut(ctx, key, data)
   }
   ```
6. Integrate with S3 gateway:
   ```go
   func (s *S3Gateway) PutObject(ctx context.Context, req *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
       userId := getUserIdFromCredentials(ctx)
       bucketName := *req.Bucket
       contentLength := req.ContentLength

       // Check user quota
       if err := s.quota.CheckUserQuota(ctx, userId, contentLength); err != nil {
           return nil, err
       }

       // Check bucket quota
       if err := s.quota.CheckBucketQuota(ctx, bucketName, contentLength); err != nil {
           return nil, err
       }

       // Upload object
       // ...

       // Update usage
       s.quota.AddUserUsage(ctx, userId, contentLength)
       s.quota.AddBucketUsage(ctx, bucketName, contentLength)

       return &s3.PutObjectOutput{}, nil
   }

   func (s *S3Gateway) DeleteObject(ctx context.Context, req *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
       // Get object size
       obj, err := s.getObjectMeta(ctx, *req.Bucket, *req.Key)
       if err != nil {
           return nil, err
       }

       // Delete object
       // ...

       // Update usage
       s.quota.SubtractUserUsage(ctx, obj.UserId, obj.Size)
       s.quota.SubtractBucketUsage(ctx, *req.Bucket, obj.Size)

       return &s3.DeleteObjectOutput{}, nil
   }
   ```
7. Add admin commands:
   ```go
   // goblob/command/quota_set.go
   func init() {
       cmd := &Command{
           Name: "quota.set",
           Help: "Set user or bucket quota",
           Flags: []Flag{
               {Name: "user", Usage: "User ID"},
               {Name: "bucket", Usage: "Bucket name"},
               {Name: "max_bytes", Usage: "Maximum bytes"},
           },
           Run: runQuotaSet,
       }
       Register(cmd)
   }

   func runQuotaSet(args []string) {
       userId := getFlag("user")
       bucket := getFlag("bucket")
       maxBytes := getFlagInt64("max_bytes")

       if userId != "" {
           // Set user quota
       } else if bucket != "" {
           // Set bucket quota
       }
   }
   ```
8. Add quota metrics:
   ```go
   var (
       QuotaUsedBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
           Namespace: "goblob",
           Subsystem: "quota",
           Name:      "used_bytes",
           Help:      "Quota used bytes",
       }, []string{"type", "id"})
       QuotaMaxBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
           Namespace: "goblob",
           Subsystem: "quota",
           Name:      "max_bytes",
           Help:      "Quota max bytes",
       }, []string{"type", "id"})
       QuotaRejectionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "quota",
           Name:      "rejections_total",
           Help:      "Total quota rejections",
       }, []string{"type", "id"})
   )
   ```

**Pitfalls**
- Not checking quota before upload causes data loss when rejected; check quota before storing
- Quota update not atomic with upload causes inconsistency; use best-effort with reconciliation
- Not handling negative UsedBytes causes underflow on deletes; clamp to 0
- Quota check without caching causes performance degradation; cache quota in memory
- Forgetting to update quota on delete causes leaked quota; always subtract on delete

**Verification**
```bash
# Set user quota
./blob quota.set -user alice -max_bytes 1048576  # 1MB

# Upload within quota
curl -X PUT http://localhost:8333/buckets/alice/file1.txt -d "$(printf 'a%.0s' {1..500000})"  # 500KB
# Should succeed

# Upload exceeding quota
curl -X PUT http://localhost:8333/buckets/alice/file2.txt -d "$(printf 'b%.0s' {1..600000})"  # 600KB
# Should return 507 Insufficient Storage

# Check quota
./blob quota.get -user alice
# Should show used_bytes=500000

# Delete file
curl -X DELETE http://localhost:8333/buckets/alice/file1.txt

# Verify quota decreased
./blob quota.get -user alice
# Should show used_bytes=0

# Set bucket quota
./blob quota.set -bucket test-bucket -max_bytes 524288  # 512KB

# Check metrics
curl http://localhost:9090/metrics | grep goblob_quota_
```

**Dependencies**
Filer Server, IAM

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/quota/quota.go`
- `goblob/command/quota_set.go`
- `goblob/command/quota_get.go`
- API updates
- `docs/advanced/quota-management.md`

---

### Task: Implement Object Lifecycle Policies

**Goal**
Add automatic object expiration and transition based on S3-compatible lifecycle rules.

**Critical Details**
- S3-compatible lifecycle rules: `<Rule><ID>...</ID><Filter><Prefix>prefix/</Prefix></Filter><Expiration><Days>30</Days></Expiration></Rule>`
- Rules stored as JSON in filer at /buckets/{bucket}/.s3/lifecycle.json
- Background scanner: runs daily, scans all objects, applies expiration (delete) and transition rules
- Package: `goblob/s3api/lifecycle/`

**Implementation Steps**

1. Create `goblob/s3api/lifecycle/` package:
   ```go
   package lifecycle
   ```
2. Define lifecycle rule structures:
   ```go
   type LifecycleConfiguration struct {
       Rules []Rule `xml:"Rule"`
   }

   type Rule struct {
       ID     string  `xml:"ID"`
       Filter Filter  `xml:"Filter"`
       Expiration *Expiration `xml:"Expiration,omitempty"`
       Transition *Transition `xml:"Transition,omitempty"`
       Status string `xml:"Status"` // "Enabled" or "Disabled"
   }

   type Filter struct {
       Prefix string `xml:"Prefix,omitempty"`
       Tag    Tag    `xml:"Tag,omitempty"`
   }

   type Tag struct {
       Key   string `xml:"Key"`
       Value string `xml:"Value"`
   }

   type Expiration struct {
       Days int `xml:"Days"`
       Date string `xml:"Date,omitempty"`
   }

   type Transition struct {
       Days      int    `xml:"Days"`
       StorageClass string `xml:"StorageClass"`
   }
   ```
3. Implement lifecycle processor:
   ```go
   type Processor struct {
       filer    filer.FilerClient
       interval time.Duration
   }

   func NewProcessor(filer filer.FilerClient) *Processor {
       return &Processor{
           filer:    filer,
           interval: 24 * time.Hour,
       }
   }

   func (p *Processor) Start(ctx context.Context) {
       ticker := time.NewTicker(p.interval)
       for {
           select {
           case <-ctx.Done():
               return
           case <-ticker.C:
               p.ProcessAllBuckets(ctx)
           }
       }
   }

   func (p *Processor) ProcessAllBuckets(ctx context.Context) error {
       // List all buckets
       buckets, err := p.listBuckets(ctx)
       if err != nil {
           return err
       }

       for _, bucket := range buckets {
           if err := p.ProcessBucket(ctx, bucket); err != nil {
               log.Error("failed to process bucket", "bucket", bucket, "error", err)
           }
       }

       return nil
   }

   func (p *Processor) ProcessBucket(ctx context.Context, bucketName string) error {
       // Load lifecycle configuration
       config, err := p.loadLifecycleConfig(ctx, bucketName)
       if err != nil {
           return err // No lifecycle config, skip
       }

       // Scan all objects in bucket
       return p.filer.ListDirectoryEntries(ctx, "/buckets/"+bucketName, "", false, 0,
           func(entry *filer.Entry) bool {
               for _, rule := range config.Rules {
                   if rule.Status != "Enabled" {
                       continue
                   }

                   if !p.matchesFilter(entry, rule.Filter) {
                       continue
                   }

                   if rule.Expiration != nil {
                       if p.shouldExpire(entry, rule.Expiration) {
                           log.Info("expiring object", "path", entry.FullPath)
                           p.filer.DeleteEntry(ctx, entry.FullPath)
                       }
                   }

                   if rule.Transition != nil {
                       if p.shouldTransition(entry, rule.Transition) {
                           log.Info("transitioning object", "path", entry.FullPath)
                           p.transitionObject(ctx, entry, rule.Transition)
                       }
                   }
               }
               return true
           })
   }
   ```
4. Implement filter matching:
   ```go
   func (p *Processor) matchesFilter(entry *filer.Entry, filter Filter) bool {
       if filter.Prefix != "" {
           if !strings.HasPrefix(string(entry.FullPath), filter.Prefix) {
               return false
           }
       }

       if filter.Tag.Key != "" {
           // Check if entry has matching tag
           if entry.Extended == nil {
               return false
           }
           tagValue, ok := entry.Extended["tag:"+filter.Tag.Key]
           if !ok || string(tagValue) != filter.Tag.Value {
               return false
           }
       }

       return true
   }
   ```
5. Implement expiration logic:
   ```go
   func (p *Processor) shouldExpire(entry *filer.Entry, expiration *Expiration) bool {
       if expiration.Days > 0 {
           cutoff := time.Now().AddDate(0, 0, -expiration.Days)
           return entry.Attr.Mtime.Before(cutoff)
       }

       if expiration.Date != "" {
           cutoff, _ := time.Parse(time.RFC3339, expiration.Date)
           return entry.Attr.Mtime.Before(cutoff)
       }

       return false
   }
   ```
6. Implement transition logic:
   ```go
   func (p *Processor) shouldTransition(entry *filer.Entry, transition *Transition) bool {
       cutoff := time.Now().AddDate(0, 0, -transition.Days)
       return entry.Attr.Mtime.Before(cutoff)
   }

   func (p *Processor) transitionObject(ctx context.Context, entry *filer.Entry, transition *Transition) error {
       // Update entry's DiskType based on StorageClass
       entry.Attr.DiskType = map[string]string{
           "STANDARD_IA": "hdd",
           "GLACIER":     "archive",
       }[transition.StorageClass]

       return p.filer.UpdateEntry(ctx, entry)
   }
   ```
7. Add lifecycle configuration API:
   ```go
   func (s *S3Gateway) PutBucketLifecycleConfiguration(ctx context.Context, req *s3.PutBucketLifecycleConfigurationInput) (*s3.PutBucketLifecycleConfigurationOutput, error) {
       // Parse XML configuration
       var config LifecycleConfiguration
       if err := xml.Unmarshal(req.LifecycleConfiguration, &config); err != nil {
           return nil, err
       }

       // Store in filer
       path := fmt.Sprintf("/buckets/%s/.s3/lifecycle.json", *req.Bucket)
       data, _ := json.Marshal(config)

       entry := &filer.Entry{
           FullPath: filer.FullPath(path),
           Attr:     filer.Attr{Mode: 0644},
           Content:  data,
       }

       if err := s.filer.InsertEntry(ctx, entry); err != nil {
           return nil, err
       }

       return &s3.PutBucketLifecycleConfigurationOutput{}, nil
   }

   func (s *S3Gateway) GetBucketLifecycleConfiguration(ctx context.Context, req *s3.GetBucketLifecycleConfigurationInput) (*s3.GetBucketLifecycleConfigurationOutput, error) {
       path := fmt.Sprintf("/buckets/%s/.s3/lifecycle.json", *req.Bucket)
       entry, err := s.filer.FindEntry(ctx, filer.FullPath(path))
       if err != nil {
           return nil, err
       }

       var config LifecycleConfiguration
       if err := json.Unmarshal(entry.Content, &config); err != nil {
           return nil, err
       }

       data, _ := xml.Marshal(config)
       return &s3.GetBucketLifecycleConfigurationOutput{
           LifecycleConfiguration: data,
       }, nil
   }
   ```
8. Add lifecycle metrics:
   ```go
   var (
       LifecycleExpiredObjectsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "lifecycle",
           Name:      "expired_objects_total",
           Help:      "Total objects expired by lifecycle policy",
       }, []string{"bucket"})
       LifecycleTransitionedObjectsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "lifecycle",
           Name:      "transitioned_objects_total",
           Help:      "Total objects transitioned by lifecycle policy",
       }, []string{"bucket", "storage_class"})
   )
   ```

**Pitfalls**
- Lifecycle XML parsing errors cause policy to fail silently; validate XML structure
- Scanner without pagination misses objects in large buckets; use ListDirectoryPrefixedEntries
- Not handling concurrent modifications causes race conditions; use optimistic locking
- Transition without tiering support causes errors; verify tiering enabled
- Expired objects not deleted from volume causes storage leak; delete chunks too

**Verification**
```bash
# Create lifecycle configuration
cat > lifecycle.xml <<EOF
<LifecycleConfiguration>
  <Rule>
    <ID>ExpireOldLogs</ID>
    <Filter>
      <Prefix>logs/</Prefix>
    </Filter>
    <Expiration>
      <Days>30</Days>
    </Expiration>
    <Status>Enabled</Status>
  </Rule>
  <Rule>
    <ID>TransitionToIA</ID>
    <Filter>
      <Prefix>data/</Prefix>
    </Filter>
    <Transition>
      <Days>90</Days>
      <StorageClass>STANDARD_IA</StorageClass>
    </Transition>
    <Status>Enabled</Status>
  </Rule>
</LifecycleConfiguration>
EOF

# Set lifecycle policy
aws s3api put-bucket-lifecycle-configuration \
  --bucket test-bucket \
  --lifecycle-configuration file://lifecycle.xml \
  --endpoint-url http://localhost:8333

# Upload old object (manually set mtime to 31 days ago)
curl -X PUT http://localhost:8333/buckets/test-bucket/logs/old.log -d "old log"
# Update mtime in filer to 31 days ago

# Run lifecycle processor
./blob lifecycle.process -bucket test-bucket

# Verify object expired
curl http://localhost:8333/buckets/test-bucket/logs/old.log
# Should return 404

# Check lifecycle metrics
curl http://localhost:9090/metrics | grep goblob_lifecycle_
```

**Dependencies**
S3 Gateway, Filer Server, Tiered Storage (optional)

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/s3api/lifecycle/lifecycle.go`
- `goblob/s3api/lifecycle/processor.go`
- `goblob/command/lifecycle_process.go`
- Background job implementation
- `docs/advanced/lifecycle-policies.md`

---

### Task: Implement WebDAV Interface

**Goal**
Add WebDAV protocol support for filesystem-like access to GoBlob storage.

**Critical Details**
- Use golang.org/x/net/webdav package (implements WebDAV FileSystem interface)
- Implement `webdav.FileSystem` interface backed by Filer gRPC client
- WebDAV routes: all methods on /{path...}
- Authentication: Basic auth or JWT
- Port: 4333 by default (separate from S3)

**Implementation Steps**

1. Create `goblob/webdav/` package:
   ```go
   package webdav

   import (
       "golang.org/x/net/webdav"
   )
   ```
2. Implement WebDAV FileSystem interface:
   ```go
   type FilerFileSystem struct {
       filer filer.FilerClient
   }

   func NewFilerFileSystem(filer filer.FilerClient) *FilerFileSystem {
       return &FilerFileSystem{filer: filer}
   }

   func (fs *FilerFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
       entry := &filer.Entry{
           FullPath: filer.FullPath(name),
           Attr: filer.Attr{
               Mode: perm | os.ModeDir,
               Mtime: time.Now(),
               Crtime: time.Now(),
           },
       }
       return fs.filer.InsertEntry(ctx, entry)
   }

   func (fs *FilerFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
       if flag&os.O_CREATE != 0 {
           // Create new file
           entry := &filer.Entry{
               FullPath: filer.FullPath(name),
               Attr: filer.Attr{
                   Mode: perm,
                   Mtime: time.Now(),
                   Crtime: time.Now(),
               },
           }
           if err := fs.filer.InsertEntry(ctx, entry); err != nil {
               return nil, err
           }
       }

       return &FilerFile{
           fs:        fs,
           path:      name,
           flag:      flag,
           entry:     entry,
       }, nil
   }

   func (fs *FilerFileSystem) RemoveAll(ctx context.Context, name string) error {
       return fs.filer.DeleteEntry(ctx, filer.FullPath(name))
   }

   func (fs *FilerFileSystem) Rename(ctx context.Context, oldName, newName string) error {
       entry, err := fs.filer.FindEntry(ctx, filer.FullPath(oldName))
       if err != nil {
           return err
       }

       entry.FullPath = filer.FullPath(newName)

       if err := fs.filer.InsertEntry(ctx, entry); err != nil {
           return err
       }

       return fs.filer.DeleteEntry(ctx, filer.FullPath(oldName))
   }

   func (fs *FilerFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
       entry, err := fs.filer.FindEntry(ctx, filer.FullPath(name))
       if err != nil {
           return nil, err
       }

       return entry.FileInfo(), nil
   }
   ```
3. Implement FilerFile:
   ```go
   type FilerFile struct {
       fs     *FilerFileSystem
       path   string
       flag   int
       entry  *filer.Entry
       offset int64
       data   []byte
   }

   func (f *FilerFile) Read(p []byte) (int, error) {
       if f.data == nil {
           // Read from filer
           data, err := f.fs.filer.ReadFile(context.Background(), f.entry)
           if err != nil {
               return 0, err
           }
           f.data = data
       }

       if f.offset >= int64(len(f.data)) {
           return 0, io.EOF
       }

       n := copy(p, f.data[f.offset:])
       f.offset += int64(n)
       return n, nil
   }

   func (f *FilerFile) Write(p []byte) (int, error) {
       if f.data == nil {
           f.data = []byte{}
       }

       f.data = append(f.data, p...)
       f.offset += int64(len(p))

       // Update entry
       f.entry.Chunks = nil // Will be set on Close
       f.entry.Content = f.data
       f.entry.Attr.Mtime = time.Now()

       return len(p), nil
   }

   func (f *FilerFile) Close() error {
       if f.flag&os.O_WRONLY != 0 || f.flag&os.O_RDWR != 0 {
           // Write back to filer
           if len(f.data) > 0 && len(f.data) < 1024 { // Inline small files
               f.entry.Content = f.data
           } else {
               // Upload to volume
               fids, err := f.fs.filer.UploadData(context.Background(), f.data)
               if err != nil {
                   return err
               }
               f.entry.Chunks = fids
               f.entry.Content = nil
           }
           return f.fs.filer.UpdateEntry(context.Background(), f.entry)
       }
       return nil
   }

   func (f *FilerFile) Stat() (os.FileInfo, error) {
       return f.entry.FileInfo(), nil
   }
   ```
4. Implement WebDAV server:
   ```go
   type Server struct {
       addr      string
       fs        webdav.FileSystem
       ls        webdav.LockSystem
       guard     *security.Guard
   }

   func NewServer(addr string, filer filer.FilerClient, guard *security.Guard) *Server {
       return &Server{
           addr:  addr,
           fs:    NewFilerFileSystem(filer),
           ls:    webdav.NewMemLS(),
           guard: guard,
       }
   }

   func (s *Server) Start() error {
       handler := &webdav.Handler{
           FileSystem: s.fs,
           LockSystem: s.ls,
       }

       // Wrap with authentication
       authenticatedHandler := s.guard.WhiteList(handler.ServeHTTP)

       return http.ListenAndServe(s.addr, authenticatedHandler)
   }
   ```
5. Add WebDAV command:
   ```go
   // goblob/command/webdav.go
   func init() {
       cmd := &Command{
           Name: "webdav",
           Help: "Start WebDAV server",
           Flags: []Flag{
               {Name: "port", Usage: "WebDAV port (default 4333)"},
               {Name: "filer", Usage: "Filer address"},
               {Name: "cert", Usage: "TLS cert file"},
               {Name: "key", Usage: "TLS key file"},
           },
           Run: runWebDAV,
       }
       Register(cmd)
   }

   func runWebDAV(args []string) {
       port := getFlagIntDefault("port", 4333)
       filerAddr := getFlag("filer")

       filerClient := connectToFiler(filerAddr)
       guard := loadGuard()

       server := webdav.NewServer(fmt.Sprintf(":%d", port), filerClient, guard)
       log.Info("starting WebDAV server", "port", port)

       if err := server.Start(); err != nil {
           log.Fatal(err)
       }
   }
   ```
6. Add WebDAV configuration:
   ```toml
   # webdav.toml
   [server]
   port = 4333
   filer = "localhost:8888"

   [auth]
   enabled = true
   type = "basic" # or "jwt"
   ```
7. Add WebDAV metrics:
   ```go
   var (
       WebdavRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
           Namespace: "goblob",
           Subsystem: "webdav",
           Name:      "requests_total",
           Help:      "Total WebDAV requests",
       }, []string{"method", "status"})
       WebdavRequestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
           Namespace: "goblob",
           Subsystem: "webdav",
           Name:      "request_latency_seconds",
           Help:      "WebDAV request latency",
       }, []string{"method"})
   )
   ```

**Pitfalls**
- Not implementing all WebDAV methods causes client compatibility issues; implement PROPFIND, PROPPATCH, MKCOL, COPY, MOVE, LOCK, UNLOCK
- File handle without proper locking causes race conditions; use LockSystem
- Not handling If-Match headers breaks concurrent edits; implement ETag support
- Large files without chunking cause memory issues; use streaming writes
- WebDAV without authentication exposes all data; always enable auth

**Verification**
```bash
# Start WebDAV server
./blob webdav -port 4333 -filer localhost:8888 &

# Mount with WebDAV client (cadaver)
cadaver http://localhost:4333/
# Should prompt for auth
# Try: ls, mkdir test, put file.txt, get file.txt, delete test

# Mount with davfs2 (Linux)
sudo mount -t davfs http://localhost:4333/ /mnt/goblob
ls /mnt/goblob
cp /etc/hosts /mnt/goblob/test
cat /mnt/goblob/test

# Test with Windows WebDAV client
# Map network drive: http://localhost:4333/

# Test PROPFIND
curl -X PROPFIND http://localhost:4333/ -u user:pass -v

# Check WebDAV metrics
curl http://localhost:9090/metrics | grep goblob_webdav_
```

**Dependencies**
Filer Server, Security

**Related Plan**
N/A (advanced feature)

**Expected Output**
- `goblob/webdav/webdav_server.go`
- `goblob/webdav/filesystem.go`
- `goblob/command/webdav.go`
- `docs/interfaces/webdav.md`

---

This completes Phase 8: Advanced Features. These features are optional post-v1 extensions that enhance GoBlob's capabilities for specialized use cases including erasure coding for space efficiency, tiered storage for cost optimization, cross-region replication for disaster recovery, caching for performance, deduplication for storage savings, quota management for multi-tenancy, lifecycle policies for automated data management, and WebDAV for filesystem-like access.

Implement these features incrementally based on your specific requirements. Each feature is self-contained and can be added independently without affecting core functionality.
