# AGENTS.md

PrimitiveBox AI agent instructions for this repository.

This is the canonical repository-wide instruction set for AI coding assistants working on PrimitiveBox.
It is written as a normative guide, not a passive description. When the repository is mid-migration, follow the stronger rule in this document unless a test, migration shim, or explicit maintainer note says otherwise.

本文档是 PrimitiveBox 仓库中 AI 编程助手的权威规范。它描述的是“必须收敛到的目标标准”，不只是“当前碰巧如此的实现现状”。

> [!IMPORTANT]
> Read this file before changing gateway, sandbox, control-plane, router, eventing, SSE, SQLite, runtime driver, or SDK code.
> 任何改动如果会触及宿主网关、路由层、沙箱生命周期、SSE、SQLite 控制面、事件模型或 Python SDK，必须先按本文件校准心智模型。

> [!NOTE]
> `AGENTS.md` is the canonical source. Any future `.cursorrules`, `.github/copilot-instructions.md`, `CLAUDE.md`, or assistant-specific rule files should be derived from this file rather than reinventing policy.
> `AGENTS.md` 是母版；其他 AI 工具规则文件应从这里裁剪，不应自行发明另一套规范。

## Mission

PrimitiveBox is a host-side JSON-RPC gateway designed to safely run AI-agent primitives inside isolated sandboxes.

The system must preserve four repository-wide invariants across all changes:

1. Clear execution boundaries between host and sandbox.
2. Durable, queryable control-plane state in SQLite.
3. Event-first observability for streaming, inspection, and replay.
4. Stable public contracts across HTTP, JSON-RPC, SSE, and SDK layers.

PrimitiveBox 是一个面向 AI Agent 工作流的宿主机 JSON-RPC 网关，用于在隔离沙箱内安全执行原语。后续任何代码都必须优先维护四点：执行边界清晰、控制面可查询、事件流可追踪、公共接口稳定。

## How To Read This File

This document intentionally mixes two kinds of statements:

- `Current implementation`: facts that must match the repository today.
- `Target architecture`: future-facing normative rules that may lead the codebase and are intentionally stronger than the current implementation.

When the two differ, do not “simplify” the document down to today’s accidental shape unless maintainers explicitly change direction.

> [!WARNING]
> Future-looking sections are not permission to bypass current safety rules, compatibility code, or routing boundaries.
> 面向未来的规范不是“可以先破坏边界再说”的借口；当前安全边界、兼容逻辑、路由链路仍然必须严格遵守。

## Architecture Overview

PrimitiveBox follows a control-plane / execution-plane architecture.

High-level request flow:

`Client -> Host Gateway -> Router Layer -> Runtime Driver -> Sandbox pb server -> Primitive execution`

### Roles

#### Host Gateway (control plane)

The host gateway is the API boundary and control plane. It:

- authenticates requests
- validates payloads
- manages sandbox lifecycle
- persists metadata and events in SQLite
- exposes JSON-RPC, SSE, and inspector endpoints
- routes requests to the correct sandbox runtime endpoint

The gateway must remain thin, explicit, and predictable.
It manages state and routing. It does not execute sandbox-owned workspace tasks.

#### Router Layer

The router layer resolves sandbox ownership and execution destination.

Its conceptual mapping is:

`sandbox_id -> runtime -> rpc_endpoint`

The router:

- determines which runtime owns a sandbox
- resolves where traffic should be sent
- provides indirection so HTTP handlers do not hardcode runtime logic

#### Runtime Driver

A runtime driver implements lifecycle and endpoint semantics for one runtime backend.

Examples:

- `DockerDriver`
- `KubernetesDriver`

A runtime driver may advertise capabilities, create sandboxes, start or stop them, and resolve their internal RPC endpoints.

#### Sandbox `pb server` (execution plane)

The in-container `pb server` is the untrusted execution plane.
It executes workspace-bound primitives such as:

- `fs.*`
- `shell.*`
- `state.*`
- future high-risk primitives such as database, browser, or tool execution

Execution rule:

- gateway manages state and routing
- sandbox executes primitives

