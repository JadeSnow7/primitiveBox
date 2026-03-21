"""
PrimitiveBox Auto-Fix-Bug Demo
==============================
Runs a reliable CVR demo against a live PrimitiveBox server.

Default behavior:
  - Creates a temporary copy of the buggy Go project
  - Starts a local pb server if PB_HOST is not already set
  - Falls back to a deterministic scripted agent when Anthropic is unavailable

Usage:
    python3 examples/auto_fix_bug_demo/run_demo.py

Optional:
    export PB_HOST="http://127.0.0.1:8091"   # use an existing server
    export PB_DEMO_MODE="llm"                # force Anthropic mode
    export ANTHROPIC_API_KEY="sk-ant-..."
"""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path


DEMO_DIR = Path(__file__).resolve().parent
REPO_ROOT = DEMO_DIR.parents[1]
SDK_ROOT = REPO_ROOT / "sdk" / "python"

if str(SDK_ROOT) not in sys.path:
    sys.path.insert(0, str(SDK_ROOT))

from primitivebox import PrimitiveBoxClient  # noqa: E402

try:  # noqa: E402
    from anthropic import Anthropic
except ImportError:  # pragma: no cover - optional dependency for demo mode
    Anthropic = None


PB_HOST = os.getenv("PB_HOST", "").rstrip("/")
PB_DEMO_MODE = os.getenv("PB_DEMO_MODE", "auto")
SANDBOX_ID = os.getenv("PB_SANDBOX_ID", "")
MODEL = os.getenv("MODEL", "claude-sonnet-4-20250514")
MAX_TURNS = 20

PROMPT_PATH = DEMO_DIR / "AGENT_PROMPT.md"
SOURCE_WORKSPACE = DEMO_DIR / "testdata" / "buggy_calc"
DEFAULT_TEST_COMMAND = "go test ./..."

with open(PROMPT_PATH, encoding="utf-8") as f:
    SYSTEM_PROMPT = f.read()

pb: PrimitiveBoxClient | None = None


