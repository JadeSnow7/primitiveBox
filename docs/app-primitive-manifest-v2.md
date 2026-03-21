# App Primitive Manifest V2 Proposal

## Purpose

This proposal extends the current dynamic `AppPrimitiveManifest` so the protocol can support stateful adapters, package installation, CVR integration, capability negotiation, and observability.

## Current V1 Surface

Current dynamic registration is effectively one manifest per primitive:

```json
{
  "app_id": "pb-kv-adapter",
  "name": "kv.set",
  "description": "Create or update one key.",
  "input_schema": {},
  "output_schema": {},
  "socket_path": "/tmp/pb-kv.sock",
  "verify_endpoint": "kv.get",
  "rollback_endpoint": "state.restore",
  "intent": {
    "category": "mutation",
    "reversible": true,
    "risk_level": "medium",
    "affected_scopes": ["app:kv"]
  }
}
```

V1 can express:

- `app_id`
- `name`
- `description`
- `input_schema`
- `output_schema`
- `socket_path`
- `verify_endpoint`
- `rollback_endpoint`
- `intent`

V1 cannot express:

- manifest/protocol version
- adapter version / minimum PrimitiveBox version
- per-primitive timeout
- checkpoint policy
- structured verify policy
- structured rollback policy
- dependencies
- lifecycle hooks
- capability requirements
- deprecation metadata
- observability/event contracts
- registration security policy

## Proposed V2 Shape

V2 should register one adapter manifest with shared metadata plus a `primitives` array.

```json
{
  "manifest_version": "2.0",
  "protocol_version": "2.0",
  "app": {
    "id": "pb-kv-adapter",
    "name": "PB KV Adapter",
    "version": "0.1.0",
    "namespace": "kv",
    "description": "Validation adapter for application primitive protocol testing",
    "min_runtime_version": "0.2.0",
    "maintainer": "PrimitiveBox"
  },
  "transport": {
    "type": "unix",
    "socket_path": "/tmp/pb-kv.sock",
    "health": {
      "kind": "rpc",
      "method": "kv.verify",
      "interval_ms": 15000,
      "failure_threshold": 3
    }
  },
  "runtime_requirements": {
    "required_capabilities": ["checkpointing", "event_stream"],
    "optional_capabilities": ["network", "volume_mounts"]
  },
  "security": {
    "allowed_namespaces": ["kv"],
    "allow_system_namespace": false
  },
  "lifecycle": {
    "setup": {
      "method": "kv.adapter_setup",
      "required": false
    },
    "teardown": {
      "method": "kv.adapter_teardown",
      "required": false
    },
    "state_snapshot": {
      "method": "kv.adapter_snapshot",
      "required": false
    },
    "state_restore": {
      "method": "kv.adapter_restore",
      "required": false
    }
  },
  "primitives": [
    {
      "name": "set",
      "full_name": "kv.set",
      "schema_version": "1.0",
      "description": "Create or update one key.",
      "input_schema": {},
      "output_schema": {},
      "side_effect": "write",
      "intent": {
        "category": "mutation",
        "risk_level": "medium",
        "reversible": true,
        "affected_scopes": ["app:kv"]
      },
      "timeout_ms": 30000,
      "checkpoint": {
        "required": true,
        "mode": "before_call",
        "state_scopes": ["filesystem", "adapter"]
      },
      "verify": {
        "mode": "primitive_call",
        "method": "kv.get",
        "args_template": {
          "key": "$params.key"
        },
        "success_when": {
          "jsonpath": "$.value",
          "equals_template": "$params.value"
        }
      },
      "rollback": {
        "mode": "checkpoint_restore"
      },
      "dependencies": [
        {
          "kind": "primitive",
          "id": "state.checkpoint",
          "required": true
        }
      ],
      "concurrency": {
        "max_in_flight": 50,
        "keyed_by": "$params.key"
      },
      "rate_limit": {
        "requests_per_second": 20
      },
      "events": {
        "allowed_prefixes": ["kv."],
        "emit_progress": true
      },
      "deprecation": {
        "deprecated": false
      }
    }
  ]
}
```

## V2 Field Rationale

### Top-Level Metadata

- `manifest_version`: version the registration document itself.
- `protocol_version`: version the adapter/runtime handshake.
- `app.version` and `app.min_runtime_version`: make compatibility explicit.
- `runtime_requirements.required_capabilities`: allow install-time validation instead of call-time failure.

### Security

- `security.allowed_namespaces`: prevent unbounded name registration.
- `allow_system_namespace=false`: block accidental registration into reserved system namespaces.

### Lifecycle And State

- `setup` / `teardown`: support adapter initialization and cleanup.
- `state_snapshot` / `state_restore`: allow checkpoints to span adapter-managed state.

### Per-Primitive Execution Policy

- `side_effect`: needed for UI/tooling parity with system primitives.
- `timeout_ms`: make timeout policy declarative and enforceable.
- `checkpoint`: separate checkpoint policy from coarse risk intent.
- `verify`: replace the current unused `verify_endpoint` string with executable semantics.
- `rollback`: let adapters declare checkpoint-based or compensating rollback behavior.
- `dependencies`: support package manager resolution.
- `concurrency` / `rate_limit`: capture runtime constraints instead of hardcoding them in adapters.
- `events`: declare whether the adapter may emit custom events and under what prefix.
- `deprecation`: make evolution safe for multi-package ecosystems.

## Migration Guidance

### Backward-Compatible V1 To V2 Mapping

- `app_id` -> `app.id`
- `name` -> `primitives[].full_name`
- `description` -> `primitives[].description`
- `socket_path` -> `transport.socket_path`
- `verify_endpoint` -> `primitives[].verify.method` with `mode=primitive_call`
- `rollback_endpoint` -> `primitives[].rollback.method` when rollback is an explicit primitive call
- `intent` -> `primitives[].intent`

### Suggested Registration Flow

1. Adapter sends one V2 registration payload containing all primitives.
2. Runtime validates:
   - protocol version
   - runtime version
   - required capabilities
   - namespace policy
3. Runtime stores adapter metadata separately from primitive metadata.
4. Runtime registers per-primitive routes.
5. Runtime health-checks the adapter using the declared health contract.

## Minimum V2 Set To Unblock The Roadmap

These fields should be considered mandatory for the next protocol revision:

- `manifest_version`
- `app.version`
- `app.min_runtime_version`
- `runtime_requirements.required_capabilities`
- `security.allowed_namespaces`
- `lifecycle.state_snapshot`
- `lifecycle.state_restore`
- `primitives[].side_effect`
- `primitives[].timeout_ms`
- `primitives[].checkpoint`
- `primitives[].verify`
- `primitives[].rollback`
- `primitives[].dependencies`
- `primitives[].events`

Without that minimum set, application primitives remain useful for simple request/response tools but are not yet a safe substrate for package-managed, stateful, replayable integrations.
