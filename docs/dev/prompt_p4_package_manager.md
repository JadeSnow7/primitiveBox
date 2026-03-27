# Phase 4: Package Manager MVP

## Role & Context

You are a Staff-Level AI Software Engineer on **PrimitiveBox** — an AI-native execution runtime
where every action passes through typed primitives with CVR guarantees.

Phase 3 delivered two production-grade reference adapters:
- `cmd/pb-os-adapter/` — 13 OS primitives (`process.*`, `service.*`, `pkg.*`)
- `cmd/pb-mcp-bridge/` — MCP stdio bridge, dynamic primitive registration

Both adapters connect via **Unix socket → `app.register` JSON-RPC call → SQLite-backed
`AppPrimitiveRegistry`**. The registration protocol is proven. The install workflow is not.

Phase 4 solves **discovery, installation, lifecycle management** with a single command:

```
pb install os
```

---

## Architecture Anchor

Read these files before writing code:

| File | Why |
|------|-----|
| `internal/control/app_registry.go` | `SQLiteAppRegistry` — Register/Unregister/List/MarkUnavailable |
| `internal/control/sqlite_store.go` | SQLite schema + transaction patterns |
| `internal/rpc/server.go` | How `appRegistry` is wired, `/api/v1/primitives` merges system + app schemas |
| `cmd/pb-os-adapter/main.go` | Adapter startup: connects to socket, calls `app.register` per primitive |
| `cmd/pb/main.go` + `cmd/pb/client.go` | CLI wiring + HTTP client to gateway |
| `cmd/pb/cmd_primitive.go` | How existing CLI commands are structured |

**Execution model constraint (from AGENTS.md):** The host gateway is the control-plane
boundary. `pb install` is a control-plane operation — it configures adapter launch, persists
install state, and manages process lifecycle. It does NOT bypass the socket registration
protocol that Phase 2/3 established.

---

## MVP Scope

| In Scope | Out of Scope |
|----------|-------------|
| Binary adapter type only | Container/image adapters |
| Local package registry (static Go map) | Remote registry fetch |
| Known packages: `os`, `mcp-bridge` | Community packages |
| Adapter auto-launch on `pb server start` | Signature verification (add TODO comment) |
| `pb install`, `pb remove`, `pb list --installed`, `pb info`, `pb search` | `pb update`, `pb audit` |
| SQLite installed-packages table | Complex dependency resolution |
| Healthcheck verification post-install | Per-parameter risk differentiation |

---

## Task 1 — Package Manifest Types (`internal/pkgmgr/manifest.go`)

Define the canonical `PackageManifest` struct and the local registry.

```go
// PackageManifest is the pb-package.json schema for a binary adapter package.
type PackageManifest struct {
    Name        string          // e.g. "os"
    Version     string          // semver
    Description string
    Adapter     AdapterConfig
    Primitives  []PrimitiveSpec  // declared primitives with intent (for pre-install display)
    Healthcheck *HealthcheckSpec // optional post-install verification
}

type AdapterConfig struct {
    Type       string // "binary" (only supported type in MVP)
    BinaryPath string // resolved at install time; can use $PB_HOME or relative to pb binary
    Args       []string
    SocketPath string // e.g. /tmp/pb-os-adapter.sock; templated with {workspace}
}

type PrimitiveSpec struct {
    Name        string
    Description string
    Intent      PrimitiveIntent  // mirrors cvr.PrimitiveIntent
}

type PrimitiveIntent struct {
    Category   string
    SideEffect string
    RiskLevel  string
    Reversible bool
}

type HealthcheckSpec struct {
    Primitive string         // primitive name to call
    Params    map[string]any // params to pass
    Timeout   time.Duration
}
```

**Local registry** — a static Go map in `internal/pkgmgr/registry.go`:

```go
var builtinPackages = map[string]PackageManifest{
    "os": {
        Name: "os", Version: "0.1.0",
        Description: "OS adapter: process.*, service.*, pkg.* primitives",
        Adapter: AdapterConfig{
            Type:       "binary",
            BinaryPath: "{pb_dir}/pb-os-adapter",  // sibling to pb binary
            SocketPath: "{workspace}/.pb/sockets/os-adapter.sock",
        },
        Healthcheck: &HealthcheckSpec{
            Primitive: "process.list",
            Params:    map[string]any{},
            Timeout:   5 * time.Second,
        },
        Primitives: []PrimitiveSpec{
            {Name: "process.list", Intent: PrimitiveIntent{Category: "query", SideEffect: "read", RiskLevel: "low", Reversible: true}},
            {Name: "process.spawn", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: false}},
            {Name: "process.terminate", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: false}},
            {Name: "process.kill", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "high", Reversible: false}},
            {Name: "service.status", Intent: PrimitiveIntent{Category: "query", SideEffect: "read", RiskLevel: "low", Reversible: true}},
            {Name: "service.start", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: true}},
            {Name: "service.stop", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: true}},
            {Name: "pkg.list", Intent: PrimitiveIntent{Category: "query", SideEffect: "read", RiskLevel: "low", Reversible: true}},
            {Name: "pkg.install", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "high", Reversible: false}},
            {Name: "pkg.remove", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "high", Reversible: false}},
            {Name: "pkg.verify", Intent: PrimitiveIntent{Category: "verification", SideEffect: "read", RiskLevel: "low", Reversible: true}},
        },
    },
    "mcp-bridge": {
        Name: "mcp-bridge", Version: "0.1.0",
        Description: "MCP bridge: mirrors any MCP stdio server as PrimitiveBox primitives",
        Adapter: AdapterConfig{
            Type:       "binary",
            BinaryPath: "{pb_dir}/pb-mcp-bridge",
            SocketPath: "{workspace}/.pb/sockets/mcp-bridge.sock",
            // Args are user-supplied at install time via --cmd flag
        },
        // No static Primitives list — MCP bridge registers dynamically
        Primitives: nil,
    },
}
```

`BinaryPath` template variables:
- `{pb_dir}` → directory of the `pb` binary (`os.Executable()` + `filepath.Dir`)
- `{workspace}` → active workspace directory (read from `--workspace` flag or default)

---

## Task 2 — Installed Package Store (`internal/pkgmgr/store.go`)

Add an `installed_packages` table to the existing SQLite database managed by
`internal/control/SQLiteStore`. Extend `sqlite_store.go` with the migration, or add
a separate migration in `pkgmgr/store.go` that opens the same DB file.

**Schema:**

```sql
CREATE TABLE IF NOT EXISTS installed_packages (
    name         TEXT PRIMARY KEY,
    version      TEXT NOT NULL,
    installed_at INTEGER NOT NULL,          -- Unix timestamp
    socket_path  TEXT NOT NULL,             -- resolved socket path
    binary_path  TEXT NOT NULL,             -- resolved binary path
    args_json    TEXT NOT NULL DEFAULT '[]',-- JSON array of extra args
    status       TEXT NOT NULL DEFAULT 'installed'  -- installed | active | error
);
```

**Go interface:**

```go
type InstalledPackage struct {
    Name        string
    Version     string
    InstalledAt time.Time
    SocketPath  string
    BinaryPath  string
    Args        []string
    Status      string
}

type PackageStore interface {
    Save(ctx context.Context, pkg InstalledPackage) error
    Remove(ctx context.Context, name string) error
    Get(ctx context.Context, name string) (*InstalledPackage, error)
    List(ctx context.Context) ([]InstalledPackage, error)
    SetStatus(ctx context.Context, name, status string) error
}
```

Implement `SQLitePackageStore` backed by the same `*sql.DB` passed from the control store.

---

## Task 3 — Installer (`internal/pkgmgr/installer.go`)

The installer runs the **resolve → provision → launch → wait → verify → persist** pipeline.

```go
type Installer struct {
    store      PackageStore
    registry   *LocalRegistry
    appReg     primitive.AppPrimitiveRegistry  // to poll for registered primitives
    gatewayURL string                           // for healthcheck RPC call
    workspace  string
    pbDir      string
}

func (i *Installer) Install(ctx context.Context, name string, extraArgs []string) error
func (i *Installer) Remove(ctx context.Context, name string) error
```

### Install pipeline

```
RESOLVE:    i.registry.Lookup(name)  →  PackageManifest (or ErrNotFound)

PREFLIGHT:  Check binary exists at resolved BinaryPath
            Check name not in reserved system namespaces (fs, state, shell, verify, macro, code, test)
            Check not already installed (idempotent: warn + return nil if same version)

LAUNCH:     os/exec.CommandContext → start adapter process with resolved SocketPath arg
            Pipe stderr to log with "[pkg:<name>]" prefix
            Store *os.Process handle for Remove()

WAIT:       Poll i.appReg.List(ctx) every 250ms for up to 10s
            Stop polling when at least one primitive from pkg.Primitives appears
            If timeout → kill process → return ErrRegistrationTimeout

VERIFY:     If pkg.Healthcheck != nil:
                POST /rpc  {"method": pkg.Healthcheck.Primitive, "params": ...}
                If error → kill process → Remove from store → return ErrHealthcheckFailed
            // TODO: signature verification (Phase 4.1 — not in MVP)

PERSIST:    store.Save(InstalledPackage{...}) with status="active"
            Emit event: {"type": "package.installed", "name": name, "version": version}
```

### Remove pipeline

```
LOOKUP:     store.Get(ctx, name)  →  InstalledPackage

DRAIN:      i.appReg.MarkUnavailable(ctx, name)   -- marks primitives unavailable in SQLite

SIGNAL:     Find process by socket path (or PID stored in DB)
            os.Process.Signal(syscall.SIGTERM)
            Wait up to 5s for socket file to disappear, then SIGKILL

PERSIST:    store.Remove(ctx, name)
            Emit event: {"type": "package.removed", "name": name}
```

---

## Task 4 — Server Auto-Launch (`cmd/pb/main.go` or `internal/rpc/server.go`)

When `pb server start` runs, after the HTTP server is ready, launch all installed packages.

Add method to `Installer`:

```go
func (i *Installer) LaunchInstalled(ctx context.Context) error
```

This calls `Install` logic (skipping RESOLVE/PREFLIGHT) for every package in
`store.List(ctx)` with status `installed` or `active`. Failures are logged but do not
abort the server.

Wire this in `cmd/pb/main.go` after `server.ListenAndServe` is confirmed ready (use the
existing `readiness` check pattern or a brief `net.DialTimeout` loop).

---

## Task 5 — CLI Commands (`cmd/pb/cmd_package.go`)

Add a `pb package` subcommand group (or top-level aliases for ergonomics).

### `pb install <name> [flags]`

```
Flags:
  --cmd string        For mcp-bridge only: MCP server command (e.g. "npx @modelcontextprotocol/server-github")
  --workspace string  Override workspace dir (default: current dir)
```

Output:
```
Resolving 'os'...
  adapter : pb-os-adapter (binary)
  socket  : /path/.pb/sockets/os-adapter.sock
  primitives declared: 11

Installing...
  ✓ binary found at /usr/local/bin/pb-os-adapter
  ✓ adapter launched (pid 12345)
  ✓ registration confirmed (11 primitives active)
  ✓ healthcheck passed (process.list → ok)

Package 'os' v0.1.0 installed.
Run 'pb primitives list' to see the new primitives.
```

### `pb remove <name>`

```
  ✓ primitives marked unavailable
  ✓ adapter process stopped
  ✓ package record removed

Package 'os' removed.
```

### `pb list --installed`

```
NAME        VERSION  STATUS   PRIMITIVES  INSTALLED
os          0.1.0    active   11          2026-03-27T18:00:00Z
mcp-bridge  0.1.0    active   dynamic     2026-03-27T18:05:00Z
```

### `pb search [query]`

```
AVAILABLE PACKAGES (local registry):

os          0.1.0   OS adapter: process.*, service.*, pkg.* primitives
mcp-bridge  0.1.0   MCP bridge: mirrors any MCP stdio server as PrimitiveBox primitives
```

### `pb info <name>`

Prints full manifest: description, version, declared primitives with intent metadata,
healthcheck config, adapter config.

---

## Task 6 — Tests (`internal/pkgmgr/*_test.go`)

Write unit tests for the pure-logic parts. Mock `PackageStore` and `AppPrimitiveRegistry`
with simple in-memory implementations.

Required test cases:

