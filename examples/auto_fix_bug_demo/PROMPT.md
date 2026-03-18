# PrimitiveBox Auto-Fix-Bug Demo

## 1. Agent System Prompt

以下是喂给 LLM（Claude / GPT）的 system prompt，agent 通过 PrimitiveBox Python SDK 执行所有操作。

---

```text
You are a code repair agent running inside PrimitiveBox — a checkpointed, verifiable execution runtime.

## Identity

You are NOT a chatbot. You are an autonomous repair agent. You receive a bug report, locate the fault, fix it, and prove the fix is correct. Every action you take is a typed primitive with an explicit contract. You do not guess; you verify.

## Runtime

You operate through PrimitiveBox primitives. You MUST use these primitives for all interactions with the workspace — never attempt raw shell access outside of `shell.exec`.

Available primitives:

| Primitive              | Purpose                                    | Reversible | Risk   |
|------------------------|--------------------------------------------|------------|--------|
| `fs.read`              | Read file content                          | n/a        | None   |
| `fs.write`             | Write / overwrite file                     | Yes        | Medium |
| `fs.list`              | List directory contents                    | n/a        | None   |
| `fs.search`            | Grep / ripgrep across workspace            | n/a        | None   |
| `shell.exec`           | Run a shell command (build, test, lint)     | No         | High   |
| `checkpoint.create`    | Snapshot current workspace state           | n/a        | None   |
| `checkpoint.restore`   | Rollback workspace to a checkpoint         | No         | High   |
| `checkpoint.list`      | List available checkpoints                 | n/a        | None   |
| `verify.test`          | Run test suite, return pass/fail + details | n/a        | None   |

## Workflow — CVR Loop

You MUST follow the Checkpoint → Verify → Recover loop for every repair attempt:

### Phase 0: Understand
1. Read the bug report provided in the task.
2. Use `fs.read` and `fs.search` to understand the relevant code.
3. Use `verify.test` or `shell.exec` to reproduce the failure. Confirm you can see the failing test(s).
4. Form a hypothesis about the root cause.

### Phase 1: Checkpoint
5. Call `checkpoint.create` with a descriptive label BEFORE making any changes.
   - Label format: `pre-fix-{brief-description}`
   - Example: `pre-fix-off-by-one-in-sort`

### Phase 2: Execute
6. Apply the minimal fix using `fs.write`. Change as few lines as possible.
   - Do NOT refactor unrelated code.
   - Do NOT add features.
   - Do NOT change test expectations.

### Phase 3: Verify
7. Run `verify.test` to execute the full test suite.
8. Evaluate the result:
   - **All tests pass** → Report success. Done.
   - **Same test still fails** → The fix is wrong. Go to Phase 4.
   - **New test failures introduced** → The fix broke something else. Go to Phase 4.
   - **Build error** → Syntax or type error in the fix. Go to Phase 4.

### Phase 4: Recover
9. Call `checkpoint.restore` to roll back to the pre-fix checkpoint.
10. Analyze why the fix failed. Update your hypothesis.
11. Return to Phase 1 with a new approach.

### Limits
- Maximum 3 repair attempts per bug. After 3 failures, escalate:
  output a structured report with your findings and remaining hypotheses.
- Never skip the checkpoint step. Never.
- Never modify test files unless the bug report explicitly says the test itself is wrong.

## Output Format

After each phase, emit a structured status block:

```json
{
  "phase": "understand | checkpoint | execute | verify | recover",
  "attempt": 1,
  "action": "primitive invoked",
  "result": "outcome summary",
  "hypothesis": "current root cause theory",
  "next": "planned next step"
}
```

On completion (success or escalation), emit a final report:

```json
{
  "status": "fixed | escalated",
  "bug_id": "from task input",
  "root_cause": "explanation",
  "fix_summary": "what was changed and why",
  "attempts": 2,
  "checkpoints_used": ["pre-fix-attempt-1", "pre-fix-attempt-2"],
  "tests_passed": true,
  "diff": "minimal unified diff of the fix"
}
```

## Principles

1. **Minimal diff.** The best fix is the smallest correct change.
2. **Checkpoint always.** No mutation without a prior snapshot.
3. **Tests are truth.** If tests pass, the fix is valid. If they don't, it isn't.
4. **Fail fast, recover clean.** A wrong fix costs nothing if you checkpoint first.
5. **No hallucinated fixes.** If you don't understand the root cause, say so.
```

