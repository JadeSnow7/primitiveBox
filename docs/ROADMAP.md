# PrimitiveBox — Project Roadmap

## Vision

PrimitiveBox is a checkpointed, verifiable, replayable execution runtime for AI agents, evolving toward an AI-native system substrate. AI interacts with containerized systems and AI-native applications through structured primitives with CVR guarantees.

---

## Completed Phases

### Phase 0 — Runtime Foundation [Done]

- 14 system primitives with typed input/output schemas
- CVR coordinator: checkpoint → execute → verify → restore loop
- Docker sandbox driver, SQLite control plane, SSE event streaming
- Sync and async Python SDK
- All 17 Go packages pass tests

### Phase 1 — Developer Experience [Done]

- CLI (`pb`) with ergonomic primitive shortcuts and sandbox lifecycle commands
- CI/CD pipeline: goreleaser multi-platform binaries, GHCR images, Homebrew tap, PyPI
- `auto_fix_bug` demo: manual CVR (BUG-001) and atomic CVR via `macro.safe_edit` (BUG-002)
- `pb doctor` and `pb primitives list` for environment self-check and discovery

### Phase 2 — Application Primitive Protocol [Done]

- `app.register` over Unix socket: external adapters register primitives at runtime
- `AppPrimitiveRegistry` with upsert, `MarkUnavailable`, and lifecycle events
- Router dispatches to app adapters with the same CVR discipline as system primitives
- Namespace reservation enforced (`fs.*`, `shell.*`, `state.*`, `verify.*`, `macro.*`, `code.*`, `test.*` are system-only)
- 15-check smoke test passes end-to-end in under 60 seconds

### Phase 3 — Reference Adapters [Done]

- **`pb-os-adapter`**: `process.list`, `process.spawn`, `process.terminate`, `process.kill`, `process.wait` — OS process management with CVR
- **`pb-mcp-bridge`**: wraps any MCP server as PrimitiveBox app primitives; dynamic registration, inferred intent metadata, lifecycle management
- **`pb-data-adapter`**: SQLite-backed `data.schema`, `data.query` (SELECT-only enforcement), `data.insert` (high-risk, reversible: false)
- **`pb-repo-adapter`**, **`pb-browser-adapter`**: additional reference adapters validating the protocol against real-world complexity
- Kubernetes runtime driver fully implemented: PVC mounts, per-namespace network policies, TTL reaping via `SandboxManager`
- UI Primitive Workspace: panel-based workspace driven by `ui.panel.open`, `ui.layout.split`, `ui.focus.panel` dispatched by the orchestrator
- Timeline & Replay engine: group-level atomic replay, `simulated` mode with zero side effects

### Phase 4 — Package Manager [Done]

- **Boxfile format**: YAML declarative manifest with `adapter`, `primitives`, `healthcheck`, and `bootstrap.files`
- **`pb install` / `pb remove`**: full install lifecycle — launch adapter, poll for registration, healthcheck, persist record, relaunch on server restart
- **`installer.go`**: binary path safety validation, reserved namespace enforcement, rollback on any post-launch failure, `--rpc-endpoint` forwarded to adapter processes
- **`examples/data-pack/`**: end-to-end demo pack (Boxfile + seed.sql + 9-step smoke test)
- **Reviewer Gate**: `AWAITING_REVIEW` orchestrator phase — high-risk primitives suspend execution, `ReviewerPanel` renders exact proposed params, Approve/Reject resolves the promise; unforgeable payload invariant enforced
- **LLM context injection**: `buildOrchestratorContext` injects available app primitives with `requires_review` hint so the model knows which calls will pause for human review
- **App primitive validator**: `validateOrchestratorOutput` accepts catalog-registered app methods in addition to static `EXECUTION_METHODS` allowlist
- Autonomous agent loop: `PLAN → ACT → OBSERVE → REPLAN` with `MaxIterations`, `AbortSignal`, stale-snapshot prevention, and `status: done` short-circuit

---

## Phase 5 — AI-Native OS (Exploratory)

The long-term vision: every application exposes its capabilities as typed primitives with CVR guarantees, a package registry makes them discoverable, and AI is the native interaction model — not a bolted-on assistant, but the primary way work gets done.

Exploratory directions:
- Public package registry with cryptographic signing and audit chain
- `pb install postgres` → `db.*` primitives appear via container adapter
- Cross-package dependency resolution and sandbox resource provisioning
- Per-parameter risk differentiation (beyond per-primitive intent)
- Full-system replay: restore sandbox to historical checkpoint and re-execute from that point
- Entity semantic network: Panel ↔ Entity strong bindings, context-dependency graph for multi-turn agent conversations
