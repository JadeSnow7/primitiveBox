"""
PrimitiveBox Auto-Fix-Bug Demo
==============================
Drives an LLM agent through the CVR loop to fix bugs in buggy_calc.
Requires: running `pb server`, anthropic SDK.

Usage:
    export ANTHROPIC_API_KEY="sk-ant-..."
    python3 run_demo.py
"""

import json
import os
import sys
from anthropic import Anthropic
from primitivebox import PrimitiveBoxClient

# ─── Config ──────────────────────────────────────────────────
PB_HOST = os.getenv("PB_HOST", "http://localhost:8080")
SANDBOX_ID = os.getenv("PB_SANDBOX_ID", "")
MODEL = os.getenv("MODEL", "claude-sonnet-4-20250514")
MAX_TURNS = 20

PROMPT_PATH = os.path.join(os.path.dirname(__file__), "AGENT_PROMPT.md")
with open(PROMPT_PATH) as f:
    SYSTEM_PROMPT = f.read()

# ─── PrimitiveBox Client ─────────────────────────────────────
pb = PrimitiveBoxClient(PB_HOST, sandbox_id=SANDBOX_ID or None)


# ─── Primitive → Tool Definitions ────────────────────────────
TOOLS = [
    {
        "name": "fs_read",
        "description": "Read file content from workspace.",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string", "description": "File path relative to workspace root"}},
            "required": ["path"],
        },
    },
    {
        "name": "fs_write",
        "description": "Write content to a file in the workspace. Creates or overwrites.",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "File path relative to workspace root"},
                "content": {"type": "string", "description": "Full file content to write"},
            },
            "required": ["path", "content"],
        },
    },
    {
        "name": "fs_list",
        "description": "List files and directories at the given path.",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string", "description": "Directory path, defaults to '.'"}},
        },
    },
    {
        "name": "fs_search",
        "description": "Search workspace files by grep pattern.",
        "input_schema": {
            "type": "object",
            "properties": {
                "pattern": {"type": "string", "description": "Search pattern (regex)"},
                "path": {"type": "string", "description": "Scope directory, defaults to '.'"},
            },
            "required": ["pattern"],
        },
    },
    {
        "name": "shell_exec",
        "description": "Execute a shell command in the workspace.",
        "input_schema": {
            "type": "object",
            "properties": {"command": {"type": "string", "description": "Shell command to execute"}},
            "required": ["command"],
        },
    },
    {
        "name": "checkpoint_create",
        "description": "Create a named checkpoint of the current workspace state.",
        "input_schema": {
            "type": "object",
            "properties": {"label": {"type": "string", "description": "Descriptive label for the checkpoint"}},
            "required": ["label"],
        },
    },
    {
        "name": "checkpoint_restore",
        "description": "Restore workspace to a previously created checkpoint.",
        "input_schema": {
            "type": "object",
            "properties": {"checkpoint_id": {"type": "string", "description": "ID returned by checkpoint_create"}},
            "required": ["checkpoint_id"],
        },
    },
    {
        "name": "checkpoint_list",
        "description": "List all available checkpoints.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "verify_test",
        "description": "Run the test suite and return pass/fail results.",
        "input_schema": {
            "type": "object",
            "properties": {
                "command": {"type": "string", "description": "Test command, defaults to 'go test ./...'"},
                "working_dir": {"type": "string", "description": "Working directory, defaults to '.'"},
            },
        },
    },
]


# ─── Tool Dispatch ───────────────────────────────────────────
def dispatch_tool(name: str, inp: dict) -> str:
    """Route LLM tool calls to PrimitiveBox primitives."""
    try:
        if name == "fs_read":
            result = pb.fs.read(inp["path"])
        elif name == "fs_write":
            result = pb.fs.write(inp["path"], inp["content"])
        elif name == "fs_list":
            result = pb.fs.list(inp.get("path", "."))
        elif name == "fs_search":
            result = pb.fs.search(inp["pattern"], path=inp.get("path"))
        elif name == "shell_exec":
            result = pb.shell.exec(inp["command"])
        elif name == "checkpoint_create":
            result = pb.checkpoint.create(label=inp["label"])
        elif name == "checkpoint_restore":
            result = pb.checkpoint.restore(inp["checkpoint_id"])
        elif name == "checkpoint_list":
            result = pb.checkpoint.list()
        elif name == "verify_test":
            cmd = inp.get("command", "go test -v ./...")
            wd = inp.get("working_dir", ".")
            result = pb.shell.exec(f"cd {wd} && {cmd}")
        else:
            return json.dumps({"error": f"unknown tool: {name}"})

        return json.dumps(result, default=str)
    except Exception as e:
        return json.dumps({"error": str(e)})


# ─── Pretty Print ────────────────────────────────────────────
def print_header(text: str):
    print(f"\n{'─' * 60}")
    print(f"  {text}")
    print(f"{'─' * 60}")


def print_tool_call(name: str, inp: dict, result: str):
    # Primitive name mapping
    prim = name.replace("_", ".")
    inp_short = json.dumps(inp, ensure_ascii=False)
    if len(inp_short) > 120:
        inp_short = inp_short[:117] + "..."
    print(f"  ▶ {prim}({inp_short})")

    res_short = result
    if len(res_short) > 200:
        res_short = res_short[:197] + "..."
    print(f"  ← {res_short}")


# ─── Agent Loop ──────────────────────────────────────────────
def run_agent():
    client = Anthropic()

    task = (
        "You have a buggy Go project at `./buggy_calc/`.\n"
        "Read `./buggy_calc/BUG_REPORT.md` to understand the bugs.\n"
        "Fix each bug following the CVR loop.\n"
        "Start now."
    )

    messages = [{"role": "user", "content": task}]

    print_header("PrimitiveBox Auto-Fix-Bug Demo")
    print(f"  Model:   {MODEL}")
    print(f"  Server:  {PB_HOST}")
    print(f"  Sandbox: {SANDBOX_ID or '(host mode)'}")

    for turn in range(MAX_TURNS):
        print(f"\n=== Turn {turn + 1} ===")

        response = client.messages.create(
            model=MODEL,
            max_tokens=4096,
            system=SYSTEM_PROMPT,
            tools=TOOLS,
            messages=messages,
        )

        assistant_content = response.content
        tool_results = []

        for block in assistant_content:
            if block.type == "text":
                text = block.text.strip()
                if text:
                    # Truncate long agent output for demo readability
                    lines = text.split("\n")
                    for line in lines[:10]:
                        print(f"  [Agent] {line}")
                    if len(lines) > 10:
                        print(f"  [Agent] ... ({len(lines) - 10} more lines)")

            elif block.type == "tool_use":
                result = dispatch_tool(block.name, block.input)
                print_tool_call(block.name, block.input, result)
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": assistant_content})

        if tool_results:
            messages.append({"role": "user", "content": tool_results})
        elif response.stop_reason == "end_turn":
            print_header("Agent Finished")
            break
    else:
        print_header("Max Turns Reached")

    print()


if __name__ == "__main__":
    run_agent()
