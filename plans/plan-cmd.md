# Feature: Command Entrypoints (CLI Binary)

## 1. Purpose

This plan describes how all GoBlob subsystems are wired together into a single runnable binary (`blob`) with subcommands for each server role. It defines the CLI flag surface, the startup sequencing for each mode, signal handling, and how configuration flows from flags and config files into the option structs consumed by each subsystem.

## 2. Responsibilities

- **Binary entrypoint**: `main()` dispatches to the correct subcommand handler
- **Subcommands**: `master`, `volume`, `filer`, `s3`, `server` (all-in-one), `shell`
- **Flag parsing**: Map CLI flags to the option structs of each subsystem
- **Configuration loading**: Load `security.toml` and per-server config files; merge with flags
- **Startup sequencing**: For `server` mode, start services in the correct order with correct delays
- **Signal handling**: `SIGTERM` → graceful shutdown; `SIGHUP` → hot-reload configuration
- **Graceful shutdown**: Drain in-flight requests before stopping; close stores; release locks

## 3. Non-Responsibilities

- Does not implement any server logic (all logic lives in the subsystem packages)
- Does not manage Raft consensus directly (delegates to `plan-raft-consensus`)
- Does not store any data
- **Utility subcommands** (`backup`, `export`, `benchmark`, `upload`): the original GoBlob binary includes these utilities; GoBlob v1 focuses on the server roles listed above. Utility subcommands can be added post-v1 as thin wrappers around the Client SDK (`plan-client-sdk`).

## 4. Architecture Design

```
main()
  |
  +-- subcommand dispatch (os.Args[1])
       |
       +-- "master"    --> runMaster()
       +-- "volume"    --> runVolume()
       +-- "filer"     --> runFiler()
       +-- "s3"        --> runS3()
       +-- "server"    --> runServer()   (all-in-one)
       +-- "shell"     --> runShell()
       +-- "version"   --> printVersion()
       +-- "help"      --> printHelp()
```

### `server` startup sequence (all-in-one mode)

```
runServer():
  1. Start Master HTTP+gRPC servers
  2. Wait for master to elect a leader (~1s)
  3. Start Volume Server HTTP+gRPC servers
  4. Sleep 1 second (allow volume to register with master)
  5. Start Filer HTTP+gRPC servers
  6. Sleep 2 seconds (allow filer to sync with master)
  7. Start S3 gateway HTTP server (if enabled)

  block until SIGTERM or SIGINT
  shutdown in reverse order (gateways → filer → volume → master)
```

## 5. Core Data Structures (Go)

```go
package main

import (
    "flag"
    "os"
    "os/signal"
    "syscall"
    "context"
)

// ServerOptions is the aggregate config for `blob server` (all-in-one) mode.
type ServerOptions struct {
    // Master
    MasterPort      int
    MasterPeers     []string
    MasterMetaDir   string
    VolumeSizeMB    uint32
    DefaultReplication string

    // Volume
    VolumePort      int
    VolumeDir       []string   // can be repeated: -dir /data1 -dir /data2
    VolumeMax       int        // max volumes per dir

    // Filer
    FilerPort       int
    FilerDefaultDir string

    // S3
    S3Port          int
    S3Enabled       bool

    // Common
    Host            string
    DataCenter      string
    Rack            string
    ConfigDir       string
}
```

## 6. Public Interfaces

```go
package main

func main()

// Each subcommand registers itself via init():
func init() {
    Commands["master"]    = &MasterCommand{}
    Commands["volume"]    = &VolumeCommand{}
    Commands["filer"]     = &FilerCommand{}
    Commands["s3"]        = &S3Command{}
    Commands["server"]    = &ServerCommand{}
    Commands["shell"]     = &ShellCommand{}
}

// Command is the interface every subcommand implements.
type Command interface {
    Name() string
    Synopsis() string
    Usage() string
    SetFlags(fs *flag.FlagSet)
    Run(ctx context.Context, args []string) error
}
```