> [!IMPORTANT]
> PrimitiveBox is safest when the host gateway is boring, the router is explicit, the sandbox is the executor, and the control plane tells the truth.
> PrimitiveBox 最稳妥的状态是：宿主网关只做网关该做的事，路由显式透明，沙箱才是真正执行者，控制面始终保存真实状态。

## Read This First

- Treat the control plane as the source of truth for sandbox metadata and event history.
- Treat SSE as a first-class transport, not a debug add-on.
- Prefer interface-driven composition and explicit wiring over hidden globals.
- Preserve backward compatibility with legacy sandbox registry import flows.
- Keep sync and async SDKs aligned.
- Favor small, explicit changes over clever shortcuts.

> [!WARNING]
> Do not confuse host mode with sandbox mode.
> `POST /rpc` operates on the explicit host workspace and may use host-local primitives by design.
> `POST /sandboxes/{id}/rpc` and sandbox-owned workspace operations must execute inside the sandbox context, not on the gateway host.
> 不要混淆宿主工作区模式和沙箱模式。宿主 `/rpc` 可以操作本地工作区；但属于沙箱工作区的动作必须在沙箱内执行，绝不能偷跑到 Gateway 宿主侧。

## Architecture Pillars

### 1. Host Gateway

#### Current implementation

Today the host gateway responsibilities include:

- request handling in `internal/rpc/`
- sandbox lifecycle orchestration in `internal/sandbox/manager.go`
- SQLite metadata and event persistence through `internal/control/`
- event fan-out and context sink propagation through `internal/eventing/`
- SSE and inspector endpoints such as `/rpc/stream`, `/sandboxes/{id}/rpc/stream`, `/api/v1/events`, and `/api/v1/events/stream`
- proxying traffic to the correct sandbox runtime endpoint

#### Target architecture

The host gateway remains the control plane and API boundary.
Its long-term responsibilities are limited to:

- authentication and request validation
- metadata CRUD
- lifecycle orchestration
- event persistence and fan-out
- transport bridging for JSON-RPC, SSE, and inspector APIs
- runtime-aware routing and proxying

The gateway is not the place to execute sandbox-owned workspace payloads.

### 2. Router Layer

#### Current implementation

Routing abstractions currently live under `internal/sandbox/`, including:

- `RuntimeDriver`
- `Store`
- `RouterDriver`
- `DockerDriver`
- `KubernetesDriver`

`RouterDriver` is the runtime indirection layer used to map a sandbox to its owning runtime implementation.

#### Target architecture

Future package boundaries may separate routing and runtime concerns more explicitly, for example:

- `internal/router/` for sandbox ownership resolution and endpoint selection
- `internal/runtime/` for concrete runtime drivers

If those package boundaries appear later, they should be a refactor of current `internal/sandbox/` responsibilities, not a semantic redesign.

> [!NOTE]
> `internal/router/` and `internal/runtime/` are target package boundaries, not current repository facts.
> Today those responsibilities still live under `internal/sandbox/`.
> `internal/router/` 和 `internal/runtime/` 代表目标包边界，不是今天仓库里已经存在的目录；当前实现仍在 `internal/sandbox/` 下。

### 3. In-Container Server

The in-container server is the untrusted execution plane.
It is the correct place for workspace-bound primitive execution such as:

- `fs.*`
- `shell.*`
- `state.*`
- future high-risk primitives such as database or browser execution

The container-local `pb server` is the actual executor for sandbox workspaces. The host gateway exists to authenticate, route, persist state, and observe.

> [!CAUTION]
> Never call `os.*`, `os/exec`, or equivalent host-side file or process APIs in gateway-side code to perform work that belongs to a sandbox workspace.
> If the task is “read this sandbox file”, “run tests in this sandbox repo”, or “modify this sandbox workspace”, route through the sandbox endpoint instead.
> 严禁在 Gateway 宿主侧直接调用 `os.*`、`os/exec` 或同类 API 去代替本应在沙箱内执行的文件读写、测试运行、代码修改等动作。

> [!IMPORTANT]
> The only intended exception is explicit host-workspace mode through `/rpc`, where the user intentionally targets the local workspace served by the host gateway.
> 唯一例外是显式宿主工作区模式，即用户明确通过 `/rpc` 操作本地工作区。

