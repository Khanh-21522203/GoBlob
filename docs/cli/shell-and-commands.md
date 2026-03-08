# Shell and CLI Commands

## Assumptions

- The `blob shell` command provides an interactive admin CLI connected to the master server.
- Shell commands are registered via `init()` functions in individual command files.
- The master server can run periodic maintenance scripts using shell commands automatically.

## Code Files / Modules Referenced

- `goblob/command/shell.go` - CLI entry for `blob shell`
- `goblob/shell/command.go` - `Command` interface, `CommandEnv`, `Commands` registry
- `goblob/shell/command_volume_balance.go` - `volume.balance` command
- `goblob/shell/command_volume_fix_replication.go` - `volume.fix.replication`
- `goblob/shell/command_volume_vacuum.go` - `volume.vacuum` command
- `goblob/shell/command_volume_move.go` - `volume.move` command
- `goblob/shell/command_s3_*.go` - S3-related commands
- `goblob/shell/command_lock.go` - Cluster-wide lock for maintenance
- `goblob/server/master_server.go` - `startAdminScripts()` for automated maintenance
- `goblob/wdclient/masterclient.go` - `MasterClient` for shell connectivity

## Overview

The GoBlob shell (`blob shell`) is an interactive command-line interface for cluster administration. It connects to the master server and provides commands for volume management (balance, move, vacuum, fix replication), S3 configuration, and cluster maintenance. The master server can also run shell commands automatically as periodic maintenance scripts.

## Responsibilities

- **Interactive admin CLI**: Provide a REPL for cluster administration
- **Volume operations**: Balance, move, vacuum, fix replication, list, configure
- **S3 management**: Configure S3 IAM identities, bucket settings, clean stale uploads
- **Cluster maintenance**: Lock/unlock cluster, run automated scripts

## Architecture Role

```
+------------------------------------------------------------------+
|                        weed shell                                 |
+------------------------------------------------------------------+
|                                                                   |
|  +--------------------+                                           |
|  | Interactive REPL   |  (stdin/stdout)                           |
|  | or Script Mode     |  (pipe commands)                         |
|  +--------+-----------+                                           |
|           |                                                       |
|  +--------v-----------+                                           |
|  |   CommandEnv       |                                           |
|  |   (execution ctx)  |                                           |
|  +--------+-----------+                                           |
|           |                                                       |
|  +--------v-----------+-----------+-------------------------------+  |
|  | Volume Cmds        | S3 Cmds           | Cluster Cmds          |  |
|  | volume.balance     | s3.configure      | cluster.ps            |  |
|  | volume.move        | s3.bucket.list    | cluster.raft.*        |  |
|  | volume.vacuum      | s3.clean.uploads  |                       |  |
|  | volume.fix.repl    |                   |                       |  |
|  | volume.list        |                   |                       |  |
|  +--------------------+-------------------+-----------------------+  |
|  | Lock Cmds                                                      |  |
|  | lock / unlock                                                  |  |
|  +----------------------------------------------------------------+  |
|           |                    |                    |             |
|           v                    v                    v             |
|  +--------+----+      +-------+------+     +-------+------+     |
|  | Master gRPC |      | Filer gRPC   |     | Volume gRPC  |     |
|  +-------------+      +--------------+     +--------------+     |
+------------------------------------------------------------------+
```

## Component Structure Diagram

```
+---------------------------------------------------------------+
|                       CommandEnv                               |
+---------------------------------------------------------------+
| MasterClient    *wdclient.MasterClient  # master connection   |
| option          *ShellOptions                                  |
|   Masters       *string                 # master addresses     |
|   FilerAddress  pb.ServerAddress        # active filer         |
|   FilerGroup    *string                 # filer group filter   |
|   GrpcDialOption grpc.DialOption                               |
+---------------------------------------------------------------+
| Commands registered via init():                                |
|   map[string]Command                                           |
+---------------------------------------------------------------+

+---------------------------------------------------------------+
|                     Command Interface                          |
+---------------------------------------------------------------+
| Name() string                                                  |
| Help() string                                                  |
| Do(args []string, env *CommandEnv, writer io.Writer) error     |
+---------------------------------------------------------------+

Example commands (registered via init()):
  commandVolumeBalance{} -> "volume.balance"
  commandVolumeVacuum{}  -> "volume.vacuum"
  commandS3CleanUploads{} -> "s3.clean.uploads"
  commandLock{}          -> "lock"
  commandUnlock{}        -> "unlock"
  ... (many more)
```

## Control Flow

### Interactive Shell Session

