# PrimitiveBox — Long-Term Vision: From Runtime to AI-Native OS

## This Document's Purpose

This is the canonical reference for PrimitiveBox's long-term architectural direction. Every phase builds on the previous one's deliverables. When evaluating any proposal, feature request, or architectural decision, trace it back to this document and verify:

1. Which phase does it belong to?
2. Are the prerequisite phases complete?
3. Does it strengthen the execution model, or compromise it for convenience?

If a proposal cannot answer these three questions, it is premature.

---

## The Core Thesis

Most AI agent systems treat execution as an afterthought — the model decides what to do, then some glue code runs a shell command and hopes for the best. PrimitiveBox inverts this: **execution is the product**.

Every action an agent takes passes through a runtime that enforces typed contracts, checkpoints state before mutations, verifies outcomes, and recovers from failures. This is not a wrapper around tools — it is an execution substrate with transactional semantics.

The thesis, extended to its conclusion: if every application exposes its capabilities as typed primitives with CVR guarantees, and a package manager makes these primitives discoverable and installable, then what you have is an operating system where AI is the native interaction model — not a bolted-on assistant, but the primary way work gets done.

---

## Phase Map

```
<<<<<<< HEAD
Phase 0  Runtime Foundation          ← COMPLETE
Phase 1  Developer Experience        ← COMPLETE (CLI, CI/CD, demo)
Phase 2  Application Primitive Protocol   ← COMPLETE (protocol validated, CVR end-to-end)
Phase 3  Reference Adapters          ← NEXT (first real-world protocol consumers)
=======
Phase 0  Runtime Foundation          ← CURRENT (mostly complete)
Phase 1  Developer Experience        ← IN PROGRESS (CLI, CI/CD, demo)
Phase 2  Application Primitive Protocol   ← NEXT (protocol validation)
Phase 3  Reference Adapters          ← First real-world protocol consumers
>>>>>>> c16f6fb (Complete Phase 2 protocol validation and adapter lifecycle)
Phase 4  Package Manager             ← Distribution and discovery
Phase 5  AI-Native OS               ← The full vision
```

Each phase has a single validation criterion: **can you demonstrate the phase's value in under 60 seconds?**

- Phase 0: `curl /health` → `curl /rpc` with a primitive → see result
- Phase 1: `pb fs read calc.go` → `python3 smoke_test.py` → all green
- Phase 2: external app registers primitives → agent calls them with CVR
- Phase 3: `pb-os-adapter` runs → agent manages processes with checkpoint/verify
- Phase 4: `pb install postgres` → `db.*` primitives appear → agent queries database
- Phase 5: user says "set up a web app with postgres" → agent installs, configures, deploys, verifies — all through primitives, all recoverable

---

## Phase 0: Runtime Foundation

<<<<<<< HEAD
**Status: Complete. 14 system primitives, CVR coordinator, Docker sandboxes, SSE events, Python SDK.**
=======
**Status: Mostly complete. 14 system primitives, CVR coordinator, Docker sandboxes, SSE events, Python SDK.**
>>>>>>> c16f6fb (Complete Phase 2 protocol validation and adapter lifecycle)

### What Exists

```
Primitives (14 registered):
  fs.read, fs.write, fs.list, fs.diff
  code.search, code.symbols
  shell.exec
  verify.test, verify.command, test.run
  state.checkpoint, state.restore, state.list
  macro.safe_edit

Architecture:
  Client → Gateway → Router → CVR Coordinator → Primitive Execution
  SQLite control plane, SSE event streaming, Docker sandbox driver

Key Properties:
  - Every primitive has typed input/output schemas
  - Mutating primitives carry intent metadata (side_effect, risk_level, reversible)
  - fs.write has checkpoint_required: true (runtime-enforced)
  - macro.safe_edit packages full CVR loop into one atomic call
  - All 17 Go packages pass tests
```

