#!/usr/bin/env python3
"""Black-box smoke test for the PrimitiveBox Phase 2 app primitive protocol."""

from __future__ import annotations

import json
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.request import urlopen


REPO_ROOT = Path(__file__).resolve().parents[2]


def reserve_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def build_binary(target_dir: Path, name: str, package: str) -> Path:
    built = target_dir / name
    subprocess.run(
        ["go", "build", "-o", str(built), package],
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


def expect(condition: bool, label: str, details: str = "") -> None:
    if condition:
        print(f"[PASS] {label}")
        return
    if details:
        print(f"[FAIL] {label}: {details}", file=sys.stderr)
    else:
        print(f"[FAIL] {label}", file=sys.stderr)
    raise AssertionError(label)


def run_cmd(args: list[str], *, check: bool = True, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(args, cwd=REPO_ROOT, text=True, capture_output=True, env=env, check=False)
    if check and proc.returncode != 0:
        raise RuntimeError(f"command failed: {' '.join(args)}\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}")
    return proc


def wait_for_registration(endpoint: str, timeout_s: int = 20) -> dict:
    deadline = time.time() + timeout_s
    last: dict | None = None
    while time.time() < deadline:
        payload = http_get_json(endpoint + "/app-primitives")
        last = payload
        manifests = payload.get("app_primitives", [])
        names = {item["name"]: item for item in manifests}
        if {"demo.echo", "demo.fail", "demo.set", "demo.state", "demo.verify_set", "demo.rollback_set"} <= names.keys():
            return payload
        time.sleep(0.25)
    raise RuntimeError(f"adapter primitives did not register in time: {json.dumps(last or {})}")


def wait_for_status(endpoint: str, name: str, status: str, timeout_s: int = 20) -> dict:
    deadline = time.time() + timeout_s
    last: dict | None = None
    while time.time() < deadline:
        payload = http_get_json(endpoint + "/app-primitives")
        last = payload
        for item in payload.get("app_primitives", []):
            if item.get("name") == name and item.get("status") == status:
                return item
        time.sleep(0.25)
    raise RuntimeError(f"{name} did not reach status {status}: {json.dumps(last or {})}")


def dump_process_output(name: str, proc: subprocess.Popen[str] | None) -> None:
    if proc is None or proc.stdout is None:
        return
    try:
        output = proc.stdout.read().strip()
    except Exception:
        output = ""
    if output:
        print(f"[{name}]\n{output}", file=sys.stderr)


def main() -> int:
    passed = 0
    with tempfile.TemporaryDirectory(prefix="primitivebox-app-smoke-") as tmp:
        tmp_path = Path(tmp)
        workspace = tmp_path / "workspace"
        apps_dir = tmp_path / "apps"
        logs_dir = tmp_path / "logs"
        data_dir = tmp_path / "data"
        for path in (workspace, apps_dir, logs_dir, data_dir):
            path.mkdir(parents=True, exist_ok=True)

        pb_bin = build_binary(tmp_path, "pb", "./cmd/pb")
        runtime_bin = build_binary(tmp_path, "pb-runtimed", "./cmd/pb-runtimed")
        adapter_bin = build_binary(tmp_path, "pb-test-adapter", "./cmd/pb-test-adapter")
        build_binary(tmp_path, "pb-repo-adapter", "./cmd/pb-repo-adapter")

        port = reserve_port()
        endpoint = f"http://127.0.0.1:{port}"
        socket_path = str(tmp_path / "pb-test-app.sock")
        control_db = data_dir / "control.db"

        runtime_proc = subprocess.Popen(
            [
                str(runtime_bin),
                "--host",
                "127.0.0.1",
                "--port",
                str(port),
                "--workspace",
                str(workspace),
                "--apps-dir",
                str(apps_dir),
                "--log-dir",
                str(logs_dir),
                "--data-dir",
                str(data_dir),
                "--sandbox-id",
                "phase2-smoke",
            ],
            cwd=REPO_ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        adapter_proc: subprocess.Popen[str] | None = None

        try:
            wait_for_http(endpoint + "/health")
            passed += 1
            print("[PASS] pb-runtimed became healthy")

            adapter_proc = subprocess.Popen(
                [
                    str(adapter_bin),
                    "--socket",
                    socket_path,
                    "--rpc-endpoint",
                    endpoint,
                ],
                cwd=REPO_ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )

            manifests = wait_for_registration(endpoint)
            by_name = {item["name"]: item for item in manifests["app_primitives"]}
            expect(by_name["demo.echo"]["status"] == "active", "adapter registration becomes visible via /app-primitives")
            passed += 1

            primitives_payload = http_get_json(endpoint + "/primitives")
            listed = {item["name"]: item for item in primitives_payload["primitives"]}
            expect(listed["demo.echo"]["source"] == "app" and listed["demo.echo"]["status"] == "active", "dynamic app primitives appear in /primitives with active status", json.dumps(listed["demo.echo"]))
            passed += 1

            cli_list = run_cmd([str(pb_bin), "--endpoint", endpoint, "primitives", "list"])
            expect("demo.echo" in cli_list.stdout and "active" in cli_list.stdout and "pb-test-adapter" in cli_list.stdout, "pb primitives list shows app source/status/adapter", cli_list.stdout)
            passed += 1

            cli_schema = run_cmd([str(pb_bin), "--endpoint", endpoint, "primitives", "schema", "demo.set", "--json"])
            schema_doc = json.loads(cli_schema.stdout)
            expect(schema_doc["verify"]["primitive"] == "demo.verify_set" and schema_doc["rollback"]["primitive"] == "demo.rollback_set", "pb primitives schema surfaces verify and rollback declarations", cli_schema.stdout)
            passed += 1

            echo = run_cmd(
                [
                    str(pb_bin),
                    "--endpoint",
                    endpoint,
                    "rpc",
                    "demo.echo",
                    "--params",
                    '{"message":"hello"}',
                ]
            )
            echo_doc = json.loads(echo.stdout)
            expect(echo_doc["message"] == "hello", "pb rpc demo.echo succeeds through the runtime", echo.stdout)
            passed += 1

            fail = run_cmd(
                [
                    str(pb_bin),
                    "--endpoint",
                    endpoint,
                    "rpc",
                    "demo.fail",
                    "--params",
                    '{"reason":"deliberate"}',
                ],
                check=False,
            )
            expect(fail.returncode != 0 and "deliberate failure: deliberate" in fail.stderr, "pb rpc demo.fail returns the deliberate adapter-side failure", fail.stderr)
            passed += 1

            expect(control_db.exists(), "runtime created the SQLite control-plane database", str(control_db))
            with sqlite3.connect(control_db) as conn:
                row = conn.execute("SELECT availability FROM app_primitives WHERE name = ?", ("demo.echo",)).fetchone()
            expect(row is not None and row[0] == "active", "adapter registration is persisted in SQLite", str(row))
            passed += 1

            adapter_proc.terminate()
            adapter_proc.wait(timeout=10)
            adapter_proc = None

            unavailable = run_cmd(
                [
                    str(pb_bin),
                    "--endpoint",
                    endpoint,
                    "rpc",
                    "demo.echo",
                    "--params",
                    '{"message":"again"}',
                ],
                check=False,
            )
            expect(unavailable.returncode != 0 and "adapter pb-test-adapter is unavailable" in unavailable.stderr, "dead adapter fails fast as unavailable", unavailable.stderr)
            passed += 1

            unavailable_manifest = wait_for_status(endpoint, "demo.echo", "unavailable")
            expect(unavailable_manifest["status"] == "unavailable", "unavailable status is reflected in list surfaces", json.dumps(unavailable_manifest))
            passed += 1

            adapter_proc = subprocess.Popen(
                [
                    str(adapter_bin),
                    "--socket",
                    socket_path,
                    "--rpc-endpoint",
                    endpoint,
                ],
                cwd=REPO_ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )
            reactivated = wait_for_status(endpoint, "demo.echo", "active")
            expect(reactivated["status"] == "active", "adapter re-registers and becomes active again", json.dumps(reactivated))
            passed += 1

            echo_again = run_cmd(
                [
                    str(pb_bin),
                    "--endpoint",
                    endpoint,
                    "rpc",
                    "demo.echo",
                    "--params",
                    '{"message":"back"}',
                ]
            )
            echo_again_doc = json.loads(echo_again.stdout)
            expect(echo_again_doc["message"] == "back", "primitive works again after reconnect", echo_again.stdout)
            passed += 1

            print(f"{passed}/12 checks pass")
            return 0
        finally:
            if adapter_proc is not None and adapter_proc.poll() is None:
                adapter_proc.terminate()
                try:
                    adapter_proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    adapter_proc.kill()
            if runtime_proc.poll() is None:
                runtime_proc.terminate()
                try:
                    runtime_proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    runtime_proc.kill()
            dump_process_output("pb-test-adapter", adapter_proc)
            dump_process_output("pb-runtimed", runtime_proc)


if __name__ == "__main__":
    raise SystemExit(main())