---

## 2. Demo Buggy Project: `testdata/buggy_calc`

以下是用于演示的 buggy 项目，一个简单的 Go 计算器，内含 3 个 bug。

### `testdata/buggy_calc/calc.go`

```go
package calc

import (
	"errors"
	"math"
)

// Add returns the sum of two numbers.
func Add(a, b float64) float64 {
	return a - b // BUG-001: should be a + b
}

// Divide returns a / b, or an error if b is zero.
func Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, nil // BUG-002: should return error, not nil
	}
	return a / b, nil
}

// Sqrt returns the square root of x, or error if x < 0.
func Sqrt(x float64) (float64, error) {
	if x < 0 {
		return 0, errors.New("negative input")
	}
	return math.Sqrt(x), nil
}
```

### `testdata/buggy_calc/calc_test.go`

```go
package calc

import (
	"testing"
)

func TestAdd(t *testing.T) {
	got := Add(2, 3)
	if got != 5 {
		t.Errorf("Add(2,3) = %v, want 5", got)
	}
}

func TestAddNegative(t *testing.T) {
	got := Add(-1, -2)
	if got != -3 {
		t.Errorf("Add(-1,-2) = %v, want -3", got)
	}
}

func TestDivide(t *testing.T) {
	got, err := Divide(10, 2)
	if err != nil || got != 5 {
		t.Errorf("Divide(10,2) = %v, %v; want 5, nil", got, err)
	}
}

func TestDivideByZero(t *testing.T) {
	_, err := Divide(10, 0)
	if err == nil {
		t.Error("Divide(10,0) should return error, got nil")
	}
}

func TestSqrt(t *testing.T) {
	got, err := Sqrt(16)
	if err != nil || got != 4 {
		t.Errorf("Sqrt(16) = %v, %v; want 4, nil", got, err)
	}
}

func TestSqrtNegative(t *testing.T) {
	_, err := Sqrt(-1)
	if err == nil {
		t.Error("Sqrt(-1) should return error, got nil")
	}
}
```

### `testdata/buggy_calc/go.mod`

```
module buggy_calc

go 1.21
```

### `testdata/buggy_calc/BUG_REPORT.md`

```markdown
# Bug Report

## BUG-001: Add returns wrong result
- Severity: High
- Repro: `Add(2, 3)` returns `-1` instead of `5`
- Expected: Addition should return the sum

## BUG-002: Divide by zero does not return error
- Severity: High
- Repro: `Divide(10, 0)` returns `(0, nil)` instead of an error
- Expected: Division by zero should return a non-nil error
```

---

## 3. Orchestration Script: `examples/auto_fix_bug/run_demo.py`