### Remaining Gaps
<<<<<<< HEAD
=======
- Integration smoke test not yet automated
>>>>>>> c16f6fb (Complete Phase 2 protocol validation and adapter lifecycle)
- shell.exec is an escape hatch that bypasses primitive typing (by design, but must be constrained in higher phases)
- Kubernetes driver is skeleton-only

### Validation
```bash
./bin/pb server start --workspace ./project
curl http://localhost:8080/health          # → {"status":"ok"}
curl -X POST http://localhost:8080/rpc \
  -d '{"jsonrpc":"2.0","method":"fs.read","params":{"path":"README.md"},"id":1}'
# → file content with typed response
```

---

## Phase 1: Developer Experience

<<<<<<< HEAD
**Status: Complete. CLI, CI/CD pipeline, auto_fix_bug demo.**
=======
**Status: In progress. CLI expansion, CI/CD automation, auto_fix_bug demo.**
>>>>>>> c16f6fb (Complete Phase 2 protocol validation and adapter lifecycle)

### Deliverables

**CLI** — primitives become commands:
```bash
pb rpc fs.read --param path=calc.go        # Generic primitive invocation
pb fs read --path calc.go                   # Ergonomic shortcut
pb shell --command "go test ./..." --stream # Streaming output
pb checkpoint create --label pre-fix        # State management
pb trace watch --type cvr.*                 # CVR event stream
pb demo run auto-fix-bug --step             # Step-through demo
pb primitives list                          # Discover all registered primitives
pb doctor                                   # Environment self-check
```

**CI/CD** — tag-triggered release pipeline:
```
git tag v0.2.0 → goreleaser (multi-platform binaries)
                → GHCR (sandbox + all-in-one Docker images)
                → Homebrew (brew install JadeSnow7/tap/pb)
                → PyPI (pip install primitivebox)
```

**Demo** — dual-mode CVR demonstration:
- BUG-001: Manual CVR (state.checkpoint → fs.write → verify.test → state.restore)
- BUG-002: Atomic CVR (macro.safe_edit — one call, full loop)
- Audience sees: PrimitiveBox is not "AI can edit code" — it's "AI edits code inside a runtime with transactional guarantees"

### Validation
```bash
pb primitives list                           # All 14 primitives visible
python3 smoke_test.py                        # 8/8 checks pass
python3 run_demo.py                          # Agent fixes both bugs via CVR
brew install JadeSnow7/tap/pb && pb version  # Distribution works
```

---

## Phase 2: Application Primitive Protocol

<<<<<<< HEAD
**Status: Complete. `app.register` works, verify/rollback dispatched through the router, 15-check smoke test passes.**
=======
**Status: Protocol validation slice in progress. This is the architectural linchpin — everything after depends on it.**
>>>>>>> c16f6fb (Complete Phase 2 protocol validation and adapter lifecycle)

### The Problem

System primitives (fs.*, shell.*, state.*) are compiled into the runtime. The world has millions of applications. The runtime cannot compile them all in. Applications must be able to **register their own primitives at runtime**, and the runtime must treat them with the same CVR discipline as system primitives.

### Protocol Design

An external application connects via Unix socket and provides a **manifest**:

```json
{
  "app_id": "postgres-adapter",
  "version": "0.3.0",
  "primitives": [
    {
      "name": "db.query",
      "input_schema": { "type": "object", "properties": { "sql": { "type": "string" } }, "required": ["sql"] },
      "output_schema": { "type": "object", "properties": { "rows": { "type": "array" }, "affected": { "type": "integer" } } },
      "intent": {
        "side_effect": "read",
        "risk_level": "none",
        "reversible": true,
        "checkpoint_required": false
      }
    },
    {
      "name": "db.migrate",
      "input_schema": { "...": "..." },
      "output_schema": { "...": "..." },
      "intent": {
        "side_effect": "exec",
        "risk_level": "high",
        "reversible": false,
        "checkpoint_required": true
      },
      "verify": {
        "strategy": "command",
        "command": "pg_migration_verify --check-schema"
      },
      "rollback": {
        "primitive": "db.rollback_migration",
        "description": "Reverts the last migration using down scripts"
      }
    }
  ],
  "sandbox_requirements": {
    "network": ["tcp:5432"],
    "volumes": ["/var/lib/postgresql"],
    "syscalls": ["socket", "bind", "listen"]
  }
}
```

