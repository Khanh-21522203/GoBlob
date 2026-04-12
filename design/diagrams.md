# GoBlob Architecture Diagrams

## 1. C4 Context — System Boundaries

```mermaid
flowchart TB
    subgraph boundary[GoBlob System]
        GB[("GoBlob\nDistributed Blob Store")]
    end

    Client[("👤 Client\nApplication")]
    S3Client[("🪣 S3 Client\nAWS SDK / boto3")]
    WebDAVClient[("📁 WebDAV Client\nOS / browser")]
    AdminOp[("🔧 Operator\nAdmin shell")]

    MetaBackend[("🗄️ Metadata Backend\nLevelDB / Redis / PG / MySQL / Cassandra")]
    Raft[("⚖️ Raft Consensus\nBoltDB log")]
    CloudTier[("☁️ Cloud Tier\nS3-compatible store")]
    Prometheus[("📊 Prometheus\nMetrics scraper")]

    Client     -->|"HTTP assign + PUT/GET blob"| GB
    S3Client   -->|"S3 REST API (SigV4)"| GB
    WebDAVClient -->|"WebDAV PROPFIND/PUT/GET"| GB
    AdminOp    -->|"Admin shell gRPC"| GB

    GB -->|"KV / SQL metadata"| MetaBackend
    GB -->|"Raft log replication"| Raft
    GB -->|"Data tiering (future)"| CloudTier
    GB -->|"Expose /metrics"| Prometheus
```

---

## 2. C4 Container — Deployable Units

```mermaid
flowchart TB
    subgraph goBlob[GoBlob Binary — single process, multiple roles]
        direction TB

        subgraph access[Access Layer]
            S3[("🪣 S3 Gateway\nHTTP SigV4")]
            WD[("📁 WebDAV Server\nRFC 4918")]
        end

        subgraph meta[Metadata Plane]
            FL[("📋 Filer Server\nHTTP + gRPC")]
        end

        subgraph ctrl[Control Plane]
            MS[("🎛️ Master Server\nHTTP + gRPC")]
        end

        subgraph data[Data Plane]
            VS[("💾 Volume Server\nHTTP + gRPC")]
        end

        subgraph consensus[Consensus]
            RT[("⚖️ Raft\nBoltDB-backed")]
        end

        subgraph obs[Observability]
            OB[("📊 Metrics\nPrometheus")]
        end
    end

    S3 -->|"filer gRPC"| FL
    WD -->|"filer gRPC"| FL
    FL -->|"assign + PUT chunks"| MS
    FL -->|"PUT blob chunks"| VS
    MS -->|"Raft.Apply"| RT
    MS -->|"gRPC AllocateVolume"| VS
    VS -->|"HTTP replicate"| VS

    subgraph stores[Persistent Stores]
        LDB[("LevelDB")]
        RDS[("Redis")]
        PG[("PostgreSQL")]
        BOLT[("BoltDB\nRaft log")]
    end

    FL --> LDB & RDS & PG
    RT --> BOLT
    OB -.->|"scrape"| MS & VS & FL
```

---

## 3. C4 Component — Master Control Plane

```mermaid
flowchart TB
    subgraph master[Master Server]
        direction TB

        subgraph http[HTTP / gRPC Handlers]
            AH[Assign Handler]
            LH[Lookup Handler]
            HH[Heartbeat Handler]
            GH[Grow Handler]
        end

        subgraph topo[Topology]
            TM[TopologyManager]
            VL[VolumeLayout]
            DN[DataNode Tree\nDC → Rack → Node]
        end

        subgraph growth[Volume Growth]
            VG[VolumeGrowth\nauto-grow worker]
        end

        subgraph seq[Sequencing]
            RS[RaftSequencer]
            FS[FileSequencer]
        end

        subgraph consensus[Raft]
            RF[RaftServer]
            FSM[MasterFSM]
            EB[Event Bus]
        end

        subgraph guard[Security]
            GD[Guard\nIP whitelist + JWT]
        end
    end

    AH --> GD --> TM
    LH --> GD --> TM
    HH --> GD --> TM
    GH --> VG

    TM --> VL --> DN
    TM --> RS --> RF --> FSM
    FSM --> EB
    VG --> TM
```

---

## 4. C4 Component — Volume Data Plane

