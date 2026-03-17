# PrimitiveBox Architecture: Event System & Observability Layer

**Document:** `docs/arch/04_event_observability.md`
**Status:** Design Proposal
**Dependencies:** `01_primitive_taxonomy.md`, `02_app_primitive_protocol.md`, `03_cvr_loop.md`

---

## 0. Current State Analysis

### 0.1 Existing Event Infrastructure

`internal/eventing/eventing.go` provides:
- `Event` struct: `{ID, Timestamp, Type, Source, SandboxID, Method, Stream, Message, Data, Sequence}`
- `Bus`: in-memory pub/sub with SQLite persistence via `Store`
- `Sink` / `MultiSink` / `SinkFunc`: context-bound emission
- `WithSink` / `SinkFromContext` / `Emit`: context propagation

`internal/control/sqlite_store.go` persists events to:
```sql
CREATE TABLE events (id, timestamp, type, source, sandbox_id, method,
                     stream, message, data_json, sequence)
CREATE TABLE trace_steps (id, task_id, trace_id, session_id, attempt_id,
                          sandbox_id, step_id, primitive, checkpoint_id,
                          verify_result, duration_ms, failure_kind, timestamp)
```

`internal/rpc/server.go` currently emits:
| Event type | Trigger |
|---|---|
| `rpc.started` | primitive call received |
| `rpc.completed` | primitive call succeeded |
| `rpc.error` | primitive call failed |
| `sandbox.proxy` | request proxied to sandbox |

`internal/primitive/shell.go` emits: `shell.started`, `shell.output`, `shell.completed`

### 0.2 Gaps

| Gap | Impact |
|---|---|
| No `trace_id` / `span_id` on `Event` | Cannot correlate events across a task boundary |
| No parent-child span linking | Cannot reconstruct execution trees |
| No `ExecutionTrace` aggregate | No per-task trace view |
| `trace_steps` not joined with `events` | Two disconnected observation planes |
| Inspector API has no trace endpoints | `/api/v1/events` is a flat unstructured list |
| No AI-facing debug interface | AI must parse logs/events manually |
| CVR events not enumerated | `03_cvr_loop.md` lists 13 names but no schemas |
| App primitive events not enumerated | `02_app_primitive_protocol.md` mentions them conceptually |

---

## 4.1 Event Type System

### 4.1.1 Extended `Event` Struct

The existing `Event` struct is extended with distributed-tracing correlation fields. **All existing fields are preserved unchanged** — new fields are additive only.

```go
// package eventing

// Event captures a structured control-plane or primitive execution event.
// Fields ID through Sequence are the original set and must not be renamed.
type Event struct {
    // ── Original fields (unchanged) ──────────────────────────────────────
    ID        int64           `json:"id,omitempty"`
    Timestamp string          `json:"timestamp"`
    Type      string          `json:"type"`
    Source    string          `json:"source,omitempty"`
    SandboxID string          `json:"sandbox_id,omitempty"`
    Method    string          `json:"method,omitempty"`
    Stream    string          `json:"stream,omitempty"`
    Message   string          `json:"message,omitempty"`
    Data      json.RawMessage `json:"data,omitempty"`
    Sequence  int64           `json:"sequence,omitempty"`

    // ── Distributed-tracing correlation fields (new) ──────────────────────
    // TraceID groups all events for a single AI task or top-level RPC call.
    // Format: UUID v4 without dashes (32 hex chars).
    TraceID string `json:"trace_id,omitempty"`

    // SpanID uniquely identifies one primitive invocation within the trace.
    // Format: 16 hex chars (8 bytes).
    SpanID string `json:"span_id,omitempty"`

    // ParentSpanID is the SpanID of the caller that triggered this invocation.
    // Empty for root spans. Enables tree reconstruction.
    ParentSpanID string `json:"parent_span_id,omitempty"`

    // PrimitiveID is the canonical name of the primitive (e.g. "fs.write").
    // Matches primitive.Schema.Name.
    PrimitiveID string `json:"primitive_id,omitempty"`

    // TaskID links this event to an orchestrator Task when applicable.
    TaskID string `json:"task_id,omitempty"`

    // StepID links this event to a specific orchestrator Step.
    StepID string `json:"step_id,omitempty"`
}
```

**Migration note:** The SQLite `events` table requires five new columns via `ALTER TABLE`:
```sql
ALTER TABLE events ADD COLUMN trace_id    TEXT;
ALTER TABLE events ADD COLUMN span_id     TEXT;
ALTER TABLE events ADD COLUMN parent_span_id TEXT;
ALTER TABLE events ADD COLUMN primitive_id  TEXT;
ALTER TABLE events ADD COLUMN task_id     TEXT;
ALTER TABLE events ADD COLUMN step_id     TEXT;

CREATE INDEX IF NOT EXISTS idx_events_trace_id ON events (trace_id, id ASC);
CREATE INDEX IF NOT EXISTS idx_events_span ON events (trace_id, span_id);
```

The `ListFilter` is extended:
```go
type ListFilter struct {
    SandboxID    string
    Method       string
    Type         string
    TraceID      string  // new: filter by trace
    SpanID       string  // new: filter by span
    PrimitiveID  string  // new: filter by primitive
    TaskID       string  // new: filter by task
    Limit        int
    Offset       int     // new: pagination
}
```

---

### 4.1.2 Event Type Enumeration

All event types are string constants grouped into four namespaces. Each constant includes the canonical payload schema.

#### Namespace: `sandbox.*` — Control Plane / Sandbox Lifecycle

```go
package eventing

const (
    // Sandbox created and driver resources allocated; container not yet started.
    EventSandboxCreating   = "sandbox.creating"
    // Sandbox container running and pb-runtimed health-check passed.
    EventSandboxRunning    = "sandbox.running"
    // Sandbox stopped cleanly (requested stop or TTL expiry).
    EventSandboxStopped    = "sandbox.stopped"
    // Sandbox container exited with error or failed to start.
    EventSandboxFailed     = "sandbox.failed"
    // Sandbox fully destroyed; storage released.
    EventSandboxDestroyed  = "sandbox.destroyed"
    // Sandbox failed consecutive health checks; entering degraded state.
    EventSandboxDegraded   = "sandbox.health_degraded"
    // Sandbox recovered after degraded state (health check passed again).
    EventSandboxRecovered  = "sandbox.health_recovered"
)
```

**`sandbox.creating` payload schema (Data field):**
```json
{
  "type": "object",
  "properties": {
    "driver":      { "type": "string", "enum": ["docker", "kubernetes"] },
    "image":       { "type": "string" },
    "workspace":   { "type": "string" },
    "labels":      { "type": "object", "additionalProperties": { "type": "string" } }
  },
  "required": ["driver", "image"]
}
```

**`sandbox.failed` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "reason":       { "type": "string" },
    "exit_code":    { "type": "integer" },
    "driver_error": { "type": "string" }
  },
  "required": ["reason"]
}
```

**`sandbox.health_degraded` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "consecutive_failures": { "type": "integer" },
    "last_error":           { "type": "string" },
    "threshold":            { "type": "integer" }
  }
}
```

---

#### Namespace: `prim.*` — Primitive Execution

```go
const (
    // Primitive invocation started. Replaces the ad-hoc "rpc.started" usage
    // when a trace context is available.
    EventPrimStarted    = "prim.started"
    // Intermediate progress event (e.g., streaming output chunks).
    EventPrimProgress   = "prim.progress"
    // Primitive completed successfully.
    EventPrimCompleted  = "prim.completed"
    // Primitive execution failed.
    EventPrimFailed     = "prim.failed"
    // Primitive execution exceeded its deadline.
    EventPrimTimedOut   = "prim.timed_out"

    // Existing shell.* events are preserved and kept as-is.
    // They are considered sub-events of a prim.* span for shell.exec primitives.
    EventShellStarted   = "shell.started"
    EventShellOutput    = "shell.output"
    EventShellCompleted = "shell.completed"

    // Legacy RPC events. Emitted when no trace context is present.
    // Deprecated for new code; prefer prim.* when trace_id is available.
    EventRPCStarted   = "rpc.started"   // kept for backward compat
    EventRPCCompleted = "rpc.completed" // kept for backward compat
    EventRPCError     = "rpc.error"     // kept for backward compat
)
```

