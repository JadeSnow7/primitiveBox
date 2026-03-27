#!/usr/bin/env python3
"""
Smoke test for pb-mcp-bridge.

Starts an in-process Python mock MCP server (Content-Length framed stdio),
runs pb-mcp-bridge against it, and verifies end-to-end registration + dispatch.

Checks (7 total):
  1. pb-runtimed becomes healthy
  2. mcp.test.echo appears in /app-primitives and is active
  3. intent.category = mutation
  4. intent.reversible = false
  5. intent.risk_level = high
  6. mcp.test.echo call returns no error
  7. mcp.test.echo call returns echoed message
"""

from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import tempfile
import textwrap
import time
from pathlib import Path
from urllib.request import urlopen

REPO_ROOT = Path(__file__).resolve().parents[2]

# ---------------------------------------------------------------------------
# Inline mock MCP server — speaks Content-Length framed JSON-RPC to stdio
# ---------------------------------------------------------------------------

MOCK_MCP_SERVER = textwrap.dedent("""\
    import sys
    import json

    def write_msg(obj):
        body = json.dumps(obj).encode()
        sys.stdout.buffer.write(
            f"Content-Length: {len(body)}\\r\\n\\r\\n".encode() + body
        )
        sys.stdout.buffer.flush()

    def read_msg():
        header = b""
        while not header.endswith(b"\\r\\n\\r\\n"):
            ch = sys.stdin.buffer.read(1)
            if not ch:
                sys.exit(0)
            header += ch
        length = int(
            header.decode().split("Content-Length:")[1].split("\\r\\n")[0].strip()
        )
        return json.loads(sys.stdin.buffer.read(length))

    while True:
        msg = read_msg()
        method = msg.get("method", "")
        if method == "initialize":
            write_msg({
                "jsonrpc": "2.0",
                "id": msg["id"],
                "result": {
                    "protocolVersion": "2024-11-05",
                    "serverInfo": {"name": "test", "version": "0.1.0"},
                    "capabilities": {},
                },
            })
        elif method == "notifications/initialized":
            pass
        elif method == "tools/list":
            write_msg({
                "jsonrpc": "2.0",
                "id": msg["id"],
                "result": {
                    "tools": [
                        {
                            "name": "echo",
                            "description": "Echo back input",
                            "inputSchema": {
                                "type": "object",
                                "properties": {"message": {"type": "string"}},
                                "required": ["message"],
                            },
                        }
                    ]
                },
            })
        elif method == "tools/call":
            args = msg["params"]["arguments"]
            write_msg({
                "jsonrpc": "2.0",
                "id": msg["id"],
                "result": {
                    "content": [{"type": "text", "text": args.get("message", "")}]
                },
            })
""")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def reserve_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


