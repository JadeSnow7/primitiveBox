# Role & Context
You are a Staff-Level AI Software Engineer working on **PrimitiveBox**, an AI-Native execution runtime.

We have officially entered and conquered the first half of **Phase 3: Reference Adapters** with the highly successful `pb-os-adapter`, proving our Checkpoint, Verify, Recover (CVR) guarantees on real physical OS resources.

Your mission is to deliver the final masterpiece of Phase 3: the **`pb-mcp-bridge`** (Model Context Protocol Bridge).

### The Problem
There are already hundreds of "MCP Servers" in the open-source ecosystem that provide access to GitHub, Notion, Weather APIs, Postgres, and more. 
PrimitiveBox currently forces developers to write Go Unix-socket adapters from scratch for every new integration. We are missing out on an entire ecosystem of existing agentic tools.

### Your Objective
Build a universal adapter (`cmd/pb-mcp-bridge`) that acts as a translator. It will run an existing MCP server (e.g., via `stdio` using `npx -y @modelcontextprotocol/server-github`), fetch that server's defined Tools and Resources, and **dynamically mirror them** as native PrimitiveBox Primitives over a Unix Socket, perfectly integrated into our Orchestrator UI and CVR security layer.

# Task Breakdown

### Task 1: The MCP Client Skeleton
- Create the core `pb-mcp-bridge` binary. It must accept a command-line argument to launch an arbitrary MCP server (e.g., `pb-mcp-bridge --command "npx" --args "-y,@modelcontextprotocol/server-sqlite"`).
- Connect to this child process using the MCP `stdio` transport contract.

### Task 2: Dynamic Primitive Mapping
- When the bridge boots, it must send `tools/list` to the MCP server.
- **Manifest Translation**: For every MCP tool returned, dynamically generate a PrimitiveBox Manifest:
  - Primitive Name ➔ `mcp.[mcp_server_name].[tool_name]` (e.g., `mcp.sqlite.read_query`)
  - Schema ➔ Translate the MCP JSON Schema directly into our Primitive Schema format.
- **Intent Boundary (Crucial)**: Since we cannot guarantee an unknown MCP tool is safe, **ALL** translated MCP tools MUST default to:
  - `Intent: Mutation`
  - `Risk Level: High`
  - `Reversible: false`
  - *Why?* This forces every blind external tool through our P1 Reviewer Gateway (HITL) by default, protecting the user.

### Task 3: Execution Translation
- Implement a catch-all RPC handler in the bridge.
- When the PrimitiveBox Control Plane sends an execution request for `mcp.github.create_issue`, the bridge must translate it back into an MCP `tools/call` JSON-RPC message, wait for the MCP server's response, and stream the result back to the sandbox gateway.

# Architectural Constraints (MUST FOLLOW)
1. Do not rewrite the host Gateway (`pb server`). The bridge purely acts as a standard PrimitiveBox App Adapter (Unix Socket) on one side, and an MCP Client (`stdio`) on the other.
2. Ensure the process lifecycle is robust: if the child MCP server crashes, the `pb-mcp-bridge` must exit gracefully or report the error, ensuring the sandbox orchestrator knows the adapter is dead.

# Acceptance Criteria
- [ ] Running the bridge against a mock/test MCP server successfully registers `mcp.test.tool` in the gateway's `/api/v1/primitives`.
- [ ] Executing `mcp.test.tool` correctly triggers the HITL Reviewer UI due to the strict `mutationHigh` default mapping.
- [ ] Post-approval, the execution correctly proxies inputs to the MCP server and returns outputs to the Workspace UI.
- [ ] Adding an integration test (`main_test.go` or `smoke.py`) verifying the JSON-RPC translation layout.