### Critical Protocol Questions (must be answered before implementation)

1. **Verify strategy propagation** — how does an app tell the runtime "run this to check my work"?
   - Option A: `verify.command` field in manifest → runtime executes after dispatch
   - Option B: companion verify primitive (e.g., `db.verify_migration`) → CVR coordinator calls automatically
   - Option C: caller's responsibility → runtime only does file-level checkpoint/restore
   - **Recommendation:** Support all three, with option B as the default for high-risk primitives

2. **App-level rollback** — `state.restore` only covers workspace files, not external state
   - App must be able to declare a rollback primitive in the manifest
   - CVR coordinator calls `db.rollback_migration` instead of (or in addition to) `state.restore`
   - If no rollback primitive declared and primitive is irreversible → force escalation to human

3. **Namespace isolation** — prevent apps from overriding system primitives
   - System namespace (`fs.*`, `state.*`, `shell.*`, `verify.*`, `macro.*`, `code.*`, `test.*`) is reserved
   - App primitives must use app-scoped namespaces: `db.*`, `email.*`, `process.*`
   - Cross-app conflicts: first-registered wins, or runtime requires explicit namespace prefix

4. **Per-parameter risk differentiation**
   - `process.signal(SIGTERM)` is medium risk; `process.signal(SIGKILL)` is high risk
   - Current intent model is per-primitive, not per-parameter
   - Options: risk escalation rules in manifest, or split into separate primitives (`process.terminate` vs `process.kill`)
   - **Recommendation:** Split into separate primitives — simpler, more explicit, better for audit trails

5. **Dynamic registration lifecycle**
   - App crash → in-flight calls fail → primitives become unavailable → runtime emits event
   - App reconnect → re-register → primitives available again
   - No grace period — crash means immediate unavailability (fail-fast principle)

### Existing Code Base

Currently implemented (needs validation and extension):
- `AppPrimitiveRegistry` — supports registration and upsert
- `inferPrimitiveIntent` — consults app registry for intent metadata
- Router dispatches to app via Unix socket
- Error path tests for unreachable socket, RPC error, malformed JSON

Likely gaps (to be confirmed by protocol validation audit):
- No verify strategy field in manifest
- No rollback primitive declaration
- No namespace enforcement
- No schema validation at registration time
- No health check or lifecycle management

### Validation
```bash
# Start the sandbox-local runtime server used by the Phase 2 protocol path.
<<<<<<< HEAD
./bin/pb server start --sandbox-mode --workspace /tmp/pb-phase2-workspace --port 8080

# Start the minimal protocol-validation adapter.
./bin/pb-test-adapter --socket /tmp/test-app.sock

# Canonical one-command proof (15 checks, all pass).
python3 tests/e2e/app_protocol_smoke.py
```

The Phase 2 smoke validates registration, listing, dispatch, verify invocation, rollback invocation, and fail-closed behavior for irreversible primitives in under 60 seconds. All 15 checks pass.
=======
./bin/pb-runtimed --host 127.0.0.1 --port 8080 --workspace /tmp/pb-phase2-workspace --data-dir /tmp/pb-phase2-data

# Start the minimal protocol-validation adapter and register it through app.register.
./bin/pb-test-adapter --socket /tmp/test-app.sock --rpc-endpoint http://127.0.0.1:8080

# Verify registration and metadata visibility.
./bin/pb --endpoint http://127.0.0.1:8080 primitives list
./bin/pb --endpoint http://127.0.0.1:8080 primitives schema demo.set --json

