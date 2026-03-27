# Role & Context
You are a Staff-Level AI Software Engineer working on **PrimitiveBox**, an AI-Native execution runtime and command center.

We have successfully locked down both the **Reviewer Gateway** and the **Workspace Entity System**. The system dynamically hydrates primitive metadata from the Backend Control Plane, and maps complex execution paths into explicitly tracked workspace objects (Entities). 

You are now tasked with executing the final capability of the P1 Phase (Entity & App Protocol): **App-Level UI Registry (Dynamic Widget Mounting)**.

### The Problem
Currently, the UI Panels (e.g., `ReviewPanel`, `DiffPanel`, `GenericJsonView`) are hardcoded against known backend primitives. As we prepare for Phase 2/3, external applications (like an MCP bridge or Postgres Adapter) will dynamically register primitives like `db.query` or `figma.design.read` over Unix Sockets. When the agent uses these, the UI has no idea how to render the domain-specific result objects, inevitably degrading to dumping a raw JSON literal to the front-end user. 

### Your Objective
Abstract the rigid Panel matching logic into a **pluggable UI Plugin Registry**. You need to allow the frontend to dynamically associate external primitive namespaces (or UI layout hints sent from the backend) with registered or schema-driven rendering widgets, effectively serving as an OS matching an unknown file type to a registered "Application View".

# Task Breakdown

### Task 1: Defining the UI Registry Abstraction
- **Registry Core**: Create a frontend `src/lib/uiRegistryStore.ts` (or extend an existing UI store). Define an interface `UIPluginRenderer` that outlines how a specific component maps to a primitive signature or a return schema type.
- **Dynamic Resolver**: The registry must be able to say: *"For primitive `db.query`, use the `DataTableWidget`, but for `*`, fallback to `RawJsonWidget`."*

### Task 2: Backend Control-Plane Metadata (UI Hints)
- Ensure the backend's `/api/v1/primitives` (or the underlying Go `Primitive` struct) can accept an optional property like `ui_layout_hint` (e.g., `'table'`, `'markdown'`, `'image_blob'`) alongside its schema input/output objects.
- This empowers the third-party backend Unix socket app to self-declare how it prefers to be rendered!

### Task 3: Execution Mapper & PanelHost Refactoring
- **The Mapper Integration**: Refactor `executionMapper.ts` and `PanelHost.tsx`. Instead of a hardcoded massive `switch (result.primitive)` tree determining which React Component to render, the `PanelHost` must dynamically instantiate the component referenced by querying `uiRegistryStore.resolveView(result)`.
- **Building the Bridge**: If a primitive explicitly requests to be rendered generically by a specific primitive name or hint, the host mounts the target UI component and injects the raw result data and the previously built `entityId` as standard props.

### Task 4: Securing the Fallback
- If a dynamically hydrated primitive has NO registered specialized UI component, it must gracefully fallback to a standard, interactive structured `GenericJsonPanel` or similar readable inspector, instead of crashing the front end.

# Architectural Constraints (MUST FOLLOW)
1. **Safety First (No XSS)**: Even though we are making the UI dynamic, we are NOT resolving string-based `eval()` or raw HTML provided directly by the backend payload. The backend simply specifies a requested *widget class name/hint* (e.g. `'GridTable'`), and the frontend must look it up in its pre-compiled *Registry map*. We are not blindly rendering third-party script tags yet.
2. **Pluggability**: Structure `uiRegistry.ts` so that in the future, transitioning to true Webpack Module Federation or Web-Components (for running completely uncompiled external UI bundles) is just a matter of swapping out the resolving logic.

# Acceptance Criteria
- [ ] `PanelHost.tsx` is completely scrubbed of its hardcoded 1:1 primitive matching switch-statement.
- [ ] A dynamic/mocked third-party primitive (`demo.tabular_data`) is fetched from the Backend Control Plane, with `ui_layout_hint: 'table'`.
- [ ] When the agent executes `demo.tabular_data`, the UI correctly avoids the raw JSON dump and mounts the generic Data Table widget based strictly on the registry's resolution.
- [ ] Running a completely unknown primitive gracefully resolves to the generic JSON/Object inspector.
- [ ] `npm test` and `npx tsc -b` run flawlessly after this refactoring.
