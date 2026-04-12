# GoBlob Architecture Diagrams

## C4 Level 1 — System Context

```mermaid
flowchart TB
    subgraph boundary[GoBlob Distributed Blob Store]
        GOBLOB[("GoBlob\nDistributed Object Store")]
    end

    CLIENT[("👤 Application Client\nboto3 / HTTP / S3 SDK")]
    OPS[("👤 Operator\nCLI / Admin API")]
    EXTFS[("💾 External Storage\nS3 / GCS / Azure")]

    CLIENT -->|"S3 API / HTTP\nPUT/GET/DELETE objects"| GOBLOB
    OPS -->|"Admin HTTP\nvolume management, topology"| GOBLOB
    GOBLOB -->|"Tiered storage\noffload to remote"| EXTFS
```

---

## C4 Level 2 — Container Diagram

```mermaid
flowchart TB
    subgraph boundary[GoBlob System]
        direction TB

        subgraph masters[Master Cluster — Raft HA]
            M1[("⚙️ Master 1\nLeader")]
            M2[("⚙️ Master 2\nFollower")]
            M3[("⚙️ Master 3\nFollower")]
            M1 <-->|Raft consensus| M2
            M1 <-->|Raft consensus| M3
        end

        subgraph filers[Filer Layer]
            F1[("📂 Filer 1\nMetadata + Namespace")]
            F2[("📂 Filer 2\nMetadata + Namespace")]
        end

        subgraph volumes[Volume Servers — Data Plane]
            V1[("💾 Volume Server 1\nDisk A, Disk B")]
            V2[("💾 Volume Server 2\nDisk C, Disk D")]
            V3[("💾 Volume Server 3\nDisk E, Disk F")]
        end

        S3[("🪣 S3 API Gateway\nS3-compatible HTTP")]
        BOLTDB[("🗄️ BoltDB\nRaft log + stable store")]
        FSTORE[("🗄️ FilerStore\nLevelDB / SQL / Redis")]
    end

    CLIENT[("👤 Client")]
    CLIENT -->|"S3 API"| S3
    CLIENT -->|"HTTP\nPUT/GET /path"| F1
    S3 -->|"gRPC FilerService"| F1
    F1 & F2 -->|"Assign, Lookup\nHTTP"| M1
    F1 & F2 -->|"Write needle\nHTTP PUT"| V1 & V2 & V3
    M1 -->|"BoltDB"| BOLTDB
    F1 & F2 -->|"FilerStore"| FSTORE
    V1 -->|"Heartbeat\ngRPC"| M1
    V2 -->|"Heartbeat\ngRPC"| M1
    V3 -->|"Heartbeat\ngRPC"| M1
    V1 <-->|"Replication\nHTTP"| V2
    V1 <-->|"Replication\nHTTP"| V3
```

---

## C4 Level 3 — Master Component

```mermaid
flowchart TB
    subgraph master[MasterServer]
        direction TB
        subgraph http[HTTP Layer]
            HA["/dir/assign"]
            HL["/dir/lookup"]
            HG["/vol/grow"]
            HV["/vol/vacuum"]
            HP["/cluster/peer"]
        end
        subgraph grpc[gRPC Layer]
            GHB["SendHeartbeat\nstream"]
            GAP["Assign\nLookupVolume"]
        end
        subgraph core[Core Services]
            SEQ["RaftSequencer\nNextFileId(n)"]
            TOPO["Topology\nDC→Rack→Node→Volume"]
            VL["VolumeLayout\nwritable + read-only sets"]
            RAFT["RaftServer\nhashicorp/raft"]
            FSM["MasterFSM\nMaxFileId, MaxVolumeId"]
        end
        subgraph store[Persistence]
            BOLT["BoltDB\nraft-log.bolt\nraft-stable.bolt"]
            SNAP["SnapshotStore\n3 retained snapshots"]
        end
    end

    HA & HG --> SEQ
    HA --> TOPO
    GHB --> TOPO
    TOPO --> VL
    SEQ --> RAFT
    RAFT --> FSM
    FSM --> BOLT
    RAFT --> SNAP
```

---

## C4 Level 3 — Volume Server Component

```mermaid
flowchart TB
    subgraph vs[VolumeServer]
        direction TB
        subgraph http[HTTP Layer]
            HW["PUT /{fid}\nhandleWrite"]
            HR["GET /{fid}\nhandleRead"]
            HS["GET /status"]
        end
        subgraph grpc[gRPC Layer]
            GAV["AllocateVolume"]
            GVC["VolumeCopy\nCompact"]
        end
        subgraph store[VolumeStore]
            VOL["Volume\n.dat + .idx files"]
            NM["NeedleMap\nin-memory index\n16B per blob"]
            CACHE["LRU BlobCache\nconfigurable max entries"]
        end
        subgraph repl[Replication]
            REP["HTTPReplicator\nparallel write to replicas"]
        end
    end

    HW --> store
    HW --> REP
    HR --> CACHE
    CACHE -->|miss| VOL
    VOL --> NM
    GAV --> VOL
```

