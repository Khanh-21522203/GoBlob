# CLI Command Runtime

### Purpose

Provide a single `blob` executable entrypoint that dispatches subcommands, manages SIGHUP reload hooks, and standardizes command exit behavior.

### Scope

**In scope:**
- Process entrypoint in `goblob/blob.go`.
- Command registration/lookup/flag parsing in `goblob/command/command.go`.
- Built-in command help/version output.
- Runtime reload hook wiring (`setReloadHook`, `HandleSIGHUP`) in `goblob/command/reload.go`.
- Server lifecycle abstractions (`RunnableServer`, `ServerDeps`, builder vars) in `goblob/command/runtime.go`.
- Registered command surfaces from `goblob/command/*.go` (`master`, `volume`, `filer`, `s3`, `webdav`, `server`, `shell`, `benchmark`, `backup`, `compact`, `export`, `fix`, `lifecycle.process`, `quota.get`, `quota.set`, `replicator`, `replication.status`, `volume.ec.encode`, `volume.tier.upload`).

**Out of scope:**
- Command-specific server internals (documented in service feature files).
- Admin shell command internals (`goblob/shell/*`, documented separately).

### Primary User Flow

1. Operator runs `blob <command> [flags]`.
2. Runtime resolves command by name from the in-memory registry.
3. Command-specific flags are parsed and validated.
4. Command `Run()` executes and returns success or error; runtime maps this to exit code.
5. If process receives SIGHUP, runtime invokes the currently registered reload hook.

### System Flow

1. Entry point: `goblob/blob.go:main` creates a context bound to `SIGINT`/`SIGTERM`, starts a goroutine for `SIGHUP`, and calls `command.Execute`.
2. Dispatch: `goblob/command/command.go:Execute` handles `help`/`version`, resolves the command from global `commands map[string]Command`, and parses flags via `flag.NewFlagSet`.
3. Execution: concrete command `Run(ctx,args)` executes (`goblob/command/*.go`).
4. Reload path: `goblob/command/reload.go:HandleSIGHUP` reads `reloadHook` under lock and prints success/failure to stderr.
5. Exit: `Execute` returns code `0` (success), `1` (runtime command error), or `2` (usage/parse/unknown command); `main` exits with that code.

```
argv -> blob.go main
       -> command.Execute
          -> command.Lookup(name)
             -> cmd.SetFlags + fs.Parse
                -> cmd.Run(ctx,args)
       -> exit code

SIGHUP -> HandleSIGHUP -> reloadHook() -> "configuration reloaded" or "reload failed"
```

### Data Model

- `command.Command` (`goblob/command/command.go`) - interface with methods: `Name()`, `Synopsis()`, `Usage()`, `SetFlags(*flag.FlagSet)`, `Run(context.Context, []string) error`.
- Global registry:
  - `commands map[string]Command` guarded by `commandsMu sync.RWMutex`.
  - population model: command packages call `Register(...)` from `init()`.
- Reload state:
  - `reloadHook func() error` protected by `reloadMu sync.RWMutex`.
  - persistence: in-memory only (not persisted across process restarts).
- Server runtime abstraction (`goblob/command/runtime.go`):
  - `RunnableServer` interface: `Addr() string`, `Shutdown(ctx) error` — uniform lifecycle handle for master, volume, and filer server instances.
  - `ServerDeps` struct: `HTTPListener net.Listener`, `GRPCListener net.Listener` — injected by tests to control bound addresses without port allocation races.
  - Builder vars (`DefaultMasterBuilder`, `DefaultVolumeBuilder`, `DefaultFilerBuilder`) are package-level function pointers pointing to production `buildMasterRuntime`/etc.; tests replace them with stubs.

### Interfaces and Contracts

- CLI contract: `blob <command> [flags]`.
- Help/version:
  - `blob help` prints command table.
  - `blob version` prints `Version`, `Commit`, `BuildDate`, Go runtime from `goblob/command/version.go`.
- Exit code contract from `Execute`:
  - `0` success.
  - `1` command returned error.
  - `2` usage/flag/unknown command error.
- Signal contract:
  - SIGHUP triggers `HandleSIGHUP`; output is written to stderr.
  - If no hook set: `received SIGHUP: no reload action for current command`.

### Dependencies

**Internal modules:**
- `goblob/command/*` - concrete command implementations.
- `goblob/config` - reload path loads `security.toml` through `config.NewViperLoader(nil)`.

**External services/libraries:**
- Go standard `flag`, `os/signal`, `syscall`.
- No mandatory network dependency at dispatch layer; dependencies are command-specific.

### Failure Modes and Edge Cases

- Unknown command name: `Execute` prints `unknown command: <name>` and returns exit code `2`.
- Flag parse failure: `flag.FlagSet.Parse` error returns exit code `2`.
- Command returns error: wrapped as `command "<name>" failed: ...`, exit code `1`.
- `Register(nil)` or empty command names are ignored.
- Reload hook missing or failing prints explicit stderr message; runtime does not crash.
- `stdin` is currently ignored by `Execute` (`_ = stdin`), so streaming input must be handled by command-specific implementations.

### Observability and Debugging

- Primary diagnostics are stderr strings from `Execute` and `HandleSIGHUP`.
- Entry debug points:
  - `goblob/blob.go:main` for signal and lifecycle behavior.
  - `goblob/command/command.go:Execute` for command resolution and exit mapping.
  - `goblob/command/reload.go:HandleSIGHUP` for reload routing.
- No global CLI metrics at dispatch level.

### Risks and Notes

- Command discovery depends on package `init()` side effects; removing an import can silently remove a command from help output.
- Global mutable command registry and reload hook are process-wide; concurrent tests must reset shared state to avoid cross-test coupling.

Changes:

