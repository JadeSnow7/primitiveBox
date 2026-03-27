# PrimitiveBox

PrimitiveBox is a **safe, replayable, and extensible OS for AI agents**.

It is not a tool-calling wrapper or a prompt-chaining framework. It is an execution substrate: every agent action passes through typed primitive contracts, every mutation checkpoints state before it runs, every outcome is verified, and every execution is replayable. When something goes wrong, you roll back — not retry and hope.

## v1.0 Release Candidate

All five core pillars are shipped and validated end-to-end:

### 1. Checkpoint-Verify-Rollback (CVR)

Every mutating primitive carries intent metadata (`risk_level`, `reversible`, `checkpoint_required`). Before a high-risk primitive executes, the runtime snapshots workspace state. After execution, a verification strategy confirms the outcome. If verification fails, the runtime invokes a rollback — either restoring the snapshot or calling an app-declared rollback primitive. This loop is not optional and cannot be bypassed.

```bash
# Atomic CVR in one call
pb rpc macro.safe_edit --param path=src/main.go --param diff="..."
# → checkpoint created → patch applied → verify.test passes → done
# → checkpoint created → patch applied → verify.test fails → state.restore called
```

### 2. Kubernetes Runtime Driver

The Kubernetes driver is production-ready. Sandboxes run as Pods, with PVC-backed workspace mounts, per-sandbox network policies, and TTL-driven reaping via the same `SandboxManager` lifecycle used by Docker. Swap `--driver docker` for `--driver kubernetes` — no other changes required.

```bash
pb sandbox create --driver kubernetes --mount ./project --ttl 3600
```

### 3. MCP Bridge (Universal Adapter)

The `pb-mcp-bridge` binary wraps any Model Context Protocol server as a PrimitiveBox app. MCP tools are automatically translated into typed primitives, registered with inferred intent metadata, and made available through the standard RPC gateway. Any existing MCP ecosystem tool — Asana, GitHub, Notion, Stripe — works without modification.

```bash
pb install mcp-bridge --mcp-server npx:@modelcontextprotocol/server-github
# → github.create_issue, github.list_repos, ... registered and callable
```

### 4. Human-in-the-Loop Reviewer Gate

High-risk primitives (`risk_level: high` or `reversible: false`) do not execute autonomously. The orchestrator suspends in `AWAITING_REVIEW` state, renders a `ReviewerPanel` showing the exact method, intent metadata, and proposed parameters, and waits for a human Approve or Reject signal. The payload dispatched after approval is identical to the one shown — the agent cannot modify it between display and execution.

### 5. Package Manager + App Primitives

Applications extend the primitive catalog through the `pb install` / Boxfile lifecycle. A Boxfile declares the adapter binary, socket path, and primitive manifest. On install, the runtime launches the adapter, polls until primitives register, runs the declared healthcheck, and persists the record. On server restart, all installed packages are relaunched automatically.

```bash
pb install data-pack   # → data.schema, data.query, data.insert registered
pb rpc data.query --param sql="SELECT * FROM products"
```

---

## Quick Start

Build all binaries:

```bash
make build
```

Start the host gateway:

```bash
./bin/pb server start --workspace ./my-project
```

Create a Kubernetes sandbox:

```bash
pb sandbox create --driver kubernetes --mount ./my-project --ttl 3600
```

Or a Docker sandbox:

```bash
make sandbox-image
./bin/pb sandbox create --driver docker --mount ./my-project --ttl 3600 --network-mode none
```

Install a demo pack:

```bash
pb install data-pack --boxfile examples/data-pack/Boxfile
```

Python SDK:

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")
print(client.fs.read("README.md"))

for event in client.shell.stream_exec("go test ./..."):
    print(event)
```

See [docs/QUICKSTART.md](docs/QUICKSTART.md) for the full zero-to-hero walkthrough.

---

## Execution Model

```
Client → Host gateway (control plane) → Router/runtime driver → Sandbox pb server → Primitive execution
```

The gateway authenticates, validates, persists control-plane state in SQLite, emits events, and routes to the correct sandbox. Workspace execution happens inside the sandbox-local `pb server` — never on the host gateway.

## Architecture

```
┌────────────────────────── Host (control plane) ─────────────────────────────┐
│                                                                              │
│  AI Agent / SDK ──► Gateway (JSON-RPC 2.0) ──► Sandbox Proxy ──► SQLite    │
│                           │                                        │        │
│                      EventBus ◄───────────────────────────── Events         │
│                           │                                                 │
│                      SandboxManager ──► RouterDriver ──► DockerDriver       │
│                                                      └──► K8sDriver         │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
                                       │ HTTP proxy
                                       ▼
┌──────────────────────── Sandbox Container (execution plane) ────────────────┐
│                                                                              │
│  pb server ──► SerialExecutor ──► Primitive Registry ──► System Primitives  │
│       │               │               │                  (fs / shell /      │
│       │          Level-0 CVR     App Adapters             state / verify /  │
│       │         (checkpoint /   (Unix socket RPC)          code / macro)    │
│       │          verify /                                                    │
│       │          restore)                                                    │
│       └──► CVRCoordinator                                                    │
│                 │                                                            │
│            VerifyStrategy + RecoveryDecisionTree + CheckpointManifest       │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Key invariants:**
- The gateway is the control-plane boundary — it never executes workspace primitives directly
- All workspace-touching execution lives inside the sandbox `pb server`
- SQLite is the sole durable store; events are append-only and are the source of truth
- Every control-plane mutation emits a corresponding event (write-and-emit rule)

## Interfaces

Primary routes:

- `POST /rpc`
- `POST /rpc/stream`
- `POST /sandboxes/{id}/rpc`
- `POST /sandboxes/{id}/rpc/stream`
- `GET /health`
- `GET /primitives`
- `GET /app-primitives`
- `GET /api/v1/sandboxes`
- `GET /api/v1/sandboxes/{id}`
- `GET /api/v1/events`
- `GET /api/v1/events/stream`

Primary CLI commands:

- `pb server start`
- `pb sandbox create / list / inspect / stop / destroy`
- `pb install / remove` (package manager)
- `pb rpc <method> --param key=value`

## Development

```bash
make build      # Build all binaries to bin/
make test       # Run Go tests
make sdk-test   # Run Python SDK tests
make lint       # Run pinned golangci-lint
make fmt        # Format Go code
make clean      # Remove bin/ and .primitivebox/
```

Architecture documents live in `docs/arch/`. Repository guidance for contributors is in [AGENTS.md](AGENTS.md) and [CLAUDE.md](CLAUDE.md).
