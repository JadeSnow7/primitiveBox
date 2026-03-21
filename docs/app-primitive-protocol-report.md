# Application Primitive Protocol Validation Report

## Executive Summary

This validation used a real dynamic adapter at `examples/kv_adapter/` plus a sequential integration suite in `examples/kv_adapter/kv_adapter_test.go`.

The biggest protocol drifts from the architectural direction are:

1. Current dynamic registration is **HTTP JSON-RPC with `X-PB-Origin: sandbox`**, not Unix-socket registration.
2. Dispatch uses **per-call Unix socket request/response** in [`internal/sandbox/router.go`](/Users/huaodong/TMP/primitivebox/internal/sandbox/router.go).
3. `verify_endpoint` and `rollback_endpoint` exist on `AppPrimitiveManifest`, but the runtime does **not** use them for CVR or dispatch policy.
4. Dynamic app primitives are discoverable through **`/app-primitives`**, not `/primitives`.
5. CVR is **orchestrator-only** today. Normal HTTP `/rpc` calls do not run the CVR coordinator.

## Test Results

| Test ID | Result | Notes | Source |
|---------|--------|-------|--------|
| TEST-REG-01 | PARTIAL | All 9 `kv.*` primitives register successfully and appear in `/app-primitives`, but `/primitives` omits them, so `pb primitive list` will not show dynamic app primitives today. | Internal harness |
| TEST-REG-02 | PARTIAL | `intent.category`, `risk_level`, `reversible`, and `affected_scopes` round-trip correctly. `side_effect` and `checkpoint_required` are not part of the current dynamic manifest. | Internal harness + code inspection |
| TEST-REG-03 | PARTIAL | Raw JSON Schema for optional fields, enums, nested objects, and typed arrays round-trips correctly via `/app-primitives`. The same schemas are not exposed from `/primitives`. | Internal harness |
| TEST-REG-04 | PASS | Re-registering with the same `app_id` updates the manifest in place, updates the socket path/schema, and did not interrupt an in-flight call. | Internal harness |
| TEST-REG-05 | PASS | Cross-app conflict detection is by fully-qualified primitive name. Re-registering `kv.get` from a different `app_id` is rejected with `app_primitive_conflict` including the primitive name and original app id. | Internal harness |
| TEST-REG-06 | PARTIAL | After adapter crash, calls fail quickly with a transport error instead of hanging. The runtime does not auto-deregister the dead adapter and has no heartbeat/eviction path in the current implementation. | Internal harness + code inspection |
| TEST-REG-07 | PASS | Two adapters can register the same namespace with different primitive names, for example `kv.get` and `kv.watch`. Namespace ownership is not enforced. | Internal harness |
| TEST-REG-08 | PARTIAL | Current manifest can express description, schemas, intent, `verify_endpoint`, and `rollback_endpoint`. Versioning, capabilities, timeout override, dependencies, rate limiting, and deprecation are not expressible. | Code inspection |
| TEST-DISP-01 | PASS | `kv.set` and `kv.get` dispatch successfully through sandbox-local `/rpc` and host `/sandboxes/{id}/rpc`. Structured results match the registered output shapes. | Internal harness |
| TEST-DISP-02 | FAIL | Adapter error codes are lost. The runtime wraps missing-key as `app_primitive_error: ...` and returns a generic JSON-RPC internal error to the client. | Internal harness |
| TEST-DISP-03 | PARTIAL | Caller deadline/default socket deadline is enforced. Per-primitive timeout from the dynamic manifest is not expressible and therefore not enforced. | Internal harness + code inspection |
| TEST-DISP-04 | PASS | `kv.set` accepted a 10 MB value and `kv.export` completed after importing 10K entries. No streaming or chunking exists, but the current request/response path handled the payloads. | Internal harness |
| TEST-DISP-05 | PASS | 50 concurrent `kv.set` calls completed successfully. Code inspection confirms the router dials a new Unix socket connection per call. | Internal harness + code inspection |
| TEST-DISP-06 | PARTIAL | Host `/sandboxes/{id}/rpc` correctly reaches the sandbox-local adapter in the in-process proxy lane. A real Docker smoke test remains manual and was not executed by the automated suite. | Internal harness |
| TEST-DISP-07 | FAIL | App primitive dispatch is request/response only. `/rpc/stream` only emits gateway SSE frames; adapter-originated multi-line responses do not become stream events. | Internal harness |
| TEST-CVR-01 | PARTIAL | Orchestrator CVR auto-checkpoints `kv.batch_set` because intent is mutation + reversible + high risk. Normal `/rpc` calls do not invoke CVR. `kv.set` cannot express `checkpoint_required` in the current manifest, so it is not auto-checkpointed by CVR. | Internal harness + code inspection |
| TEST-CVR-02 | PARTIAL | For `kv.delete`, the coordinator treats the manifest intent as irreversible/high-risk and selects rollback on verify failure. There is no confirmation/escalation policy on the HTTP `/rpc` path. | Internal harness |
| TEST-CVR-03 | FAIL | `verify_endpoint` is preserved in the manifest but unused. The runtime does not automatically invoke `kv.get` after `kv.set`. | Internal harness + code inspection |
| TEST-CVR-04 | FAIL | `state.restore` restores filesystem state only. Adapter-managed KV state survives restore unchanged. | Internal harness |
| TEST-CVR-05 | FAIL | `macro.safe_edit` is hardcoded around `fs.write` and `verify.test`. There is no generic `macro.safe_call` for app primitives. | Internal harness + code inspection |
| TEST-CVR-06 | PARTIAL | Current CVR distinguishes timeout/error/verify-fail at the coordinator level and applies retry/rollback decisions from the decision tree. The adapter cannot supply structured recovery policy beyond generic success/failure because `verify_endpoint`/`rollback_endpoint` are unused. | Internal harness + code inspection |
| TEST-CVR-07 | FAIL | App primitive calls emit `rpc.started`/`rpc.completed` plus `started`/`completed` SSE frames, but not `prim.*`, `cvr.*`, or app-specific observability events. | Internal harness |

