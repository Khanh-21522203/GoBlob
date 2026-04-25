# GoBlob

A distributed blob storage system written in Go — store and serve billions of files efficiently with an S3-compatible API.

GoBlob separates metadata management (Master + Filer) from data storage (Volume Servers), enabling independent horizontal scaling of each layer. It is inspired by SeaweedFS and uses Raft consensus for master high availability.

## Architecture

```
Clients (S3 SDK / HTTP / gRPC)
        │
        ▼
┌───────────────┐    ┌────────────────┐
│  S3 Gateway   │    │  Filer Server  │   ← metadata + namespace
│  :8333        │    │  :8888         │
└──────┬────────┘    └──────┬─────────┘
       │                    │
       ▼                    ▼
┌──────────────────────────────────────┐
│   Master Cluster (Raft, 3 nodes)     │   ← topology + file-ID allocation
│   master1:9333  master2:9333  ...    │
└────────────────┬─────────────────────┘
                 │
     ┌───────────┼───────────┐
     ▼           ▼           ▼
┌─────────┐ ┌─────────┐ ┌─────────┐
│ Volume1 │ │ Volume2 │ │ Volume3 │   ← raw blob storage (needles)
│  :8080  │ │  :8081  │ │  :8082  │
└─────────┘ └─────────┘ └─────────┘
```

See [docs/README.md](docs/README.md) for the full architecture reference.

## Quick Start

**Prerequisites:** Docker and Docker Compose.

```bash
# Start 3-master + 3-volume + filer + S3 gateway
docker compose up --build -d

# Verify the cluster is healthy (~20s for Raft election)
curl -X POST http://localhost:9333/dir/assign   # returns a file ID
curl http://localhost:8888/healthz               # filer health check
```

**Ports (host → service):**

| Service      | HTTP           | gRPC            | Metrics |
|--------------|----------------|-----------------|---------|
| master1      | 9333           | 19333           | 9090    |
| master2      | 9335           | 19335           | 9095    |
| master3      | 9337           | 19337           | 9096    |
| volume1      | 8080           | 18080           | 9091    |
| volume2      | 8081           | 18081           | 9097    |
| volume3      | 8082           | 18082           | 9098    |
| Filer        | 8888           | 18888           | 9092    |
| S3 Gateway   | 8333           | —               | 9093    |

## Benchmark Results

Benchmarks were run against a 3-master / 3-volume Docker Compose cluster on an AMD Ryzen 7 PRO 8840HS (16 cores, 90 GB RAM) running Linux. All operations completed with **0 failures**.

The benchmark binary runs inside the Docker network (`docker compose exec`) so volume service names resolve correctly.

### How to reproduce

```bash
docker compose up --build -d
sleep 20   # wait for Raft election

# Volume ops (assign/upload/download) — run inside master1 container
docker compose exec master1 /blob benchmark -master master1:9333 -op assign       -n 5000 -c 50
docker compose exec master1 /blob benchmark -master master1:9333 -op upload       -n 2000 -c 30 -size 4096
docker compose exec master1 /blob benchmark -master master1:9333 -op upload       -n 1000 -c 20 -size 65536
docker compose exec master1 /blob benchmark -master master1:9333 -op upload       -n 500  -c 10 -size 1048576
docker compose exec master1 /blob benchmark -master master1:9333 -op upload       -n 100  -c 5  -size 10485760 -timeout 60s
docker compose exec master1 /blob benchmark -master master1:9333 -op download     -n 2000 -c 30 -size 4096
docker compose exec master1 /blob benchmark -master master1:9333 -op download     -n 500  -c 10 -size 1048576  -timeout 30s
docker compose exec master1 /blob benchmark -master master1:9333 -op download     -n 100  -c 5  -size 10485760 -timeout 60s

# Filer ops — run inside filer container
docker compose exec filer /blob benchmark -filer filer:8888 -op filer-write -n 1000 -c 20 -size 4096
docker compose exec filer /blob benchmark -filer filer:8888 -op filer-read  -n 1000 -c 20 -size 4096
docker compose exec filer /blob benchmark -filer filer:8888 -op filer-write -n 500  -c 10 -size 1048576  -timeout 30s
docker compose exec filer /blob benchmark -filer filer:8888 -op filer-read  -n 500  -c 10 -size 1048576  -timeout 30s
docker compose exec filer /blob benchmark -filer filer:8888 -op filer-write -n 50   -c 5  -size 10485760 -timeout 60s
docker compose exec filer /blob benchmark -filer filer:8888 -op filer-read  -n 50   -c 5  -size 10485760 -timeout 60s
```

### Results — 3 master nodes, 3 volume nodes

All results from a single Docker host (bridge network, in-container benchmark binary).

#### Volume server (assign / upload / download)

| Operation       | Size   | n     | c  | QPS    | p50      | p95      | p99      | p999     |
|-----------------|--------|-------|----|--------|----------|----------|----------|----------|
| assign          | —      | 5,000 | 50 | 5,976  | 7.91 ms  | 12.84 ms | 19.43 ms | 23.16 ms |
| upload          | 4 KB   | 2,000 | 30 | 4,525  | 6.36 ms  | 10.35 ms | 12.39 ms | 14.96 ms |
| upload          | 64 KB  | 1,000 | 20 | 2,821  | 6.76 ms  | 10.75 ms | 12.63 ms | 13.87 ms |
| upload          | 1 MB   | 500   | 10 | 642    | 14.64 ms | 23.84 ms | 27.90 ms | 30.71 ms |
| upload          | 10 MB  | 100   | 5  | 65     | 75.44 ms | 102.93 ms| 105.05 ms| 105.05 ms|
| download        | 4 KB   | 2,000 | 30 | 18,906 | 0.97 ms  | 4.43 ms  | 12.90 ms | 18.88 ms |
| download        | 1 MB   | 500   | 10 | 1,616  | 5.93 ms  | 9.33 ms  | 11.52 ms | 11.89 ms |
| download        | 10 MB  | 100   | 5  | 120    | 38.69 ms | 60.15 ms | 67.24 ms | 67.24 ms |