**`prim.started` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "primitive_id":   { "type": "string", "description": "e.g. fs.write" },
    "input_summary":  { "type": "string", "description": "truncated to 256 chars" },
    "risk_level":     { "type": "string", "enum": ["none","low","medium","high","critical"] },
    "is_reversible":  { "type": "boolean" },
    "source_system":  { "type": "string", "enum": ["system","app"] }
  },
  "required": ["primitive_id"]
}
```

**`prim.completed` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "primitive_id":    { "type": "string" },
    "duration_ms":     { "type": "integer" },
    "output_summary":  { "type": "string", "description": "truncated to 256 chars" }
  },
  "required": ["primitive_id", "duration_ms"]
}
```

**`prim.failed` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "primitive_id":   { "type": "string" },
    "duration_ms":    { "type": "integer" },
    "failure_kind":   { "type": "string",
                        "enum": ["environment","test_failure","syntax_error",
                                 "timeout","duplicate_retry","app_unavailable",
                                 "verify_timeout","unknown"] },
    "error_code":     { "type": "string" },
    "error_summary":  { "type": "string", "description": "max 512 chars, LLM-safe" }
  },
  "required": ["primitive_id", "failure_kind"]
}
```

**`prim.progress` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "primitive_id": { "type": "string" },
    "stream":       { "type": "string", "enum": ["stdout","stderr","log"] },
    "chunk":        { "type": "string" },
    "bytes_written":{ "type": "integer" }
  }
}
```

---

#### Namespace: `cvr.*` — Checkpoint-Verify-Recover

These are the 13 event types enumerated in `03_cvr_loop.md` § 6.4, now with full payload schemas.

```go
const (
    // A CheckpointManifest was successfully created.
    EventCVRCheckpointTaken   = "cvr.checkpoint_taken"
    // Checkpoint attempt failed (git error, disk full, etc.).
    EventCVRCheckpointFailed  = "cvr.checkpoint_failed"
    // Verify strategy execution started.
    EventCVRVerifyStarted     = "cvr.verify_started"
    // Verify strategy returned Passed.
    EventCVRVerifyPassed      = "cvr.verify_passed"
    // Verify strategy returned Failed.
    EventCVRVerifyFailed      = "cvr.verify_failed"
    // Verify strategy did not complete within deadline.
    EventCVRVerifyTimeout     = "cvr.verify_timeout"
    // RecoveryDecisionTree chose a recovery action.
    EventCVRRecoverTriggered  = "cvr.recover_triggered"
    // Recovery action completed successfully.
    EventCVRRecoverCompleted  = "cvr.recover_completed"
    // Recovery action itself failed.
    EventCVRRecoverFailed     = "cvr.recover_failed"
    // Workspace successfully rolled back to a prior checkpoint.
    EventCVRRolledBack        = "cvr.rolled_back"
    // Step marked UNKNOWN due to verify timeout (human inspection needed).
    EventCVRMarkUnknown       = "cvr.mark_unknown"
    // CVRCoordinator escalated to human because all automated recovery exhausted.
    EventCVREscalated         = "cvr.escalated_to_human"
    // CVR loop completed (success or known failure); summarises the outcome.
    EventCVRLoopCompleted     = "cvr.loop_completed"
)
```

**`cvr.checkpoint_taken` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "manifest_id":       { "type": "string" },
    "commit_hash":       { "type": "string" },
    "checkpoint_reason": { "type": "string",
                           "enum": ["pre_write","pre_exec","manual","scheduled",
                                    "pre_task","recovery_fallback"] },
    "trigger_primitive": { "type": "string" },
    "files_modified":    { "type": "integer" }
  },
  "required": ["manifest_id", "commit_hash", "checkpoint_reason"]
}
```

**`cvr.verify_started` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "strategy_type": { "type": "string",
                       "enum": ["exit_code","test_suite","schema_check",
                                "ai_judge","composite"] },
    "strategy_config": { "type": "object", "description": "strategy-specific parameters" },
    "manifest_id":    { "type": "string" }
  },
  "required": ["strategy_type"]
}
```

**`cvr.verify_failed` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "strategy_type":   { "type": "string" },
    "outcome":         { "type": "string", "enum": ["failed","error"] },
    "failure_summary": { "type": "string", "description": "max 1024 chars" },
    "tests_failed":    { "type": "integer" },
    "tests_total":     { "type": "integer" },
    "manifest_id":     { "type": "string" }
  },
  "required": ["strategy_type", "outcome", "failure_summary"]
}
```

**`cvr.recover_triggered` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "recover_action":  { "type": "string",
                         "enum": ["retry","rollback","fallback_earlier",
                                  "rewrite","escalate","mark_unknown"] },
    "recover_hint":    { "type": "string" },
    "failure_kind":    { "type": "string" },
    "attempt":         { "type": "integer" },
    "has_checkpoint":  { "type": "boolean" },
    "manifest_id":     { "type": "string" }
  },
  "required": ["recover_action", "failure_kind", "attempt"]
}
```

**`cvr.rolled_back` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "target_manifest_id": { "type": "string" },
    "target_commit":      { "type": "string" },
    "files_restored":     { "type": "integer" },
    "rollback_depth":     { "type": "integer",
                            "description": "0 = immediate parent, 1 = grandparent, etc." }
  },
  "required": ["target_manifest_id", "target_commit"]
}
```

**`cvr.mark_unknown` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "step_id":        { "type": "string" },
    "primitive_id":   { "type": "string" },
    "verify_timeout_ms": { "type": "integer" },
    "manifest_id":    { "type": "string" },
    "inspect_url":    { "type": "string",
                        "description": "Inspector deep-link for human review" }
  },
  "required": ["step_id", "primitive_id"]
}
```

**`cvr.loop_completed` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "final_status":    { "type": "string",
                         "enum": ["passed","rolled_back","unknown","escalated","failed"] },
    "total_attempts":  { "type": "integer" },
    "checkpoints_taken": { "type": "integer" },
    "verify_runs":     { "type": "integer" },
    "total_duration_ms": { "type": "integer" }
  },
  "required": ["final_status", "total_attempts"]
}
```

---

#### Namespace: `app.*` — Application Primitive Events

```go
const (
    // An app primitive server completed registration via Unix socket.
    EventAppRegistered      = "app.registered"
    // An app was deregistered (graceful shutdown or health-check eviction).
    EventAppDeregistered    = "app.deregistered"
    // Health probe returned OK.
    EventAppHealthOK        = "app.health_ok"
    // Health probe returned failure (consecutive_failures incremented).
    EventAppHealthFail      = "app.health_fail"
    // An app-namespace primitive was invoked.
    EventAppPrimCalled      = "app.prim_called"
    // An app-namespace primitive call completed successfully.
    EventAppPrimCompleted   = "app.prim_completed"
    // An app-namespace primitive call returned an error.
    EventAppPrimFailed      = "app.prim_failed"
    // App re-registered an already-known primitive (version update).
    EventAppPrimUpdated     = "app.prim_updated"
)
```

**`app.registered` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "app_id":          { "type": "string" },
    "app_version":     { "type": "string" },
    "namespace":       { "type": "string" },
    "primitive_count": { "type": "integer" },
    "primitives":      { "type": "array", "items": { "type": "string" },
                         "description": "list of primitive names" },
    "socket_path":     { "type": "string" }
  },
  "required": ["app_id", "namespace", "primitive_count"]
}
```

**`app.health_fail` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "app_id":                { "type": "string" },
    "consecutive_failures":  { "type": "integer" },
    "last_error":            { "type": "string" },
    "route_status":          { "type": "string",
                               "enum": ["active","degraded","evicted"] }
  },
  "required": ["app_id", "consecutive_failures", "route_status"]
}
```

**`app.prim_failed` payload schema:**
```json
{
  "type": "object",
  "properties": {
    "app_id":        { "type": "string" },
    "primitive_id":  { "type": "string" },
    "error_summary": { "type": "string" },
    "transport_err": { "type": "boolean",
                       "description": "true if the app process is unreachable" }
  },
  "required": ["app_id", "primitive_id"]
}
```

#### Co-emission with `prim.*` events

> **架构自检补充**（来自 `00_arch_review.md` 检查 2a）

For the same app primitive invocation, **both** `prim.*` and `app.*` events are emitted. They serve different purposes and must not be conflated:

| Event | Emitter | Purpose | Carries `span_id`? |
|---|---|---|---|
| `prim.started` | AppRouter | Opens a trace span; carries `trace_id` + `span_id` | **Yes** — this is the span boundary |
| `app.prim_called` | AppRouter | Router-level dispatch counter; carries `app_id` + `namespace` | No |
| `prim.completed` / `prim.failed` | AppRouter | Closes the trace span | **Yes** |
| `app.prim_completed` / `app.prim_failed` | AppRouter | App health/stats accounting | No |

**Emission sequence for a successful app primitive call:**

```
1. prim.started         {trace_id, span_id, primitive_id, source_system:"app"}
2. app.prim_called      {app_id, namespace, primitive_id}   ← no span_id
3. [dispatch to app process via Unix socket]
4. prim.completed       {trace_id, span_id, duration_ms}
5. app.prim_completed   {app_id, primitive_id, duration_ms} ← no span_id
```

**Special case — app unavailable before dispatch:**
If `AppRouter` detects `route_status = evicted` before the Unix socket call is made, the span never starts:
```
1. app.prim_failed {app_id, primitive_id, transport_err: true, route_status: "evicted"}
   ← prim.started is NOT emitted (span never opened)
