# Plan: Rotating Log Files

## Architectural decisions

- **Library**: `gopkg.in/natefinch/lumberjack.v2` — implements `io.Writer`, drop-in swap for `os.Stderr` in `slog.NewJSONHandler`
- **Output**: file only; stderr is replaced, not tee'd
- **Scope**: `serve` and `mcp` commands only; `profiles` is unchanged (short-lived, stdout output)
- **Log directory**: `${XDG_STATE_HOME:-$HOME/.local/state}/sandy/logs/imds-broker/`
- **Filenames**: `serve-<pid>.log`, `mcp-<pid>.log`
- **Directory creation**: `os.MkdirAll` at command startup, before logger construction; fail fast on error
- **Rotation**: MaxSize=10 MB, MaxBackups=3, MaxAge=7 days, Compress=false
- **Old PID files**: known limitation — each process creates a new file; files are cleaned by MaxAge (7 days), not by MaxBackups across processes
- **Configuration**: no flag; path is derived from environment at startup
- **Shutdown**: `defer lumberjack.Close()` in each command action

---

## Phase 1: Add lumberjack and wire file logging

**Single commit, single PR**

### What to build

Add `gopkg.in/natefinch/lumberjack.v2` to `go.mod`.

In `cmd/imds-broker/main.go`, extract a shared helper (or inline in each command) that:

1. Resolves the log directory: `filepath.Join(xdgStateHome(), "sandy", "logs", "imds-broker")` where `xdgStateHome()` returns `$XDG_STATE_HOME` if set, else `filepath.Join(os.UserHomeDir(), ".local", "state")`.
2. Calls `os.MkdirAll(dir, 0o755)` — return an error if it fails.
3. Constructs `&lumberjack.Logger{Filename: filepath.Join(dir, name), MaxSize: 10, MaxBackups: 3, MaxAge: 7}` where `name` is `serve-<pid>.log` or `mcp-<pid>.log` (`fmt.Sprintf("serve-%d.log", os.Getpid())`).
4. Passes the `*lumberjack.Logger` as the writer to `slog.NewJSONHandler` instead of `os.Stderr`.
5. Defers `lumberjack.Close()`.

No changes to any package under `pkg/` — the swap is entirely within `cmd/`.

### Acceptance criteria

- [ ] `go.mod` and `go.sum` include `gopkg.in/natefinch/lumberjack.v2`
- [ ] Running `imds-broker serve` creates `~/.local/state/sandy/logs/imds-broker/serve-<pid>.log` (or XDG equivalent) and writes JSON log lines to it
- [ ] Running `imds-broker mcp` creates `~/.local/state/sandy/logs/imds-broker/mcp-<pid>.log`
- [ ] The log directory is created if it does not exist; the command fails with a clear error if it cannot be created
- [ ] `imds-broker profiles` is unaffected — no file created, no behaviour change
- [ ] No log output appears on stderr during normal operation
- [ ] All existing tests continue to pass