```python
"""
PrimitiveBox Auto-Fix-Bug Demo
==============================
Drives an LLM agent through the CVR loop to fix bugs in buggy_calc.
Requires: running `pb server`, anthropic SDK (or openai SDK).
"""

import json
import os
import sys
from anthropic import Anthropic
from primitivebox import PrimitiveBoxClient

# ─── Config ──────────────────────────────────────────────────
PB_HOST = os.getenv("PB_HOST", "http://localhost:8080")
SANDBOX_ID = os.getenv("PB_SANDBOX_ID", "")  # empty = host mode
MODEL = os.getenv("MODEL", "claude-sonnet-4-20250514")
MAX_TURNS = 20

SYSTEM_PROMPT = open("PROMPT.md").read()  # the agent system prompt above

# ─── PrimitiveBox Client ─────────────────────────────────────
pb = PrimitiveBoxClient(PB_HOST, sandbox_id=SANDBOX_ID or None)

# ─── Primitive → Tool Mapping ────────────────────────────────
TOOLS = [
    {
        "name": "fs_read",
        "description": "Read file content. Input: {path: string}",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
        },
    },
    {
        "name": "fs_write",
        "description": "Write content to file. Input: {path: string, content: string}",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "content": {"type": "string"},
            },
            "required": ["path", "content"],
        },
    },
    {
        "name": "fs_search",
        "description": "Search workspace files by pattern. Input: {pattern: string, path?: string}",
        "input_schema": {
            "type": "object",
            "properties": {
                "pattern": {"type": "string"},
                "path": {"type": "string"},
            },
            "required": ["pattern"],
        },
    },
    {
        "name": "shell_exec",
        "description": "Execute shell command. Input: {command: string}",
        "input_schema": {
            "type": "object",
            "properties": {"command": {"type": "string"}},
            "required": ["command"],
        },
    },
    {
        "name": "checkpoint_create",
        "description": "Create workspace checkpoint. Input: {label: string}",
        "input_schema": {
            "type": "object",
            "properties": {"label": {"type": "string"}},
            "required": ["label"],
        },
    },
    {
        "name": "checkpoint_restore",
        "description": "Restore workspace to checkpoint. Input: {checkpoint_id: string}",
        "input_schema": {
            "type": "object",
            "properties": {"checkpoint_id": {"type": "string"}},
            "required": ["checkpoint_id"],
        },
    },
    {
        "name": "verify_test",
        "description": "Run test suite. Input: {command?: string, working_dir?: string}",
        "input_schema": {
            "type": "object",
            "properties": {
                "command": {"type": "string"},
                "working_dir": {"type": "string"},
            },
        },
    },
]

# ─── Tool Dispatch ───────────────────────────────────────────
def dispatch_tool(name: str, input: dict) -> str:
    """Route LLM tool calls to PrimitiveBox primitives."""
    try:
        if name == "fs_read":
            result = pb.fs.read(input["path"])
        elif name == "fs_write":
            result = pb.fs.write(input["path"], input["content"])
        elif name == "fs_search":
            result = pb.fs.search(input["pattern"], path=input.get("path"))
        elif name == "shell_exec":
            result = pb.shell.exec(input["command"])
        elif name == "checkpoint_create":
            result = pb.checkpoint.create(label=input["label"])
        elif name == "checkpoint_restore":
            result = pb.checkpoint.restore(input["checkpoint_id"])
        elif name == "verify_test":
            cmd = input.get("command", "go test ./...")
            wd = input.get("working_dir", ".")
            result = pb.shell.exec(f"cd {wd} && {cmd}")
        else:
            return json.dumps({"error": f"unknown tool: {name}"})

        return json.dumps(result, default=str)

    except Exception as e:
        return json.dumps({"error": str(e)})


# ─── Agent Loop ──────────────────────────────────────────────
def run_agent():
    client = Anthropic()

    # Initial task message — point agent at the bug report
    task = """
    You have a buggy Go project at `./buggy_calc/`.
    Read `./buggy_calc/BUG_REPORT.md` to understand the bugs.
    Then fix each bug following the CVR loop.
    Start now.
    """

    messages = [{"role": "user", "content": task}]

    print("=" * 60)
    print("  PrimitiveBox Auto-Fix-Bug Demo")
    print("=" * 60)

    for turn in range(MAX_TURNS):
        print(f"\n--- Turn {turn + 1} ---")

        response = client.messages.create(
            model=MODEL,
            max_tokens=4096,
            system=SYSTEM_PROMPT,
            tools=TOOLS,
            messages=messages,
        )

        # Process response blocks
        assistant_content = response.content
        tool_results = []

        for block in assistant_content:
            if block.type == "text":
                print(f"\n[Agent] {block.text[:500]}")
            elif block.type == "tool_use":
                print(f"\n[Primitive] {block.name}({json.dumps(block.input, ensure_ascii=False)[:200]})")
                result = dispatch_tool(block.name, block.input)
                print(f"[Result] {result[:300]}")
                tool_results.append(
                    {
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result,
                    }
                )

        # Append assistant message
        messages.append({"role": "assistant", "content": assistant_content})

        # If there were tool calls, send results back
        if tool_results:
            messages.append({"role": "user", "content": tool_results})
        elif response.stop_reason == "end_turn":
            print("\n[Demo] Agent finished.")
            break
    else:
        print("\n[Demo] Max turns reached.")

    print("=" * 60)
    print("  Demo Complete")
    print("=" * 60)


if __name__ == "__main__":
    run_agent()
```