```
weed shell -master=localhost:9333
    |
    +--> Parse options (master, filer, filerGroup)
    +--> NewCommandEnv(options)
    |       +--> NewMasterClient(grpcDialOption, ...)
    |       +--> go MasterClient.KeepConnectedToMaster()
    |
    +--> Enter REPL loop:
    |    +--> Read line from stdin
    |    +--> Parse command name + args
    |    +--> Find command in Commands registry
    |    +--> command.Do(args, env, stdout)
    |    +--> Print result / error
    |    +--> Loop
    |
    +--> On EOF/Ctrl+D: exit
```

### Automated Maintenance Scripts (Master)

```
startAdminScripts()  (called by master leader)
    |
    +--> Read master.maintenance.scripts from config
    |    (default: "volume.balance -force;
    |              volume.fix.replication;
    |              volume.vacuum;
    |              s3.clean.uploads -olderThan=24h")
    |
    +--> Prepend "lock" if not present
    +--> Append "unlock" if not present
    |
    +--> NewCommandEnv(shellOptions)
    +--> go MasterClient.KeepConnectedToMaster()
    |
    +--> go func() {
    |       for {
    |           sleep(master.maintenance.sleep_minutes)  // default 17 min
    |           if isLeader && masterConnected:
    |               if adminServerConnected: skip
    |               for each script line:
    |                   processEachCmd(regex, line, commandEnv)
    |       }
    |    }
```

## Runtime Sequence Flow

### volume.balance Command

```
Shell                   Master              Volume Servers
  |                       |                      |
  |-- volume.balance ---->|                      |
  |                       |                      |
  |  (query topology) --->|                      |
  |  <-- datacenter/rack/ |                      |
  |      node layout      |                      |
  |                       |                      |
  |  (calculate ideal     |                      |
  |   distribution)       |                      |
  |                       |                      |
  |  For each imbalance:  |                      |
  |  move volume from     |                      |
  |  overloaded to under- |                      |
  |  loaded server ------>|----> VolumeCopy ---->|
  |                       |<---- OK -------------|
  |                       |----> VolumeMount --->|
  |                       |<---- OK -------------|
  |                       |----> VolumeDelete -->|
  |                       |     (on source)      |
  |                       |<---- OK -------------|
  |                       |                      |
  |<-- done --------------|                      |
```

## Data Flow Diagram

```
Command Registration:

  command_volume_balance.go:
    func init() {
        Commands = append(Commands, &commandVolumeBalance{})
    }

  ... (each command file registers itself)

  Result: Commands []Command = [volume.balance, volume.vacuum, ..., s3.clean.uploads, ...]


Maintenance Script Flow:

  master.toml
  +----------------------------------------+
  | [master.maintenance]                   |
  |   scripts = """                        |
  |     volume.balance -force              |
  |     volume.fix.replication             |
  |     volume.vacuum                      |
  |     s3.clean.uploads -olderThan=24h    |
  |   """                                  |
  |   sleep_minutes = 17                   |
  +----------------------------------------+
       |
       v (master leader reads)
  +--------+
  | Master | --> lock --> run scripts --> unlock
  | Leader |     (every 17 minutes)
  +--------+
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `goblob/wdclient` | Master client for gRPC connectivity |
| `goblob/security` | TLS for gRPC connections |
| `goblob/pb/master_pb` | Master service RPC calls |
| `goblob/pb/volume_server_pb` | Volume server RPC calls |
| `goblob/pb/filer_pb` | Filer RPC calls |
| `goblob/operation` | Client-side volume operations |

## Error Handling

- **Command not found**: Prints help text listing available commands
- **Master disconnected**: MasterClient reconnects; commands fail with connection error
- **Volume operation failure**: Logged and reported; typically non-fatal for batch operations
- **Lock contention**: `lock` command blocks until cluster lock acquired; timeout possible
- **Admin server connected**: Master skips automated scripts if admin server is connected (user has priority)

## Async / Background Behavior

| Goroutine | Purpose |
|---|---|
| `MasterClient.KeepConnectedToMaster()` | Persistent connection for command execution |
| Master maintenance loop | Periodic script execution (every `sleep_minutes`) |
| Volume move/copy | Long-running operations during balance/replication |

## Configuration

- **Default scripts**: `volume.balance`, `volume.fix.replication`, `volume.vacuum`, `s3.clean.uploads`
- **Sleep interval**: `master.maintenance.sleep_minutes` (default 17)
- **Shell flags**: `-master`, `-filer`, `-filerGroup`
- **volume.balance flags**: `-force`, `-collection`, `-diskType`
- **s3.clean.uploads flags**: `-olderThan` (24h)

## Edge Cases

- **Lock wrapping**: Automated scripts auto-wrap with `lock`/`unlock` if not present
- **Admin server priority**: If admin server is connected, maintenance scripts are skipped
- **Leader-only execution**: Automated scripts only run on the master leader
- **Pipe mode**: Commands can be piped via stdin for scripted automation