```

---

### 4.1.3 Event Emission Rules

All emission follows the **write-and-emit rule** from AGENTS.md: persist control-plane state first, then emit the event. The table below maps each namespace to the component responsible for emission.

| Namespace | Emitting Component | Context Key |
|---|---|---|
| `sandbox.*` | `internal/sandbox/manager.go` | `sandbox_id` |
| `prim.*` | `internal/rpc/server.go` (via context sink) | `span_id`, `trace_id` |
| `shell.*` | `internal/primitive/shell.go` | inherits span from context |
| `cvr.*` | `internal/cvr/coordinator.go` (proposed) | `trace_id`, `step_id`, `manifest_id` |
| `app.*` | `internal/sandbox/router.go` (app routes) | `app_id`, `namespace` |
| `rpc.*` | `internal/rpc/server.go` (legacy path) | none (no trace context) |

**Span ID generation:** when a primitive call starts, `server.go` generates a `span_id` if none is present in the incoming request context. The `trace_id` is propagated from the `X-PrimitiveBox-Trace-ID` request header (new header) or generated fresh.

```go
// New header constants (internal/runtrace/runtrace.go additions)
const (
    HeaderTraceStep = "X-PrimitiveBox-Trace-Step"  // existing
    HeaderTraceID   = "X-PrimitiveBox-Trace-ID"    // new
    HeaderSpanID    = "X-PrimitiveBox-Span-ID"     // new
    HeaderParentSpanID = "X-PrimitiveBox-Parent-Span-ID" // new
)
```

---

## 4.2 ExecutionTrace

### 4.2.1 Data Structure

An `ExecutionTrace` aggregates all events for a single `trace_id` into a navigable tree. The tree mirrors the primitive call graph: each node is one primitive invocation (one span), and its children are sub-primitives it triggered (e.g., `macro.safe_edit` spawns `state.checkpoint`, `fs.write`, `verify.test`).

```go
// package runtrace (extends existing package)

// SpanStatus mirrors orchestrator StepStatus vocabulary for consistency.
type SpanStatus string

const (
    SpanRunning    SpanStatus = "running"
    SpanPassed     SpanStatus = "passed"
    SpanFailed     SpanStatus = "failed"
    SpanRolledBack SpanStatus = "rolled_back"
    SpanUnknown    SpanStatus = "unknown"  // verify timeout case
    SpanSkipped    SpanStatus = "skipped"
)

// TraceSpan represents a single primitive invocation within a trace.
// Corresponds to one "span" in distributed tracing terminology.
type TraceSpan struct {
    // Identity
    SpanID       string `json:"span_id"`
    ParentSpanID string `json:"parent_span_id,omitempty"` // empty = root span
    TraceID      string `json:"trace_id"`

    // Primitive info
    PrimitiveID string          `json:"primitive_id"`
    Input       json.RawMessage `json:"input,omitempty"`
    Output      json.RawMessage `json:"output,omitempty"`

    // Timing
    StartTime   time.Time     `json:"start_time"`
    EndTime     time.Time     `json:"end_time,omitempty"`
    DurationMs  int64         `json:"duration_ms"`

    // Status
    Status      SpanStatus    `json:"status"`
    FailureKind string        `json:"failure_kind,omitempty"`
    ErrorSummary string       `json:"error_summary,omitempty"`

    // CVR linkage
    CheckpointManifestID string `json:"checkpoint_manifest_id,omitempty"`
    VerifyOutcome        string `json:"verify_outcome,omitempty"`
    RecoverAction        string `json:"recover_action,omitempty"`
    RolledBack           bool   `json:"rolled_back,omitempty"`

    // Orchestrator linkage
    TaskID  string `json:"task_id,omitempty"`
    StepID  string `json:"step_id,omitempty"`

    // Source info
    SandboxID   string `json:"sandbox_id,omitempty"`
    SourceSystem string `json:"source_system,omitempty"` // "system" | "app"
    AppID       string `json:"app_id,omitempty"`

    // Tree structure: populated when building ExecutionTrace.
    // Not stored in SQLite — derived at read time.
    Children []*TraceSpan `json:"children,omitempty"`

    // Raw events ordered by timestamp for this span.
    Events []eventing.Event `json:"events,omitempty"`
}

// ExecutionTrace is the complete record of all activity under one trace_id.
type ExecutionTrace struct {
    TraceID   string       `json:"trace_id"`
    TaskID    string       `json:"task_id,omitempty"`
    SandboxID string       `json:"sandbox_id,omitempty"`

    // StartTime and EndTime bracket the full trace.
    StartTime time.Time    `json:"start_time"`
    EndTime   time.Time    `json:"end_time,omitempty"`
    DurationMs int64       `json:"duration_ms"`

    // FinalStatus is derived from root span status.
    FinalStatus SpanStatus `json:"final_status"`

    // RootSpans are spans with no parent (usually one, unless parallel execution).
    RootSpans []*TraceSpan `json:"root_spans"`

    // FlatSpans provides O(1) lookup by span_id without tree traversal.
    // Populated at build time, not serialised to JSON (omitempty + nil).
    FlatSpans map[string]*TraceSpan `json:"-"`

    // Summary statistics
    TotalSpans       int `json:"total_spans"`
    FailedSpans      int `json:"failed_spans"`
    CheckpointsTaken int `json:"checkpoints_taken"`
    RollbackCount    int `json:"rollback_count"`
}
```

**`TraceStore` interface** (extends `runtrace.Store`):

```go
// TraceStore persists and retrieves execution traces.
// Builds ExecutionTrace by joining trace_steps + events tables.
type TraceStore interface {
    runtrace.Store

    // GetTrace returns the fully assembled ExecutionTrace for the given trace_id.
    // Returns ErrNotFound if no spans exist for that trace_id.
    GetTrace(ctx context.Context, traceID string) (*ExecutionTrace, error)

    // ListTraces returns trace summaries (no children/events) matching the filter.
    ListTraces(ctx context.Context, filter TraceFilter) ([]Tracesummary, error)

    // AppendSpan records a completed TraceSpan to persistent storage.
    // Called by CVRCoordinator and rpc/server.go at span completion.
    AppendSpan(ctx context.Context, span TraceSpan) error
}

type TraceFilter struct {
    SandboxID  string
    TaskID     string
    Status     SpanStatus
    Since      time.Time
    Limit      int
    Offset     int
}

// TraceSummary is a lightweight trace descriptor without the full tree.
type TraceSummary struct {
    TraceID      string       `json:"trace_id"`
    TaskID       string       `json:"task_id,omitempty"`
    SandboxID    string       `json:"sandbox_id,omitempty"`
    StartTime    time.Time    `json:"start_time"`
    DurationMs   int64        `json:"duration_ms"`
    FinalStatus  SpanStatus   `json:"final_status"`
    TotalSpans   int          `json:"total_spans"`
    FailedSpans  int          `json:"failed_spans"`
}
```

**SQLite schema additions:**

```sql
-- Span-level persistence; events are already in the events table and linked via trace_id+span_id
CREATE TABLE IF NOT EXISTS trace_spans (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    span_id       TEXT NOT NULL UNIQUE,
    parent_span_id TEXT,
    trace_id      TEXT NOT NULL,
    primitive_id  TEXT NOT NULL,
    input_json    TEXT,
    output_json   TEXT,
    start_time    TEXT NOT NULL,
    end_time      TEXT,
    duration_ms   INTEGER,
    status        TEXT NOT NULL,
    failure_kind  TEXT,
    error_summary TEXT,
    checkpoint_manifest_id TEXT,
    verify_outcome  TEXT,
    recover_action  TEXT,
    rolled_back     INTEGER DEFAULT 0,
    task_id         TEXT,
    step_id         TEXT,
    sandbox_id      TEXT,
    source_system   TEXT,
    app_id          TEXT
);