## 7. Internal Algorithms

### main()
```
main():
  if len(os.Args) < 2:
    printHelp()
    os.Exit(1)

  name = os.Args[1]
  cmd = Commands[name]
  if cmd == nil:
    fmt.Fprintf(os.Stderr, "unknown command: %s\n", name)
    os.Exit(1)

  fs = flag.NewFlagSet(name, flag.ExitOnError)
  cmd.SetFlags(fs)
  fs.Parse(os.Args[2:])

  ctx, cancel = signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
  defer cancel()

  // SIGHUP → hot-reload
  sighup = make(chan os.Signal, 1)
  signal.Notify(sighup, syscall.SIGHUP)
  go func():
    for range sighup:
      onSIGHUP()

  if err = cmd.Run(ctx, fs.Args()); err != nil:
    fmt.Fprintf(os.Stderr, "error: %v\n", err)
    os.Exit(1)
```

### runMaster()
```
MasterCommand.Run(ctx, args):
  // Load security config
  secCfg = security.LoadConfig(opt.ConfigDir)

  // Create master option from flags
  masterOpt = &master.MasterOption{
    Host:               opt.Host,
    Port:               opt.MasterPort,
    MetaDir:            opt.MasterMetaDir,
    Peers:              opt.MasterPeers,
    VolumeSizeLimitMB:  opt.VolumeSizeMB,
    DefaultReplication: opt.DefaultReplication,
    SecurityConfig:     secCfg,
  }

  // Start HTTP server
  httpMux = http.NewServeMux()
  ms, err = master.NewMasterServer(httpMux, masterOpt, opt.MasterPeers)
  if err: return err

  // Start Raft
  raftSvr, err = raft.NewRaftServer(masterOpt.MetaDir, masterOpt.Peers, masterOpt.Host, masterOpt.Port)
  ms.SetRaftServer(raftSvr)

  // Start gRPC server
  grpcSvr = startGRPCServer(ms, secCfg, masterOpt.Port+10000)

  // Start HTTP server
  httpSvr = &http.Server{Addr: fmt.Sprintf(":%d", masterOpt.Port), Handler: httpMux}
  go httpSvr.ListenAndServe()

  log.Info("master started", "port", masterOpt.Port)

  // Block until context cancelled
  <-ctx.Done()

  // Graceful shutdown
  ms.Shutdown()
  grpcSvr.GracefulStop()
  httpSvr.Shutdown(context.Background())
  return nil
```

### runVolume()
```
VolumeCommand.Run(ctx, args):
  secCfg = security.LoadConfig(opt.ConfigDir)

  dirs = parseDirFlags(opt.VolumeDir, opt.VolumeMax)
  volumeOpt = &volume.VolumeServerOption{
    Host:        opt.Host,
    Port:        opt.VolumePort,
    Masters:     opt.Masters,
    Directories: dirs,
    DataCenter:  opt.DataCenter,
    Rack:        opt.Rack,
    SecurityConfig: secCfg,
  }

  adminMux  = http.NewServeMux()
  publicMux = http.NewServeMux()
  vs, err = volume.NewVolumeServer(adminMux, publicMux, volumeOpt)
  if err: return err

  grpcSvr = startGRPCServer(vs, secCfg, volumeOpt.Port+10000)
  httpSvr = startHTTPServer(adminMux, volumeOpt.Port)
  if volumeOpt.PublicPort > 0:
    publicSvr = startHTTPServer(publicMux, volumeOpt.PublicPort)

  log.Info("volume server started", "port", volumeOpt.Port)

  <-ctx.Done()

  vs.Shutdown()
  grpcSvr.GracefulStop()
  httpSvr.Shutdown(context.Background())
  return nil
```

