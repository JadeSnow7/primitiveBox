# Role & Context
You are a Staff-Level AI Software Engineer working on the **PrimitiveBox** project, an AI-Native Execution Runtime spanning from backend sandbox isolation to frontend Workspace Orchestration. 

We have successfully completed **Phase P0 (Stability & CVR Core)**. The Agent Loop now possesses a Deadlock Guard and is physically bound to Sandbox Checkpoints (Full-System Replay).

We are now initiating **Phase P1: Entity & App Protocol**, focusing on its most critical slice: **The Reviewer Model & Human-in-the-Loop (HITL) Security Gateway**.

Your objective is to implement a strict, three-tier separation of power (`Planner -> Executor -> Reviewer`). When an agent attempts an irreversible, high-risk primitive (e.g., `email.send` or `db.drop_table`), the orchestrator MUST auto-pause, escalate the primitive call to a Human/Reviewer UI Panel, and await manual approval before resuming or aborting.

# Task Breakdown

### Task 1: Intent-Based Interception (The Router/Dispatcher Guard)
The frontend `orchestratorDispatcher.ts` or primitive resolution layer must dynamically parse the `intent` metadata (e.g., `side_effect`, `risk_level`, `reversible`) of the requested primitive.
- If `reversible === false` AND/OR `risk_level === 'high'`:
  - **Intercept the execution**: Do NOT dispatch the network request to the backend sandbox.
  - **State mutation**: Change the Orchestrator loop state to `AWAITING_REVIEW` (instead of `RUNNING` or `PLANNING`).
  - **Timeline logging**: Record an `execution.pending_review` entry in the Timeline Store so the UI knows an action is stuck in the queue.

### Task 2: Reviewer UI Panel (Workspace Integration)
- Create a dedicated standard Workspace Component (e.g., `ReviewerPanel.tsx` or similar).
- When the state is `AWAITING_REVIEW`, this panel must render automatically to the forefront.
- It must vividly display: The primitive name, the exact JSON arguments the Agent wants to send (e.g., the recipient and body of the draft), and the declared risks.
- It must expose two explicit actions: **`[Approve]`** and **`[Reject]`**.

### Task 3: Loop Resumption & Escalation Feedback
- **On Approve**: The pending primitive call is officially dispatched to the Sandbox gateway. The Timeline transitions to `execution.result`, and the Agent Loop resumes its `OBSERVE -> REPLAN` cycle.
- **On Reject**: The dispatcher intercepts the flow and injects a synthetic error observation back to the Agent (e.g., `Error: Execution completely REJECTED by Human Reviewer. Re-evaluate your plan.`). The Agent Loop receives this feedback and must replan accordingly without executing the primitive.

# Architectural Constraints (MUST FOLLOW)
1. **Canonical Rule from AGENTS.md**: "email.send is the first primitive that requires human-in-the-loop and demonstrates the CVRCoordinator's escalate recovery path."
2. **Never Bypass Security**: The interception must happen deterministically based on the primitive's schema constraints, not based on an LLM's promise.
3. **Timeline Causality**: The timeline must reflect `execution.call` → `execution.pending_review` → (user interacts) → `execution.result` / `execution.rejected`. Never override or erase history; everything is an append-only timeline.

# Acceptance Criteria
- [ ] A mock or actual high-risk primitive (like `email.send` or `demo.irrevocable_action`) is called by the Executor.
- [ ] The Agent Loop pauses automatically and UI transitions to an awaiting state; no network request is sent to the sandbox.
- [ ] Clicking "Reject" cleanly provides feedback to the Agent Planner (it logs the failure to the timeline and thinks of an alternate plan).
- [ ] Clicking "Approve" dispatches the exact payload created by the agent, and the loop successfully completes.

Please analyze the current `src/store/workspaceStore.ts` and `agentLoop.ts` to identify the best injection point for the Interceptor Gateway before writing code.
