# PrimitiveBox Roadmap

PrimitiveBox is aiming toward a broader agent execution platform. The items below describe the target direction, not the current MVP guarantee.

## Current Foundation (Iteration 1)

- Local workspace primitives over HTTP JSON-RPC
- Docker sandbox gateway with host-side proxy routes
- Git-backed checkpoints and restore
- Synchronous Python SDK
- Audit logging and configurable shell policy

## Planned Iterations

### Iteration 2: AI Agent Experience Enhancement
**Goal**: Increase LLM auto-fix success rates and reduce token consumption overhead.
- **AST `code.symbols`**: Integrate Tree-sitter. Instead of returning full file strings for context, return skeletonized class/function boundaries.
- **`fs.diff`**: Compare the workspace against the latest checkpoint to help the agent verify its own changes.
- **Macro Primitive (`macro.safe_edit`)**: A compound primitive that atomically performs `checkpoint -> write -> verify -> (restore on fail)`. Saves HTTP network hops.
- **LLM Schema Adapters**: Expose helper libraries in the Python SDK `primitivebox.adapters` to auto-translate `client.list_primitives()` into **OpenAI Function Calling** / **Claude `tool_use`** formats.
- **Summarized Failures**: Intercept and truncate vast compiler error stacks down to concise error abstracts before returning to LLMs.
- **Async Python SDK**: Add `aiohttp`-based `AsyncPrimitiveBoxClient` class.

### Iteration 3: Multi-Tenancy & Operations
**Goal**: Scale the project to a state where large enterprises can host multiple distinct agents safely on the same hardware.
- **Task Persistency & Orchestration**: Move orchestration logs into a persistent store (e.g. SQLite database) on the Gateway host. Provide user-facing task execution and richer execution history / replay UX.
- **Stronger Sandbox Lifecycle**: Persistent sandbox state management.
- **Web UI Dashboard**: A local dashboard serving from the Gateway displaying: Active Sandboxes, Logs, and Primitive Call Traces.
- **CI / GitHub Action Wrapper**: Allow running PrimitiveBox headless as a GitHub bot to react to PRs and auto-push commit fixes.
- **Alternative Runtimes**: Investigate plugging `Firecracker` microVMs (via `firectl`) or `gVisor` behind `RuntimeDriver` for tighter kernel-level boundary separation than Docker.
- **RBAC API Keys**: Issue Gateway-level access tokens so that shared models can't cross-contaminate sandboxes.

## Accuracy Rule

The `README.md` documents only verified MVP behavior. Roadmap items should move into README only after implementation and tests land in this repository.