def build_binary(target_dir: Path, name: str, package: str) -> Path:
    out = target_dir / name
    subprocess.run(
        ["go", "build", "-o", str(out), package],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    return out


def wait_for_http(url: str, timeout_s: int = 30) -> None:
    deadline = time.time() + timeout_s
    last_err: Exception | None = None
    while time.time() < deadline:
        try:
            with urlopen(url, timeout=5) as r:
                if r.status == 200:
                    return
        except Exception as e:
            last_err = e
            time.sleep(0.25)
    raise RuntimeError(f"server at {url} did not become healthy: {last_err}")


def http_get_json(url: str) -> dict:
    with urlopen(url, timeout=30) as r:
        return json.loads(r.read().decode())


def expect(cond: bool, label: str, details: str = "") -> None:
    if cond:
        print(f"[PASS] {label}")
        return
    msg = f"[FAIL] {label}"
    if details:
        msg += f": {details}"
    print(msg, file=sys.stderr)
    raise AssertionError(label)


def wait_for_primitive(endpoint: str, name: str, timeout_s: int = 20) -> dict:
    """Poll /app-primitives until `name` appears, then return its record."""
    deadline = time.time() + timeout_s
    last: dict | None = None
    while time.time() < deadline:
        payload = http_get_json(endpoint + "/app-primitives")
        last = payload
        by_name = {item["name"]: item for item in payload.get("app_primitives", [])}
        if name in by_name:
            return by_name[name]
        time.sleep(0.25)
    raise RuntimeError(
        f"primitive {name!r} did not appear after {timeout_s}s: "
        f"{json.dumps(last or {})}"
    )


def call_via_socket(socket_path: str, method: str, params: dict) -> dict:
    """Send a JSON-RPC request over the bridge Unix socket."""
    req = json.dumps({"id": 1, "method": method, "params": params}) + "\n"
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.settimeout(10)
        s.connect(socket_path)
        s.sendall(req.encode())
        data = b""
        while not data.endswith(b"\n"):
            chunk = s.recv(4096)
            if not chunk:
                break
            data += chunk
    return json.loads(data.strip())


def dump_output(name: str, proc: subprocess.Popen | None) -> None:
    if proc is None or proc.stdout is None:
        return
    try:
        out = proc.stdout.read().strip()
    except Exception:
        out = ""
    if out:
        print(f"[{name}]\n{out}", file=sys.stderr)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    passed = 0
    with tempfile.TemporaryDirectory(prefix="primitivebox-mcp-smoke-") as tmp:
        tmp_path = Path(tmp)
        workspace = tmp_path / "workspace"
        apps_dir = tmp_path / "apps"
        logs_dir = tmp_path / "logs"
        data_dir = tmp_path / "data"
        mock_script = tmp_path / "mock_mcp_server.py"
        for d in (workspace, apps_dir, logs_dir, data_dir):
            d.mkdir(parents=True, exist_ok=True)

        mock_script.write_text(MOCK_MCP_SERVER)

        runtime_bin = build_binary(tmp_path, "pb-runtimed", "./cmd/pb-runtimed")
        bridge_bin = build_binary(tmp_path, "pb-mcp-bridge", "./cmd/pb-mcp-bridge")

        port = reserve_port()
        endpoint = f"http://127.0.0.1:{port}"
        socket_path = str(tmp_path / "pb-mcp.sock")

        runtime_proc = subprocess.Popen(
            [
                str(runtime_bin),
                "--host", "127.0.0.1",
                "--port", str(port),
                "--workspace", str(workspace),
                "--apps-dir", str(apps_dir),
                "--log-dir", str(logs_dir),
                "--data-dir", str(data_dir),
                "--sandbox-id", "mcp-bridge-smoke",
            ],
            cwd=REPO_ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        bridge_proc: subprocess.Popen | None = None

        try:
            # Check 1
            wait_for_http(endpoint + "/health")
            passed += 1
            print("[PASS] pb-runtimed became healthy")

            bridge_proc = subprocess.Popen(
                [
                    str(bridge_bin),
                    "--socket", socket_path,
                    "--rpc-endpoint", endpoint,
                    "--app-id", "pb-mcp-bridge",
                    "--",
                    sys.executable,
                    str(mock_script),
                ],
                cwd=REPO_ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )

            # Check 2: primitive registered and active
            mcp_echo = wait_for_primitive(endpoint, "mcp.test.echo")
            expect(
                mcp_echo.get("status") == "active",
                "mcp.test.echo registered and active",
                json.dumps(mcp_echo),
            )
            passed += 1

            intent = mcp_echo.get("intent", {})

            # Check 3: category = mutation
            expect(
                intent.get("category") == "mutation",
                "mcp.test.echo intent.category = mutation",
                json.dumps(intent),
            )
            passed += 1

            # Check 4: reversible = false  (CVR auto-checkpoints)
            expect(
                intent.get("reversible") is False,
                "mcp.test.echo intent.reversible = false",
                json.dumps(intent),
            )
            passed += 1

            # Check 5: risk_level = high  (forces Reviewer Gate)
            expect(
                intent.get("risk_level") == "high",
                "mcp.test.echo intent.risk_level = high",
                json.dumps(intent),
            )
            passed += 1

            # Checks 6–7: call the tool via the bridge's Unix socket
            resp = call_via_socket(
                socket_path,
                "mcp.test.echo",
                {"message": "hello-from-smoke"},
            )

            # Check 6: no RPC error
            expect(
                resp.get("error") is None,
                "mcp.test.echo call returns no error",
                json.dumps(resp),
            )
            passed += 1

            # Check 7: echoed message is correct
            result = resp.get("result", {})
            content = result.get("content", []) if isinstance(result, dict) else []
            expect(
                len(content) > 0 and content[0].get("text") == "hello-from-smoke",
                "mcp.test.echo returns echoed message",
                json.dumps(resp),
            )
            passed += 1

            print(f"\n{passed}/7 checks pass")
            return 0

        finally:
            if bridge_proc is not None and bridge_proc.poll() is None:
                bridge_proc.terminate()
                try:
                    bridge_proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    bridge_proc.kill()
            if runtime_proc.poll() is None:
                runtime_proc.terminate()
                try:
                    runtime_proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    runtime_proc.kill()
            dump_output("pb-mcp-bridge", bridge_proc)
            dump_output("pb-runtimed", runtime_proc)


if __name__ == "__main__":
    raise SystemExit(main())
