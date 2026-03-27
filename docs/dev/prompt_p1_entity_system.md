# Role & Context
You are a Staff-Level AI Software Engineer working on **PrimitiveBox**, an AI-Native Command Center.

We have successfully locked down the security layer of the Orchestrator with the Reviewer Gateway and Dynamic Intent Hydration from the Backend Control Plane. 
We are now moving deep into the **P1 Phase Constraints: Entity & App Protocol**, focusing specifically on building the **Entity System (实体语义网)**.

### The Problem
Currently, when the Agent calls a primitive (e.g., `code.search` or `fs.read`), the Workspace catches the `execution.result` and renders it blindly into a Panel. The system only sees "text data", and the UI panels exist as isolated islands. If the Agent's next step is "Let's edit the file we just found," the AI relies entirely on its fragile conversational context window to infer the file path. Furthermore, the UI doesn't know that a generic JSON view and a code editor are actually pointing to the exact same physical Sandbox file.

### Your Objective
Implement a **Workspace Entity Resolver and Tracker**. You need to map raw primitive execution results into strongly typed **Entities** (e.g., `Entity { type: 'file', uri: '/calc.go' }`), bind these Entities to the Workspace Panels, and inject this focused Entity graph back into the Agent's observation context so the LLM knows exactly what "objects" are physically on the desk in front of it.

# Task Breakdown

### Task 1: Defining the Entity Abstraction
- **Types Definition**: Create the core entity types in the frontend (e.g., `src/lib/entityTracker.ts`). Define a base interface `WorkspaceEntity` containing `id`, `type` (e.g., `'file'`, `'directory'`, `'process'`), and `metadata`.
- **Entity Store**: Extend `workspaceStore.ts` or create `entityStore.ts` to hold a global map of active Entities currently loaded or focused in the workspace.

### Task 2: Execution-to-Entity Resolution
- **The Mapper**: Intercept primitive results in the Timeline (`fs.read`, `fs.list`, `code.search`). If a result yields a file path, mechanically mint/upsert an `Entity` representing that file.
- **Linkage**: The resulting Timeline Entry and the spawned Workspace Panel should hold a reference to this `entityId`.

### Task 3: Panel ↔ Entity Binding
- Modify the Panel component architecture (`PanelHost.tsx` or specific panels like `SandboxPanel`).
- Panels that deal with resources should accept an `entityId`. 
- **Bonus State Linkage**: By linking Panels to Entities, if an Agent modifies an entity in one panel, other panels holding the same `entityId` can mathematically know they are looking at stale data (even if you only implement the visual/context tracking for now, the data structure must support this).

### Task 4: Context Injection back to the Agent Loop
- The true power of the Entity System lies in helping the LLM. 
- Modify the `OBSERVE` phase of the `agentLoop.ts`. When the Orchestrator gathers workspace context to send to the LLM, append a structured block: `"Active Workspace Entities: [ { type: 'file', path: '/calc.go' } ]"`, explicitly telling the Agent what objects are currently "in focus" on the UI desk.

# Architectural Constraints (MUST FOLLOW)
1. **Frontend-Led Semantic Layer**: The backend Sandbox executes purely structural primitives. Do NOT pollute the Go backend with UI Entity definitions. The translation from "primitive JSON output" to "semantic UI Entity" is strictly the job of the Frontend Workspace Orchestrator.
2. **Causality Integrity**: An Entity only exists if it was touched by an Execution or UI manual operation documented in the Timeline. Do not "hallucinate" entities that haven't passed through the event stream.
3. **Pluggable Mapping Code**: Build the "Resolution" mapping logically separate so that when Phase 2 Apps arrive (e.g., Postgres adapter returning rows), it is trivial to add a new resolver mapping `db.query` results into an `Entity { type: 'database_table' }`.

# Acceptance Criteria
- [ ] A generic `workspaceStore` tracks an array or map of `ActiveEntities`.
- [ ] Executing `fs.read` (or a mocked file read) automatically produces an Entity of type `file` with the correct path metadata.
- [ ] A Panel rendering the result visually or programmatically reflects its binding to that specific `Entity`.
- [ ] The LLM's system prompt or observation block dynamically includes a list of actively focused Entities mapped from the open Panels, reducing reliance on conversational memory.