def reserve_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def build_pb_binary(target_dir: Path) -> Path:
    pb_bin = REPO_ROOT / "bin" / "pb"
    if pb_bin.exists():
        return pb_bin

    built = target_dir / "pb"
    subprocess.run(
        ["go", "build", "-o", str(built), "./cmd/pb"],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    return built


def wait_for_health(endpoint: str, timeout_s: int = 30) -> None:
    deadline = time.time() + timeout_s
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(endpoint + "/health", timeout=5) as response:
                payload = json.loads(response.read().decode("utf-8"))
                if response.status == 200 and payload.get("status") == "ok":
                    return
        except Exception as err:
            last_error = err
            time.sleep(0.25)
    raise RuntimeError(f"PrimitiveBox server at {endpoint} did not become healthy: {last_error}")


def ensure_workspace_root(pb_client: PrimitiveBoxClient) -> None:
    bug_report = unwrap_data(pb_client.fs.read("BUG_REPORT.md"))
    if "BUG-001" not in bug_report.get("content", ""):
        raise RuntimeError("workspace does not look like the buggy_calc demo project")


def start_demo_server(temp_root: Path) -> tuple[str, subprocess.Popen[str] | None, Path | None]:
    if PB_HOST:
        wait_for_health(PB_HOST)
        return PB_HOST, None, None

    workspace = temp_root / "buggy_calc"
    shutil.copytree(SOURCE_WORKSPACE, workspace)

    pb_bin = build_pb_binary(temp_root)
    port = reserve_port()
    endpoint = f"http://127.0.0.1:{port}"
    server = subprocess.Popen(
        [str(pb_bin), "server", "start", "--workspace", str(workspace), "--port", str(port)],
        cwd=REPO_ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    wait_for_health(endpoint)
    return endpoint, server, workspace


def stop_demo_server(server: subprocess.Popen[str] | None) -> None:
    if server is None:
        return
    if server.poll() is None:
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
    if server.stdout is not None:
        output = server.stdout.read().strip()
        if output:
            print(output, file=sys.stderr)


def unwrap_data(result: dict) -> dict:
    data = result.get("data")
    if isinstance(data, dict):
        return data
    return result


def call_primitive(name: str, inp: dict) -> dict:
    assert pb is not None

    if name == "fs_read":
        result = pb.fs.read(inp["path"])
    elif name == "fs_write":
        result = pb.fs.write(inp["path"], inp["content"])
    elif name == "fs_list":
        result = pb.fs.list(inp.get("path", "."))
    elif name == "fs_search":
        result = pb.code.search(inp["pattern"], path=inp.get("path", ""))
    elif name == "shell_exec":
        result = pb.shell.exec(inp["command"])
    elif name == "checkpoint_create":
        result = pb.state.checkpoint(label=inp["label"])
    elif name == "checkpoint_restore":
        result = pb.state.restore(inp["checkpoint_id"])
    elif name == "checkpoint_list":
        result = pb.state.list()
    elif name == "verify_test":
        result = pb.verify.test(inp.get("command", DEFAULT_TEST_COMMAND))
    elif name == "macro_safe_edit":
        result = pb.macro.safe_edit(
            path=inp["path"],
            test_command=inp["test_command"],
            content=inp.get("content", ""),
            mode=inp.get("mode", "overwrite"),
            search=inp.get("search", ""),
            replace=inp.get("replace", ""),
            checkpoint_label=inp.get("checkpoint_label", ""),
        )
    else:
        raise ValueError(f"unknown tool: {name}")

    print_tool_call(name, inp, json.dumps(result, ensure_ascii=False, default=str))
    return result


def dispatch_tool(name: str, inp: dict) -> str:
    try:
        return json.dumps(call_primitive(name, inp), ensure_ascii=False, default=str)
    except Exception as exc:  # pragma: no cover - demo error path
        payload = {"error": str(exc)}
        print_tool_call(name, inp, json.dumps(payload, ensure_ascii=False))
        return json.dumps(payload, ensure_ascii=False)


def read_file(path: str) -> str:
    return unwrap_data(call_primitive("fs_read", {"path": path}))["content"]


def replace_exact(content: str, old: str, new: str) -> str:
    if old not in content:
        raise RuntimeError(f"expected snippet not found: {old!r}")
    return content.replace(old, new, 1)


def expect(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def print_header(text: str) -> None:
    print(f"\n{'─' * 60}")
    print(f"  {text}")
    print(f"{'─' * 60}")


def print_tool_call(name: str, inp: dict, result: str) -> None:
    prim = name.replace("_", ".")
    inp_short = json.dumps(inp, ensure_ascii=False)
    if len(inp_short) > 120:
        inp_short = inp_short[:117] + "..."
    print(f"  ▶ {prim}({inp_short})")

    result_short = result
    if len(result_short) > 240:
        result_short = result_short[:237] + "..."
    print(f"  ← {result_short}")


def emit_status(phase: str, attempt: int, action: str, result: str, hypothesis: str, next_step: str) -> None:
    print(
        json.dumps(
            {
                "phase": phase,
                "attempt": attempt,
                "action": action,
                "result": result,
                "hypothesis": hypothesis,
                "next": next_step,
            },
            ensure_ascii=False,
            indent=2,
        )
    )


def run_scripted_agent() -> None:
    print_header("PrimitiveBox Auto-Fix-Bug Demo")
    print("  Mode:    scripted")
    print(f"  Server:  {pb.endpoint}")
    print(f"  Sandbox: {SANDBOX_ID or '(host mode)'}")

    print_header("Understand")
    bug_report = unwrap_data(call_primitive("fs_read", {"path": "BUG_REPORT.md"}))
    call_primitive("fs_search", {"pattern": "BUG-001", "path": "."})
    call_primitive("fs_search", {"pattern": "BUG-002", "path": "."})
    baseline = unwrap_data(call_primitive("verify_test", {"command": DEFAULT_TEST_COMMAND}))
    expect(baseline.get("passed") is False, "baseline test suite should fail before any fix")
    emit_status(
        "understand",
        1,
        "verify.test",
        "Confirmed the baseline suite is red and both bug reports are present.",
        "BUG-001 is the wrong operator in Add; BUG-002 returns nil on divide-by-zero.",
        "Start manual CVR for BUG-001.",
    )
    _ = bug_report

    print_header("BUG-001 Manual CVR")
    bug_one_bad_cp = unwrap_data(call_primitive("checkpoint_create", {"label": "pre-fix-bug-001-attempt-1"}))
    bug_one_source = read_file("calc.go")
    bug_one_bad_edit = replace_exact(
        bug_one_source,
        "return a - b // BUG-001: should be a + b",
        "return a * b // BUG-001 wrong first attempt",
    )
    call_primitive("fs_write", {"path": "calc.go", "content": bug_one_bad_edit})
    bug_one_bad_verify = unwrap_data(
        call_primitive("verify_test", {"command": "go test -run 'TestAdd|TestAddNegative' ./..."})
    )
    expect(bug_one_bad_verify.get("passed") is False, "first BUG-001 attempt should fail verification")
    emit_status(
        "verify",
        1,
        "verify.test",
        "Targeted Add tests still fail after the wrong mutation.",
        "The first fix changed the operator, but to the wrong one.",
        "Restore the checkpoint before retrying.",
    )
    call_primitive("checkpoint_restore", {"checkpoint_id": bug_one_bad_cp["checkpoint_id"]})
    emit_status(
        "recover",
        1,
        "state.restore",
        "Workspace restored to the pre-fix checkpoint for BUG-001.",
        "A clean rollback is cheaper than reasoning on top of a bad edit.",
        "Retry BUG-001 with the minimal correct operator change.",
    )

    bug_one_good_cp = unwrap_data(call_primitive("checkpoint_create", {"label": "pre-fix-bug-001-attempt-2"}))
    bug_one_restored = read_file("calc.go")
    bug_one_good_edit = replace_exact(
        bug_one_restored,
        "return a - b // BUG-001: should be a + b",
        "return a + b",
    )
    call_primitive("fs_write", {"path": "calc.go", "content": bug_one_good_edit})
    bug_one_good_verify = unwrap_data(
        call_primitive("verify_test", {"command": "go test -run 'TestAdd|TestAddNegative' ./..."})
    )
    expect(bug_one_good_verify.get("passed") is True, "second BUG-001 attempt should pass targeted verification")
    emit_status(
        "verify",
        2,
        "verify.test",
        "Targeted Add tests are green after the minimal fix.",
        "BUG-001 is resolved; BUG-002 is still pending.",
        "Use macro.safe_edit for BUG-002 to demonstrate atomic CVR.",
    )
    _ = bug_one_good_cp

    print_header("BUG-002 Atomic CVR")
    bug_two_source = read_file("calc.go")
    bug_two_bad_edit = replace_exact(
        bug_two_source,
        "return 0, nil // BUG-002: should return error, not nil",
        'return 0, fmt.Errorf("divide by zero")',
    )
    bug_two_bad_result = unwrap_data(
        call_primitive(
            "macro_safe_edit",
            {
                "path": "calc.go",
                "content": bug_two_bad_edit,
                "test_command": "go test -run TestDivideByZero ./...",
                "checkpoint_label": "pre-fix-bug-002-attempt-1",
            },
        )
    )
    expect(bug_two_bad_result.get("passed") is False, "first BUG-002 macro edit should fail")
    expect(bug_two_bad_result.get("rolled_back") is True, "first BUG-002 macro edit should roll back")
    emit_status(
        "recover",
        1,
        "macro.safe_edit",
        "Atomic edit failed verification and rolled back automatically.",
        "The first fix introduced a build error because fmt was not imported.",
        "Retry BUG-002 with errors.New and the full suite as verification.",
    )

    bug_two_restored = read_file("calc.go")
    bug_two_good_edit = replace_exact(
        bug_two_restored,
        "return 0, nil // BUG-002: should return error, not nil",
        'return 0, errors.New("divide by zero")',
    )
    bug_two_good_result = unwrap_data(
        call_primitive(
            "macro_safe_edit",
            {
                "path": "calc.go",
                "content": bug_two_good_edit,
                "test_command": DEFAULT_TEST_COMMAND,
                "checkpoint_label": "pre-fix-bug-002-attempt-2",
            },
        )
    )
    expect(bug_two_good_result.get("passed") is True, "second BUG-002 macro edit should pass")
    final_verify = unwrap_data(call_primitive("verify_test", {"command": DEFAULT_TEST_COMMAND}))
    expect(final_verify.get("passed") is True, "final suite should pass after both fixes")

    print_header("Agent Finished")
    print(
        json.dumps(
            {
                "status": "fixed",
                "bug_id": "BUG-001, BUG-002",
                "root_cause": "Add used subtraction; Divide returned nil on divide-by-zero.",
                "fix_summary": "BUG-001 used manual checkpoint/write/verify/restore; BUG-002 used macro.safe_edit with rollback-on-failure and a final passing edit.",
                "attempts": 4,
                "checkpoints_used": [
                    bug_one_bad_cp["checkpoint_id"],
                    bug_two_bad_result["checkpoint_id"],
                    bug_two_good_result["checkpoint_id"],
                ],
                "tests_passed": True,
                "diff": bug_two_good_result.get("diff", ""),
            },
            ensure_ascii=False,
            indent=2,
        )
    )


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
        "description": "Search workspace files by pattern.",
        "input_schema": {
            "type": "object",
            "properties": {
                "pattern": {"type": "string", "description": "Search pattern"},
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
            },
        },
    },
    {
        "name": "macro_safe_edit",
        "description": "Atomically checkpoint, edit, verify, and auto-restore on failure.",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "content": {"type": "string"},
                "test_command": {"type": "string"},
                "checkpoint_label": {"type": "string"},
            },
            "required": ["path", "content", "test_command"],
        },
    },
]


