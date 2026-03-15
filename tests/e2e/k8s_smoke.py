#!/usr/bin/env python3
"""Minimal Kubernetes sandbox smoke test for PrimitiveBox."""

from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import time
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen


REPO_ROOT = Path(__file__).resolve().parents[2]
PB_BIN = REPO_ROOT / "bin" / "pb"
CONTROLPLANE_DB = Path.home() / ".primitivebox" / "controlplane.db"


def reserve_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def run_json(args: list[str]) -> dict:
    result = subprocess.run(args, check=True, capture_output=True, text=True)
    return json.loads(result.stdout)


def http_json(url: str, payload: dict) -> dict:
    req = Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urlopen(req, timeout=120) as response:
        return json.loads(response.read().decode("utf-8"))


def wait_for_http(url: str, timeout_s: int = 30) -> None:
    deadline = time.time() + timeout_s
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urlopen(url, timeout=5) as response:
                if response.status == 200:
                    return
        except Exception as err:  # pragma: no cover - demo helper
            last_error = err
            time.sleep(0.5)
            continue
    raise RuntimeError(f"server at {url} did not become healthy: {last_error}")


def wait_for_status(sandbox_id: str, expected: str, timeout_s: int = 60) -> dict:
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        sandbox = run_json([str(PB_BIN), "sandbox", "inspect", sandbox_id])
        if sandbox.get("status") == expected:
            return sandbox
        time.sleep(1)
    raise RuntimeError(f"sandbox {sandbox_id} did not reach {expected}")


def main() -> int:
    port = reserve_port()
    server = subprocess.Popen(
        [str(PB_BIN), "server", "start", "--workspace", str(REPO_ROOT), "--port", str(port)],
        cwd=REPO_ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    sandbox_id = ""
    try:
        wait_for_http(f"http://127.0.0.1:{port}/health")
        sandbox = run_json(
            [
                str(PB_BIN),
                "sandbox",
                "create",
                "--driver",
                "kubernetes",
                "--image",
                "primitivebox-sandbox:latest",
                "--namespace",
                "default",
                "--ttl",
                "45",
            ]
        )
        sandbox_id = sandbox["id"]
        print(f"created sandbox: {sandbox_id}")
        wait_for_status(sandbox_id, "running")

        rpc = http_json(
            f"http://127.0.0.1:{port}/sandboxes/{sandbox_id}/rpc",
            {"jsonrpc": "2.0", "method": "fs.list", "params": {"path": "."}, "id": "k8s-smoke"},
        )
        print(json.dumps(rpc, indent=2))

        if CONTROLPLANE_DB.exists():
            print(f"control plane db present: {CONTROLPLANE_DB}")

        print("waiting for TTL reaper...")
        deadline = time.time() + 90
        while time.time() < deadline:
            try:
                run_json([str(PB_BIN), "sandbox", "inspect", sandbox_id])
            except subprocess.CalledProcessError:
                print("sandbox reaped from control plane")
                break
            time.sleep(3)
        return 0
    except HTTPError as err:
        print(err.read().decode("utf-8", errors="replace"), file=sys.stderr)
        return 1
    finally:
        if sandbox_id:
            subprocess.run(
                [str(PB_BIN), "sandbox", "destroy", sandbox_id],
                check=False,
                capture_output=True,
                text=True,
            )
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
