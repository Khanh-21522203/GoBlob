## Phase 5 Implementation Plan

- [x] Task 1: Client SDK (`goblob/operation`) complete and validated
- [x] Task 2: IAM System (`goblob/s3api/iam` + gRPC service on filer port)
- [x] Task 3: S3 API Gateway Core (`goblob/s3api` server/router + bucket/object handlers)
- [x] Task 4: S3 SigV4 Authentication (`goblob/s3api/auth`)
- [x] Task 5: Multipart Upload (`goblob/s3api/multipart` or integrated handlers)
- [x] Task 6: S3 Advanced Features (SSE/versioning/policy/CORS/tagging)
- [x] Integration: wire IAM service registration in `FilerServer`
- [x] Integration: run focused tests (`operation`, `s3api`, `server`, `filer`)
- [x] Verification: full suite `go test ./goblob/...` and phase-targeted race suite

## Review

- Implemented `goblob/s3api/iam` with filer KV persistence (`__iam_config__`), lookup/authorization logic, reload support, signing key derivation, and unit tests.
- Added filer-side IAM gRPC service (`goblob/server/iam_grpc_server.go`) and registration in `NewFilerServer`.
- Implemented `goblob/s3api/auth` SigV4 header + presigned verification and IAM authorization integration.
- Implemented `goblob/s3api` gateway with S3 bucket/object handlers, XML responses, batch delete, multipart flow, and advanced features (SSE-S3 metadata, versioning, bucket policy, CORS, tagging).
- Added integration tests in `goblob/s3api/server_test.go` covering bucket/object CRUD, multipart end-to-end, and advanced feature flows.
- Verification completed:
  - `go test ./goblob/s3api/...`
  - `go test ./goblob/operation/...`
  - `go test ./goblob/server -run TestIAMGRPCServer`
  - `go test ./goblob/filer/...`
  - `go test ./goblob/...`
  - `go test -race ./goblob/s3api/... ./goblob/operation/...`

## Multipart Durability Follow-up

- [x] Persist multipart upload state under filer staging path (`/buckets/{bucket}/.uploads/{uploadId}/...`)
- [x] Remove in-memory-only upload dependency from complete/abort flow
- [x] Validate restart resilience with integration test (fresh S3 server instance completes existing upload)
- [x] Re-run `go test ./goblob/s3api/...` and `go test ./goblob/...`

### Durability Result

- Multipart uploads now persist metadata (`.meta`) and parts under filer path `/buckets/{bucket}/.uploads/{uploadId}/`.
- Completion and abort now resolve upload state from filer instead of in-memory S3 process state.
- Added restart validation test: `TestS3MultipartSurvivesRestart`.

## Phase 6 Implementation Plan

- [x] Implement command framework (`goblob/command/command.go`) and CLI entrypoint dispatch in `goblob/blob.go`
- [x] Implement server role subcommands: `master`, `volume`, `filer`, `s3`
- [x] Implement all-in-one `server` subcommand with startup/shutdown sequencing
- [x] Implement shell package (`goblob/shell`) with REPL, `shellSplit`, command registry
- [x] Implement shell commands required for checkpoint: `cluster.status`, `volume.list`, `fs.ls`, `lock`, `unlock`
- [x] Wire `blob shell` subcommand to shell REPL and one-shot `-c` command execution
- [x] Add unit tests for command dispatch and shell parsing/registry
- [x] Run `go test ./goblob/command/... ./goblob/shell/...` and full `go test ./goblob/...`

## Phase 6 Review

- Completed `blob` entrypoint dispatch with signal-aware context and SIGHUP handling scaffold in `goblob/blob.go`.
- Fixed CLI role command issues in `goblob/command` (including S3 command compile/runtime wiring and all-in-one server S3 filer address wiring).
- Implemented `goblob/shell` with command registry, REPL, one-shot exec support, shell argument splitting, and required commands:
  - `cluster.status` (plus alias `cluster.ps`)
  - `volume.list`
  - `fs.ls`
  - `lock` / `unlock` (distributed lock via filer gRPC)
- Added tests:
  - `goblob/command/command_test.go`
  - `goblob/shell/shell_test.go`
- Verification completed:
  - `go test ./goblob/command/... ./goblob/shell/...`
  - `go test ./goblob/...`