---

## C4 Level 3 — Filer Component

```mermaid
flowchart TB
    subgraph filer[FilerServer]
        direction TB
        subgraph http[HTTP Layer]
            FW["PUT /{path}"]
            FR["GET /{path}"]
            FD["DELETE /{path}"]
            FL["GET /{dir}/ listing"]
        end
        subgraph grpc[gRPC FilerService]
            GLE["LookupDirectoryEntry"]
            GLIST["ListEntries"]
            GCE["CreateEntry / UpdateEntry"]
            GSUB["SubscribeMetadata\nstream"]
        end
        subgraph core[Core]
            FILER["Filer\nchunk routing"]
            LB["LogBuffer\nmutation log segments"]
            DLM["DistLockManager"]
        end
        subgraph backends[FilerStore Backends]
            LDB["LevelDB\nembedded"]
            SQL["MySQL / Postgres\nremote SQL"]
            REDIS["Redis\nremote KV"]
            CASS["Cassandra\ndistributed NoSQL"]
        end
        subgraph client[Outbound Clients]
            WCLI["wdclient\nmaster discovery"]
            OP["operation.Assign()\noperation.Lookup()"]
        end
    end

    FW & FR & FD --> FILER
    FILER --> core
    FILER --> client
    GCE & GSUB --> LB
    GLE & GLIST --> backends
    LDB & SQL & REDIS & CASS -.-|implements FilerStore| backends
```

---

## Sequence — File Upload

```mermaid
sequenceDiagram
    participant C as Client
    participant F as FilerServer
    participant M as Master (Leader)
    participant V1 as VolumeServer (Primary)
    participant V2 as VolumeServer (Replica)
    participant FS as FilerStore

    C->>F: PUT /photos/vacation.jpg (body)
    F->>M: POST /dir/assign?count=N&replication=001
    M->>M: RaftSequencer.NextFileId(N)
    M->>M: Topology.PickForWrite()
    M-->>F: {fid: "3,0a1b2c", locations: [V1, V2]}
    loop for each chunk
        F->>V1: PUT /3,0a1b2c (needle bytes)
        V1->>V1: Volume.WriteNeedle() → .dat + .idx
        V1->>V2: HTTPReplicator.ReplicatedWrite()
        V2->>V2: Volume.WriteNeedle()
        V2-->>V1: 200 OK
        V1-->>F: {size, etag}
    end
    F->>FS: InsertEntry(path, Attr, Chunks[])
    F->>F: LogBuffer.AppendEntry()
    F-->>C: 201 Created
```

---

## Sequence — File Download

```mermaid
sequenceDiagram
    participant C as Client
    participant F as FilerServer
    participant FS as FilerStore
    participant M as Master
    participant V as VolumeServer

    C->>F: GET /photos/vacation.jpg
    F->>FS: FindEntry("/photos/vacation.jpg")
    FS-->>F: Entry{Attr, Chunks[{FileId, Offset, Size}]}
    loop for each chunk
        F->>M: GET /dir/lookup?volumeId=3
        M->>M: Topology.GetVolumeLayout(3)
        M-->>F: locations: [V1-addr, V2-addr]
        F->>V: GET /3,0a1b2c
        V->>V: ReadNeedleByFileId() → verify Cookie
        V-->>F: needle data
    end
    F-->>C: 200 OK (reassembled stream)
```

---

## Sequence — S3 PutObject

```mermaid
sequenceDiagram
    participant CLI as S3 Client (boto3)
    participant S3 as S3ApiServer
    participant F as FilerServer (gRPC)
    participant M as Master
    participant V as VolumeServer

    CLI->>S3: PUT /bucket/key (SigV4 signed)
    S3->>S3: IAM.Authenticate() + Quota.Check()
    S3->>F: gRPC CreateEntry(/buckets/bucket/key)
    F->>M: Assign FileIds
    M-->>F: fids + locations
    F->>V: Write needle(s)
    V-->>F: OK
    F->>F: InsertEntry metadata
    F-->>S3: OK
    S3-->>CLI: 200 OK (ETag header)
```

---

## Sequence — Raft Leader Election & Sequencer

```mermaid
sequenceDiagram
    participant M1 as Master 1 (Candidate)
    participant M2 as Master 2
    participant M3 as Master 3
    participant FSM as MasterFSM
    participant BOLT as BoltDB

    Note over M1,M3: ElectionTimeout fires on M1
    M1->>M2: RequestVote(term=5)
    M1->>M3: RequestVote(term=5)
    M2-->>M1: VoteGranted
    M3-->>M1: VoteGranted
    Note over M1: Quorum reached — M1 is Leader
    M1->>M1: Raft.Apply(MaxFileIdCommand{delta=1000})
    M1->>BOLT: Append log entry
    M1->>M2: AppendEntries(log[5])
    M1->>M3: AppendEntries(log[5])
    M2-->>M1: ACK
    M3-->>M1: ACK
    M1->>FSM: Apply(MaxFileIdCommand)
    FSM-->>M1: EventMaxFileId{newMax=5000}
    Note over M1: NextFileId returns [4001..5000]
```