---

## 4. Event Stream Demo (SSE): `examples/auto_fix_bug/stream_demo.py`

```python
"""
Stream version — shows real-time CVR events via SSE.
"""

from primitivebox import PrimitiveBoxClient

pb = PrimitiveBoxClient("http://localhost:8080")

# Stream all runtime events
print("Listening for CVR events...\n")
for event in pb.events.stream():
    etype = event.get("type", "unknown")

    if etype == "primitive.start":
        print(f"  ▶ {event['primitive']}  (risk: {event.get('risk_level', '?')})")
    elif etype == "cvr.checkpoint":
        print(f"  📌 Checkpoint: {event['checkpoint_id']}")
    elif etype == "cvr.verify":
        ok = "✓" if event.get("passed") else "✗"
        print(f"  {ok} Verify: {event.get('summary', '')}")
    elif etype == "cvr.recover":
        print(f"  ↩ Recover: {event['action']} → {event.get('reason', '')}")
    elif etype == "primitive.complete":
        print(f"  ✓ {event['primitive']} done ({event.get('duration_ms', '?')}ms)")
    else:
        print(f"  · {etype}: {str(event)[:120]}")
```

---

## 5. Demo Run Instructions

```bash
# 1. Build PrimitiveBox
make build

# 2. Start server with buggy project as workspace
./bin/pb server start --workspace ./testdata/buggy_calc

# 3. (Optional) Create sandbox
./bin/pb sandbox create --driver docker --mount ./testdata/buggy_calc --ttl 3600

# 4. In another terminal, stream events
python3 examples/auto_fix_bug/stream_demo.py

# 5. In another terminal, run the agent
export ANTHROPIC_API_KEY="sk-ant-..."
export PB_SANDBOX_ID="sb-xxxxxxxx"  # or leave empty for host mode
python3 examples/auto_fix_bug/run_demo.py
```

Expected output flow:
```
=== Turn 1 ===
[Agent] Reading bug report...
[Primitive] fs_read({"path": "./buggy_calc/BUG_REPORT.md"})
[Result] {"content": "# Bug Report\n\n## BUG-001: ..."}

=== Turn 2 ===
[Agent] Reproducing failures...
[Primitive] verify_test({"command": "go test ./...", "working_dir": "./buggy_calc"})
[Result] {"exit_code": 1, "stdout": "--- FAIL: TestAdd ..."}

=== Turn 3 ===
[Agent] Creating checkpoint before fix attempt...
[Primitive] checkpoint_create({"label": "pre-fix-bug-001-add-operator"})
  📌 Checkpoint: chk-a1b2c3d4

=== Turn 4 ===
[Primitive] fs_read({"path": "./buggy_calc/calc.go"})
[Primitive] fs_write({"path": "./buggy_calc/calc.go", "content": "..."})
  ▶ fs.write  (risk: Medium)

=== Turn 5 ===
[Primitive] verify_test(...)
  ✓ Verify: TestAdd PASS, TestDivideByZero FAIL
[Agent] BUG-001 fixed. Moving to BUG-002...

=== Turn 6 ===
[Primitive] checkpoint_create({"label": "pre-fix-bug-002-divide-error"})
...
```
