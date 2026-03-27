# Iteration 3 Implementation Plan

## Goal

Iteration 3 moves PrimitiveBox from a single-runtime sandbox gateway toward a production-oriented control plane with:

- durable sandbox metadata and event history
- runtime abstraction upgrades for multiple backends
- streaming transport for tool execution
- inspector-friendly query APIs

This document tracks the intended rollout and the first-cut implementation that already landed in this repository.

## Scope Split

### P1. ControlStore / EventStore

Status: implemented

- Added `internal/control/` with a SQLite-backed store for sandbox metadata and events.
- Added one-time import support for legacy `~/.primitivebox/sandboxes/*.json`.
- Added `internal/eventing/` with shared event types, bus, and execution-context sink helpers.

Acceptance:

- sandbox metadata persists in `~/.primitivebox/controlplane.db`
- events can be queried and streamed without scraping audit files

### P2. RuntimeDriver v2 + TTL

Status: implemented as first cut

- Extended `RuntimeDriver` with `Inspect()` and `Capabilities()`.
- Extended `SandboxConfig` with driver, namespace, lifecycle, and network policy fields.
- Added TTL metadata (`created_at`, `last_accessed_at`, `expires_at`) to `Sandbox`.
- Added `RouterDriver` and host-side `Manager.RunReaper()`.

Acceptance:

- multiple runtime names can share one manager via `RouterDriver`
- idle/absolute TTL can be stored and reaped

### P3. Kubernetes Driver Skeleton

Status: implemented as architecture skeleton

- Added `internal/sandbox/kubernetes.go`.
- Defined manifest/status/port-forward/client abstractions.
- Added unit tests for manifest generation and status mapping.

Acceptance:

- `pb sandbox create --driver kubernetes ...` routes through the same manager path
- the driver compiles, advertises capabilities, and is ready for a real client implementation

### P4. `/rpc/stream` + gateway streaming

Status: implemented

- Added `POST /rpc/stream`.
- Added `POST /sandboxes/{id}/rpc/stream`.
- `shell.exec`, `state.checkpoint`, `state.restore`, and `fs.diff` emit structured events.

Acceptance:

- SDKs and future UI can receive `started/stdout/stderr/progress/completed/error` SSE events

### P5. Inspector APIs

Status: implemented as API layer, UI fully integrated

- Added `/api/v1/sandboxes`
- Added `/api/v1/sandboxes/{id}`
- Added `/api/v1/sandboxes/{id}/tree`
- Added `/api/v1/sandboxes/{id}/checkpoints`
- Added `/api/v1/events`
- Added `/api/v1/events/stream`

Acceptance:

- an inspector can reconstruct sandbox state and event history from API responses alone

### P6. `db.*`

Status: planned

Recommended next steps:

- `db.schema`
- `db.query_readonly`
- DSN allowlist and row/result-size caps

### P7. `browser.*`

Status: planned

Recommended next steps:

- browser image profile
- `browser.goto`, `browser.click`, `browser.type`, `browser.extract`, `browser.screenshot`
- reuse `/rpc/stream` for progress output

## Public Interfaces Added

- CLI:
  - `pb sandbox create --driver ... --namespace ... --ttl ... --idle-ttl ... --network-mode ...`
- Runtime:
  - `RuntimeDriver.Inspect`
  - `RuntimeDriver.Capabilities`
- HTTP:
  - `POST /rpc/stream`
  - `POST /sandboxes/{id}/rpc/stream`
  - `GET /api/v1/events`
  - `GET /api/v1/events/stream`
  - `GET /api/v1/sandboxes/{id}/tree`
  - `GET /api/v1/sandboxes/{id}/checkpoints`
- Python SDK:
  - `PrimitiveBoxClient.stream_call`
  - `PrimitiveBoxClient.shell.stream_exec`
  - `PrimitiveBoxClient.fs.stream_diff`
  - `AsyncPrimitiveBoxClient.stream_call`

## Validation

Implemented validation in this repo:

- `go test ./...`
- `python3 -m pytest sdk/python/tests -q`

## Known Gaps

- Docker still treats network policy as coarse intent metadata; strict allowlists are not yet enforced there.
- The Kubernetes driver is a tested skeleton and still needs a concrete cluster client/port-forward implementation for real deployment use.
- The inspector UI itself is not yet embedded; the data plane it will consume is now in place.