### 4. Host Workspace Mode Safety

Host workspace mode (`POST /rpc`) is a powerful capability and must be used intentionally.

AI agents and automation should default to sandbox execution.

Host workspace operations should only occur when:

- the user explicitly targets the host workspace
- the operation is known to be safe
- sandbox isolation is not required

Accidentally executing sandbox tasks on the host breaks isolation guarantees and can create correctness or security bugs.

## Repository Layout

This section distinguishes current repository facts from target package shape.

### Current implementation

#### `cmd/pb/`

- `main.go` wires the gateway, control-plane store, event bus, and sandbox manager.

#### `internal/control/`

- control-plane persistence
- SQLite store implementation
- sandbox metadata and event storage

#### `internal/eventing/`

- event bus
- event sinks
- context propagation
- streaming integration

#### `internal/sandbox/`

- sandbox lifecycle orchestration
- runtime abstractions
- router implementation
- Docker runtime support
- Kubernetes runtime skeleton
- TTL reaper

#### `internal/rpc/`

- JSON-RPC transport
- host and sandbox proxy endpoints
- SSE endpoints
- inspector API endpoints

#### `internal/primitive/`

- primitive interfaces
- primitive registry
- built-in primitives and schemas

#### `sdk/python/primitivebox/`

- `client.py` — sync client
- `async_client.py` — async client
- `primitives.py` — primitive helpers

### Target architecture

The intended architectural shape is:

`control plane -> router -> runtime -> sandbox executor`

That may later be reflected more explicitly as:

- `internal/control/`
- `internal/router/`
- `internal/runtime/`
- sandbox-local execution via `pb server`

If the package structure evolves, preserve the current semantics and compatibility expectations.

## Control Plane Rules

PrimitiveBox Iteration 3 is control-plane first.
Sandbox metadata and event history belong in SQLite, not in ad hoc files or transient memory.

Required control-plane behavior:

- sandbox metadata is persisted through the configured `Store`
- event history is persisted through the event store used by `eventing.Bus`
- inspector and streaming features must consume the same durable event model
- host-side lifecycle state must remain queryable without scraping logs

### Current implementation

Current repo facts to preserve:

- SQLite control-plane store lives in `internal/control/sqlite_store.go`
- legacy JSON registry files under `~/.primitivebox/sandboxes/*.json` are imported via `ImportLegacyRegistryDir`
- event fan-out and context sink propagation live in `internal/eventing/`
- TTL cleanup is driven by `Manager.RunReaper()`

### Target architecture

Future refactors may move pieces around, but must preserve:

- durable control-plane truth in SQLite
- event persistence as a first-class capability
- queryable lifecycle state for inspector and replay use cases
- compatibility import from legacy registry data

> [!WARNING]
> Do not reintroduce JSON registry files as the primary source of truth.
> Legacy JSON is a compatibility input, not the canonical control plane.
> 不要把旧的零散 JSON registry 重新当成主存储；它只用于兼容导入，不是新的真相来源。

## Go Backend Conventions

### Interface-First Composition

- Depend on interfaces for core lifecycle, persistence, and routing logic.
- Use abstractions such as `RuntimeDriver`, `Store`, and event sinks instead of concrete package-level singletons.
- Assemble dependencies in `cmd/pb/main.go` or similarly explicit bootstrap code.
- Do not introduce package global mutable state for runtime selection, event buses, stores, or clients.

### Write-And-Emit Rule

State changes in the control plane must be observable.
Whenever code persists a meaningful lifecycle or control-plane mutation, it must publish the associated event immediately after the write succeeds.

This applies to operations such as:

- sandbox creation, start, stop, destroy, and reaping
- status transitions or lifecycle timestamp updates
- persisted RPC or primitive execution milestones that power streaming or inspection

Use the shared eventing model:

- attach sinks through `eventing.WithSink`
- emit primitive progress through `eventing.Emit`
- publish durable control-plane events through `eventing.Bus`

> [!IMPORTANT]
> Silent writes are not acceptable for control-plane state.
> “Persisted but not emitted” is a correctness bug because it breaks SSE, replay, and inspector assumptions.
> 对控制面来说，“写入了但没发事件”就是 bug，会破坏流式消费、回放和 Inspector。