# Call through the runtime.
./bin/pb --endpoint http://127.0.0.1:8080 rpc demo.echo --params '{"message":"hello"}'
./bin/pb --endpoint http://127.0.0.1:8080 rpc demo.fail --params '{"reason":"deliberate"}'

# Canonical one-command proof.
python3 tests/e2e/app_protocol_smoke.py
```

The Phase 2 smoke intentionally validates registration, listing, dispatch, deliberate failure, crash-to-unavailable fail-fast behavior, and reconnect-to-active recovery in under 60 seconds. The adapter also declares verify and rollback primitives so the public manifest surfaces show the full protocol contract, even when the smoke is focused on the transport/lifecycle path. It uses `demo.*` rather than `test.*` because `test.*` is a reserved system namespace.
>>>>>>> c16f6fb (Complete Phase 2 protocol validation and adapter lifecycle)

---

## Phase 3: Reference Adapters

**Status: Design only. Depends on Phase 2 protocol stabilization.**

### Purpose

Build 2-3 real adapters to validate the protocol against real-world complexity. These are not toy examples — they must handle genuine operational concerns.

### Adapter 1: `pb-os-adapter`

Exposes operating system resources as primitives:

```
process.list        read        none       List running processes
process.spawn       exec        medium     Start a new process
process.terminate   exec        medium     Send SIGTERM (graceful)
process.kill        exec        high       Send SIGKILL (force)
process.wait        read        none       Wait for exit, return code

service.status      read        none       Check systemd/launchd service
service.start       exec        medium     Start service (verify: status check)
service.stop        exec        medium     Stop service (rollback: service.start)
service.restart     exec        medium     Compound: stop → start → verify

pkg.list            read        none       List installed packages
pkg.install         exec        high       Install package (checkpoint_required, verify: dpkg --verify)
pkg.remove          exec        high       Remove package (escalation required)

net.listen          read        none       List listening ports
net.resolve         read        none       DNS lookup
net.firewall.allow  exec        high       Add firewall rule (rollback: net.firewall.deny)
```

**Key validation:** Can the CVR coordinator correctly handle `pkg.install` failure → rollback via `pkg.remove`? This exercises app-level rollback (not just file-level state.restore).

### Adapter 2: `pb-postgres-adapter`

Exposes database operations with external state management:

```
db.query            read        none       Execute SELECT, return rows
db.execute          exec        medium     Execute INSERT/UPDATE/DELETE
db.migrate          exec        high       Run migration (verify: schema check, rollback: db.rollback_migration)
db.backup           write       medium     pg_dump to workspace file
db.restore          exec        high       Restore from backup (escalation required)
db.rollback_migration exec      high       Revert last migration
```

**Key validation:** `db.migrate` modifies external state that `state.restore` cannot undo. The protocol must route recovery through `db.rollback_migration`, not just file checkpoint restore.

### Adapter 3: `pb-mcp-bridge`

Wraps any MCP server as a PrimitiveBox app:

```
MCP tool "create_task"    →  asana.task.create    (inferred: exec, medium)
MCP tool "list_projects"  →  asana.project.list   (inferred: read, none)
MCP tool "delete_task"    →  asana.task.delete     (inferred: exec, high)
```

**Key validation:**
- MCP tools have no intent metadata → bridge must infer or require manual annotation
- MCP servers appear/disappear dynamically → lifecycle management
- Primitive names generated at runtime → dynamic registration
- No verify strategy available from MCP → bridge defaults to "caller verifies"

### Validation (per adapter)
```bash
# Each adapter must pass:
pb primitives list                    # Shows adapter's primitives with correct intent
pb rpc <primitive> --param ...        # Successful dispatch and response
pb trace inspect <checkpoint-id>      # CVR decision path is correct
# Intentional failure → recovery path works
# High-risk primitive → escalation triggers
```

---

## Phase 4: Package Manager

**Status: Vision only. Depends on Phase 3 proving the protocol works with real adapters.**

### The Problem

Phase 3 adapters are manually installed. The user has to build the binary, configure the socket, set up dependencies. This doesn't scale. The package manager solves: **discovery, installation, dependency resolution, sandboxing, and lifecycle management — all in one command.**

### Design

```bash
pb install postgres
```

This single command:

```
1. RESOLVE    Query registry for "postgres" package
              → manifest declares: primitives, dependencies, sandbox requirements

