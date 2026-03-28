# Admin Shell and Maintenance

### Purpose

Provide operator-focused control commands for cluster inspection, rebalancing, replication fixes, lock coordination, and periodic maintenance automation.

### Scope

**In scope:**
- Interactive shell runtime in `goblob/shell/shell.go`.
- Shell command registry and command implementations in `goblob/shell/command_*.go`.
- Maintenance scheduler and script loading in `goblob/command/maintenance.go`.
- CLI commands that invoke shell automation (`blob shell`, `blob fix`, master/server maintenance script scheduling).

**Out of scope:**
- Core master/volume/filer APIs invoked by these commands.
- S3 request path behavior outside maintenance commands.

### Primary User Flow

1. Operator runs `blob shell` (interactive) or `blob shell -c "..."` (single command).
2. Shell resolves command names and executes gRPC/HTTP calls against master/filer/volume services.
3. Commands print human-readable status or perform maintenance actions (optionally dry-run).
4. Long-running master/server processes can run scheduled maintenance scripts periodically when leader.

### System Flow

1. `ShellCommand.Run` builds `ShellOption` and creates `shell.NewShell` with shared gRPC dial options.
2. `Shell.Exec` tokenizes input (`shellSplit`) and dispatches to command implementation from runtime `commands map`.
3. Commands use `CommandEnv` helpers (`masterGRPCAddress`, `masterHTTPAddress`, `filerGRPCAddress`) and invoke APIs:
   - `cluster.status` / `cluster.ps`: master `VolumeList`.
   - `volume.list`: master `VolumeList` with per-disk rows.
   - `fs.ls`: filer `ListEntries` streaming.
   - `lock` / `unlock`: filer distributed lock APIs.
   - maintenance commands (`volume.balance`, `volume.move`, `volume.vacuum`, `volume.fix.replication`, `s3.clean.uploads`) orchestrate multi-step operations.
4. Scheduler path (`startMaintenanceScheduler`):
   - loads script from file or default script string.
   - every interval, if master is leader, runs shell script via `shell.RunScript`.

```
Operator -> blob shell
         -> Shell.Exec(command)
            -> command.Do(args,env,writer)
               -> master/filer/volume APIs

Leader loop (master/server command)
  -> startMaintenanceScheduler ticker
     -> if Raft.IsLeader() run shell script lines
```

### Data Model

- `ShellOption` (`goblob/shell/env.go`):
  - `Masters []string`, `FilerAddress string`, `Directory string`.
- `CommandEnv` shared state:
  - gRPC dial option, address helpers, and lock state (`isLocked`, `lockRenewToken`, `lockOwner`) guarded by mutex.
- Maintenance script format:
  - newline-delimited shell commands; default script includes:
    - `volume.balance -dryRun=true`
    - `volume.fix.replication -dryRun=true`
    - `volume.vacuum`
    - `s3.clean.uploads -olderThan=24h`

### Interfaces and Contracts

- CLI shell commands:
  - `blob shell -masters <csv> -filer <addr> [-c "cmd"]`.
  - `blob fix -cmd "volume.fix.replication -dryRun=true"` executes one shell command non-interactively.
- Registered shell command names (current code):
  - `cluster.status`, `cluster.ps`, `volume.list`, `volume.balance`, `volume.move`, `volume.vacuum`, `volume.fix.replication`, `s3.clean.uploads`, `fs.ls`, `lock`, `unlock`.
- Scheduler contract:
  - master/server commands support maintenance script path + interval flags.
  - only leader executes script body.

### Dependencies

**Internal modules:**
- `goblob/shell` command implementations.
- `goblob/pb/*` client helpers for master/filer/volume gRPC calls.
- `goblob/command/maintenance.go` for scheduler wiring.

**External services/libraries:**
- Requires reachable master/filer/volume endpoints.
- Maintenance actions that call HTTP endpoints depend on network reachability and service health.

### Failure Modes and Edge Cases

- Unknown shell command -> `unknown command: <name>`.
- Missing master/filer addresses -> command-specific validation errors.
- Lock/unlock with missing token or wrong token returns explicit lock errors.
- Maintenance script file read failure aborts master/server startup path when configured.
- Scheduler is a best-effort loop; command failures are logged to writer and next interval continues.
- Some maintenance commands default to dry-run and must be explicitly switched off to mutate state.

### Observability and Debugging

- Shell command output goes to provided writer (stdout for CLI).
- Scheduled maintenance writes failures like `maintenance script failed: ...` to stderr/writer.
- Debug points:
  - `shell/shell.go:Exec` for parsing/dispatch issues.
  - `shell/command_maintenance.go` for move/repair decision logic.
  - `command/maintenance.go:startMaintenanceScheduler` for leader-only execution checks.

### Risks and Notes

- Most shell commands are orchestration wrappers around live APIs and have limited transactional rollback guarantees.
- `volume.move` and balance operations involve multi-step workflows (readonly, copy, delete) and can leave partial state if intermediate operations fail.
- Command catalog is registered via `init()` side effects, so missing package imports remove commands silently.

Changes:

