# Role & Context

You are a Staff-Level Senior Architect conducting the final Code Quality & Stability Hardening Audit for PrimitiveBox, an AI-native execution runtime.

This is a hardening pass only. Do not add features, adapters, primitives, or public API changes.

Before auditing, respect the repository architecture rules in AGENTS.md:
- preserve host/sandbox execution boundaries
- do not move sandbox-owned execution onto the gateway host
- preserve control-plane write-and-emit semantics
- keep RouterDriver/runtime indirection intact
- do not break legacy compatibility paths

# Objective

Perform a targeted hardening review and implement fixes only in these areas:
- concurrency and cancellation safety
- memory retention / leak prevention
- panic and error containment
- UI security hygiene

Do not change public HTTP, JSON-RPC, SSE, or SDK contracts unless required to preserve existing behavior.

# Execution Order

1. Establish a clean audit baseline first.
- Detect and resolve merge-conflict markers or syntax blockers that prevent the repo from building or testing.
- Inventory unrelated modified/untracked files, but do not auto-commit or normalize them as part of this audit.
- Keep audit fixes isolated from unrelated worktree changes.

2. Run backend safety checks.
- Run `go test -race ./...` after the baseline compiles.
- Audit goroutine lifetime, blocked readers/writers, and `ctx.Done()` handling in:
  - `internal/rpc/`
  - `internal/sandbox/`
  - `cmd/pb-mcp-bridge/`
  - `cmd/pb-*-adapter/`
- Focus on Unix socket closure, crashed child processes, port-forward shutdown, and long-lived polling loops.
- If an MCP server crashes or a sandbox socket closes unexpectedly, the gateway and adapters must not leak goroutines or hang forever.

3. Run frontend memory/state hardening.
- Audit:
  - `web/src/store/timelineStore.ts`
  - `web/src/store/workspaceStore.ts`
  - `web/src/lib/executionMapper.ts`
  - `web/src/lib/agentLoop.ts`
  - `web/src/store/orchestratorStore.ts`
  - `web/src/components/workspace/AICommandBar.tsx`
- Ensure large primitive results are not retained redundantly in multiple stores/panel props without bounds.
- Prefer bounded retention, truncation, or lightweight summaries over cloning large payloads.
- Ensure abort, remount, and AWAITING_REVIEW flows cannot leave stale promises/resolvers or double-fire callbacks.

4. Panic and error containment.
- Preserve the existing `/rpc` panic recovery behavior.
- Extend structured recovery to sandbox proxy paths and adapter socket dispatch boundaries.
- Any panic in dispatch / verify / rollback paths must become a structured JSON-RPC or app-RPC error, not a broken socket or crashed gateway.

5. UI security hygiene.
- Verify current text/JSON renderers keep untrusted content inert.
- If any renderer parses markdown or HTML, enforce sanitization explicitly.
- Validate that browser-derived content strips executable script/style content and renders safely in the workspace UI.

# Deliverables

- Report findings ordered by severity with file references.
- Implement only hardening fixes.
- Add or update regression tests for each fixed class of issue.
- Leave unrelated dirty worktree changes out of the audit commit unless explicitly requested.

# Acceptance Criteria

- The repo builds cleanly and contains no merge-conflict markers in tracked source files.
- `go test -race ./...` passes cleanly.
- Frontend Vitest suites pass.
- Regression coverage exists for:
  - unexpected MCP/server crash or closed Unix socket
  - bounded frontend retention for oversized execution payloads
  - review-pause / cancel / remount lifecycle safety
  - panic-to-structured-error conversion in adapter or proxy verify paths
  - inert rendering of script-bearing browser content
- If a "multi-hour run" claim is made, back it with a deterministic stress test or bounded-retention assertion, not anecdotal observation.