### runFiler()
```
FilerCommand.Run(ctx, args):
  secCfg = security.LoadConfig(opt.ConfigDir)

  defaultMux  = http.NewServeMux()
  readonlyMux = http.NewServeMux()
  filerOpt = &filerserver.FilerOption{
    Masters:         opt.Masters,
    Host:            opt.Host,
    Port:            opt.FilerPort,
    DefaultStoreDir: opt.FilerDefaultDir,
    SecurityConfig:  secCfg,
  }

  fs, err = filerserver.NewFilerServer(defaultMux, readonlyMux, filerOpt)
  if err: return err

  grpcSvr = startFilerGRPCServer(fs, secCfg, filerOpt.Port+10000)
  httpSvr = startHTTPServer(defaultMux, filerOpt.Port)

  log.Info("filer started", "port", filerOpt.Port)

  <-ctx.Done()
  fs.Shutdown()
  grpcSvr.GracefulStop()
  httpSvr.Shutdown(context.Background())
  return nil
```

### runS3()
```
S3Command.Run(ctx, args):
  secCfg = security.LoadConfig(opt.ConfigDir)

  s3Opt = &s3api.S3ApiServerOption{
    Filer:          types.ServerAddress(opt.FilerAddr),
    Port:           opt.S3Port,
    FilerRootPath:  opt.FilerRootPath,
    BucketsPath:    opt.BucketsPath,
    DomainName:     opt.DomainName,
    SecurityConfig: secCfg,
  }

  mux = http.NewServeMux()
  s3Server, err = s3api.NewS3ApiServer(mux, s3Opt)
  if err: return err

  httpSvr = startHTTPServer(mux, s3Opt.Port)
  log.Info("s3 gateway started", "port", s3Opt.Port)

  <-ctx.Done()
  httpSvr.Shutdown(context.Background())
  return nil
```

### runServer() — all-in-one
```
ServerCommand.Run(ctx, args):
  secCfg = security.LoadConfig(opt.ConfigDir)

  // 1. Master
  masterSvr = startMasterInProcess(opt, secCfg)

  // 2. Wait ~1s for Raft election
  time.Sleep(1 * time.Second)

  // 3. Volume
  volumeSvr = startVolumeInProcess(opt, secCfg)

  // 4. Wait for volume to register
  time.Sleep(1 * time.Second)

  // 5. Filer
  filerSvr = startFilerInProcess(opt, secCfg)

  // 6. Wait for filer sync
  time.Sleep(2 * time.Second)

  // 7. Optional gateways
  if opt.S3Enabled:      s3Svr   = startS3InProcess(opt, secCfg)

  log.Info("blob server started (all-in-one mode)")

  <-ctx.Done()

  // Graceful shutdown in reverse order
  if opt.S3Enabled:      s3Svr.Shutdown()
  filerSvr.Shutdown()
  volumeSvr.Shutdown()
  masterSvr.Shutdown()
  return nil
```

### Signal handling — SIGHUP (hot-reload)
```
onSIGHUP():
  // Reload security configuration (TLS certs, JWT keys)
  security.ReloadConfig()

  // Volume server: scan for new volume files added since startup
  if volumeSvr != nil:
    volumeSvr.LoadNewVolumes()

  // Filer: reload FilerConf (path-specific collection/replication rules)
  if filerSvr != nil:
    filerSvr.ReloadConfig()

  log.Info("configuration reloaded (SIGHUP)")
```

## 8. Persistence Model

The `cmd` package itself persists nothing. Each subsystem handles its own persistence. The config files (`security.toml`, `filer.toml`, etc.) are read-only from the cmd layer's perspective.

## 9. Concurrency Model

Each subcommand runs in the main goroutine, blocks on `<-ctx.Done()`, then shuts down in sequence. Each server (master, volume, filer, etc.) runs its own goroutine pool internally.

```
main goroutine:  cmd.Run() → blocks on <-ctx.Done()
signal goroutine: SIGTERM/SIGINT → cancel ctx
sighup goroutine: SIGHUP → call onSIGHUP()
```

## 10. Configuration

### CLI Flags

