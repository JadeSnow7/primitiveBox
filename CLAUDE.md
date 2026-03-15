# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build          # Build CLI binary to bin/pb
make run            # Build and run server on current workspace
make test           # Run Go tests: go test ./... -v
make sdk-test       # Run Python SDK tests: python3 -m pytest sdk/python/tests -q
make sandbox-image  # Build Docker sandbox image (requires local Docker daemon)
make demo           # Run the sandbox demo script
make fmt            # Format Go code: go fmt ./...
make clean          # Remove bin/ and .primitivebox/
```

Run a single Go test:
```bash
go test ./internal/primitive/... -run TestFSRead -v
```

## Architecture

PrimitiveBox is a host-side JSON-RPC 2.0 gateway that exposes AI-native primitives (filesystem, shell, code search, state checkpoints, test verification) to agent workflows. It supports two modes:

1. **Host workspace mode**: `pb server start --workspace <dir>` serves primitives directly against a local workspace directory.
2. **Docker sandbox gateway mode**: `pb sandbox create` provisions a Docker container running its own internal `pb server` (on port 8080). The host gateway proxies RPC calls to the container's internal server via `/sandboxes/{id}/rpc`.
3. **Control-plane / streaming mode**: the gateway now persists sandbox metadata + inspector events in SQLite and exposes SSE/inspector APIs alongside JSON-RPC.

### Key Packages & Structure

| Path | Role |
|------|------|
| `cmd/pb/` | Cobra CLI entry point (`server start`, `sandbox create/list/inspect/stop/destroy`) |
| `internal/rpc/` | JSON-RPC 2.0 HTTP server; routes `/rpc`, `/rpc/stream`, `/health`, `/primitives`, `/sandboxes/{id}/*`, and `/api/v1/*` inspector APIs |
| `internal/primitive/` | Primitive interfaces + implementations: `fs.*`, `shell.exec`, `code.search`, `state.*` (Git-backed), `verify.test`; `registry.go` for dynamic registration |
| `internal/sandbox/` | Runtime lifecycle and routing (`manager.go`, `docker.go`, `kubernetes.go`, `router.go`, `runtime.go`); TTL/reaper logic lives here |
| `internal/control/` | SQLite control-plane store for sandboxes and events |
| `internal/eventing/` | Shared event types, event bus, and context sink helpers |
| `internal/config/` | YAML config management (`.primitivebox.yaml`) |
| `internal/audit/` | Audit logging of all RPC operations (inputs, outputs, errors, duration, metadata) |
| `internal/orchestrator/` | Task execution engine, replay, and failure recovery policy |
| `sdk/python/primitivebox/`| Sync + async Python clients, typed wrappers, callbacks, and SSE streaming helpers |
| `examples/auto_fix_bug/` | Reference agent: Python script demonstrating the API (list files, checkpoint, verify, etc.) |
| `testdata/docker/` | Dockerfile for building the sandbox base image (`primitivebox-sandbox:latest`) |

### RPC API Surface

All primitives are called via JSON-RPC 2.0 `POST /rpc` (host mode) or `POST /sandboxes/{id}/rpc` (sandbox mode).
Methods:
- `fs.read`, `fs.write`, `fs.list`
- `code.search`
- `shell.exec` (supports command whitelisting)
- `state.checkpoint`, `state.restore`, `state.list` (backed by Git under the hood)
- `verify.test` (runs tests and structures output)

Other routes:
- `POST /rpc/stream`, `POST /sandboxes/{id}/rpc/stream`
- `GET /health`, `GET /sandboxes/{id}/health`
- `GET /primitives`, `GET /sandboxes/{id}/primitives`
- `GET /sandboxes`
- `GET /api/v1/sandboxes`, `GET /api/v1/sandboxes/{id}`
- `GET /api/v1/sandboxes/{id}/tree`, `GET /api/v1/sandboxes/{id}/checkpoints`
- `GET /api/v1/events`, `GET /api/v1/events/stream`

## Runtime Notes

- `controlplane.db` under `~/.primitivebox/` is now the source of truth for sandbox metadata and event history.
- Legacy `~/.primitivebox/sandboxes/*.json` entries are imported once on startup for compatibility.
- `DockerDriver` is the working runtime. `KubernetesDriver` is currently a tested skeleton with manifest/status abstractions and CLI routing.
- `pb sandbox create` now accepts `--driver`, `--namespace`, `--ttl`, `--idle-ttl`, and `--network-mode`.

### Adding a New Primitive

1. Implement the `Primitive` interface (`Name()`, `Schema()`, `Execute()`) in `internal/primitive/`.
2. Register it in `internal/primitive/registry.go` (in `RegisterDefaults`).
3. Add a Python wrapper method in `sdk/python/primitivebox/primitives.py`.
4. Update `README.md` and this `CLAUDE.md`.