## Protocol Gaps Identified

### GAP-01: State Rollback Boundary

- Severity: blocking
- Current behavior: `state.restore` only restores workspace files. Adapter state is out of band and survives restore.
- Expected behavior: app primitive state that participates in CVR should have an explicit snapshot/restore contract.
- Proposed fix: add adapter-level snapshot/restore hooks that the checkpoint flow can call, and persist app-state references in the checkpoint manifest.
- Impact on package manager: stateful packages like databases cannot safely advertise rollback semantics without this.

### GAP-02: Verify Strategy Expressiveness

- Severity: blocking
- Current behavior: `verify_endpoint` exists but is never executed and cannot declare argument mapping or success criteria.
- Expected behavior: an adapter should be able to declare "after primitive X, call primitive Y with derived args Z and treat condition C as success."
- Proposed fix: add a structured `verify` block with primitive name, param templates, and success predicates.
- Impact on package manager: post-install verification for non-filesystem resources cannot be described.

### GAP-03: Dependency Resolution

- Severity: significant
- Current behavior: no manifest field can declare primitive/package dependencies.
- Expected behavior: adapters should be able to declare dependencies on system primitives and other packages.
- Proposed fix: add a `dependencies` array with primitive/package ids and version constraints.
- Impact on package manager: `pb install` cannot resolve install order or compatibility.

### GAP-04: Lifecycle Hooks

- Severity: significant
- Current behavior: no setup/teardown/init contract exists for dynamic adapters.
- Expected behavior: adapters should be able to declare startup initialization, shutdown cleanup, and health/heartbeat behavior.
- Proposed fix: add lifecycle hooks and heartbeat/health metadata to the manifest.
- Impact on package manager: packages must self-bootstrap implicitly on first call, which is fragile and hard to observe.

### GAP-05: Capability Negotiation

- Severity: significant
- Current behavior: adapters cannot declare required runtime capabilities or query them during registration.
- Expected behavior: registration should fail fast when runtime capabilities do not satisfy the adapter contract.
- Proposed fix: add `required_capabilities` and runtime version/capability negotiation on registration.
- Impact on package manager: install-time compatibility checks are impossible.

### GAP-06: Versioning and Compatibility

- Severity: significant
- Current behavior: dynamic `AppPrimitiveManifest` has no runtime version floor, manifest version, or primitive schema version.
- Expected behavior: both adapter and runtime should negotiate manifest protocol version and minimum runtime compatibility.
- Proposed fix: add `manifest_version`, `app_version`, `min_runtime_version`, and per-primitive `schema_version`.
- Impact on package manager: upgrades and backward compatibility become guesswork.

### GAP-07: Observability Contract

- Severity: significant
- Current behavior: adapters cannot emit custom events into SSE/event store, and app primitive calls do not emit `prim.*` or `app.*` events.
- Expected behavior: adapters should be able to publish structured progress and side-effect events through the shared event model.
- Proposed fix: add adapter event emission APIs and standard app-primitive event types.
- Impact on package manager: install/debug flows will be opaque and hard to replay.

### GAP-08: Security Boundary

- Severity: significant
- Current behavior: built-in primitives win routing precedence, so dynamic adapters cannot override system execution, but there is no policy that restricts what names an adapter is allowed to register.
- Expected behavior: registration should be scoped by namespace ownership and policy.
- Proposed fix: add namespace allowlists and registration policy checks at the control plane.
- Impact on package manager: without namespace ownership, multi-package ecosystems will have naming collisions and weak trust boundaries.

## Recommendations

### Must Fix Before Phase 3 (application primitives)

- Add a first-class dynamic manifest schema version and runtime compatibility contract.
- Decide how checkpoint/restore spans adapter-managed state, not just files.
- Add structured verify semantics instead of the current unused `verify_endpoint` string.
- Expose dynamic app primitives in a unified discovery surface, or clearly version the split between `/primitives` and `/app-primitives`.
- Define the error contract for app primitives so adapter error codes/messages survive routing.

### Must Fix Before Phase 4 (package manager)

- Add dependency declaration and resolution fields.
- Add capability negotiation and install-time validation.
- Add lifecycle hooks and liveness management for adapter crash/eviction.
- Add namespace ownership/policy for secure multi-package registration.
- Add structured observability and adapter-emitted events.

### Can Defer To V2

- Fine-grained rate limiting and concurrency policy.
- Deprecation metadata and replacement hints.
- Richer route health accounting beyond basic crash detection.

## Manual Docker Smoke

The automated suite validated sandbox-local registration and host proxy behavior in-process. A real Docker smoke remains manual:

```bash
make build
./bin/pb server start --workspace /tmp/kv-workspace

# In a sandbox-local pb runtime:
go build -o /tmp/kv-adapter ./examples/kv_adapter
/tmp/kv-adapter \
  --socket /tmp/pb-kv.sock \
  --manifest ./examples/kv_adapter/manifest.json \
  --rpc-endpoint http://127.0.0.1:8080
```

Then verify:

- `curl http://127.0.0.1:8080/app-primitives`
- `curl -X POST http://127.0.0.1:8080/rpc ...`
- `curl -X POST http://<host-gateway>/sandboxes/<id>/rpc ...`

This implementation does not claim that a real Docker smoke was executed automatically.
