# Feature: Admin Shell & Maintenance

## 1. Purpose

The Admin Shell provides an interactive REPL and a batch-script interface for operating a live GoBlob cluster. It exposes commands for volume maintenance (balance, vacuum, move, fix-replication) and cluster coordination (lock, unlock, cluster status). The master server runs a subset of these commands automatically on a leader-elected schedule.

## 2. Responsibilities

- **Interactive REPL**: Read-eval-print loop over stdin/stdout
- **Batch scripts**: Execute newline-separated command strings (used by master's automated maintenance)
- **Volume operations**: balance, move, vacuum, fix-replication, mark-readonly
- **Cluster operations**: lock, unlock, cluster.ps, cluster.check
- **S3 operations**: s3.clean.uploads (remove incomplete multipart uploads)
- **Distributed lock**: Acquire/release admin lock to prevent concurrent shell sessions from conflicting

## 3. Non-Responsibilities

- Does not store blob data or metadata directly
- Does not implement the volume server, filer, or master logic itself (shells into their APIs)
- Does not provide end-user file access (that is the gateway layer's job)
- Does not manage Raft consensus or leader election

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                      AdminShell                               |
+--------------------------------------------------------------+
|                                                               |
|  stdin             Automated (master leader)                 |
|  REPL loop         startAdminScripts()                       |
|       |                   |                                  |
|  CommandEnv           CommandEnv                             |
|  (shared context)     (same env, script mode)               |
|       |                   |                                  |
|  +----+-------------------+----+                             |
|  |        Command Registry     |                             |
|  |  lock        volume.vacuum  |                             |
|  |  unlock      volume.balance |                             |
|  |  cluster.ps  volume.move    |                             |
|  |  s3.clean.uploads           |                             |
|  +-------------+---------------+                             |
|                |                                             |
|  +-------------+---------------+                             |
|  |        CommandEnv           |                             |
|  |  masterClient  filerClient  |                             |
|  |  topology      option       |                             |
|  +-----------------------------+                             |
+--------------------------------------------------------------+
         |                  |
   Master gRPC         Filer HTTP/gRPC
   Volume gRPC         Volume HTTP
```

### Command Registration

Commands self-register via `init()` functions. The registry is a package-level slice populated at program startup. The shell looks up commands by name at runtime.

## 5. Core Data Structures (Go)

```go
package shell

import (
    "io"
    "sync"
    "goblob/core/types"
    "goblob/pb"
    "goblob/topology"
)

// CommandEnv is the shared execution context passed to every command.
// It holds live connections to cluster services and current operational state.
type CommandEnv struct {
    // MasterClient connects to the master server for topology queries.
    MasterClient *pb.MasterClient

    // FilerAddress is the current target filer for filesystem commands.
    FilerAddress types.ServerAddress

    // GrpcDialOption is the TLS/insecure option for all outbound gRPC dials.
    GrpcDialOption grpc.DialOption

    // option holds the current shell configuration.
    option *ShellOption

    // isLocked tracks whether the admin lock is held by this session.
    isLocked bool
    lockMu   sync.Mutex

    // topology is a local snapshot of the cluster topology for inspection.
    // Updated on each command that requires it.
    topology *topology.Topology
}

// ShellOption holds configuration for an admin shell session.
type ShellOption struct {
    Masters      []string
    FilerAddress string
    Directory    string // current working directory for filesystem commands
}

// Command is the interface all admin commands must implement.
type Command interface {
    // Name returns the command name as typed by the user (e.g., "volume.balance").
    Name() string

    // Help returns a short description and usage example.
    Help() string

    // Do executes the command with the given arguments.
    // args[0] is the command name; args[1:] are the arguments.
    // Output is written to writer (typically stdout or a TCP connection).
    Do(args []string, env *CommandEnv, writer io.Writer) error
}

// Shell is the top-level REPL struct.
type Shell struct {
    env      *CommandEnv
    commands map[string]Command
    writer   io.Writer
    reader   io.Reader
}

// AdminLock represents a cluster-level distributed lock for maintenance operations.
// The lock is stored as a KV entry in the filer store.
type AdminLock struct {
    LockedBy  string    // shell session identifier
    LockedAt  time.Time
    ExpiresAt time.Time // 0 = no expiry
}
```

### Command Registration Pattern

```go
// In each command file:
func init() {
    Commands = append(Commands, &CommandVolumeBalance{})
}

// Commands is the package-level registry.
var Commands []Command
```

## 6. Public Interfaces

```go
package shell

// NewShell creates a new admin shell connected to the given masters.
func NewShell(opt *ShellOption, grpcDialOption grpc.DialOption, reader io.Reader, writer io.Writer) (*Shell, error)

// Run starts the REPL loop, reading commands from reader and writing output to writer.
// Returns when the reader is exhausted (EOF) or "exit" is entered.
func (s *Shell) Run() error

// RunScript executes a newline-separated sequence of commands.
// Used by the master's automated maintenance goroutine.
func (s *Shell) RunScript(script string) error

// Exec executes a single command line string.
func (s *Shell) Exec(line string) error

// RegisterCommand adds a command to this shell's registry.
// Normally called during init() via the package-level Commands slice.
func (s *Shell) RegisterCommand(cmd Command)
```

### Built-in Commands (implementing Command interface)

```go
// Cluster coordination
type CommandLock   struct{}   // "lock"   — acquire admin lock in filer KV
type CommandUnlock struct{}   // "unlock" — release admin lock
type CommandClusterPs struct{} // "cluster.ps" — list connected nodes

// Volume maintenance
type CommandVolumeBalance       struct{} // "volume.balance"
type CommandVolumeMove          struct{} // "volume.move"
type CommandVolumeVacuum        struct{} // "volume.vacuum"
type CommandVolumeFixReplication struct{} // "volume.fix.replication"
type CommandVolumeMarkReadonly   struct{} // "volume.mark.readonly"

// S3
type CommandS3CleanUploads struct{} // "s3.clean.uploads"
```

## 7. Internal Algorithms

### REPL Loop
```
Shell.Run():
  for:
    print prompt: "goblob> "
    line = reader.ReadLine()
    if line == "exit" or EOF: return

    err = s.Exec(line)
    if err != nil:
      fmt.Fprintf(writer, "error: %v\n", err)
```

### Exec (single command dispatch)
```
Shell.Exec(line):
  args = shellSplit(line)  // respects quoted strings
  if len(args) == 0: return nil
  name = args[0]

  cmd = s.commands[name]
  if cmd == nil:
    fmt.Fprintf(writer, "unknown command: %s\ntype 'help' for a list\n", name)
    return nil

  return cmd.Do(args, s.env, s.writer)
```

### lock / unlock
```
CommandLock.Do(args, env, writer):
  // Store lock entry in filer KV
  key = []byte("admin-shell-lock")
  existing, err = env.filerKvGet(key)
  if err == nil and existing != nil:
    lock = parseLock(existing)
    if !lock.IsExpired():
      fmt.Fprintf(writer, "already locked by %s since %s\n", lock.LockedBy, lock.LockedAt)
      return ErrLocked

  lock = AdminLock{LockedBy: env.SessionId, LockedAt: time.Now()}
  env.filerKvPut(key, marshalLock(lock))
  env.lockMu.Lock()
  env.isLocked = true
  env.lockMu.Unlock()
  fmt.Fprintf(writer, "lock acquired\n")

CommandUnlock.Do(args, env, writer):
  env.filerKvDelete([]byte("admin-shell-lock"))
  env.lockMu.Lock()
  env.isLocked = false
  env.lockMu.Unlock()
  fmt.Fprintf(writer, "lock released\n")
```

### volume.vacuum
```
CommandVolumeVacuum.Do(args, env, writer):
  threshold = parseFlag(args, "-threshold", env.option.GarbageThreshold)
  collection = parseFlag(args, "-collection", "")

  // Step 1: check garbage ratio on each volume server
  for each dataNode in env.topology.AllDataNodes():
    resp = GET http://dataNode.HttpAddr/vol/vacuum/check?collection=...
    if resp.GarbageRatio < threshold: continue

    // Step 2: trigger compaction
    GET http://dataNode.HttpAddr/vol/vacuum/needle?collection=...

    // Step 3: commit
    GET http://dataNode.HttpAddr/vol/vacuum/commit?collection=...
    fmt.Fprintf(writer, "vacuumed volume %d on %s\n", vid, dataNode.Addr)
```

### volume.balance
```
CommandVolumeBalance.Do(args, env, writer):
  dryRun = hasFlag(args, "-dryRun")

  // Fetch topology snapshot via master gRPC
  topo = fetchTopology(env.MasterClient)

  // Find overloaded sources and underloaded targets
  for each (collection, rp, ttl) in topo.VolumeLayouts():
    avg = totalVolumes / totalNodes
    for each node where node.VolumeCount > avg+1:
      for each node where node.VolumeCount < avg:
        vid = pickVolumeToMove(source, target, rp)
        if dryRun:
          fmt.Fprintf(writer, "would move %d from %s to %s\n", vid, source, target)
        else:
          volume.move(vid, source, target, env)
```

### volume.move
```
moveVolume(vid, sourceAddr, targetAddr, env):
  // 1. Copy volume to target via gRPC VolumeCopy
  pb.WithVolumeServerClient(targetAddr, func(client):
    client.VolumeCopy(ctx, &volume_server_pb.VolumeCopyRequest{
      VolumeId:       vid,
      SourceDataNode: sourceAddr,
    })
  )

  // 2. Mark source volume as readonly
  pb.WithVolumeServerClient(sourceAddr, func(client):
    client.VolumeMarkReadonly(ctx, &volume_server_pb.VolumeMarkReadonlyRequest{VolumeId: vid})
  )

  // 3. Mount on target, unmount from source via master
  masterClient.VolumeList(...)
  // wait for replication to catch up
  // 4. Delete from source
  pb.WithVolumeServerClient(sourceAddr, func(client):
    client.VolumeDelete(ctx, &volume_server_pb.VolumeDeleteRequest{VolumeId: vid})
  )
```

### s3.clean.uploads
```
CommandS3CleanUploads.Do(args, env, writer):
  bucket = parseFlag(args, "-bucket", "")
  olderThan = parseFlag(args, "-olderThan", "24h")
  threshold = time.Now().Add(-olderThan)

  // List staging directories under /buckets/<bucket>/.uploads/
  prefix = fmt.Sprintf("/buckets/%s/.uploads/", bucket)
  filerClient.ListEntries(ctx, prefix, func(entry):
    if entry.Attr.Crtime.Before(threshold):
      // Delete all parts and the upload directory
      filerClient.DeleteEntry(ctx, entry.FullPath, recursive=true)
      fmt.Fprintf(writer, "cleaned upload %s\n", entry.FullPath)
```

### Automated Maintenance (called by master)
```
startAdminScripts(ms):
  // Default script (if none configured):
  defaultScript = `
    volume.balance
    volume.fix.replication
    volume.vacuum
    s3.clean.uploads -olderThan=24h
  `
  script = ms.option.MaintenanceConfig.Scripts
  if script == "": script = defaultScript

  // Wrap with lock/unlock if not present
  if !strings.Contains(script, "lock"):
    script = "lock\n" + script + "\nunlock"

  go func():
    ticker = time.NewTicker(ms.option.MaintenanceConfig.SleepMinutes * time.Minute)
    for:
      select:
      case <-ticker.C:
        if !ms.Raft.IsLeader(): continue
        if ms.adminLocks.IsLocked(): continue
        shell = NewShell(opt, grpcDialOption, strings.NewReader(script), logWriter)
        shell.RunScript(script)
      case <-ms.ctx.Done():
        return
```

## 8. Persistence Model

The admin shell itself is stateless. The only persistent artifact is the distributed lock stored in the filer's KV store:

```
Key:   []byte("__admin_shell_lock__")
Value: protobuf AdminLock{LockedBy, LockedAt, ExpiresAt}
```

Locks expire if the shell session crashes without calling `unlock`. The master's automated maintenance checks `adminLocks.IsLocked()` (a local flag set when the master's own shell session holds the lock) before running scripts.

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `AdminLocks.mu sync.Mutex` | Prevents concurrent automated maintenance runs on the same master |
| Filer KV distributed lock | Prevents concurrent interactive shell sessions across different machines |
| Sequential command execution | Commands within a script run sequentially; no parallelism within a session |
| Parallel volume operations | `volume.balance` issues parallel gRPC calls to different volume servers |

The shell REPL is single-threaded per session. Multiple concurrent sessions are allowed but must each acquire the distributed lock before running mutating operations.

## 10. Configuration

```go
type ShellOption struct {
    // Masters is the list of master addresses for topology queries.
    Masters []string

    // FilerAddress is the target filer for filesystem commands.
    FilerAddress string

    // GarbageThreshold is the default vacuum trigger ratio (0.0-1.0).
    GarbageThreshold float64 `default:"0.3"`
}

// MaintenanceConfig (embedded in MasterOption) controls automated runs.
type MaintenanceConfig struct {
    // Scripts is a newline-separated list of shell commands.
    // Empty string uses the built-in default maintenance script.
    Scripts string

    // SleepMinutes is the interval between automated maintenance runs.
    SleepMinutes int `default:"17"`
}
```

## 11. Observability

- Automated maintenance script start/completion logged at INFO with master node ID
- Each command's execution is logged at DEBUG with command name and arguments
- Lock acquisition and release logged at INFO with session ID
- Volume move operations logged at INFO with source, target, and volume ID
- Failed commands logged at ERROR with full error text
- `obs.AdminShellCommandsTotal.WithLabelValues(commandName, "ok"/"error").Inc()` per command

Debug endpoints (on master HTTP):
- `GET /cluster/status` — shows whether admin lock is currently held

## 12. Testing Strategy

- **Unit tests**:
  - `TestShellExecUnknownCommand`: unknown command name, assert error message to writer
  - `TestShellRunScript`: multi-line script with known commands, assert all executed in order
  - `TestCommandLockAcquire`: acquire lock on empty KV, assert success
  - `TestCommandLockConflict`: acquire lock, then acquire again from different session, assert blocked
  - `TestCommandUnlock`: acquire then release, assert KV entry removed
  - `TestCommandVolumeVacuumDryRun`: mock volume server, assert no actual vacuum triggered
  - `TestCommandS3CleanUploads`: mock filer with 2 stale uploads and 1 fresh, assert only stale deleted
  - `TestAutomatedScriptLockCheck`: script runs when unlocked, skipped when locked

- **Integration tests**:
  - `TestShellEndToEnd`: start master + filer + volume server, run `lock; volume.balance; unlock` via shell, assert output correct
  - `TestAutomatedMaintenance`: start master as leader, wait one maintenance interval, assert maintenance log entries

## 13. Open Questions

None.
