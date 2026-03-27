# Role & Context
You are a Staff-Level AI Software Engineer working on **PrimitiveBox**, an AI-Native Execution Runtime. 

We have successfully completed Phase P1 (Dynamic UI, Entity System, Reviewer Gate) and half of Phase P2 (Database and Browser Automation Primitives). 

We are now pivoting to the remaining crucial domain of Phase P2: **High-Level Structured Document Primitives (`doc.*`)**.

### The Problem
When Agents edit large, dense text blocks (e.g., 50-page architecture documents, runbooks, or extensive code files treated as plaintext), they rely heavily on `fs.read` and regular expressions or `shell.exec(sed ...)`. This is extremely brittle. The LLM's context window gets overwhelmed, and search-and-replace fails spectacularly when document structure (like markdown headings or broken links) is disturbed.

### Your Objective
Implement a family of semantic document primitives that manipulate files based on their Abstract Syntax Tree (AST) or structured elements (like Markdown headings/sections), not just raw strings. These high-level primitives must be tightly integrated with our CVR (Checkpoint, Verify, Recover) framework to guarantee document structural integrity.

# Task Breakdown

### Task 1: Semantic Document Parsing (`doc.read_struct`)
- **Function**: Read a document (prioritize Markdown) and return its AST or a semantic Table of Contents (TOC). 
- **Output**: Returns JSON representing the document skeleton (e.g., `{ headers: [{ level: 2, title: "Overview" }], sections: [...] }`) so the Agent can query the skeleton without reading a 50kb text file.
- **Intent**: `side_effect: 'read'`, `risk_level: 'none'`.
- **UI Hint**: `ui_layout_hint: 'tree'` or `'json'`.

### Task 2: Structural Manipulation (`doc.replace_section` & `doc.insert`)
- **Function**: Primitives that allow the agent to command: "Replace the content under the heading `### Architecture` with this new text." 
- **Implementation**: The backend parses the markdown heading, safely replaces the content below it until the next heading of identical or higher level, and saves the file.
- **Intent**: `side_effect: 'write'`, `risk_level: 'low'`, `reversible: true`. 
- *(Note: Since `state.restore` operates on the workspace file level, it natively supports rollback for file modifications.)*

### Task 3: CVR Integration (`doc.verify_struct`)
- Document editing requires strict verification. A malformed edit (e.g., unclosed code blocks, broken header hierarchies) is a failure.
- **Function**: Implement a verifier primitive (`doc.verify_struct`) that checks if the markdown is still valid.
- **Integration**: Ensure the mutation primitives are fully integrated into the Orchestrator's verification cycle, so failure in structural integrity immediately triggers the sandbox rollback!

# Architectural Constraints (MUST FOLLOW)
1. **Execution Plane Isolation**: These structural parsers and edit operations must execute inside the untrusted Sandbox (or a dedicated adapter), NOT on the Host Gateway. Use a robust parser (e.g., a standard Markdown/AST parser library) within the execution environment.
2. **Leverage P1 Wins**: Rely on the existing P1 Registry (to return dynamic Schema hints) and the Workspace Entity System (so editing long docs feels like editing targeted 'Entities').

# Acceptance Criteria
- [ ] `doc.*` primitives are successfully registered in the Control Plane’s catalog with their schemas and `ui_layout_hint`s.
- [ ] `doc.read_struct` returns generic JSON describing a Markdown file's heading hierarchy, avoiding a raw string dump.
- [ ] An Agent successfully updates an exact section of a 500-line Markdown file using `doc.replace_section` without corrupting or even reading the rest of the document.
- [ ] If an Agent submits a structural error that a verifier normally catches (e.g., breaking markdown links), the Orchestrator records a verification failure and the file physically reverts to its exact previous state via `state.restore`.
