#!/usr/bin/env python3
"""Black-box smoke test for the PrimitiveBox Phase 1 host runtime."""

from __future__ import annotations

import json
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.request import Request, urlopen


REPO_ROOT = Path(__file__).resolve().parents[2]


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


def wait_for_http(url: str, timeout_s: int = 30) -> None:
    deadline = time.time() + timeout_s
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urlopen(url, timeout=5) as response:
                if response.status == 200:
                    return
        except Exception as err:
            last_error = err
            time.sleep(0.25)
    raise RuntimeError(f"server at {url} did not become healthy: {last_error}")


def http_get_json(url: str) -> dict:
    with urlopen(url, timeout=30) as response:
        return json.loads(response.read().decode("utf-8"))


def rpc(url: str, method: str, params: dict) -> dict:
    req = Request(
        url,
        data=json.dumps({"jsonrpc": "2.0", "method": method, "params": params, "id": method}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urlopen(req, timeout=60) as response:
        return json.loads(response.read().decode("utf-8"))


def expect(condition: bool, label: str, details: str = "") -> None:
    if condition:
        print(f"[PASS] {label}")
        return
    if details:
        print(f"[FAIL] {label}: {details}", file=sys.stderr)
    else:
        print(f"[FAIL] {label}", file=sys.stderr)
    raise AssertionError(label)


def main() -> int:
    passed = 0
    with tempfile.TemporaryDirectory(prefix="primitivebox-smoke-") as tmp:
        tmp_path = Path(tmp)
        workspace = tmp_path / "workspace"
        workspace.mkdir(parents=True, exist_ok=True)
        sample_file = workspace / "hello.txt"
        sample_file.write_text("original content\n", encoding="utf-8")

        pb_bin = build_pb_binary(tmp_path)
        port = reserve_port()
        endpoint = f"http://127.0.0.1:{port}"

        server = subprocess.Popen(
            [str(pb_bin), "server", "start", "--workspace", str(workspace), "--port", str(port)],
            cwd=REPO_ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )

        try:
            wait_for_http(endpoint + "/health")

            health = http_get_json(endpoint + "/health")
            expect(health.get("status") == "ok", "GET /health returns status=ok", json.dumps(health))
            passed += 1

            read_before = rpc(endpoint + "/rpc", "fs.read", {"path": "hello.txt"})
            read_before_content = read_before["result"]["data"]["content"]
            expect("original content" in read_before_content, "fs.read returns known file content", json.dumps(read_before))
            passed += 1

            checkpoint = rpc(endpoint + "/rpc", "state.checkpoint", {"label": "smoke-pre-write"})
            checkpoint_id = checkpoint["result"]["data"]["checkpoint_id"]
            expect(bool(checkpoint_id), "state.checkpoint returns a checkpoint id", json.dumps(checkpoint))
            passed += 1

            write_result = rpc(
                endpoint + "/rpc",
                "fs.write",
                {"path": "hello.txt", "content": "mutated content\n"},
            )
            bytes_written = write_result["result"]["data"]["bytes_written"]
            expect(bytes_written > 0, "fs.write mutates the file", json.dumps(write_result))
            passed += 1

            verify = rpc(
                endpoint + "/rpc",
                "verify.test",
                {"command": "grep -q '^mutated content$' hello.txt"},
            )
            verify_data = verify["result"]["data"]
            expect(isinstance(verify_data.get("passed"), bool), "verify.test returns structured pass/fail", json.dumps(verify))
            passed += 1

            restore = rpc(endpoint + "/rpc", "state.restore", {"checkpoint_id": checkpoint_id})
            restored_to = restore["result"]["data"]["restored_to"]
            expect(bool(restored_to), "state.restore succeeds", json.dumps(restore))
            passed += 1

            read_after = rpc(endpoint + "/rpc", "fs.read", {"path": "hello.txt"})
            restored_content = read_after["result"]["data"]["content"]
            expect("original content" in restored_content, "fs.read returns original content after restore", json.dumps(read_after))
            passed += 1

            expect(verify_data["passed"] is True and restored_content != "mutated content\n", "restore undoes the verified mutation")
            passed += 1

            print(f"{passed}/8 checks pass")
            return 0
        finally:
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


if __name__ == "__main__":
    raise SystemExit(main())
