#!/usr/bin/env python3
"""Minimal E2E driver for db.query_readonly against a sandbox."""

from __future__ import annotations

import json
import socket
import sqlite3
import subprocess
import sys
import time
from pathlib import Path
from urllib.request import Request, urlopen


REPO_ROOT = Path(__file__).resolve().parents[2]
PB_BIN = REPO_ROOT / "bin" / "pb"
TEST_DB = REPO_ROOT / "tests" / "e2e" / "sample.db"


def reserve_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def rpc(url: str, method: str, params: dict) -> dict:
    req = Request(
        url,
        data=json.dumps({"jsonrpc": "2.0", "method": method, "params": params, "id": method}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urlopen(req, timeout=120) as response:
        return json.loads(response.read().decode("utf-8"))


def wait_for_http(url: str, timeout_s: int = 30) -> None:
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            with urlopen(url, timeout=5) as response:
                if response.status == 200:
                    return
        except Exception:
            time.sleep(0.5)
    raise RuntimeError(f"server at {url} did not become healthy")


def main() -> int:
    TEST_DB.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(TEST_DB)
    try:
        conn.executescript(
            "DROP TABLE IF EXISTS widgets;"
            "CREATE TABLE widgets(id INTEGER PRIMARY KEY, name TEXT);"
            "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x < 250) "
            "INSERT INTO widgets(name) SELECT 'widget-' || x FROM cnt;"
        )
        conn.commit()
    finally:
        conn.close()

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

        sandbox = json.loads(
            subprocess.run(
                [str(PB_BIN), "sandbox", "create", "--driver", "docker", "--image", "primitivebox-sandbox:latest", "--mount", str(REPO_ROOT)],
                check=True,
                capture_output=True,
                text=True,
            ).stdout
        )
        sandbox_id = sandbox["id"]
        time.sleep(3)

        base_url = f"http://127.0.0.1:{port}/sandboxes/{sandbox_id}/rpc"
        print(
            json.dumps(
                rpc(
                    base_url,
                    "db.query_readonly",
                    {
                        "connection": {"dialect": "sqlite", "path": "tests/e2e/sample.db"},
                        "query": "SELECT * FROM widgets ORDER BY id",
                        "max_rows": 100,
                    },
                ),
                indent=2,
            )
        )
        print(
            json.dumps(
                rpc(
                    base_url,
                    "db.query_readonly",
                    {
                        "connection": {"dialect": "sqlite", "path": "tests/e2e/sample.db"},
                        "query": "SELECT * FROM widgets; DROP TABLE widgets;",
                        "max_rows": 100,
                    },
                ),
                indent=2,
            )
        )
        return 0
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