def run_llm_agent() -> None:
    if Anthropic is None:
        raise RuntimeError("anthropic SDK is not installed")
    if not os.getenv("ANTHROPIC_API_KEY"):
        raise RuntimeError("ANTHROPIC_API_KEY is not set")

    client = Anthropic()
    task = (
        "You have a buggy Go project in the workspace root.\n"
        "Read BUG_REPORT.md to understand BUG-001 and BUG-002.\n"
        "Use manual CVR for BUG-001 and macro.safe_edit for BUG-002.\n"
        "Do not modify tests. Finish only when go test ./... passes.\n"
    )

    messages = [{"role": "user", "content": task}]

    print_header("PrimitiveBox Auto-Fix-Bug Demo")
    print("  Mode:    llm")
    print(f"  Model:   {MODEL}")
    print(f"  Server:  {pb.endpoint}")
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
                    for line in text.splitlines()[:10]:
                        print(f"  [Agent] {line}")
            elif block.type == "tool_use":
                result = dispatch_tool(block.name, block.input)
                tool_results.append(
                    {
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result,
                    }
                )

        messages.append({"role": "assistant", "content": assistant_content})
        if tool_results:
            messages.append({"role": "user", "content": tool_results})
            continue
        if response.stop_reason == "end_turn":
            break


def select_mode() -> str:
    if PB_DEMO_MODE in {"scripted", "llm"}:
        return PB_DEMO_MODE
    if Anthropic is not None and os.getenv("ANTHROPIC_API_KEY"):
        return "llm"
    return "scripted"


def main() -> int:
    global pb

    temp_dir = tempfile.TemporaryDirectory(prefix="primitivebox-auto-fix-demo-")
    temp_root = Path(temp_dir.name)
    server: subprocess.Popen[str] | None = None

    try:
        endpoint, server, _workspace = start_demo_server(temp_root)
        pb = PrimitiveBoxClient(endpoint, sandbox_id=SANDBOX_ID or "")
        ensure_workspace_root(pb)

        mode = select_mode()
        if mode == "llm":
            run_llm_agent()
        else:
            run_scripted_agent()
        return 0
    except (RuntimeError, urllib.error.URLError, ConnectionError) as exc:
        print(f"demo failed: {exc}", file=sys.stderr)
        return 1
    finally:
        stop_demo_server(server)
        temp_dir.cleanup()


if __name__ == "__main__":
    raise SystemExit(main())