**Master:**
```
-port              int     Master HTTP port (default: 9333)
-grpc.port         int     Master gRPC port (default: port+10000)
-mdir              string  Raft metadata directory (required)
-peers             string  Comma-separated peer master addresses
-volumeSizeLimitMB uint    Max volume size in MB (default: 30000)
-replication       string  Default replication (default: "000")
-garbageThreshold  float64 Vacuum trigger ratio (default: 0.3)
-defaultReplication string Same as -replication
-dataCenter        string  Data center label
-rack              string  Rack label
```

**Volume:**
```
-port              int     HTTP port (default: 8080)
-grpc.port         int     gRPC port (default: port+10000)
-publicPort        int     Public read-only port (default: 0=disabled)
-masters           string  Comma-separated master addresses (required)
-dir               string  Data directory (repeatable)
-max               int     Max volumes per directory (default: 8)
-dataCenter        string
-rack              string
-readMode          string  local/proxy/redirect (default: redirect)
-fileSizeLimitMB   int     Max file size (default: 256)
-concurrentUploadLimitMB int64  Upload throttle (default: 0=unlimited)
-concurrentDownloadLimitMB int64
```

**Filer:**
```
-port              int     HTTP port (default: 8888)
-grpc.port         int
-masters           string  Comma-separated master addresses (required)
-defaultReplication string
-defaultCollection string
-maxMB             int     Max file chunk size (default: 4)
-concurrentUploadLimitMB int64
```

**S3:**
```
-port              int     HTTP port (default: 8333)
-filer             string  Filer address (default: localhost:8888)
-filer.path        string  Root path in filer namespace (default: /)
-domainName        string  Virtual-hosted-style domain (optional)
-allowEmptyFolder  bool
-allowDeleteBucketNotEmpty bool
```

**Common to all:**
```
-ip                string  Bind/advertise IP (default: detected)
-config            string  Config directory for security.toml (default: .)
-logLevel          string  debug/info/warn/error (default: info)
-logFormat         string  text/json (default: text)
-metricsPort       int     Prometheus metrics port (default: 0=disabled)
-metricsAddress    string  Pushgateway address (optional)
```

**Server (all-in-one merges all above flags):**
```
All master, volume, filer, s3 flags plus:
-s3               bool    Enable S3 gateway (default: false)
-volume.port      int     Volume port (overrides -port for volume role)
-filer.port       int     Filer port (overrides -port for filer role)
-s3.port          int     S3 port (default: 8333)
```

## 11. Observability

- Version and build info printed at startup: `log.Info("blob starting", "version", Version, "commit", Commit)`
- Each subcommand logs its bind address and port at INFO level on start
- Shutdown sequence logged at INFO: `"shutting down <component>"`
- SIGHUP reload logged at INFO: `"configuration reloaded"`
- `obs.BuildInfo.WithLabelValues(version, commit, goVersion).Set(1)` — emitted at startup

## 12. Testing Strategy

- **Unit tests**:
  - `TestFlagParsing`: parse flag strings into option structs, assert correct field values
  - `TestDefaultValues`: call SetFlags with no args, assert all defaults applied correctly
  - `TestSubcommandDispatch`: mock Command registry, assert correct command invoked for each subcommand name
  - `TestUnknownSubcommand`: pass unknown subcommand, assert non-zero exit code

- **Integration tests**:
  - `TestServerModeStartup`: run `blob server` in-process with all-in-one mode, assert all services respond on their ports within 5 seconds
  - `TestMasterSubcommand`: start `blob master`, assert `/dir/status` returns 200
  - `TestVolumeSubcommand`: start master + `blob volume`, assert heartbeat received by master
  - `TestFilerSubcommand`: start master + volume + `blob filer`, assert filer connects
  - `TestGracefulShutdown`: start `blob server`, send SIGTERM, assert all ports close cleanly within 10s
  - `TestSIGHUP`: start server, send SIGHUP, assert no crash and log line emitted

## 13. Open Questions

None.