| Test | What it verifies |
|------|-----------------|
| `TestLocalRegistry_Lookup` | "os" and "mcp-bridge" found; unknown name returns ErrNotFound |
| `TestInstaller_Preflight_BinaryMissing` | Returns error when binary not found; no process spawned |
| `TestInstaller_Preflight_AlreadyInstalled` | Same version → idempotent (no error, no duplicate launch) |
| `TestInstaller_WaitTimeout` | Returns ErrRegistrationTimeout when appReg never shows primitives |
| `TestInstaller_HealthcheckFailed` | Returns ErrHealthcheckFailed; remove cleans up process and store |
| `TestInstaller_Remove_NotInstalled` | Returns ErrNotInstalled |
| `TestSQLitePackageStore_SaveAndList` | Round-trip through SQLite: save → list → get |
| `TestSQLitePackageStore_Remove` | Remove deletes row; subsequent Get returns nil |

Use a temporary `*.db` file via `t.TempDir()`. Do not mock SQLite.

---

## Task 7 — E2E Smoke Test (`tests/e2e/package_manager_smoke.py`)

```python
#!/usr/bin/env python3
"""
Phase 4 Package Manager smoke test.
Requires: pb-os-adapter binary in same dir as pb binary.
Run: python3 tests/e2e/package_manager_smoke.py
Expected: 10/10 checks pass.
"""
```

Checks:
1. `pb search` lists "os" and "mcp-bridge"
2. `pb info os` shows primitive list with intent metadata
3. `pb install os` exits 0
4. `pb list --installed` shows os with status=active
5. `pb primitives list` includes `process.list` after install
6. `process.list` call via `/rpc` returns a non-empty processes array
7. `pb install os` again (idempotent) exits 0, no duplicate registration
8. `pb remove os` exits 0
9. `pb list --installed` shows empty after remove
10. `pb primitives list` does NOT include `process.list` after remove

---

## Acceptance Criteria

- [ ] `pb install os` completes in < 15 seconds with process registration confirmed
- [ ] After install, `GET /api/v1/primitives` includes all 11 `process.*`/`service.*`/`pkg.*` primitives with correct intent metadata
- [ ] After install, `process.kill` triggers Reviewer Gate (risk_level=high, reversible=false) in the frontend
- [ ] `pb remove os` stops the adapter, marks primitives unavailable, and removes the DB record
- [ ] `pb server start` auto-launches all previously installed packages on next start
- [ ] All 8 unit tests pass: `go test ./internal/pkgmgr/... -v`
- [ ] E2E smoke: `python3 tests/e2e/package_manager_smoke.py` → 10/10 checks pass
- [ ] `go test $(go list ./... | grep -v 'pb-test-adapter\|kv_adapter') -v` continues to pass

---

## Architectural Constraints (MUST FOLLOW)

1. **Never bypass socket registration.** `pb install` does not inject primitives directly
   into the registry. It starts the adapter binary; the binary calls `app.register` via
   the existing Unix socket protocol. The installation is confirmed by observing registration
   in `appReg.List()`, not by writing directly to `app_primitives` table.

2. **Reserved namespace enforcement.** The installer must reject any package whose declared
   primitive names start with `fs.`, `state.`, `shell.`, `verify.`, `macro.`, `code.`, or
   `test.`. Fail loud at install time, not at call time.

3. **Fail-closed on healthcheck.** If the healthcheck fails, the adapter is stopped and the
   install is rolled back (process killed + store.Remove). A failed install must leave the
   system in the same state as before the install attempt.

4. **Control-plane only.** `pb install` is a CLI → HTTP gateway operation. It must not
   SSH into sandboxes or write directly to sandbox filesystems. Adapter processes run on
   the host, connecting via Unix socket to the host gateway.

5. **Same DB, same transaction discipline.** The `installed_packages` table lives in the
   same SQLite file as `app_primitives`. Use the same `BeginTx → defer Rollback → Commit`
   pattern as `app_registry.go`. Never write to DB before the install is confirmed successful.

6. **No shell injection.** The adapter binary path and args are stored as structured data
   and passed to `exec.Command(binary, args...)` — never via shell string interpolation.
   Validate that `BinaryPath` after template resolution is an absolute path containing
   only safe characters before execution.