### Context-Bound Background Work

- Every long-lived background task must accept `context.Context`.
- Every polling loop must use `select` and handle `<-ctx.Done()` for graceful shutdown.
- Use tickers carefully and always stop them.
- Avoid goroutine leaks in reapers, watchers, and streaming helpers.

The TTL reaper pattern in `Manager.RunReaper()` is the expected baseline.

### API Boundary Telemetry

- Do not rely on scattered `Println` debugging at HTTP or primitive boundaries.
- Success and failure at the API boundary should be normalized through structured events such as `rpc.completed` and `rpc.error`.
- Audit logging may exist as a secondary trace, but eventing is the first-class integration point for runtime observability.

### Routing And Runtime Evolution

- Runtime-specific logic belongs in driver implementations, not in generic gateway handlers.
- New runtime backends must advertise capabilities through `Capabilities()`.
- Runtime routing must continue to flow through `RouterDriver` and sandbox ownership resolution.
- Treat `KubernetesDriver` as a tested skeleton that future code should complete, not bypass.

## Primitive Definition Rules

Primitives are part of the public execution contract.
Every primitive should be designed with predictable semantics.

Required properties:

- JSON input / JSON output
- explicit schema
- deterministic behavior where feasible
- side effects scoped to the intended workspace or runtime boundary
- event emission for meaningful progress or execution milestones

A primitive must not silently broaden its execution scope.
A workspace primitive must remain workspace-bound.
A host primitive must remain host-bound.

### Current implementation

Today built-in primitives implement `primitive.Primitive` and are registered through `primitive.Registry.RegisterDefaults(...)`.
Public behavior is exposed through JSON-RPC methods such as:

- `fs.read`
- `fs.write`
- `fs.list`
- `fs.diff`
- `code.search`
- `code.symbols`
- `shell.exec`
- `state.checkpoint`
- `state.restore`
- `state.list`
- `verify.test`
- `macro.safe_edit`

### Target architecture

Future primitives should continue to follow the same contract even if schema plumbing, internal packages, or code generation evolve.

When adding or changing a primitive:

- define or update input schema
- define or update output schema
- register it in the primitive registry
- add tests for success and failure paths
- update SDK wrappers
- update docs if public behavior changed

## Python SDK Conventions

The Python SDK is part of the public contract, not a thin afterthought.

### Sync/Async Symmetry

- Any public API added to `sdk/python/primitivebox/client.py` must be reflected in `sdk/python/primitivebox/async_client.py`.
- Any new primitive helper added in `primitives.py` must receive equivalent async support.
- Do not allow the async client to lag behind indefinitely.

> [!NOTE]
> The repository is not fully converged yet: async `stream_*` convenience helpers are still a target-state requirement.
> Future changes must close this gap, not widen it.
> 当前仓库在异步 `stream_*` helper 上尚未完全收敛；后续改动必须补齐这类能力，而不是继续扩大差距。

### Streaming Contract

- Streaming RPC interfaces must return `Iterator[dict[str, Any]]` or `AsyncIterator[dict[str, Any]]`.
- Parse Server-Sent Events incrementally from the wire; do not buffer the full response before yielding.
- Preserve event names and structured payloads so SDK users can consume `started`, `stdout`, `stderr`, `progress`, `completed`, and `error` frames consistently.

Copy-paste-valid sync examples today:

```python
for event in client.shell.stream_exec("printf 'hello\\n'"):
    if event["event"] == "stdout":
        print(event["data"])

for event in client.stream_call("fs.diff", {"path": "README.md"}):
    print(event["event"], event["data"])
```

Any future helper names not currently present in the SDK should be labeled as aspirational shorthand, not implied to already exist.

### Typing Discipline

- Use modern Python type hints for public methods, parameters, and return values.
- Avoid implicit `Any` where a more precise type is available.
- Do not add catch-all `**kwargs` passthrough APIs that hide the public contract.
- Keep wrapper method shapes explicit so generated tooling and human users can trust them.

### SDK Change Discipline

When adding or changing a primitive or route:

- update sync client transport if needed
- update async client transport if needed
- update primitive wrappers
- add or update tests under `sdk/python/tests/`
- update README or assistant-facing docs if public behavior changed

## RPC Lifecycle

PrimitiveBox RPC requests follow a structured lifecycle.

Copy-paste-valid current route example:

`POST /sandboxes/{id}/rpc`

Execution flow:

1. Request received by gateway.
2. Authentication and payload validation.
3. Router resolves sandbox runtime.
4. Runtime driver resolves sandbox endpoint.
5. Gateway proxies request to sandbox `pb server`.
6. Sandbox executes primitive.
7. Events are emitted during execution.
8. Gateway streams events to the client through SSE when requested.
9. Final result is returned.

Typical streamed event names today include:

- `started`
- `stdout`
- `stderr`
- `progress`
- `completed`
- `error`

Event emission must follow the write-and-emit rule when control-plane state changes.

## Data Schema And Backward Compatibility

### JSON Contract Is The API

- Public Go request/response structs, especially `*Config` and `*Result`, must use explicit JSON tags.
- Use lower snake_case JSON names.
- Do not rely on Go field-name defaults for API payloads.
- Do not introduce camelCase into public payloads unless an existing public contract already requires it.

Examples of the expected style:

- `json:"mount_source"`
- `json:"rpc_endpoint,omitempty"`
- `json:"timeout_s,omitempty"`

### No Untracked Schema Drift

- If an HTTP or JSON-RPC payload changes, update all affected layers deliberately.
- That usually means Go structs, SDK wrappers, tests, docs, and any inspector-facing consumers.
- Backward compatibility matters more than short-term convenience.

### Legacy Import Must Survive

- Do not remove, bypass, or silently break `ImportLegacyRegistryDir`.
- Do not add migrations that strand existing users with unreadable historical sandbox state.
- Treat legacy registry ingestion as a protected compatibility path.

> [!CAUTION]
> History is an asset. Compatibility code around legacy sandbox registry import is part of the product contract.
> 历史数据是资产。兼容导入逻辑不是“可以顺手删掉的旧代码”，而是用户迁移路径的一部分。

## Event Model

PrimitiveBox uses a structured event model for execution visibility.

### Current implementation

Today the persisted and streamed event model is the concrete `eventing.Event` shape:

```json
{
  "id": 123,
  "timestamp": "2026-03-14T10:12:45Z",
  "type": "rpc.completed",
  "source": "rpc",
  "sandbox_id": "sb-abc123",
  "method": "fs.read",
  "stream": "",
  "message": "fs.read",
  "data": {
    "duration_ms": 42,
    "success": true
  },
  "sequence": 0
}
```

Current event fields to preserve unless a deliberate migration is introduced:

- `id`
- `timestamp`
- `type`
- `source`
- `sandbox_id`
- `method`
- `stream`
- `message`
- `data`
- `sequence`

### Target architecture

The long-term public event contract may be normalized further for external consumers, but any such evolution must be explicit and backward-aware.

Potential target concepts include:

- stable event identifiers
- normalized lifecycle naming
- clearer separation of metadata vs payload
- optional workspace-mode markers when relevant

If the public model evolves, document it as a deliberate migration rather than quietly changing field shapes.

> [!NOTE]
> A more normalized `payload`-style event envelope is a possible target contract, not the current repository fact.
> If introduced, it must coexist with or carefully migrate from the existing `eventing.Event` shape.
> 更统一的 `payload` 风格事件包络可以是未来目标，但不是当前仓库事实；若未来引入，必须有明确迁移方案。

Common event types include:

#### RPC lifecycle

- `rpc.started`
- `rpc.completed`
- `rpc.error`

#### Sandbox lifecycle

- `sandbox.created`
- `sandbox.started`
- `sandbox.stopped`
- `sandbox.destroyed`
- `sandbox.reaped`

#### Primitive and stream lifecycle

- primitive-specific progress events emitted through `eventing.Emit`
- stream names surfaced to clients as `started`, `stdout`, `stderr`, `progress`, `completed`, and `error`

Events are:

- persisted in the control plane
- streamed through SSE
- consumed by inspector tooling

Logs should never be the primary source of truth.
Events are the canonical execution history.