2. FETCH      Pull adapter (container image or binary)
              → verify signature against registry public key
              → check hash integrity

3. PROVISION  Configure sandbox environment
              → create volume for /var/lib/postgresql
              → set network policy (allow tcp:5432 outbound)
              → apply resource limits (CPU, memory)
              → install runtime dependencies

4. REGISTER   Start adapter process
              → adapter connects via Unix socket
              → registers db.* primitives with full manifest
              → runtime validates schemas and intent declarations

5. VERIFY     Post-install health check
              → adapter declares healthcheck: "SELECT 1"
              → runtime calls db.query as verification
              → if fails → rollback: remove adapter, release sandbox resources

6. AVAILABLE  Primitives appear in registry
              → pb primitives list shows db.* with correct metadata
              → agent can immediately call db.query with full CVR
```

### Package Manifest (`pb-package.json`)

```json
{
  "name": "postgres",
  "version": "16.2.1",
  "description": "PostgreSQL database adapter for PrimitiveBox",
  "adapter": {
    "type": "container",
    "image": "ghcr.io/primitivebox/adapter-postgres:16.2.1",
    "binary": null
  },
  "primitives": [
    {
      "name": "db.query",
      "intent": { "side_effect": "read", "risk_level": "none" }
    },
    {
      "name": "db.migrate",
      "intent": { "side_effect": "exec", "risk_level": "high", "checkpoint_required": true },
      "verify": { "strategy": "primitive", "primitive": "db.verify_schema" },
      "rollback": { "primitive": "db.rollback_migration" }
    }
  ],
  "sandbox_requirements": {
    "network": { "egress": ["tcp:5432"], "ingress": [] },
    "volumes": [
      { "path": "/var/lib/postgresql", "size_limit": "10Gi", "persistent": true }
    ],
    "resources": { "cpu": "500m", "memory": "512Mi" },
    "syscalls": ["socket", "bind", "listen", "accept"]
  },
  "dependencies": {
    "runtime": ["pb >= 0.3.0"],
    "system": ["libpq5"]
  },
  "healthcheck": {
    "primitive": "db.query",
    "params": { "sql": "SELECT 1" },
    "interval": "30s",
    "timeout": "5s"
  },
  "security": {
    "signature": "...",
    "audit_hash": "sha256:..."
  }
}
```

### Security Model — Three Layers

**Layer 1: Manifest-Declared Permissions (install-time)**
- Package must declare all required permissions in manifest
- `pb install` presents permissions to user for confirmation
- Undeclared access is blocked at sandbox level
- Similar to Android/iOS permission model, but for system resources

**Layer 2: Runtime CVR Enforcement (call-time)**
- Every primitive call passes through CVR coordinator
- High-risk primitives trigger checkpoint or escalation regardless of app intent
- Even if an adapter is compromised, the runtime limits blast radius

**Layer 3: Package Signing + Audit Chain (supply-chain)**
- Registry requires cryptographic signature on every package
- `pb install` verifies signature before installation
- Audit log: who published, when, what changed, hash of every artifact
- Unsigned or tampered packages are rejected

### Package Ecosystem Growth

```
Official packages (maintained by PrimitiveBox):
  pb install os          → process.*, service.*, pkg.*, net.*
  pb install postgres    → db.*
  pb install redis       → cache.*
  pb install mcp-bridge  → bridge for any MCP server