## Phase 6 Follow-up: SIGHUP Reload

- [x] Wire SIGHUP from `main` to command-level reload dispatcher
- [x] Add command runtime reload hooks for `master`, `volume`, `filer`, `s3`, and all-in-one `server`
- [x] Implement live server reload handlers:
  - `MasterServer.ReloadSecurityConfig`
  - `VolumeServer.ReloadSecurityConfig`
  - `VolumeServer.LoadNewVolumes`
  - `FilerServer.ReloadConfig`
- [x] Make `security.Guard` runtime-updatable and concurrency-safe (white list + JWT keys)
- [x] Add command-level SIGHUP hook tests (`goblob/command/reload_test.go`)
- [x] Re-run full verification suite

### SIGHUP Result

- SIGHUP now triggers real reload logic via `command.HandleSIGHUP(...)` instead of a log-only scaffold.
- Security config is reloaded and applied to live guard state for master/volume/filer runtime servers.
- Volume runtime additionally re-scans disk locations for newly discovered volumes on reload.

## Phase 0-6 Wiring Closure Plan

- [x] Implement missing maintenance shell commands: `volume.balance`, `volume.vacuum`, `volume.fix.replication`, `s3.clean.uploads`
- [x] Wire maintenance scheduler for master/server modes using `MaintenanceSleep` and optional script override
- [x] Add utility CLI subcommands expected by roadmap outputs: `backup`, `export`, `compact`, `fix`, `benchmark`
- [x] Replace filer metadata aggregator TODO with concrete startup wiring
- [x] Add focused tests for new command/shell wiring and run full `go test ./goblob/...`
- [x] Update review notes with residual caveats (if any)

### Wiring Closure Review

- Added shell maintenance commands and wiring:
  - `volume.balance`
  - `volume.move`
  - `volume.vacuum`
  - `volume.fix.replication`
  - `s3.clean.uploads`
- Added master/server maintenance scheduler wiring with default script + optional script file override.
- Added utility CLI subcommands:
  - `backup`
  - `export`
  - `compact`
  - `fix`
  - `benchmark`
- Replaced filer metadata aggregator TODO with `metadataAggregatorLoop()` startup wiring.
- Added focused tests:
  - `goblob/command/maintenance_test.go`
  - expanded `goblob/shell/shell_test.go` built-in command assertions.
- Verification completed:
  - `go test ./goblob/command/... ./goblob/shell/... ./goblob/server/...`
  - `go test ./goblob/...`
  - `go build -o /tmp/blob ./goblob` + `blob help` + `blob shell -c help`

## Phase 0-6 Wiring Closure Follow-up Plan

- [x] Implement real volume data copy path (`VolumeCopy` + `ReadAllNeedles`) for inter-node maintenance actions
- [x] Enable `volume.move` apply mode with source readonly protection, target copy, and source delete
- [x] Enable `volume.fix.replication` apply mode with candidate selection and replica copy workflow
- [x] Ensure master topology removes stale per-node volumes when heartbeats stop reporting them
- [x] Add regression tests for copy path and stale-volume topology cleanup
- [x] Re-run targeted and full verification suite

### Follow-up Review

- Implemented actual gRPC copy pipeline:
  - `VolumeGRPCServer.ReadAllNeedles` now streams live needles from source volume.
  - `VolumeGRPCServer.VolumeCopy` now allocates target volume, pulls source needles over gRPC, and writes them locally.
- `volume.move` now supports apply mode:
  - sets source volume readonly,
  - copies volume to target node,
  - deletes source volume after successful copy.
- `volume.fix.replication` now supports apply mode:
  - detects under-replicated volumes,
  - picks candidate target nodes with free slots,
  - copies replicas from an existing source node.
- Topology heartbeat processing now removes stale volumes that are no longer reported by a live node heartbeat.
- Added focused regression tests:
  - `goblob/server/volume_grpc_server_copy_test.go`
  - `goblob/topology/topology_test.go` stale-volume heartbeat case
- Verification completed:
  - `go test ./goblob/server/... ./goblob/shell/... ./goblob/topology/...`
  - `go test ./goblob/...`

### Remaining Caveats

- No known Phase 0-6 wiring caveats remain from the previous closure list.