CREATE INDEX IF NOT EXISTS idx_spans_trace_id     ON trace_spans (trace_id, start_time ASC);
CREATE INDEX IF NOT EXISTS idx_spans_parent       ON trace_spans (trace_id, parent_span_id);
CREATE INDEX IF NOT EXISTS idx_spans_task         ON trace_spans (task_id);
CREATE INDEX IF NOT EXISTS idx_spans_sandbox      ON trace_spans (sandbox_id, start_time DESC);
```

---

### 4.2.2 Replay Semantics

Two distinct replay modes with fundamentally different semantics:

#### Mode A: Re-Execution Replay

**Definition:** Re-execution replay re-runs the original primitive sequence against the workspace state at the time of each checkpoint, effectively reconstructing the execution from scratch.

**Use case:** Debugging a failed AI task — engineer restores to the last known-good checkpoint and steps through the remaining primitives one by one, with the option to override inputs.

**Mechanism:**
1. The `ExecutionTrace` provides the ordered primitive sequence and inputs.
2. For each span in topological order (DFS pre-order):
   - If `span.CheckpointManifestID` is set, restore workspace to that manifest before executing.
   - Re-issue the original `Input` to the primitive (or allow override).
   - Record the new result as a parallel "replay span" with `replay_of` set to the original `span_id`.
3. Re-execution stops at the first user-specified breakpoint (by `span_id` or `primitive_id`).

**Key invariant:** Re-execution replay **mutates workspace state**. It must operate on an isolated copy (new sandbox) or from an explicit checkpoint restore. The original trace is never modified.

```go
// ReExecutionReplayRequest specifies a re-execution replay session.
type ReExecutionReplayRequest struct {
    TraceID     string                     `json:"trace_id"`
    // SandboxID of the target sandbox; must be stopped or a new clone.
    // If empty, the system creates a new sandbox from the original config.
    TargetSandboxID string                 `json:"target_sandbox_id,omitempty"`
    // BreakpointSpanID: stop just before executing this span.
    // If empty, replay runs all spans to completion.
    BreakpointSpanID string               `json:"breakpoint_span_id,omitempty"`
    // InputOverrides: per-span input overrides keyed by original span_id.
    InputOverrides map[string]json.RawMessage `json:"input_overrides,omitempty"`
    // SkipPassedSpans: skip spans whose original status was SpanPassed.
    // Workspace state is NOT restored for skipped spans.
    SkipPassedSpans bool                   `json:"skip_passed_spans"`
}

type ReExecutionReplaySession struct {
    SessionID     string               `json:"session_id"`
    OriginalTraceID string             `json:"original_trace_id"`
    ReplayTraceID string               `json:"replay_trace_id"` // new trace_id for replay run
    TargetSandboxID string            `json:"target_sandbox_id"`
    Status        string              `json:"status"` // "running"|"paused"|"completed"|"failed"
    CurrentSpanID string              `json:"current_span_id,omitempty"`
    SpanResults   []ReplaySpanResult  `json:"span_results"`
}

type ReplaySpanResult struct {
    OriginalSpanID string          `json:"original_span_id"`
    ReplaySpanID   string          `json:"replay_span_id"`
    Status         SpanStatus      `json:"status"`
    DurationMs     int64           `json:"duration_ms"`
    OutputDiff     string          `json:"output_diff,omitempty"` // diff vs original output
    Skipped        bool            `json:"skipped"`
}
```

---

#### Mode B: Event Stream Replay

**Definition:** Event stream replay plays back the recorded event sequence in temporal order from the `events` table **without executing any primitives**. It is a pure data playback: a read-only projection of what happened.

**Use case:** Observability and auditing — render a timeline of what an AI agent did, display it in the Inspector UI, or stream it to an external monitoring system. No workspace mutation occurs.

**Mechanism:**
1. Fetch all events for `trace_id` ordered by `(timestamp ASC, id ASC)` from the `events` table.
2. Emit events through an SSE channel at configurable speed (real-time, 2x, 10x, or instant).
3. Client receives standard `Event` objects; the Inspector UI renders them as an animated timeline.
4. A synthetic `replay.started` event is prepended and `replay.completed` is appended.

```go
// EventStreamReplayRequest specifies an event stream replay.
type EventStreamReplayRequest struct {
    TraceID     string  `json:"trace_id"`
    // SpeedFactor: 1.0 = real-time, 0 = instant (no delay between events).
    SpeedFactor float64 `json:"speed_factor"`
    // FilterTypes: if non-empty, only replay events whose Type is in this list.
    FilterTypes []string `json:"filter_types,omitempty"`
    // StartSpanID: begin replay from the first event of this span.
    StartSpanID string   `json:"start_span_id,omitempty"`
}

// EventStreamReplaySession is returned when the client opens the SSE stream.
type EventStreamReplaySession struct {
    SessionID   string `json:"session_id"`
    TraceID     string `json:"trace_id"`
    TotalEvents int    `json:"total_events"`
    TraceStart  string `json:"trace_start"`
    TraceEnd    string `json:"trace_end"`
    DurationMs  int64  `json:"duration_ms"`
}
```

**Comparison table:**

| Dimension | Re-Execution Replay | Event Stream Replay |
|---|---|---|
| Workspace mutation | Yes (requires checkpoint restore) | No |
| Requires running sandbox | Yes | No |
| Output determinism | Non-deterministic (side effects replay) | Deterministic (identical playback) |
| Use case | Debugging, step-over, input override | Auditing, UI timeline, export |
| Breakpoints | Supported | Not applicable |
| Speed control | Step-by-step only | Configurable speed factor |
| New trace generated | Yes (`replay_trace_id`) | No |
| Primitive calls made | Yes | No |
| SSE stream | Optional (per-span progress) | Primary output |

---

## 4.3 Inspector API Additions

All new endpoints are under `/api/v1/`. Existing endpoints (`/api/v1/sandboxes`, `/api/v1/events`, `/api/v1/events/stream`) are unchanged.

---

### Endpoint 1: `GET /api/v1/traces/{trace_id}`

Returns the full `ExecutionTrace` for a given trace, including the full span tree and per-span events.

```yaml
# OpenAPI 3.0 fragment
paths:
  /api/v1/traces/{trace_id}:
    get:
      operationId: getTrace
      summary: Get full execution trace by trace ID
      parameters:
        - name: trace_id
          in: path
          required: true
          schema:
            type: string
            example: "a3f8b2c1d4e5f6a7b8c9d0e1f2a3b4c5"
        - name: include_events
          in: query
          required: false
          schema:
            type: boolean
            default: true
          description: >
            When true, each TraceSpan includes its Events array.
            Set to false for lightweight tree navigation.
        - name: include_inputs
          in: query
          required: false
          schema:
            type: boolean
            default: false
          description: >
            When true, each TraceSpan includes the raw Input JSON.
            Omitted by default to reduce response size.
      responses:
        "200":
          description: Execution trace found
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ExecutionTrace'
        "404":
          description: Trace not found
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorResponse'
        "500":
          description: Internal server error