```mermaid
flowchart TB
    subgraph volume[Volume Server]
        direction TB

        subgraph http[HTTP / gRPC Handlers]
            WH[Write Handler\nPUT /{fid}]
            RH[Read Handler\nGET /{fid}]
            DH[Delete Handler]
            RPH[Replication Handler\nPUT + X-Replication]
        end

        subgraph store[VolumeStore]
            VM[Volume Manager]
            VF[Volume Files\n.dat / .idx]
            NE[Needle Engine\nencode/decode/CRC32]
            EC[Erasure Coding\n(optional)]
        end

        subgraph rep[Replication]
            SY[SyncReplicator\nHTTP fan-out]
        end

        subgraph cache[Cache]
            MC[In-Memory LRU\noptional Redis]
        end

        subgraph guard[Security]
            GD2[Guard\nIP whitelist + JWT]
        end
    end

    WH --> GD2 --> NE --> VM --> VF
    WH --> SY
    RH --> GD2 --> MC
    MC -->|"miss"| VM --> NE
    RPH --> GD2 --> NE --> VM
```

---

## 5. C4 Component — Filer Metadata Service

```mermaid
flowchart TB
    subgraph filer[Filer Server]
        direction TB

        subgraph http[HTTP / gRPC Handlers]
            PH[PUT /path]
            GH2[GET /path]
            DH2[DELETE /path]
            LH2[LIST /path]
        end

        subgraph ns[Namespace]
            FS2[Filer\nCreateEntry / FindEntry\nMkdirAll / DeleteEntry]
            LB[LogBuffer\nring buffer of changes]
            LM[LockManager\ndistributed KV locks]
        end

        subgraph backend[Pluggable Store]
            ST[FilerStore interface]
            LD[LevelDB2]
            RD[Redis3]
            PG2[Postgres2]
            MY[MySQL2]
            CS[Cassandra]
        end

        subgraph uploader[Chunk Uploader]
            CU[ChunkUploader\nassign → PUT per chunk]
        end
    end

    PH --> FS2
    GH2 --> FS2
    DH2 --> FS2
    LH2 --> FS2

    PH -->|"large files"| CU
    FS2 --> LB
    FS2 --> ST
    ST --> LD & RD & PG2 & MY & CS
    FS2 --> LM --> ST
```

---

## 6. Sequence — Blob Write (full path)

```mermaid
sequenceDiagram
    participant C as Client
    participant MS as Master
    participant RT as Raft
    participant VS as Volume Primary
    participant VR as Volume Replica
    participant CA as Cache

    C->>MS: POST /dir/assign {collection, replication, count}
    MS->>MS: Guard.Allowed() [IP + JWT]
    MS->>MS: Topo.PickForWrite()
    MS->>RT: Raft.Apply(MaxFileIdCommand)
    RT-->>MS: fid_start
    MS->>MS: SignJWT(fid, ttl) [if key present]
    MS-->>C: {fid, url, publicUrl, auth}

    C->>VS: PUT /{fid} [multipart body + auth header]
    VS->>VS: Guard.Allowed()
    VS->>VS: BuildNeedle(request)
    VS->>VS: store.WriteVolumeNeedle(vid, needle)
    Note over VS: seek EOF, align 8B, write .dat, update .idx

    par Replicate in parallel
        VS->>VR: HTTP PUT /{fid} [X-Replication:true]
        VR->>VR: WriteVolumeNeedle()
        VR-->>VS: 200 OK
    end

    VS->>CA: cache.Put(key, blob)
    VS-->>C: {size, eTag} 201 Created
```

---

## 7. Sequence — Blob Read

```mermaid
sequenceDiagram
    participant C as Client
    participant MS as Master
    participant VS as Volume Server
    participant CA as Cache

    C->>MS: GET /dir/lookup?volumeId=5
    MS-->>C: {locations: [{url, publicUrl}]}

    C->>VS: GET /{fid}
    VS->>VS: Guard.Allowed()
    VS->>CA: cache.Get(key)

    alt Cache hit
        CA-->>VS: blob bytes
        VS-->>C: 200 blob bytes [X-Cache:HIT]
    else Cache miss
        VS->>VS: idx.Get(needleId) → (offset, size)
        VS->>VS: dat.ReadAt(offset, size)
        VS->>VS: needle.VerifyCRC32()
        VS->>CA: cache.Put(key, blob)
        VS-->>C: 200 blob bytes
    end
```

