# Role & Context
You are a Staff-Level AI Software Engineer working on **PrimitiveBox**, an AI-Native execution runtime. 

We have successfully traversed Phase P1, giving the system a dynamic UI Extension Registry, a Workspace Entity System, and a strictly enforced HITL (Human-in-the-Loop) Reviewer Gateway.

We are now officially opening **Phase P2: Domain Expansion**. Your objective is to move PrimitiveBox beyond just local file/shell management (`fs.*`, `shell.*`) by introducing two rich-domain primitive families: **Database Interaction (`db.*`)** and **Browser Automation (`browser.*`)**.

# Task Breakdown

### Task 1: The Database Primitives (`db.*`)
Raw shell commands like `psql` bypass our structured safety rules. You must implement native structured primitives:
- **`db.query`**:
  - **Function**: Executes Read-Only SQL (SELECT) and returns a structured JSON array of row objects.
  - **Intent**: `side_effect: 'read'`, `risk_level: 'none'`, `reversible: true`.
  - **UI Hint**: MUST include `ui_layout_hint: 'table'` in its schema so the frontend mounts the `DataTableWidget` we built in P1.
- **`db.execute`**:
  - **Function**: Executes DDL/DML (INSERT, UPDATE, DELETE, DROP).
  - **Intent**: `side_effect: 'exec'`, `risk_level: 'high'`, `reversible: false`. (This configuration will automatically trigger the existing P1 Reviewer Gateway!).

### Task 2: The Browser Primitives (`browser.*`)
A modern agent needs to browse the web safely within the Sandbox, returning semantic data, not just raw messy HTML.
- **`browser.goto`** / **`browser.read`**:
  - **Function**: Navigates to a URL and returns the accessible Markdown/Text extraction of the DOM tree (minimizing LLM token usage).
  - **Intent**: `side_effect: 'read'`, `risk_level: 'none'`.
  - **UI Hint**: `ui_layout_hint: 'markdown'`.
- *(Optional extension based on complexity)* **`browser.action`**:
  - Accept structured parameters to click specific semantic elements or input text. 

### Task 3: Sandbox Integration & Adapter Routing
- **Architectural Placement**: These primitives should NOT run directly on the Host Gateway's `os/exec`. They must run inside the Sandbox Execution Plane (or via a separate external Adapter proxy routed via Unix sockets if you are fully adopting the Phase 2 app protocol).
- **Environment Management**: The database connection string (DSN) should not be hardcoded. It should ideally be pulled from sandbox environment variables or safely passed as a verified parameter.

# Architectural Constraints (MUST FOLLOW)
1. **Safety over Convenience**: If a query mutates state, it is `db.execute` and MUST be forced into the high-risk intent category. We rely on the P1 HITL Gateway to catch accidental `DROP TABLE` hallucinated commands.
2. **Tabular Output Purity**: Do not convert SQL query outputs to raw strings. Return strict JSON arrays (e.g., `[{ id: 1, name: "Alice" }]`) so the P1 Workspace Entity System can parse them correctly and the UI Registry can mount the Table Widget.

# Acceptance Criteria
- [ ] Both `db.*` and `browser.*` are successfully registered into the Control Plane's `/api/v1/primitives` catalog with their schemas and `ui_layout_hint`s.
- [ ] Executing `db.query` against a test dataset returns JSON array outputs, and the unmodified frontend mounts the `DataTableWidget`.
- [ ] Attempting to execute `db.execute` with a destructive query strictly hits the `AWAITING_REVIEW` Orchestrator pause, requiring a human click.
- [ ] Calling `browser.goto` successfully fetches a webpage content cleaned of script tags, returning semantic markdown/text.