---

## Volume Lifecycle — State Diagram

```mermaid
stateDiagram-v2
    [*] --> Growing : AllocateVolume (Master assigns)
    Growing --> Writable : Volume reaches node
    Writable --> ReadOnly : Volume full (8GB) OR manual seal
    ReadOnly --> Vacuuming : Vacuum triggered (>30% deleted)
    Vacuuming --> ReadOnly : Compact complete
    ReadOnly --> Deleting : DeleteVolume command
    Writable --> Deleting : Force delete
    Deleting --> [*] : Files removed from disk
```

---

## Topology Tree

```mermaid
flowchart TD
    ROOT["Root\n(cluster)"]
    DC1["DataCenter: dc1"]
    DC2["DataCenter: dc2"]
    R1["Rack: rack1"]
    R2["Rack: rack2"]
    R3["Rack: rack3"]
    DN1["DataNode\n10.0.0.1:8080"]
    DN2["DataNode\n10.0.0.2:8080"]
    DN3["DataNode\n10.0.0.3:8080"]
    D1["Disk: /data1\nmaxVol=100"]
    D2["Disk: /data2\nmaxVol=100"]
    D3["Disk: /data3\nmaxVol=100"]
    V1["Vol 1 (RW)\ncol=photos, rep=001"]
    V2["Vol 2 (RO)\ncol=photos, rep=001"]
    V3["Vol 3 (RW)\ncol=logs, rep=000"]

    ROOT --> DC1 & DC2
    DC1 --> R1 & R2
    DC2 --> R3
    R1 --> DN1
    R2 --> DN2
    R3 --> DN3
    DN1 --> D1
    DN2 --> D2
    DN3 --> D3
    D1 --> V1 & V2
    D2 --> V1
    D3 --> V3
```

---

## Replication Placement — ReplicaPlacement Encoding

```mermaid
flowchart LR
    subgraph rp[ReplicaPlacement byte: 0x21 = rep 021]
        direction LR
        B0["bits 7-6\nDifferentDC = 0\n(all in same DC)"]
        B1["bits 5-2\nDifferentRack = 2\n(across 2 racks)"]
        B2["bits 1-0\nSameRack = 1\n(one extra on same rack)"]
    end

    rp -->|Total copies = DC+Rack+Rack+1| TOTAL["3 copies total"]
```

---

## Needle Storage Format — Volume File Layout

```mermaid
flowchart TD
    subgraph dat[".dat file (append-only)"]
        N1["Needle 1 — Header 16B | Data | Footer 8B"]
        N2["Needle 2 — Header 16B | Data | Footer 8B"]
        N3["Needle 3 deleted — Header 16B | Size=0 | Footer 8B"]
        N1 --> N2 --> N3
    end

    subgraph idx[".idx file — 16B per entry"]
        I1["NeedleId → Offset, Size"]
        I2["NeedleId → Offset, Size"]
        I3["NeedleId → Offset=0, Size=0 tombstone"]
        I1 --- I2 --- I3
    end

    subgraph mem["NeedleMap in-memory"]
        M1["NeedleId → NeedleValue Offset/8, Size"]
    end

    dat -->|"each write appends and updates index"| idx
    idx -->|"loaded on startup"| mem
```

---

## FilerStore — Entry Data Model

```mermaid
classDiagram
    class Entry {
        +FullPath path
        +Attr attr
        +FileChunk[] chunks
        +[]byte content
        +map extended
        +HardLinkId hardLinkId
        +Remote remote
    }
    class Attr {
        +os.FileMode mode
        +time.Time mtime
        +string mimeType
        +string replication
        +string collection
        +TTL ttl
        +string userName
        +string groupName
    }
    class FileChunk {
        +string fileId
        +int64 offset
        +uint64 size
        +int64 modifiedTsNs
        +string etag
        +bool isCompressed
    }
    class Remote {
        +string storageType
        +string key
        +string bucket
    }

    Entry "1" *-- "1" Attr : has
    Entry "1" *-- "*" FileChunk : contains
    Entry --> Remote : optional
```

---

## Security Layer

```mermaid
flowchart LR
    subgraph sec[Security Middleware Stack]
        direction TB
        IP["IPWhitelist\nCIDR allow-list"]
        RATE["RateLimiter\nper-IP token bucket"]
        JWT["JWTAuth\nHS256/RS256 Bearer token"]
        TLS["TLS\ncert + key, optional mTLS"]
    end

    CLIENT["Client"] -->|HTTPS| TLS --> IP --> RATE --> JWT --> HANDLER["Handler"]
```
