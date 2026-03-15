#!/usr/bin/env python3
"""Postgres-backed readonly database E2E for PrimitiveBox."""

from __future__ import annotations

import json
import os
import socket
import subprocess
import time
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import Request, urlopen


REPO_ROOT = Path(__file__).resolve().parents[2]
PB_BIN = REPO_ROOT / "bin" / "pb"
POSTGRES_IMAGE = os.environ.get("PB_POSTGRES_IMAGE", "postgres:16-alpine")


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
    with urlopen(req, timeout=180) as response:
        return json.loads(response.read().decode("utf-8"))


def get_json(url: str) -> dict:
    with urlopen(url, timeout=30) as response:
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


def docker(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["docker", *args],
        check=check,
        capture_output=True,
        text=True,
    )


def wait_for_postgres(container_name: str, timeout_s: int = 60) -> None:
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        result = docker("exec", container_name, "pg_isready", "-U", "postgres", "-d", "primitivebox", check=False)
        if result.returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError(f"postgres container {container_name} did not become ready")


def main() -> int:
    gateway_port = reserve_port()
    postgres_port = reserve_port()
    container_name = f"primitivebox-e2e-pg-{int(time.time())}"
    server = subprocess.Popen(
        [str(PB_BIN), "server", "start", "--workspace", str(REPO_ROOT), "--port", str(gateway_port)],
        cwd=REPO_ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    sandbox_id = ""
    try:
        wait_for_http(f"http://127.0.0.1:{gateway_port}/health")
        docker(
            "run",
            "--rm",
            "-d",
            "--name",
            container_name,
            "-e",
            "POSTGRES_PASSWORD=primitivebox",
            "-e",
            "POSTGRES_DB=primitivebox",
            "-p",
            f"127.0.0.1:{postgres_port}:5432",
            POSTGRES_IMAGE,
        )
        wait_for_postgres(container_name)

        docker(
            "exec",
            container_name,
            "psql",
            "-U",
            "postgres",
            "-d",
            "primitivebox",
            "-c",
            (
                "DROP TABLE IF EXISTS widgets; "
                "CREATE TABLE widgets(id SERIAL PRIMARY KEY, name TEXT NOT NULL); "
                "INSERT INTO widgets(name) "
                "SELECT 'widget-' || generate_series(1, 250);"
            ),
        )

        sandbox = json.loads(
            subprocess.run(
                [
                    str(PB_BIN),
                    "sandbox",
                    "create",
                    "--driver",
                    "docker",
                    "--image",
                    "primitivebox-sandbox:latest",
                    "--mount",
                    str(REPO_ROOT),
                    "--network-mode",
                    "full",
                ],
                check=True,
                capture_output=True,
                text=True,
            ).stdout
        )
        sandbox_id = sandbox["id"]
        time.sleep(3)

        base_url = f"http://127.0.0.1:{gateway_port}/sandboxes/{sandbox_id}/rpc"
        dsn = f"postgres://postgres:primitivebox@host.docker.internal:{postgres_port}/primitivebox?sslmode=disable"

        normal = rpc(
            base_url,
            "db.query_readonly",
            {
                "connection": {"dialect": "postgres", "dsn": dsn},
                "query": "SELECT * FROM widgets ORDER BY id",
                "max_rows": 500,
            },
        )
        malicious = rpc(
            base_url,
            "db.query_readonly",
            {
                "connection": {"dialect": "postgres", "dsn": dsn},
                "query": "SELECT * FROM widgets; DROP TABLE widgets;",
                "max_rows": 10,
            },
        )
        events_url = (
            f"http://127.0.0.1:{gateway_port}/api/v1/events?"
            + urlencode({"method": "db.query_readonly", "type": "rpc.error", "limit": 5})
        )
        events = get_json(events_url)

        print(json.dumps(normal, indent=2))
        print(json.dumps(malicious, indent=2))
        print(json.dumps(events, indent=2))
        return 0
    finally:
        if sandbox_id:
            subprocess.run(
                [str(PB_BIN), "sandbox", "destroy", sandbox_id],
                check=False,
                capture_output=True,
                text=True,
            )
        docker("rm", "-f", container_name, check=False)
        if server.poll() is None:
            server.terminate()
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server.kill()
        if server.stdout is not None:
            output = server.stdout.read().strip()
            if output:
                print(output)


if __name__ == "__main__":
    raise SystemExit(main())