components:
  schemas:
    TraceSpan:
      type: object
      required: [span_id, trace_id, primitive_id, start_time, status]
      properties:
        span_id:              { type: string }
        parent_span_id:       { type: string, nullable: true }
        trace_id:             { type: string }
        primitive_id:         { type: string, example: "fs.write" }
        input:                { type: object, nullable: true,
                                description: "Omitted unless include_inputs=true" }
        output:               { type: object, nullable: true }
        start_time:           { type: string, format: date-time }
        end_time:             { type: string, format: date-time, nullable: true }
        duration_ms:          { type: integer }
        status:               { type: string,
                                enum: [running, passed, failed, rolled_back,
                                       unknown, skipped] }
        failure_kind:         { type: string, nullable: true }
        error_summary:        { type: string, nullable: true,
                                maxLength: 512 }
        checkpoint_manifest_id: { type: string, nullable: true }
        verify_outcome:       { type: string, nullable: true }
        recover_action:       { type: string, nullable: true }
        rolled_back:          { type: boolean }
        task_id:              { type: string, nullable: true }
        step_id:              { type: string, nullable: true }
        sandbox_id:           { type: string, nullable: true }
        source_system:        { type: string, enum: [system, app], nullable: true }
        app_id:               { type: string, nullable: true }
        children:             { type: array, items: { $ref: '#/components/schemas/TraceSpan' } }
        events:               { type: array, items: { $ref: '#/components/schemas/Event' },
                                description: "Populated when include_events=true" }

    ExecutionTrace:
      type: object
      required: [trace_id, start_time, final_status, root_spans, total_spans]
      properties:
        trace_id:         { type: string }
        task_id:          { type: string, nullable: true }
        sandbox_id:       { type: string, nullable: true }
        start_time:       { type: string, format: date-time }
        end_time:         { type: string, format: date-time, nullable: true }
        duration_ms:      { type: integer }
        final_status:     { type: string,
                            enum: [running, passed, failed, rolled_back,
                                   unknown, skipped] }
        root_spans:       { type: array,
                            items: { $ref: '#/components/schemas/TraceSpan' } }
        total_spans:      { type: integer }
        failed_spans:     { type: integer }
        checkpoints_taken:{ type: integer }
        rollback_count:   { type: integer }

    ErrorResponse:
      type: object
      required: [error]
      properties:
        error:   { type: string }
        code:    { type: string }
        details: { type: object, nullable: true }
```

---

### Endpoint 2: `GET /api/v1/traces/{trace_id}/replay`

Returns replay session metadata and starts the appropriate replay mode. Separate sub-endpoints handle the two modes.

```yaml
paths:
  /api/v1/traces/{trace_id}/replay:
    get:
      operationId: getReplayMeta
      summary: Get replay session metadata for a trace
      parameters:
        - name: trace_id
          in: path
          required: true
          schema: { type: string }
        - name: mode
          in: query
          required: false
          schema:
            type: string
            enum: [event_stream, re_execution]
            default: event_stream
          description: Which replay mode to describe
      responses:
        "200":
          description: Replay session info
          content:
            application/json:
              schema:
                oneOf:
                  - $ref: '#/components/schemas/EventStreamReplaySession'
                  - $ref: '#/components/schemas/ReExecutionReplaySession'

  /api/v1/traces/{trace_id}/replay/stream:
    get:
      operationId: streamReplay
      summary: >
        Stream recorded events for a trace via SSE (event stream replay mode).
        Does not execute any primitives.
      parameters:
        - name: trace_id
          in: path
          required: true
          schema: { type: string }
        - name: speed_factor
          in: query
          schema: { type: number, default: 1.0, minimum: 0 }
          description: >
            Playback speed multiplier. 0 = instant (no delay).
        - name: filter_types
          in: query
          schema:
            type: array
            items: { type: string }
          style: form
          explode: false
          description: >
            Comma-separated list of event types to include.
            If omitted, all event types are replayed.
        - name: start_span_id
          in: query
          schema: { type: string }
      responses:
        "200":
          description: SSE event stream
          content:
            text/event-stream:
              schema:
                type: string
                description: >
                  Server-Sent Events stream. Each SSE event has:
                    event: <event-type>   (matches Event.Type or "replay.started"/"replay.completed")
                    data: <json-encoded Event>
              example: |
                event: replay.started
                data: {"session_id":"...","trace_id":"...","total_events":42}

                event: prim.started
                data: {"trace_id":"...","span_id":"...","primitive_id":"fs.write",...}

                event: replay.completed
                data: {"session_id":"...","total_replayed":42}
        "404":
          description: Trace not found

  /api/v1/traces/{trace_id}/replay/execute:
    post:
      operationId: startReExecution
      summary: >
        Start a re-execution replay session. Requires a running or new sandbox.
        Re-executes primitives in topological order; mutates workspace state.
      parameters:
        - name: trace_id
          in: path
          required: true
          schema: { type: string }
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ReExecutionReplayRequest'
            example:
              target_sandbox_id: "sb-abc123"
              skip_passed_spans: true
              breakpoint_span_id: "a1b2c3d4e5f60001"
      responses:
        "202":
          description: Re-execution replay session started
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ReExecutionReplaySession'
        "400":
          description: Invalid request (e.g., sandbox not available)
        "404":
          description: Trace not found

  # ── Trace listing ───────────────────────────────────────────────────────
  /api/v1/traces:
    get:
      operationId: listTraces
      summary: List execution trace summaries
      parameters:
        - name: sandbox_id
          in: query
          schema: { type: string }
        - name: task_id
          in: query
          schema: { type: string }
        - name: status
          in: query
          schema:
            type: string
            enum: [running, passed, failed, rolled_back, unknown, skipped]
        - name: since
          in: query
          schema: { type: string, format: date-time }
        - name: limit
          in: query
          schema: { type: integer, default: 50, maximum: 500 }
        - name: offset
          in: query
          schema: { type: integer, default: 0 }
      responses:
        "200":
          description: List of trace summaries
          content:
            application/json:
              schema:
                type: object
                properties:
                  traces:
                    type: array
                    items: { $ref: '#/components/schemas/TraceSummary' }
                  total:   { type: integer }
                  offset:  { type: integer }

    components:
      schemas:
        TraceSummary:
          type: object
          required: [trace_id, start_time, final_status, total_spans]
          properties:
            trace_id:       { type: string }
            task_id:        { type: string, nullable: true }
            sandbox_id:     { type: string, nullable: true }
            start_time:     { type: string, format: date-time }
            duration_ms:    { type: integer }
            final_status:   { type: string }
            total_spans:    { type: integer }
            failed_spans:   { type: integer }
```

---

### Endpoint 3: `GET /api/v1/primitives`

Returns all registered primitives, including app-registered primitives. The existing `/primitives` endpoint on the sandbox server returns only sandbox-local primitives; this Inspector endpoint aggregates across all sources.

```yaml
paths:
  /api/v1/primitives:
    get:
      operationId: listAllPrimitives
      summary: >
        List all primitives visible to the gateway: system primitives
        (registered in primitive.Registry) plus any app primitives
        registered via the app primitive protocol.
      parameters:
        - name: source
          in: query
          schema:
            type: string
            enum: [all, system, app]
            default: all
          description: Filter by primitive source
        - name: sandbox_id
          in: query
          schema: { type: string }
          description: >
            If specified, include primitives registered by apps in this sandbox.
        - name: category
          in: query
          schema: { type: string }
          description: Filter by primitive category (e.g. "fs", "state", "macro")
        - name: include_schema
          in: query
          schema: { type: boolean, default: true }
          description: >
            When false, returns only name/category/source without full schema JSON.
      responses:
        "200":
          description: Primitive list
          content:
            application/json:
              schema:
                type: object
                properties:
                  primitives:
                    type: array
                    items:
                      $ref: '#/components/schemas/PrimitiveInfo'
                  total: { type: integer }

components:
  schemas:
    PrimitiveInfo:
      type: object
      required: [name, category, source_system]
      properties:
        name:
          type: string
          example: "code.review_file"
        category:
          type: string
          example: "code"
        description:
          type: string
        source_system:
          type: string
          enum: [system, app]
          description: >
            "system" = built-in (fs.*, shell.*, state.*, etc.).
            "app" = registered by an application via app primitive protocol.
        app_id:
          type: string
          nullable: true
          description: Only present when source_system = "app"
        app_version:
          type: string
          nullable: true
        is_reversible:
          type: boolean
          nullable: true
        risk_level:
          type: string
          enum: [none, low, medium, high, critical]
          nullable: true
        side_effect:
          type: boolean
          nullable: true
        checkpoint_required:
          type: boolean
          nullable: true
        input_schema:
          type: object
          nullable: true
          description: >
            JSON Schema object for primitive input parameters.
            Omitted when include_schema=false.
        output_schema:
          type: object
          nullable: true
          description: >
            JSON Schema object for primitive output.
            Omitted when include_schema=false.
        route_status:
          type: string
          enum: [active, degraded, evicted]
          nullable: true
          description: Only present for app primitives; reflects AppRoute.Status
