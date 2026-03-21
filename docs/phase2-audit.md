## Namespace Isolation
Current state: `internal/sandbox/router.go` resolves built-in primitives first, so an app cannot override an existing system primitive at dispatch time. `internal/primitive/app_manifest.go` only rejects exact-name conflicts across different `app_id` values. `sdk/python/primitivebox/app.py` prefixes decorator registrations as `<app_id>.<name>`, but raw manifests and the Go adapter examples can still register flat names such as `kv.set`.

Gap: there is no explicit reserved system namespace check (`fs.*`, `state.*`, `verify.*`, etc.), no requirement that app primitives use an app-scoped namespace, and no policy for two apps claiming the same namespace family. Today "first registered exact name wins" is the only protection.

Implementation note: add registration-time validation in one place, ideally the `app.register` path before `AppPrimitiveRegistry.Register`, that rejects reserved prefixes and requires a non-system namespace segment. Keep built-in precedence in the router, but make invalid app namespaces impossible to register.

## Schema Validation at Registration
Current state: `internal/rpc/server.go` normalizes `input_schema` and `output_schema` with `normalizeManifestSchema`, accepts either raw JSON or stringified JSON, and only checks that the payload is valid JSON. `sdk/python/primitivebox/app.py` currently sends schemas as JSON strings.

Gap: registration does not enforce that schemas are objects, does not require an object-shaped root, does not validate expected JSON Schema structure, and does not reject obviously unusable contracts before runtime dispatch.

Implementation note: introduce a schema validator on the `app.register` path that canonicalizes both string and object input into raw JSON objects, then enforces a minimal JSON Schema contract such as top-level object, valid `type`, and sane `properties`/`required` structure. Preserve current wire compatibility by continuing to accept stringified schemas, but normalize them immediately.

## Verify Strategy Field
Current state: `primitive.AppPrimitiveManifest` already has `VerifyEndpoint`, and `examples/kv_adapter/manifest.json` uses it. However `internal/orchestrator/engine.go` only reads `manifest.Intent`, and `examples/kv_adapter/kv_adapter_test.go` explicitly asserts that `verify_endpoint` is ignored by the current CVR path.

Gap: Phase 2 needs a real verification contract, not just an unused field. There is no way today for an app to declare "verify by primitive", "verify by command", or "caller-owned verification" in a way the coordinator actually executes.

Implementation note: replace or extend `VerifyEndpoint` with an explicit verify declaration that can encode strategy and parameters. The minimal compatible path is to treat existing `verify_endpoint` as shorthand for `strategy=primitive`, then teach the CVR coordinator or orchestrator to execute that verification step after the main app primitive succeeds and before the step is marked complete.

## Rollback Primitive Declaration
Current state: `primitive.AppPrimitiveManifest` also already has `RollbackEndpoint`, and adapter tests verify that the field round-trips through registration. Recovery logic in `internal/orchestrator/engine.go` still hard-codes `state.restore`, and `examples/kv_adapter/kv_adapter_test.go` confirms that adapter-managed state survives `state.restore`.

Gap: external app state is not actually recoverable through the current protocol. The manifest can describe a rollback endpoint, but the runtime never consults it, so app-level mutations remain outside the real CVR recovery path.

Implementation note: formalize rollback as part of the app primitive contract and make recovery prefer the declared app rollback primitive when present. For reversible mixed-scope operations, the minimal behavior is `app rollback` first and optional `state.restore` second; for irreversible app primitives with no rollback declaration, fail closed and escalate instead of pretending filesystem restore is sufficient.

## Lifecycle Management (crash / reconnect)
Current state: app registrations live in the in-memory registry in `internal/primitive/app_manifest.go`. `internal/sandbox/router.go` dials the Unix socket on every call and only discovers failure at request time. A dead socket leaves the manifest registered, and reconnect is only implicit via re-registering the same name. `sdk/python/primitivebox/app.py` serves requests on a background thread but has no lease, heartbeat, unregister, or crash reporting.

Gap: the control plane cannot distinguish "registered", "healthy", and "stale". Adapter crashes are fail-late, stale registrations remain visible, reconnect semantics are undocumented, and no lifecycle events are emitted for availability transitions.

Implementation note: add explicit adapter lifecycle state with at least `registered`, `unavailable`, and `active` semantics. The minimal implementation is to mark a manifest unavailable on dial failure, emit an event, and let a same-name/same-app re-registration reactivate it. A stronger follow-up is a lease or heartbeat tied to the registration so stale entries age out without waiting for the next RPC call.

## Recommended Implementation Order
1. Namespace isolation on registration. This is the cheapest contract hardening step and prevents Phase 2 from baking in ambiguous names.
2. Schema validation at registration. Once names are controlled, reject weak manifests early so later verify/rollback logic can trust the contracts it receives.
3. Verify strategy declaration and execution. Verification is the core Phase 2 semantic gap, and existing `VerifyEndpoint` gives a backwards-compatible bridge.
4. Rollback primitive declaration and recovery wiring. This should follow verify because rollback policy depends on whether the runtime can first detect failure correctly.
5. Lifecycle management for crash and reconnect. This is important, but it is safer to layer on after registration, verify, and rollback semantics are explicit, otherwise the system would just monitor stale contracts more accurately without improving recovery behavior.
