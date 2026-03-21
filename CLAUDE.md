# CLAUDE.md

This file provides a concise execution guide for Claude Code when working in this repository.
It is derived from [AGENTS.md](/Users/huaodong/TMP/primitivebox/AGENTS.md), which is the canonical source of repository intent, architecture rules, and compatibility constraints.

## Repository Direction

PrimitiveBox is a checkpointed, verifiable, replayable execution runtime for AI agents.
It is evolving toward an AI-native system substrate where AI interacts with containerized systems and future AI-native applications through structured primitives.

Do not treat this repository as:

- a generic chat assistant
- a prompt-only workflow engine
- an unconstrained shell wrapper
- a UI-first product whose runtime semantics can be weakened for convenience

## Before Implementing

Before writing code, explicitly align the change with the execution model:

1. Classify the work as one of:
   - runtime
   - primitive layer
   - sandbox management
   - orchestrator semantics
   - future application-primitive extensibility
2. Explain how it strengthens at least one of:
   - primitive clarity
   - sandbox safety
   - checkpoint / restore semantics
   - verification discipline
   - recovery behavior
   - replayability
3. Prefer minimal, contract-first changes over convenience abstractions.
4. If the change risks moving sandbox-owned execution onto the host gateway, stop and redesign it.
5. If a public contract changes, update the affected SDK/tests/docs in the same iteration.

## Commands

```bash
make build          # Build CLI binary to bin/pb
make run            # Build and run server on current workspace
make test           # Run Go tests: go test ./... -v
make sdk-test       # Run Python SDK tests: python3 -m pytest sdk/python/tests -q
make lint           # Run pinned golangci-lint with repo-managed cache dirs
make sandbox-image  # Build Docker sandbox image (requires local Docker daemon)
make demo           # Run the sandbox demo script
make fmt            # Format Go code: go fmt ./...
make clean          # Remove bin/ and .primitivebox/
```

Notes:

- `make build`, `make test`, and `make fmt` use a repo-managed `GOCACHE` path for better behavior in restricted environments.
- `make lint` reads the pinned linter version from `.golangci-version` and configures writable cache directories automatically.
- Some socket/listener-based tests may skip in highly restricted local sandboxes if the environment disallows `bind`; they should still run in normal developer environments and CI.

Run a single Go test:

```bash
go test ./internal/primitive/... -run TestFSRead -v
```

## Architecture Snapshot

PrimitiveBox currently exposes its runtime through a host-side gateway, but the gateway is only the control-plane boundary. Sandbox-owned execution must remain inside the sandbox `pb server`.

| Path | Role |
|------|------|
| `cmd/pb/` | CLI entry point and explicit dependency wiring |
| `internal/rpc/` | JSON-RPC, SSE, inspector, and proxy transport surfaces |
| `internal/control/` | SQLite-backed control-plane store for sandboxes and events |
| `internal/eventing/` | Shared event model, bus, and context-bound sinks |
| `internal/sandbox/` | Runtime drivers, router indirection, manager, and TTL/reaper logic |
| `internal/primitive/` | Primitive contracts, schemas, registry, and built-in primitives |
| `internal/orchestrator/` | Task execution, replay, and recovery policy |
| `sdk/python/primitivebox/` | Sync/async clients and primitive wrappers |

Conceptual flow:

`Client -> Gateway control plane -> Router/runtime driver -> Sandbox pb server -> Primitive execution`

## Current Guardrails

- `POST /rpc` is explicit host-workspace mode and may use host-local primitives.
- `POST /sandboxes/{id}/rpc` and sandbox-owned workspace actions must execute inside the sandbox, not on the host gateway.
- SQLite control-plane state and persisted events are the source of truth, not ad hoc JSON files or logs.
- SSE is a real API surface, not a debug side channel.
- Sync and async Python SDKs should remain aligned.

## Change Discipline

When adding or changing a primitive:

1. Update the Go primitive and schema.
2. Register it in the primitive registry.
3. Add sync SDK support.
4. Add async SDK support.
5. Add tests for success and failure paths.
6. Update docs if the public behavior changed.

When adding or changing runtime/lifecycle behavior:

1. Keep `RuntimeDriver` and routing abstractions as the boundary.
2. Persist control-plane state through the configured store.
3. Emit the corresponding event after the write succeeds.
4. Verify SSE/inspector surfaces still reflect the same truth.