```

---

### Endpoint 4: `GET /api/v1/checkpoints/{sandbox_id}`

Returns checkpoints for a sandbox enriched with `CheckpointManifest` data from `03_cvr_loop.md`. The existing `/api/v1/sandboxes/{id}/checkpoints` proxies `state.list` inside the sandbox and returns only git commit metadata. This new endpoint joins the git history with the SQLite `checkpoint_manifests` table.

```yaml
paths:
  /api/v1/checkpoints/{sandbox_id}:
    get:
      operationId: listCheckpointsWithManifests
      summary: >
        List checkpoints for a sandbox, enriched with CheckpointManifest
        semantic metadata. Joins git checkpoint history with the
        checkpoint_manifests SQLite table.
      parameters:
        - name: sandbox_id
          in: path
          required: true
          schema: { type: string }
        - name: limit
          in: query
          schema: { type: integer, default: 20, maximum: 100 }
        - name: include_effect_log
          in: query
          schema: { type: boolean, default: false }
          description: >
            When true, includes the EffectLog array in each manifest.
            Omitted by default (can be verbose).
        - name: include_app_states
          in: query
          schema: { type: boolean, default: false }
          description: >
            When true, includes AppStateSnapshot[] in each manifest.
        - name: trace_id
          in: query
          schema: { type: string }
          description: >
            Filter to checkpoints taken within a specific trace.
      responses:
        "200":
          description: Checkpoint list with manifests
          content:
            application/json:
              schema:
                type: object
                properties:
                  sandbox_id: { type: string }
                  checkpoints:
                    type: array
                    items:
                      $ref: '#/components/schemas/CheckpointEntry'

  /api/v1/checkpoints/{sandbox_id}/{manifest_id}:
    get:
      operationId: getCheckpointManifest
      summary: Get a single CheckpointManifest by manifest_id
      parameters:
        - name: sandbox_id
          in: path
          required: true
          schema: { type: string }
        - name: manifest_id
          in: path
          required: true
          schema: { type: string }
        - name: include_effect_log
          in: query
          schema: { type: boolean, default: true }
        - name: include_app_states
          in: query
          schema: { type: boolean, default: true }
      responses:
        "200":
          description: Checkpoint manifest found
          content:
            application/json:
              schema: { $ref: '#/components/schemas/CheckpointManifest' }
        "404":
          description: Manifest not found

components:
  schemas:
    CheckpointEntry:
      type: object
      required: [commit_hash, timestamp]
      properties:
        commit_hash:    { type: string }
        timestamp:      { type: string, format: date-time }
        label:          { type: string }
        # Manifest data if the checkpoint was taken via CVRCoordinator
        manifest_id:    { type: string, nullable: true }
        checkpoint_reason: { type: string, nullable: true,
                             enum: [pre_write, pre_exec, manual, scheduled,
                                    pre_task, recovery_fallback] }
        trigger_primitive: { type: string, nullable: true }
        task_id:        { type: string, nullable: true }
        trace_id:       { type: string, nullable: true }
        step_id:        { type: string, nullable: true }
        files_modified: { type: integer, nullable: true }
        prev_manifest_id: { type: string, nullable: true }
        # Call stack at the time of checkpoint
        call_stack:
          type: array
          nullable: true
          items:
            $ref: '#/components/schemas/CallFrame'
        # Populated when include_effect_log=true
        effect_log:
          type: array
          nullable: true
          items:
            $ref: '#/components/schemas/EffectEntry'
        # Populated when include_app_states=true
        app_states:
          type: array
          nullable: true
          items:
            $ref: '#/components/schemas/AppStateSnapshot'

    CallFrame:
      type: object
      properties:
        primitive_id:   { type: string }
        span_id:        { type: string }
        invocation_index: { type: integer }
        input_summary:  { type: string }

    EffectEntry:
      type: object
      properties:
        effect_type:    { type: string, enum: [file_written, file_deleted,
                                                command_executed, db_mutated,
                                                network_called, app_state_mutated] }
        target:         { type: string }
        primitive_id:   { type: string }
        span_id:        { type: string }
        timestamp:      { type: string, format: date-time }
        reversible:     { type: boolean }

    AppStateSnapshot:
      type: object
      properties:
        app_id:         { type: string }
        state_key:      { type: string }
        state_value:    { type: object }
        captured_at:    { type: string, format: date-time }

    CheckpointManifest:
      type: object
      required: [manifest_id, commit_hash, sandbox_id, created_at]
      properties:
        manifest_id:        { type: string }
        commit_hash:        { type: string }
        sandbox_id:         { type: string }
        checkpoint_reason:  { type: string }
        trigger_primitive:  { type: string }
        task_id:            { type: string, nullable: true }
        trace_id:           { type: string, nullable: true }
        step_id:            { type: string, nullable: true }
        attempt:            { type: integer }
        workspace_root:     { type: string }
        files_modified_since_prev: { type: integer }
        prev_checkpoint_id: { type: string, nullable: true }
        corrupted:          { type: boolean }
        created_at:         { type: string, format: date-time }
        call_stack:         { type: array, items: { $ref: '#/components/schemas/CallFrame' } }
        effect_log:         { type: array, items: { $ref: '#/components/schemas/EffectEntry' } }
        app_states:         { type: array, items: { $ref: '#/components/schemas/AppStateSnapshot' } }
```

---

## 4.4 AI Debug Interface

### 4.4.1 Design Principles

The AI debug interface is designed for **machine consumption**, not human readability:
- All response fields have stable names and types (no prose summaries)
- Truncation is deterministic (max lengths are specified in schema)
- Recommended recovery paths are expressed as typed action codes, not advice strings
- The interface operates on a single failed step, not the entire task history

### 4.4.2 Endpoint

```yaml
paths:
  /api/v1/debug/step_failure:
    post:
      operationId: debugStepFailure
      summary: >
        Query structured debug information for a failed step.
        Designed for AI agent consumption. Returns machine-readable
        diagnosis without prose summaries or human-oriented formatting.
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/StepFailureQuery'
      responses:
        "200":
          description: Structured failure analysis
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/StepFailureReport'
        "404":
          description: Span or trace not found

components:
  schemas:
    StepFailureQuery:
      type: object
      required: [span_id]
      properties:
        span_id:
          type: string
          description: >
            The span_id of the failed step. Obtained from:
            - prim.failed event Data.span_id
            - cvr.verify_failed event Data.manifest_id → span lookup
            - X-PrimitiveBox-Span-ID response header
        trace_id:
          type: string
          description: >
            Optional. If provided, constrains the lookup to a specific trace.
            Useful when span_ids are non-unique across traces.
        include_checkpoint_diff:
          type: boolean
          default: false
          description: >
            When true, includes file-level diff between the last checkpoint
            and current workspace state. Can be large; use sparingly.
        max_output_bytes:
          type: integer
          default: 4096
          maximum: 65536
          description: >
            Maximum bytes for primitive output / test output fields.
            Content is truncated from the end if it exceeds this limit.
