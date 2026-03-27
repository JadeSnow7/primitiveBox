# Role & Context
You are a Staff-Level AI Software Engineer working on **PrimitiveBox**, an AI-Native Execution Runtime. 

We have successfully completed the implementation of the **Reviewer Gateway (Human-In-The-Loop)**. The Orchestrator's execution dispatcher now correctly pauses high-risk operations based on Primitive Intent metadata. 

However, we currently have a significant architectural tech debt: the Primitive Intent metadata (e.g., `reversible`, `risk_level`) is hardcoded in the frontend (`primitiveIntent.ts`). As we prepare for Phase 2 where external Unix-socket applications will register their own non-system primitives dynamically, the frontend MUST NOT rely on a static local registry.

Your objective is to achieve **Dynamic Intent Hydration**. The frontend must fetch the absolute source of truth for primitive schemas and intents straight from the Backend Control Plane, and use this dynamic catalog to power the Reviewer Gateway interception mapping.

# Task Breakdown

### Task 1: Backend Control-Plane API (The Source of Truth)
Before the frontend can hydrate, the backend must expose the primitive catalog.
- **Implement/Extend the Registry API**: Ensure there is an HTTP REST endpoint exposed by the host gateway (e.g., `GET /api/v1/primitives` or `GET /api/v1/registry`) that returns a serialized list of all currently active primitives.
- **Payload Requirements**: The returned JSON must include the primitive `name`, its JSON Schema for inputs/outputs, and strictly include its `intent` definition (`side_effect`, `risk_level`, `reversible`, `checkpoint_required`). 

### Task 2: Frontend Data Hydration (The Store)
- **Implement a Primitive Store**: Create a new store (e.g., `primitiveStore.ts` utilizing Zustand/React context) or extend an existing initialization action to fetch the primitive catalog from the backend as the App/Workspace loads.
- **Fail-Safe Integrity**: Ensure the Workspace doesn't allow agents to run blind. If the primitive manifest fails to fetch, the system must fail-closed or display an initialization error, rather than defaulting all intents to low-risk.

### Task 3: Refactor the Dispatcher's Intent Resolution
- **Remove Hardcoding**: Refactor the frontend's static definitions inside `primitiveIntent.ts`. It should no longer be a static hardcoded map.
- **Wire the Dispatcher**: Modify the `orchestratorDispatcher.ts` or its intent-resolver utility to query the dynamically hydrated `primitiveStore` instead. 
- **Verify Causality**: Ensure that the Reviewer Gateway logic perfectly remains intact—it still halts the Agent Loop and displays a Review Panel for operations tagged `RiskLevel: high` or `reversible: false` by the backend payload.

# Architectural Constraints (MUST FOLLOW)
1. **Follow the Gateway Rule (AGENTS.md)**: "The host gateway remains the control plane and API boundary. Its long-term responsibilities are limited to: authentication, request validation, metadata CRUD..." The API serving the primitive catalog must be part of the gateway HTTP serving layer, not direct sandbox execution.
2. **Schema Alignment**: Do not introduce camelCase into public payloads unless an existing public contract already requires it. Stick to lower `snake_case` JSON names (`risk_level`, `side_effect`, etc.).
3. **No Breaking Changes to the Loop**: The internal mechanics of how the Agent Loop handles `AWAITING_REVIEW` should NOT be touched. You are merely changing *how the condition is evaluated* (Dynamic vs Static source).

# Acceptance Criteria
- [ ] `GET /api/v1/primitives` (or equivalent) returns a 200 OK with a list of primitives, including their schemas and fully populated intent blocks.
- [ ] The frontend dynamically fetches this manifest on startup and stores it in memory.
- [ ] An agent attempts to call a newly registered external primitive (or a mock high-risk capability). Because the API returned `risk_level: high` for it, the Dispatcher correctly intercepts it and pulls up the Reviewer Panel, without requiring a single hardcoded rule in the frontend code.
- [ ] Previous automated tests testing the Reviewer Gate still pass (by mocking the HTTP catalog response instead of the local static map).

Review the Go backend code handling the Primitive Registry and the Frontend `primitiveIntent.ts` to plan your data structures before executing.