---

## 8. Sequence — S3 PutObject

```mermaid
sequenceDiagram
    participant SC as S3 Client
    participant GW as S3 Gateway
    participant IA as IAM
    participant QM as QuotaManager
    participant FL as Filer
    participant MS as Master
    participant VS as Volume

    SC->>GW: PUT /<bucket>/<key> [SigV4 signed]
    GW->>GW: s3auth.VerifyRequest() [SigV4]
    GW->>IA: Authenticate(accessKey)
    IA-->>GW: identity
    GW->>IA: IsAuthorized(s3:PutObject, bucket/key)
    IA-->>GW: allow/deny

    GW->>QM: CheckQuota(bucket, size)
    QM-->>GW: ok

    GW->>FL: store.PutObject(bucket, key, body)

    loop per 100MB chunk
        FL->>MS: POST /dir/assign
        MS-->>FL: {fid, url}
        FL->>VS: PUT /{fid} [chunk bytes]
        VS-->>FL: {size, eTag}
    end

    FL->>FL: CreateEntry(chunks, extended metadata)
    FL-->>GW: ok

    GW-->>SC: 200 OK [ETag header]
```

---

## 9. Sequence — Raft Leader Election & ID Sequencing

```mermaid
sequenceDiagram
    participant M1 as Master-1 (candidate)
    participant M2 as Master-2
    participant M3 as Master-3
    participant FS as FileSequencer
    participant EV as EventBus

    Note over M1,M3: Leader election
    M1->>M2: RequestVote(term=5)
    M1->>M3: RequestVote(term=5)
    M2-->>M1: vote granted
    M3-->>M1: vote granted
    M1->>EV: publish StateEvent{Leader: M1}

    Note over M1: ID allocation
    M1->>M1: Raft.Apply(MaxFileIdCommand{count: 100})
    M1->>M2: AppendEntries(MaxFileIdCommand)
    M1->>M3: AppendEntries(MaxFileIdCommand)
    M2-->>M1: ack
    M3-->>M1: ack
    M1->>FS: FSM.Apply → update maxFileId
    FS-->>M1: {start: 10001, end: 10100}
```

---

## 10. State — Volume Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Allocated : Master AllocateVolume RPC

    Allocated --> Writable : Heartbeat received
    Writable --> ReadOnly : Capacity full OR\ncompaction scheduled
    ReadOnly --> Writable : After compaction
    Writable --> Replica : Replication role assigned

    Replica --> Writable : Primary failure + promotion

    ReadOnly --> Compacting : Compact triggered
    Compacting --> ReadOnly : Compaction complete

    ReadOnly --> Deleted : Admin delete
    Deleted --> [*]
```

---

## 11. State — Filer Entry Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Created : PUT /path or S3 PutObject

    Created --> Updated : PUT /path (overwrite)\nor S3 PutObject (same key)
    Updated --> Updated : subsequent overwrites

    Created --> Chunked : file > 64KB\n(chunked upload)
    Chunked --> Created : all chunks committed

    Created --> Deleted : DELETE /path\nor S3 DeleteObject
    Updated --> Deleted : DELETE /path

    Deleted --> [*]

    Created --> Versioned : S3 versioning enabled
    Versioned --> Deleted : delete marker placed
```

---

## 12. Data Model — Core Types