```

```yaml
    StepFailureReport:
      type: object
      description: >
        Complete structured diagnosis for a single failed step.
        All fields have stable names; null values indicate data not available.
      required: [span_id, diagnosis, recovery]
      properties:

        # ── Identity ──────────────────────────────────────────────────────
        span_id:       { type: string }
        trace_id:      { type: string }
        task_id:       { type: string, nullable: true }
        step_id:       { type: string, nullable: true }
        sandbox_id:    { type: string, nullable: true }
        timestamp:     { type: string, format: date-time }

        # ── Failed step info ──────────────────────────────────────────────
        failed_step:
          type: object
          required: [primitive_id, status, failure_kind]
          properties:
            primitive_id:
              type: string
              description: "e.g. fs.write, macro.safe_edit, code.review_diff"
            input:
              type: object
              nullable: true
              description: >
                Params passed to the primitive.
                Sensitive keys (passwords, tokens) are redacted.
            output:
              type: string
              nullable: true
              description: >
                Primitive output truncated to max_output_bytes.
                For shell-based primitives, this is stdout+stderr combined.
            status:
              type: string
              enum: [failed, unknown]
            failure_kind:
              type: string
              enum: [environment, test_failure, syntax_error, timeout,
                     duplicate_retry, app_unavailable, verify_timeout, unknown]
            error_code:
              type: string
              nullable: true
              description: "PrimitiveError.Code if available"
            duration_ms:
              type: integer
            attempt_number:
              type: integer
              description: "Which attempt this was (1-indexed)"
            max_attempts:
              type: integer

        # ── Verify details (present when a verify strategy ran) ───────────
        verify_details:
          type: object
          nullable: true
          properties:
            strategy_type:
              type: string
              enum: [exit_code, test_suite, schema_check, ai_judge, composite]
            outcome:
              type: string
              enum: [passed, failed, skipped, timeout, error]
            test_command:
              type: string
              nullable: true
            tests_passed:
              type: integer
              nullable: true
            tests_failed:
              type: integer
              nullable: true
            tests_total:
              type: integer
              nullable: true
            # Individual test failures (max 20 entries)
            failed_tests:
              type: array
              nullable: true
              maxItems: 20
              items:
                type: object
                properties:
                  test_name:    { type: string }
                  error_output: { type: string, maxLength: 512 }
            # Schema violation details (schema_check strategy)
            schema_violations:
              type: array
              nullable: true
              maxItems: 10
              items:
                type: object
                properties:
                  check_kind: { type: string,
                                enum: [json_schema, syntax, type_check,
                                       import_resolution, structure] }
                  message:    { type: string, maxLength: 256 }
                  location:   { type: string, nullable: true,
                                description: "file:line or symbol path" }

        # ── Checkpoint context ────────────────────────────────────────────
        checkpoint:
          type: object
          nullable: true
          description: "Present when a checkpoint exists that can be used for recovery"
          properties:
            manifest_id:
              type: string
            commit_hash:
              type: string
            checkpoint_reason:
              type: string
            timestamp:
              type: string
              format: date-time
            files_in_checkpoint:
              type: integer
            diff_from_checkpoint:
              type: string
              nullable: true
              description: >
                Git diff between checkpoint and current workspace.
                Only present when include_checkpoint_diff=true.
                Truncated to max_output_bytes.

        # ── Diagnosis ─────────────────────────────────────────────────────
        diagnosis:
          type: object
          required: [failure_category, confidence, signals]
          properties:
            failure_category:
              type: string
              enum:
                - write_error          # fs.write or similar failed
                - test_regression      # verify.test found test failures
                - syntax_error         # code has syntax/compilation errors
                - dependency_missing   # env dependency not available
                - timeout              # operation exceeded deadline
                - verify_inconclusive  # verify timed out; outcome unknown
                - app_unavailable      # app primitive server not responding
                - duplicate_attempt    # identical retry detected (loop guard)
                - unknown_error        # cannot classify
            confidence:
              type: string
              enum: [high, medium, low]
              description: >
                "high" = deterministic signal (exit code, exception).
                "medium" = heuristic (pattern match on output).
                "low" = no clear signal.
            # Structured signals that led to this diagnosis
            signals:
              type: array
              minItems: 0
              maxItems: 10
              items:
                type: object
                required: [signal_type, value]
                properties:
                  signal_type:
                    type: string
                    enum:
                      - exit_code
                      - output_pattern
                      - test_count_mismatch
                      - schema_violation
                      - timeout_ms
                      - no_checkpoint_available
                      - app_health_status
                      - retry_count_exceeded
                      - duplicate_input_hash
                  value:
                    description: "Type depends on signal_type"
                    oneOf:
                      - type: string
                      - type: integer
                      - type: boolean

        # ── Recovery recommendations ──────────────────────────────────────
        recovery:
          type: object
          required: [recommended_action, alternatives, can_auto_recover]
          properties:
            recommended_action:
              type: string
              enum: [retry, rollback, fallback_earlier, rewrite, escalate, mark_unknown]
            action_rationale_code:
              type: string
              description: >
                Machine-readable reason code for the recommended action.
                Examples: "has_checkpoint_and_test_failure",
                          "environment_error_no_checkpoint",
                          "verify_timeout_preserve_state"
            can_auto_recover:
              type: boolean
              description: >
                True if CVRCoordinator can execute the recommended action
                without human input (rollback, retry within limits).
                False if human intervention is required (escalate, rewrite).
            rollback_target:
              type: object
              nullable: true
              description: >
                Present when recommended_action = rollback or fallback_earlier.
              properties:
                manifest_id:   { type: string }
                commit_hash:   { type: string }
                timestamp:     { type: string, format: date-time }
                depth:         { type: integer,
                                 description: "0=parent, 1=grandparent, etc." }
            # Alternative actions in priority order (max 3)
            alternatives:
              type: array
              maxItems: 3
              items:
                type: object
                properties:
                  action:      { type: string }
                  rationale_code: { type: string }
                  preconditions: { type: array, items: { type: string } }

        # ── Execution context (preceding steps in the same trace) ─────────
        execution_context:
          type: object
          nullable: true
          description: >
            Lightweight summary of preceding successful spans in the same trace.
            Allows AI to understand what was accomplished before the failure.
          properties:
            preceding_passed_count:
              type: integer
            preceding_failed_count:
              type: integer
            # Last N successful spans (max 5)
            last_passed_spans:
              type: array
              maxItems: 5
              items:
                type: object
                properties:
                  span_id:      { type: string }
                  primitive_id: { type: string }
                  duration_ms:  { type: integer }
            # Primitives that modified workspace files before this failure
            files_modified_by_preceding:
              type: array
              maxItems: 20
              items:
                type: string
              description: "File paths touched by preceding prim.* spans"
