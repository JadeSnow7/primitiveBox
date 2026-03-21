# PrimitiveBox

PrimitiveBox is a checkpointed sandbox runtime for AI agents.

It is a primitive-based execution system for containerized environments, not just a tool-calling wrapper. PrimitiveBox is built around structured primitives, checkpoint-first side effects, verification before success, and replayable execution traces, with a long-term path toward AI-native applications exposing their own application primitives inside the container.

## Why PrimitiveBox

Most agent systems stop at “the model can call tools.” PrimitiveBox is trying to make execution itself a first-class runtime concern:

- primitives are explicit contracts, not loose prompt conventions
- side effects should be checkpointable before risky work continues
- success should be verification-driven, not declared by the model
- failures should be recoverable rather than terminal
- execution history should be replayable and inspectable
- sandbox and runtime boundaries should stay explicit

That makes PrimitiveBox meaningfully different from:

- chat-first assistants
- unconstrained shell automation wrappers
- prompt-only workflow engines
- broad “AI platform” narratives without concrete execution semantics

## Execution Model

PrimitiveBox currently follows this shape:

`Client -> Host gateway control plane -> Router/runtime driver -> Sandbox pb server -> Primitive execution`

The gateway is the control-plane boundary. It authenticates, validates, persists control-plane state in SQLite, emits events, and routes requests to the correct sandbox endpoint. Sandbox-owned workspace execution happens inside the sandbox-local `pb server`, not on the host gateway.

Core execution ideas:

- structured primitives with explicit JSON contracts
- sandboxed container execution
- checkpoint / restore support for workspace state
- verification-oriented task completion
- durable event history for streaming, inspection, and replay
- an architecture that can later separate system primitives from application primitives

## What Works Today

PrimitiveBox already provides a usable execution substrate for agent workflows:

- host workspace mode at `POST /rpc` when the caller explicitly targets the host workspace
- sandbox mode via `POST /sandboxes/{id}/rpc`, with the gateway proxying to sandbox-local execution
- Docker-backed sandboxes through the current runtime driver path
- SQLite-backed control-plane state for sandbox metadata and persisted event history
- SSE streaming for runtime and primitive execution events
- built-in primitives including filesystem, shell, code search, state checkpointing, and test verification
- sync and async Python SDK support for core RPC and streaming flows
- inspector-oriented APIs over sandboxes, checkpoints, trees, and events

## What Is Experimental

Some areas are intentionally present but still converging:

- the Kubernetes runtime is a real architectural path, but still maturing relative to Docker
- some sandbox-only primitive families, such as database and browser-oriented execution, are early-stage
- inspector and UI surfaces exist to support the control plane, but the runtime model remains the priority
- async convenience parity is improving, but should continue converging with the sync SDK

## What Is Next

The near-term direction is to strengthen PrimitiveBox as an execution runtime before broadening the product surface:

- tighten checkpoint / verify / recover loops around side-effectful work
- make replay and inspection more useful as first-class runtime features
- continue hardening runtime routing, lifecycle persistence, and event semantics
- expand the primitive model carefully rather than accumulating ad hoc tools
- prepare a clean split between system primitives and future application primitives exposed by AI-native apps

## Quick Start

Build the CLI:

```bash
make build
```

Run the host gateway against a local workspace:

```bash
./bin/pb server start --workspace ./my-project
```

Create a Docker sandbox:

```bash
make sandbox-image
./bin/pb sandbox create --driver docker --mount ./my-project --ttl 3600 --network-mode none
```

Example Python usage:

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")
print(client.health())
print(client.fs.read("README.md"))

for event in client.shell.stream_exec("printf 'hello\\n'"):
    print(event)
```

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
- `pb sandbox create`
- `pb sandbox list`
- `pb sandbox inspect <id>`
- `pb sandbox stop <id>`
- `pb sandbox destroy <id>`

## Architecture

PrimitiveBox has a two-plane architecture separated by the container boundary.

```
┌─────────────────────────────────── Host (control plane) ────────────────────────────────────┐
│                                                                                              │
│   AI Agent / SDK ──► Gateway (JSON-RPC 2.0)  ──► Sandbox Proxy ──► SQLiteStore            │
│                            │                                            │                  │
│                       EventBus ◄──────────────────────────────────── Events                │
│                            │                                                               │
│                       SandboxManager ──► RouterDriver ──► DockerDriver / K8sDriver        │
│                                                                                            │
└────────────────────────────────────────────────────────────────────────────────────────────┘
                                          │ HTTP proxy
                                          ▼
┌─────────────────────────────── Sandbox Container (execution plane) ────────────────────────┐
│                                                                                             │
│   pb server ──► SerialExecutor ──► Primitive Registry ──► System Primitives               │
│        │               │               │                   (fs / shell / state /           │
│        │          Level-0 CVR     App Adapters              verify / code / macro)         │
│        │         (checkpoint /        │                                                    │
│        │          verify /        App Process                                              │
│        │          restore)     (Unix socket RPC)                                           │
│        └──► CVRCoordinator (proposed)                                                      │
│                  │                                                                         │
│             VerifyStrategy + RecoveryDecisionTree + CheckpointManifest                    │
│                                                                                            │
└────────────────────────────────────────────────────────────────────────────────────────────┘
```

**Key invariants:**
- The gateway is the control-plane boundary — it authenticates, persists, routes, and emits events, but never executes workspace primitives directly
- All workspace-touching execution lives inside the sandbox `pb server`
- SQLite is the sole durable store; events are append-only and are the source of truth
- Every control-plane state mutation emits a corresponding event (write-and-emit rule)

**Architecture documents** (`docs/arch/`):

| Document | Contents |
|---|---|
| [`01_primitive_taxonomy.md`](docs/arch/01_primitive_taxonomy.md) | System / Code / Document primitive type hierarchy; `AIPrimitive` interface; Layer 1–3 schemas |
| [`02_app_primitive_protocol.md`](docs/arch/02_app_primitive_protocol.md) | App primitive registration (Unix socket + JSON-RPC); AppRouter; Python + Go SDK |
| [`03_cvr_loop.md`](docs/arch/03_cvr_loop.md) | CVR closed loop; CheckpointManifest; VerifyStrategy polymorphism; RecoveryDecisionTree |
| [`04_event_observability.md`](docs/arch/04_event_observability.md) | 39-event type system; ExecutionTrace; Inspector API extensions; AI debug interface |
| [`05_system_architecture.md`](docs/arch/05_system_architecture.md) | Full system diagram; module boundaries; 4 ADRs; implementation gap analysis |

## Development Notes

```bash
make build
make test
make sdk-test
make lint
```

`make build` and `make test` use a repository-owned Go build cache path so they are more stable in restricted local sandboxes and CI runners.

`make lint` runs `./scripts/lint.sh`, which reads the pinned linter version from `.golangci-version`, uses the Go version declared in `go.mod`, and configures writable cache directories for both `golangci-lint` and the Go toolchain. If `golangci-lint` is not installed locally, the script prints the exact `go install ...@version` command to use.

Some integration-style Go tests open local TCP or Unix listeners. In environments that forbid `bind` or local socket creation, those tests now skip with an explicit reason instead of failing the whole suite. In a normal developer environment or GitHub Actions runner, they still execute normally.

Additional repository guidance lives in:

- [AGENTS.md](AGENTS.md) for canonical repository intent and architecture rules
- [CLAUDE.md](CLAUDE.md) for concise assistant-facing execution guidance
