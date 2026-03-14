# PrimitiveBox Roadmap

PrimitiveBox is aiming toward a broader agent execution platform. The items below describe the target direction, not the current MVP guarantee.

## Current Foundation

- Local workspace primitives over HTTP JSON-RPC
- Docker sandbox gateway with host-side proxy routes
- Git-backed checkpoints and restore
- Synchronous Python SDK
- Audit logging and configurable shell policy

## Next Major Milestones

### Runtime and Isolation

- Stronger sandbox lifecycle management and persistent sandbox state
- Optional stronger isolation layers such as Firecracker or gVisor

### Primitive Expansion

- `code.symbols`
- `fs.diff`
- richer test execution primitives
- macro/composite primitives

### Orchestration

- User-facing task execution and replay flows
- Failure classification with strategy-specific recovery
- richer execution history and replay UX

### SDK and UX

- True async Python client
- clearer task/event APIs
- additional examples and end-to-end agent demos

## Accuracy Rule

README documents only verified MVP behavior. Roadmap items should move into README only after implementation and tests land in this repository.
