# PrimitiveBox Architecture

PrimitiveBox is designed to provide secure, verifiable, and structured execution capabilities for AI agents.
The core philosophy is **Boundaries through containers, Capabilities through primitives, Reliability through verification.**

## 1. Core Principles

- **Primitives as Contracts**: Operations are defined by strict JSON-RPC schemas. AI agents know exactly what input is allowed and what output is expected.
- **Snapshot-First**: Before any destructive action (like file modification), a checkpoint is created. Failures trigger automatic rollbacks.
- **Built-in Verification**: Every action must be verified (e.g., tests pass) to be considered successful.
- **Defense in Depth**: Sandboxes run with non-root users (`1000:1000`), coarse network isolation by default, explicit command whitelists for shells, and strict workspace path jailing.
- **Control-Plane First**: Sandbox metadata and event history live in a queryable control-plane store so inspector tooling and new runtimes do not scrape log files.

## 2. The 3-Tier Sandbox Gateway Architecture

Starting from Iteration 1, PrimitiveBox utilizes a 3-tier proxy model. This avoids directly exposing the container's internals to the agent while managing container lifecycles seamlessly.

```text
┌─────────────────────────────────────────────────────────────┐
│                     CLI / Python SDK                        │
│ pb server start | pb sandbox create | pb sandbox list       │
│ client.PrimitiveBoxClient(sandbox_id="...")                 │
├─────────────────────────────────────────────────────────────┤
│         Host Control Plane & API Gateway (pb server)        │
│ ┌───────────────┐ ┌────────────────┐ ┌───────────────────┐  │
│ │ Local  /rpc   │ │ Proxy /sandbox │ │ SQLite Control DB │  │
│ │ /rpc/stream   │ │ /{id}/rpc*     │ │ sandboxes/events  │  │
│ └───────┬───────┘ └───────┬────────┘ └─────────┬─────────┘  │
│         │                 │                    │            │
├─────────│─────────────────│────────────────────│────────────┤
│         ▼                 │                    ▼            │
│  [Local Primitive Layer]  │           [Event Bus / SSE]     │
│                           │                    │            │
│                           │            ┌───────▼──────────┐ │
│                           │            │ RouterDriver     │ │
│                           │            │ Docker | K8s     │ │
│                           │            └───────┬──────────┘ │
│                           │                    │            │
├───────────────────────────│────────────────────│────────────┤
│   Runtime Environments    │                    │ Sandbox    │
│                           │                    │ Lifecycle  │
│ ┌─────────────────────────▼─────────────┐      │ (Start/    │
│ │ Sandbox Runtime (id-123)              │◄─────┘ Stop/TTL)  │
│ │ ┌───────────────────────────────────┐ │                   │
│ │ │ In-Container `pb server` (:8080)  │ │                   │
│ │ │ ┌───────────────────────────────┐ │ │                   │
│ │ │ │       Primitive Layer         │ │ │                   │
│ │ │ │ fs.* | code.* | shell.* | ... │ │ │                   │
│ │ │ └───────────────────────────────┘ │ │                   │
│ │ └───────────────────────────────────┘ │                   │
│ │   /workspace (Mounted via Host)       │                   │
│ └───────────────────────────────────────┘                   │
└─────────────────────────────────────────────────────────────┘
```

1. **Host Control Plane (Gateway)**: A long-running `pb server` on the user's host machine.
    - Persists sandbox metadata and event history in `~/.primitivebox/controlplane.db`.
    - Handles requests to `/rpc` and `/rpc/stream` for the host workspace.
    - Proxies requests matching `/sandboxes/{id}/*` to the correct runtime.
    - Exposes inspector APIs under `/api/v1/*`.
2. **RouterDriver + Runtime Drivers**:
    - `DockerDriver` remains the working runtime for local sandboxes.
    - `KubernetesDriver` is now present as a compileable skeleton with manifest/status/port-forward abstractions and tests.
    - `Manager` computes TTL metadata, updates last-access timestamps, and runs a host-side reaper loop.
3. **Event Bus + SSE**:
    - Primitive execution emits structured events (`shell.output`, `checkpoint.created`, `fs.diff`, `rpc.completed`, etc.).
    - `/rpc/stream` and `/sandboxes/{id}/rpc/stream` expose live SSE frames for SDKs and the future inspector UI.

## 3. Primitives Layer

Primitives are pluggable Go interfaces managed by `Registry`. Each primitive defines its name, its JSON Schema, and an `Execute(ctx, params)` handler.

### Core Primitives
- **`fs.read` & `fs.write`**: File operations. `fs.write` forces isolated search-and-replace to prevent multi-site catastrophic updates by LLMs.
- **`fs.list`**: Safe directory traversal restricted to the workspace.
- **`code.search`**: AST-agnostic (using `ripgrep` underneath) keyword regex search.
- **`shell.exec`**: Arbitrary command execution with timeout blocks, output length truncation (saving LLM context windows), and optional command whitelists.
- **`state.checkpoint`, `state.restore`, `state.list`**: The MVP uses Git internally for zero-dependency dirty-snapshot rollbacks. This protects agents during auto-fix attempts.
- **`verify.test`**: A structural wrapper around `shell.exec` that summarizes test framework output.

## 4. Control Plane & Audit

- **SQLite Control Store**:
  - Stores sandbox metadata, driver, namespace, lifecycle timestamps, and capability flags.
  - Imports legacy JSON registry files once, but does not dual-write back to them.
- **Event Store**:
  - Stores queryable control-plane and primitive events for `/api/v1/events`.
  - Powers `/api/v1/events/stream` and future visual replay tooling.
- **Audit Logger**:
  - JSONL audit logging remains available as a secondary append-only trace for debugging and compliance.

## 5. Agent Python SDK

The official SDK (`PrimitiveBoxClient`) is completely disjoint from the Go binaries. Its responsibility is to simplify HTTP JSON-RPC wrappers into dot-syntax chains (`client.fs.read()`).

- Sync SDK now supports `stream_call()` and high-frequency helpers such as `client.shell.stream_exec()`.
- Async SDK is implemented via `httpx` and mirrors the same transport model.
- The next inspector/web UI phase can reuse the same SSE and REST query surfaces instead of inventing a separate protocol.