Community packages:
  pb install figma       → design.*
  pb install notion      → doc.*
  pb install github      → repo.*
  pb install stripe      → payment.*
  pb install aws-s3      → storage.*
  pb install chromium    → browser.*
  pb install smtp        → email.*

Meta-packages (bundles):
  pb install webstack    → os + postgres + redis + nginx (http.proxy.*)
  pb install datastack   → os + postgres + redis + jupyter (notebook.*)
```

### CLI Interface

```bash
pb install <package> [--version X.Y.Z] [--sandbox <id>]
pb remove <package>
pb update <package>
pb list --installed
pb search <query>
pb info <package>           # Show manifest, permissions, primitives
pb audit <package>          # Verify signatures, check for known vulnerabilities
pb registry add <url>       # Add third-party registry
```

### Validation
```bash
pb install postgres
pb primitives list | grep db.    # → db.query, db.migrate, db.backup, ...
pb rpc db.query --param sql="SELECT version()"  # → PostgreSQL 16.2.1
pb remove postgres
pb primitives list | grep db.    # → (empty)
```

---

## Phase 5: AI-Native OS

**Status: Long-term vision. Depends on package ecosystem reaching critical mass.**

### The Thesis, Fully Realized

An AI-native OS is not "Linux with a chatbot bolted on." It is an operating system where:

1. **Every application is a primitive provider.** Apps don't have UIs that agents automate — they expose typed primitives that agents call directly. The "UI" is optional, for human oversight, not for primary interaction.

2. **The runtime is the kernel's agent-facing surface.** Just as POSIX defines the syscall interface between processes and the kernel, PrimitiveBox defines the primitive interface between agents and applications.

3. **CVR is the transaction model.** Every agent action is checkpointable, verifiable, and recoverable. This is what makes it safe for AI to operate autonomously — not trust, but verified execution.

4. **The package manager is the app store.** `pb install` is how capabilities enter the system. Discovery, installation, dependency resolution, sandboxing, security — all handled.

5. **Composition replaces configuration.** Instead of the user configuring each app individually, the agent composes primitives from multiple apps to achieve goals. "Set up a blog" = `pb install postgres` + `pb install nginx` + `pb install ghost` → agent calls primitives to configure, connect, and verify.

### Architecture at Scale

```
┌───────────────────────────────────────────────────────┐
│  Agent Layer                                          │
│  (LLM + planning + memory — NOT PrimitiveBox's job)   │
│  Agents see: a flat namespace of typed primitives      │
└──────────────────────┬────────────────────────────────┘
                       │ primitive calls
┌──────────────────────▼────────────────────────────────┐
│  PrimitiveBox Runtime (the "kernel")                  │
│  ├── Primitive Registry (system + all installed apps)  │
│  ├── CVR Coordinator (checkpoint/verify/recover)       │
│  ├── Router (dispatch to correct adapter)              │
│  ├── Event Store (full execution history)              │
│  ├── Package Manager (install/remove/update)           │
│  └── Sandbox Manager (isolation per adapter)           │
└──────────────────────┬────────────────────────────────┘
                       │ Unix socket dispatch
┌──────────────────────▼────────────────────────────────┐
│  Adapter Layer (the "userspace")                      │
│  ├── pb-os-adapter      → process.*, service.*, pkg.* │
│  ├── pb-postgres-adapter → db.*                        │
│  ├── pb-redis-adapter    → cache.*                     │
│  ├── pb-nginx-adapter    → http.proxy.*                │
│  ├── pb-mcp-bridge       → any MCP server              │
│  └── third-party adapters → anything                   │
└──────────────────────┬────────────────────────────────┘
                       │ actual system calls / API calls
┌──────────────────────▼────────────────────────────────┐
│  Actual Resources                                     │
│  Processes, files, databases, networks, APIs, devices  │
└───────────────────────────────────────────────────────┘
```

### What PrimitiveBox Is NOT Responsible For

Even at Phase 5, PrimitiveBox remains a **runtime**, not a complete AI system:

- **NOT responsible for:** agent planning, LLM inference, prompt engineering, conversation management, user authentication, task decomposition heuristics
- **IS responsible for:** primitive dispatch, CVR enforcement, sandbox isolation, event recording, package lifecycle, execution safety

The agent layer (planning, memory, reasoning) is built by others on top of PrimitiveBox, not inside it. PrimitiveBox is to AI agents what the Linux kernel is to applications: it doesn't decide what to do, but it guarantees that whatever gets done is safe, observable, and recoverable.

### How This Differs From Existing Approaches

| Approach | What It Does | What It Lacks |
|----------|-------------|---------------|
| MCP (Model Context Protocol) | Tool discovery + invocation protocol | No execution semantics (no checkpoint, no verify, no recover) |
| LangChain / LlamaIndex | Agent orchestration + tool calling | No runtime isolation, no transactional guarantees |
| Docker / Kubernetes | Container isolation + orchestration | No agent-aware execution model, no primitive typing |
| Traditional OS | Process isolation + syscall interface | Not designed for AI interaction, no CVR |
| PrimitiveBox | Typed primitives + CVR + sandboxing + package manager | Requires ecosystem to deliver full value |

PrimitiveBox's moat is not any single feature — it's the **combination** of typed contracts, transactional execution, and a distribution model that makes every application agent-accessible with safety guarantees.

---

## Phase Dependencies (Do Not Skip)

```
Phase 0 ──→ Phase 1 ──→ Phase 2 ──→ Phase 3 ──→ Phase 4 ──→ Phase 5
Runtime      DX/CI       Protocol    Adapters    PkgMgr      OS
                                     (2-3 real)

Each phase validates the previous:
- Phase 1 validates Phase 0: can a developer actually use the runtime?
- Phase 2 validates Phase 0+1: is the primitive model extensible?
- Phase 3 validates Phase 2: does the protocol survive real-world adapters?
- Phase 4 validates Phase 3: can adapters be distributed at scale?
- Phase 5 validates Phase 4: does the ecosystem compose into something coherent?

RULE: Never start Phase N+1 until Phase N has a working, demonstrable deliverable.
The demo matters more than the code. If you can't show it in 60 seconds, it's not done.
```

---

## Current Position and Next Actions

<<<<<<< HEAD
**You are here: Phase 0, 1, and 2 complete. Phase 3 is next.**

Phase 2 deliverables shipped:
- `app.register` JSON-RPC endpoint accepts manifests with `verify_endpoint`, `rollback_endpoint`, and `intent`
- Router dispatches verify and rollback through the socket after every mutation call
- `pb-test-adapter` provides `demo.*` primitives for integration validation
- `tests/e2e/app_protocol_smoke.py` — 15 checks, all pass
- CI pipeline fixed and green

Phase 3 immediate priorities (in order):
1. Design `pb-os-adapter` primitive set and verify/rollback contract
2. Implement `process.*` and `service.*` primitives with real CVR round-trips
3. Validate that app-level rollback routes through the declared `rollback_endpoint`, not just `state.restore`
4. Document remaining Phase 2 gaps (GAP-01 through GAP-08 in `app-primitive-protocol-report.md`) as Phase 3 acceptance criteria
=======
**You are here: Phase 0 complete, Phase 1 in progress.**

Immediate priorities (in order):
1. Integration smoke test — verify full RPC chain
2. Branch cleanup + CI fix — unblock automation
3. Phase 2 CI/CD — goreleaser, GHCR, Homebrew, PyPI
4. auto_fix_bug demo — prove CVR value to an audience
5. Application primitive protocol audit — find gaps before Phase 2 implementation
6. Godoc coverage — make the codebase contributor-ready

After these six items are done, Phase 1 is complete and Phase 2 begins.
>>>>>>> c16f6fb (Complete Phase 2 protocol validation and adapter lifecycle)