```

---

### 4.4.3 Request / Response Example

**Query (AI agent POSTs after receiving `prim.failed` event):**

```json
{
  "span_id": "a1b2c3d4e5f60001",
  "trace_id": "a3f8b2c1d4e5f6a7b8c9d0e1f2a3b4c5",
  "include_checkpoint_diff": false,
  "max_output_bytes": 8192
}
```

**Response (machine-readable, structured, no prose):**

```json
{
  "span_id": "a1b2c3d4e5f60001",
  "trace_id": "a3f8b2c1d4e5f6a7b8c9d0e1f2a3b4c5",
  "task_id": "task-7f3a8b2c",
  "step_id": "step-00000003",
  "sandbox_id": "sb-dev-001",
  "timestamp": "2026-03-16T12:34:56.789Z",

  "failed_step": {
    "primitive_id": "macro.safe_edit",
    "input": {
      "path": "src/auth/handler.go",
      "mode": "search_replace",
      "search": "func validateToken(",
      "replace": "func validateToken(ctx context.Context,",
      "test_command": "go test ./internal/auth/... -run TestValidateToken"
    },
    "output": "--- FAIL: TestValidateToken (0.02s)\n    handler_test.go:47: expected 3 args, got 2\nFAIL\nFAIL\tprimitivebox/internal/auth\t0.031s\n",
    "status": "failed",
    "failure_kind": "test_failure",
    "error_code": null,
    "duration_ms": 4821,
    "attempt_number": 1,
    "max_attempts": 3
  },

  "verify_details": {
    "strategy_type": "test_suite",
    "outcome": "failed",
    "test_command": "go test ./internal/auth/... -run TestValidateToken",
    "tests_passed": 0,
    "tests_failed": 1,
    "tests_total": 1,
    "failed_tests": [
      {
        "test_name": "TestValidateToken",
        "error_output": "handler_test.go:47: expected 3 args, got 2"
      }
    ],
    "schema_violations": null
  },

  "checkpoint": {
    "manifest_id": "mfst-8f2a1b3c",
    "commit_hash": "c8f4a1b2d3e5f6a7",
    "checkpoint_reason": "pre_exec",
    "timestamp": "2026-03-16T12:34:51.000Z",
    "files_in_checkpoint": 312,
    "diff_from_checkpoint": null
  },

  "diagnosis": {
    "failure_category": "test_regression",
    "confidence": "high",
    "signals": [
      {
        "signal_type": "exit_code",
        "value": 1
      },
      {
        "signal_type": "test_count_mismatch",
        "value": 1
      },
      {
        "signal_type": "output_pattern",
        "value": "expected 3 args, got 2"
      }
    ]
  },

  "recovery": {
    "recommended_action": "rollback",
    "action_rationale_code": "has_checkpoint_and_test_failure",
    "can_auto_recover": true,
    "rollback_target": {
      "manifest_id": "mfst-8f2a1b3c",
      "commit_hash": "c8f4a1b2d3e5f6a7",
      "timestamp": "2026-03-16T12:34:51.000Z",
      "depth": 0
    },
    "alternatives": [
      {
        "action": "retry",
        "rationale_code": "test_failure_may_be_transient",
        "preconditions": ["attempt_number < max_attempts"]
      },
      {
        "action": "rewrite",
        "rationale_code": "search_replace_may_need_correction",
        "preconditions": ["can_rollback_first"]
      }
    ]
  },

  "execution_context": {
    "preceding_passed_count": 2,
    "preceding_failed_count": 0,
    "last_passed_spans": [
      {
        "span_id": "a1b2c3d4e5f60000",
        "primitive_id": "fs.read",
        "duration_ms": 12
      }
    ],
    "files_modified_by_preceding": []
  }
}
```

---

## 5. Integration Points Summary

### 5.1 `internal/eventing/eventing.go`

Add five fields to `Event` (trace_id, span_id, parent_span_id, primitive_id, task_id, step_id) and extend `ListFilter`. No existing field removed.

### 5.2 `internal/runtrace/runtrace.go`

Add three HTTP header constants (`HeaderTraceID`, `HeaderSpanID`, `HeaderParentSpanID`).

Extend `StepRecord` with `SpanID`, `ParentSpanID` fields for the header-propagation path.

Add `TraceSpan` and `ExecutionTrace` types.

Define `TraceStore` interface extending `Store`.

### 5.3 `internal/control/sqlite_store.go`

Six `ALTER TABLE events ADD COLUMN` migrations (guarded by `IF NOT EXISTS` equivalent — check column existence before altering).

New `CREATE TABLE trace_spans` DDL with four indexes.

Implement `TraceStore` interface methods: `GetTrace`, `ListTraces`, `AppendSpan`.

`GetTrace` implementation: JOIN `trace_spans` with `events` on `(trace_id, span_id)`, build tree in Go by iterating flat list and linking via `parent_span_id`.

### 5.4 `internal/rpc/server.go`

In `handleRPCRequest`: extract or generate `trace_id`/`span_id`/`parent_span_id` from request headers. Set them on context. Emit `prim.started` (with correlation fields) at call start and `prim.completed`/`prim.failed` at call end, in addition to (or replacing) `rpc.started`/`rpc.completed` when trace context is present.

New route registrations in `Handler()`:
```
GET /api/v1/traces          → handleAPITraces
GET /api/v1/traces/{id}     → handleAPITraceDetail
GET /api/v1/traces/{id}/replay → handleAPIReplayMeta
GET /api/v1/traces/{id}/replay/stream → handleAPIReplayStream (SSE)
POST /api/v1/traces/{id}/replay/execute → handleAPIReplayExecute
GET /api/v1/primitives      → handleAPIAllPrimitives
GET /api/v1/checkpoints/{sandbox_id} → handleAPICheckpoints
GET /api/v1/checkpoints/{sandbox_id}/{manifest_id} → handleAPICheckpointManifest
POST /api/v1/debug/step_failure → handleAPIDebugStepFailure
```

### 5.5 `internal/cvr/coordinator.go` (proposed in `03_cvr_loop.md`)

Emit all 13 `cvr.*` events with full correlation (trace_id, span_id, task_id, step_id, manifest_id).

At span completion (success or failure), call `TraceStore.AppendSpan()` to persist the `TraceSpan` record.

### 5.6 `internal/sandbox/manager.go` and `router.go`

Emit `sandbox.*` lifecycle events on every state transition.

Emit `app.*` events on registration, deregistration, health changes, and primitive dispatch.

### 5.7 Package Placement

```
internal/
  eventing/
    eventing.go          -- extended Event struct, ListFilter (existing)
  runtrace/
    runtrace.go          -- extended StepRecord, new header constants (existing)
    trace.go             -- NEW: TraceSpan, ExecutionTrace, TraceStore interface
  control/
    sqlite_store.go      -- extended: trace_spans table, TraceStore impl (existing)
```

No new top-level packages are required. `trace.go` is the only new file.

---

## 6. Backward Compatibility

| Concern | Impact | Mitigation |
|---|---|---|
| `Event` struct gains 6 new fields | Additive; existing JSON consumers ignore unknown keys | `omitempty` on all new fields |
| `events` SQLite table gains 6 new columns | Old binaries reading existing DB see NULLs for new columns | `ALTER TABLE` with default NULL; existing queries unaffected |
| New `trace_spans` table | Old binaries ignore unknown tables | `CREATE TABLE IF NOT EXISTS` |
| Existing `/api/v1/events` filter | `ListFilter` adds 4 new fields | All new fields have zero-value default (= no filter) |
| `rpc.started`/`rpc.completed` events | Not removed; kept for streams without trace context | `prim.*` emitted additionally when trace_id present |
| `/primitives` endpoint | Unchanged; `/api/v1/primitives` is a new path | No overlap |
| `/api/v1/sandboxes/{id}/checkpoints` | Unchanged; `/api/v1/checkpoints/{id}` is a new path | No conflict |
| Python SDK `SyncClient`/`AsyncClient` | No SDK-level changes required for this doc | SDK uses `/rpc`; new endpoints are Inspector-only |

---

## 7. Complete Event Type Reference

For quick lookup, all event type constants grouped by namespace:

| Constant | Value | Emitting Component |
|---|---|---|
| `EventSandboxCreating` | `sandbox.creating` | sandbox/manager |
| `EventSandboxRunning` | `sandbox.running` | sandbox/manager |
| `EventSandboxStopped` | `sandbox.stopped` | sandbox/manager |
| `EventSandboxFailed` | `sandbox.failed` | sandbox/manager |
| `EventSandboxDestroyed` | `sandbox.destroyed` | sandbox/manager |
| `EventSandboxDegraded` | `sandbox.health_degraded` | sandbox/manager |
| `EventSandboxRecovered` | `sandbox.health_recovered` | sandbox/manager |
| `EventPrimStarted` | `prim.started` | rpc/server |
| `EventPrimProgress` | `prim.progress` | rpc/server (via sink) |
| `EventPrimCompleted` | `prim.completed` | rpc/server |
| `EventPrimFailed` | `prim.failed` | rpc/server |
| `EventPrimTimedOut` | `prim.timed_out` | rpc/server |
| `EventShellStarted` | `shell.started` | primitive/shell |
| `EventShellOutput` | `shell.output` | primitive/shell |
| `EventShellCompleted` | `shell.completed` | primitive/shell |
| `EventRPCStarted` | `rpc.started` | rpc/server (legacy) |
| `EventRPCCompleted` | `rpc.completed` | rpc/server (legacy) |
| `EventRPCError` | `rpc.error` | rpc/server (legacy) |
| `EventCVRCheckpointTaken` | `cvr.checkpoint_taken` | cvr/coordinator |
| `EventCVRCheckpointFailed` | `cvr.checkpoint_failed` | cvr/coordinator |
| `EventCVRVerifyStarted` | `cvr.verify_started` | cvr/coordinator |
| `EventCVRVerifyPassed` | `cvr.verify_passed` | cvr/coordinator |
| `EventCVRVerifyFailed` | `cvr.verify_failed` | cvr/coordinator |
| `EventCVRVerifyTimeout` | `cvr.verify_timeout` | cvr/coordinator |
| `EventCVRRecoverTriggered` | `cvr.recover_triggered` | cvr/coordinator |
| `EventCVRRecoverCompleted` | `cvr.recover_completed` | cvr/coordinator |
| `EventCVRRecoverFailed` | `cvr.recover_failed` | cvr/coordinator |
| `EventCVRRolledBack` | `cvr.rolled_back` | cvr/coordinator |
| `EventCVRMarkUnknown` | `cvr.mark_unknown` | cvr/coordinator |
| `EventCVREscalated` | `cvr.escalated_to_human` | cvr/coordinator |
| `EventCVRLoopCompleted` | `cvr.loop_completed` | cvr/coordinator |
| `EventAppRegistered` | `app.registered` | sandbox/router |
| `EventAppDeregistered` | `app.deregistered` | sandbox/router |
| `EventAppHealthOK` | `app.health_ok` | sandbox/router |
| `EventAppHealthFail` | `app.health_fail` | sandbox/router |
| `EventAppPrimCalled` | `app.prim_called` | sandbox/router |
| `EventAppPrimCompleted` | `app.prim_completed` | sandbox/router |
| `EventAppPrimFailed` | `app.prim_failed` | sandbox/router |
| `EventAppPrimUpdated` | `app.prim_updated` | sandbox/router |

**Total: 39 event types** across 4 namespaces (7 sandbox + 8 prim/shell/rpc + 13 cvr + 8 app + 3 legacy rpc).