```mermaid
classDiagram
    class FileId {
        +VolumeId volumeId
        +NeedleId needleId
        +Cookie cookie
        +String() string
    }

    class Needle {
        +Cookie cookie
        +NeedleId id
        +uint32 dataSize
        +[]byte data
        +string name
        +string mime
        +[]byte pairs
        +int64 lastModified
        +TTL ttl
        +uint32 checksum
        +int64 appendAtNs
    }

    class Volume {
        +VolumeId id
        +string collection
        +NeedleVersion version
        +DiskType diskType
        +ReplicationFactor replication
        +bool readOnly
        +WriteNeedle(n Needle)
        +ReadNeedle(fid FileId) Needle
        +DeleteNeedle(nid NeedleId)
        +Compact()
    }

    class Entry {
        +FullPath fullPath
        +Attr attr
        +[]byte content
        +[]FileChunk chunks
        +map extended
    }

    class FileChunk {
        +string fileId
        +int64 offset
        +int64 size
        +string eTag
        +bool isCompressed
    }

    class DataNode {
        +ServerAddress addr
        +DiskType diskType
        +int64 freeSpace
        +[]VolumeId volumes
        +IsWritable() bool
    }

    class Topology {
        +[]DataCenter datacenters
        +GetOrCreateVolumeLayout()
        +PickForWrite(option)
        +ProcessJoinMessage(hb)
    }

    FileId --> Needle : references
    Volume "1" *-- "*" Needle : stores
    Entry "1" *-- "*" FileChunk : composed of
    FileChunk --> FileId : resolved to
    Topology "1" *-- "*" DataNode : manages
    DataNode "1" *-- "*" Volume : hosts
```

---

## 13. ER — Filer Metadata Schema (logical)

```mermaid
erDiagram
    ENTRY ||--o{ FILE_CHUNK : "has (chunked)"
    ENTRY }o--|| DIRECTORY : "child of"
    ENTRY ||--o{ KV_EXTENDED : "has metadata"
    BUCKET ||--o{ ENTRY : "contains"
    IAM_IDENTITY ||--o{ IAM_POLICY : "has"
    IAM_POLICY ||--o{ BUCKET : "governs"

    ENTRY {
        string full_path PK
        int64  mtime
        int64  ctime
        uint32 mode
        string mime
        int64  file_size
        bytes  content
        string collection
        string replication
        string ttl
        bytes  extended_json
    }

    FILE_CHUNK {
        string fid PK
        string entry_path FK
        int64  offset
        int64  size
        string etag
        bool   is_compressed
    }

    BUCKET {
        string name PK
        string owner
        string versioning
        bytes  acl_json
        bytes  lifecycle_json
    }

    IAM_IDENTITY {
        string access_key PK
        string secret_key
        string name
        string account_id
    }

    IAM_POLICY {
        string identity FK
        string action
        string resource
        string effect
    }
```

---

## 14. Flowchart — Volume Growth Decision

```mermaid
flowchart TD
    A([Assign Request]) --> B{Has writable volume\nfor layout?}
    B -->|Yes| C[Pick volume for write]
    B -->|No| D[Enqueue VolumeGrowOption]

    D --> E[processVolumeGrowRequests\nbackground loop]
    E --> F{Writable ratio\n≥ 90%?}
    F -->|Yes| G[Skip grow]
    F -->|No| H[VolumeGrowth.GrowByType]

    H --> I[Find candidate DataNodes\nby DiskType + Replication]
    I --> J{Enough candidates?}
    J -->|No| K[Return ErrNoFreeVolumes]
    J -->|Yes| L[Reserve slots on DataNodes]

    L --> M[gRPC AllocateVolume\nto each candidate]
    M --> N[Update Topology\nadd new volumes]
    N --> C

    C --> O[Sequencer.NextFileId]
    O --> P([Return fid + url])
```

---

## 15. Flowchart — Replication Pipeline (Async)

```mermaid
flowchart LR
    subgraph source[Source Cluster]
        SF[Source Filer]
        SLB[LogBuffer\nring buffer]
        SF --> SLB
    end

    subgraph replicator[Async Replicator Process]
        direction TB
        SUB[Subscribe\nMetadata stream]
        CLS[Classify event\ncreate/update/delete/rename]
        FETCH[Fetch entry + chunks\nfrom source volumes]
        ASSIGN[Assign target volumes\nfrom target master]
        PUSH[PUT chunks to\ntarget volumes]
        UPSERT[Create/Update/Delete\ntarget entry]
        METRICS[Record lag + status\nPrometheus]
    end

    subgraph target[Target Cluster]
        TM[Target Master]
        TV[Target Volume]
        TF[Target Filer]
    end

    SLB -->|"gRPC SubscribeMetadata"| SUB
    SUB --> CLS --> FETCH --> ASSIGN
    ASSIGN -->|"POST /dir/assign"| TM
    ASSIGN --> PUSH
    PUSH -->|"PUT /{fid}"| TV
    PUSH --> UPSERT
    UPSERT -->|"filer gRPC"| TF
    UPSERT --> METRICS
```