#### Filer (metadata + blob write/read)

| Operation       | Size   | n     | c  | QPS    | p50      | p95      | p99      | p999     |
|-----------------|--------|-------|----|--------|----------|----------|----------|----------|
| filer-write     | 4 KB   | 1,000 | 20 | 11,216 | 1.45 ms  | 4.33 ms  | 5.61 ms  | 7.21 ms  |
| filer-read      | 4 KB   | 1,000 | 20 | 21,894 | 0.79 ms  | 2.16 ms  | 3.01 ms  | 3.57 ms  |
| filer-write     | 1 MB   | 500   | 10 | 402    | 21.98 ms | 34.39 ms | 77.21 ms | 230.13 ms|
| filer-read      | 1 MB   | 500   | 10 | 706    | 12.23 ms | 28.33 ms | 39.16 ms | 45.61 ms |
| filer-write     | 10 MB  | 50    | 5  | 63     | 74.76 ms | 105.25 ms| 119.63 ms| 119.63 ms|
| filer-read      | 10 MB  | 50    | 5  | 97     | 49.27 ms | 71.60 ms | 74.42 ms | 74.42 ms |

**Notes:**
- All benchmarks run inside the Docker bridge network on a single host — numbers reflect intra-host throughput, not production distributed performance.
- Assign / upload / download requests go through master1 (a Raft follower); write commands are proxied to the leader. The ~40% QPS overhead vs. single-node is the Raft consensus round-trip.
- 4 KB filer writes/reads operate on inline data stored in LevelDB (no volume hop), which is why they are faster than 4 KB volume reads.
- Large file filer-write goes through ChunkUpload → assign → volume PUT. Filer-read fetches each chunk from the volume server by looking up the location via the master.
- Download throughput for large files is bounded by the volume server's read I/O + Docker bridge network copy.

### Native Raft transport microbenchmarks

These benchmarks exercise the experimental native Raft transport layer added for future optimization work. They run on localhost with `-benchtime=100x`, so the numbers are useful for relative comparison and regression detection, not production sizing.

```bash
GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache \
  go test -run '^$' \
  -bench 'BenchmarkTransport|BenchmarkNativeAssignApply|BenchmarkHashicorpAssignApply' \
  -benchtime=100x \
  ./goblob/consensus/native ./goblob/consensus/hashicorpraft

GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache \
  go test -run '^$' \
  -bench 'BenchmarkNativeAssignApply/tcp_three_node' \
  -benchtime=200x \
  -cpuprofile /tmp/goblob-native-assign.cpu \
  ./goblob/consensus/native
```

| Benchmark | Result |
|-----------|--------|
| native heartbeat, memory transport | 232.3 ns/op |
| native heartbeat, TCP transport | 114.157 us/op |
| native append, memory transport | 100.7 ns/op |
| native append, TCP transport | 125.615 us/op |
| native batch append, memory transport | 2.043 us/op, 31.64M entries/s |
| native batch append, TCP transport | 274.595 us/op, 233K entries/s |
| native 1 MiB snapshot install, memory transport | 3.027 ms/op, 346 MB/s |
| native 1 MiB snapshot install, TCP transport | 12.290 ms/op, 85 MB/s |
| native assign apply, single-node memory | 2.687 us/op, 379K assigns/s |
| native assign apply, 3-node TCP | 267.569 us/op, 3.7K assigns/s |
| HashiCorp Raft assign apply, 3-node TCP | 1.077 ms/op, 932 assigns/s |

Transport decision notes:
- Native TCP is intentionally simple today: JSON framing, one TCP connection per RPC, and no binary codec or connection reuse.
- Batch append already changes the network profile substantially: native TCP batch append reached 233K entries/s in this local benchmark, so batching should be tuned before considering kernel-bypass networking.
- Snapshot transfer is dominated by payload encode/copy and FSM restore behavior in this benchmark; optimize binary framing and streaming before lower-level network work.
- The native file store still uses simple JSON persistence, so fsync/write-path policy should be designed before production benchmarking.
- DPDK is not justified by the current evidence. The next transport improvements should be connection reuse, binary encoding, batching policy, and async disk/write pipeline work.

## Project Layout

```
goblob/
├── command/       CLI subcommands (master, volume, filer, s3, benchmark, …)
├── server/        Server implementations (MasterServer, VolumeServer, FilerServer, S3)
├── filer/         Filer metadata layer + pluggable store backends
│   ├── leveldb2/  Default embedded metadata backend
│   ├── redis3/    Redis backend
│   ├── postgres2/ PostgreSQL backend
│   └── mysql2/    MySQL backend
├── storage/       Volume needle storage engine
├── topology/      Cluster topology and volume placement
├── raft/          Raft consensus (via hashicorp/raft)
├── operation/     Client-side assign/upload/download helpers
├── s3api/         S3-compatible API handlers
└── security/      JWT auth, rate limiting, TLS
```

## Documentation

- [Architecture deep-dive](docs/README.md)