## Eventing And Streaming Expectations

PrimitiveBox uses an event-driven execution model.
SSE is an official interface for SDKs and future inspector tooling.

Required behavior:

- primitive execution should emit structured progress events when applicable
- gateway-level RPC lifecycle events should be published for start, completion, and failure
- streamed transport should reuse the same event semantics as durable storage where possible
- inspector APIs should be able to reconstruct state using persisted events rather than log scraping

Do not create one-off streaming protocols when SSE and `eventing.Bus` can express the same behavior.

## Change Checklist

Use this checklist before merging architecture-relevant changes.

### If You Add Or Change A Primitive

- implement or update the Go primitive
- register it in the registry
- wire event emission if it produces progress or state transitions
- add sync SDK support
- add async SDK support
- add tests
- update public docs if the API surface changed

### If You Add Or Change A Runtime Or Lifecycle Path

- keep `RuntimeDriver` as the abstraction boundary
- preserve `RouterDriver` ownership routing
- persist control-plane state through the store
- emit lifecycle events after successful persistence
- verify inspector and SSE surfaces still reflect the new behavior

### If You Add Or Change A Public Route

- define the payload contract explicitly
- keep JSON field naming stable
- add or update gateway tests
- update Python SDK transport and wrappers if relevant
- update `README.md`, `CLAUDE.md`, and this file when expectations change

## Anti-Patterns

The following are repository-level red flags.

- executing sandbox-owned payloads directly on the host gateway
- adding package-level mutable globals for runtime, store, or event state
- writing control-plane state without corresponding event publication
- starting background goroutines without context cancellation
- inventing parallel side channels instead of using the event bus and SSE
- changing JSON contracts without SDK, tests, and docs updates
- removing or weakening legacy registry import compatibility
- bypassing `RouterDriver` with runtime-specific hacks in generic gateway code
- defaulting to host workspace mode when sandbox execution is appropriate
- treating future architecture sections as permission to skip current safety boundaries

> [!WARNING]
> “It works locally” is not a valid reason to violate host/sandbox boundaries.
> Boundary violations usually look convenient in the short term and become security or correctness bugs later.
> 不能因为“本地能跑”就破坏宿主机与沙箱边界；这类捷径通常会演变成安全或一致性问题。

## Current State vs Target State

This document intentionally describes the desired steady-state standard for PrimitiveBox.
Some areas of the repository are still converging toward it.

Known examples:

- `KubernetesDriver` is a compileable, tested skeleton rather than a full production driver.
- async streaming convenience parity is not yet complete.
- some lifecycle refresh paths may still need stricter write-and-emit consistency over time.
- target package boundaries such as `internal/router/` and `internal/runtime/` are not yet split out from current `internal/sandbox/` responsibilities.

These gaps are not permission to lower the bar. They are the backlog for future cleanup.

## Working Style For Future AI Agents

- Read the relevant package before editing it.
- Preserve the existing architecture vocabulary: control plane, router, runtime driver, event bus, sandbox proxy, SSE.
- Prefer small, explicit changes over magic abstractions.
- When a rule in this file conflicts with a tempting shortcut, follow the rule.
- If behavior seems ambiguous, preserve isolation and compatibility first.

## AI Agent Editing Guidelines

When modifying this repository:

1. Prefer small, explicit changes over broad refactors.
2. Preserve host/sandbox execution boundaries.
3. Do not move execution from sandbox to gateway.
4. Follow the control-plane-first architecture.
5. Preserve event semantics across persistence, streaming, and inspection.
6. Keep sync and async SDK contracts aligned.
7. Preserve backward compatibility unless maintainers explicitly authorize a break.

If a change appears to violate host/sandbox boundaries, assume the boundary rule is correct and adjust the implementation instead.

If a change seems to require runtime-specific behavior in generic handlers, push that logic back into the runtime driver layer.

If a public schema changes, assume SDKs, tests, docs, and inspector surfaces also need updates.

> [!IMPORTANT]
> PrimitiveBox should evolve by becoming more explicit, not more magical.
> PrimitiveBox 的演进方向应当是“更显式、更可验证”，而不是“更隐式、更依赖猜测”。
