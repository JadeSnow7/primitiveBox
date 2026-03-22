#!/usr/bin/env python3
"""Black-box smoke test for the PrimitiveBox Phase 3 OS adapter process slice."""

from __future__ import annotations

import json
import socket
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


def run_cmd(args: list[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(args, cwd=REPO_ROOT, text=True, capture_output=True, check=False)
    if check and proc.returncode != 0:
        raise RuntimeError(f"command failed: {' '.join(args)}\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}")
    return proc


def wait_for_registration(endpoint: str, timeout_s: int = 20) -> dict:
    deadline = time.time() + timeout_s
    last: dict | None = None
    expected = {
        "process.list",
        "process.spawn",
        "process.wait",
        "process.terminate",
        "process.kill",
    }
    while time.time() < deadline:
        payload = http_get_json(endpoint + "/app-primitives")
        last = payload
        names = {item["name"]: item for item in payload.get("app_primitives", [])}
        if expected <= names.keys():
            return payload
        time.sleep(0.25)
    raise RuntimeError(f"os adapter primitives did not register in time: {json.dumps(last or {})}")


def dump_process_output(name: str, proc: subprocess.Popen[str] | None) -> None:
    if proc is None or proc.stdout is None:
        return
    try:
        output = proc.stdout.read().strip()
    except Exception:
        output = ""
    if output:
        print(f"[{name}]\n{output}", file=sys.stderr)


def parse_rpc_output(proc: subprocess.CompletedProcess[str]) -> dict:
    return json.loads(proc.stdout)


def main() -> int:
    passed = 0
    with tempfile.TemporaryDirectory(prefix="primitivebox-os-smoke-") as tmp:
        tmp_path = Path(tmp)
        workspace = tmp_path / "workspace"
        apps_dir = tmp_path / "apps"
        logs_dir = tmp_path / "logs"
        data_dir = tmp_path / "data"
        for path in (workspace, apps_dir, logs_dir, data_dir):
            path.mkdir(parents=True, exist_ok=True)

        pb_bin = build_binary(tmp_path, "pb", "./cmd/pb")
        runtime_bin = build_binary(tmp_path, "pb-runtimed", "./cmd/pb-runtimed")
        adapter_bin = build_binary(tmp_path, "pb-os-adapter", "./cmd/pb-os-adapter")
        build_binary(tmp_path, "pb-repo-adapter", "./cmd/pb-repo-adapter")

        port = reserve_port()
        endpoint = f"http://127.0.0.1:{port}"
        socket_path = str(tmp_path / "pb-os.sock")

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
                "phase3-os-smoke",
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
            expect(by_name["process.spawn"]["status"] == "active", "os adapter registration becomes visible via /app-primitives")
            passed += 1

            primitives_payload = http_get_json(endpoint + "/primitives")
            listed = {item["name"]: item for item in primitives_payload["primitives"]}
            expect(listed["process.spawn"]["source"] == "app" and listed["process.spawn"]["status"] == "active", "process primitives appear in /primitives with active status", json.dumps(listed["process.spawn"]))
            passed += 1

            cli_list = run_cmd([str(pb_bin), "--endpoint", endpoint, "primitives", "list"])
            expect("process.spawn" in cli_list.stdout and "active" in cli_list.stdout and "pb-os-adapter" in cli_list.stdout, "pb primitives list shows app source/status/adapter for process primitives", cli_list.stdout)
            passed += 1

            short_spawn = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.spawn", "--params", '{"command":["sleep","1"]}',
            ]))
            short_process_id = short_spawn["process_id"]
            wait_short = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.wait", "--params", json.dumps({"process_id": short_process_id, "timeout_s": 5}),
            ]))
            expect(wait_short["exited"] is True and wait_short["timed_out"] is False, "short-lived process exits and wait reports completion", json.dumps(wait_short))
            passed += 1

            long_spawn = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.spawn", "--params", '{"command":["sleep","30"]}',
            ]))
            long_process_id = long_spawn["process_id"]
            wait_timeout = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.wait", "--params", json.dumps({"process_id": long_process_id, "timeout_s": 0.2}),
            ]))
            expect(wait_timeout["timed_out"] is True and wait_timeout["running"] is True, "process.wait reports timeout for a still-running process", json.dumps(wait_timeout))
            passed += 1

            terminated = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.terminate", "--params", json.dumps({"process_id": long_process_id}),
            ]))
            expect(terminated["terminated"] is True and terminated["signal_sent"] == "SIGTERM", "process.terminate sends SIGTERM", json.dumps(terminated))
            passed += 1

            wait_terminated = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.wait", "--params", json.dumps({"process_id": long_process_id, "timeout_s": 5}),
            ]))
            expect(wait_terminated["exited"] is True and wait_terminated.get("signal") == "SIGTERM", "terminated process exits with SIGTERM", json.dumps(wait_terminated))
            passed += 1

            terminated_again = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.terminate", "--params", json.dumps({"process_id": long_process_id}),
            ]))
            expect(terminated_again["already_exited"] is True, "repeated terminate is explicit and idempotent", json.dumps(terminated_again))
            passed += 1

            kill_spawn = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.spawn", "--params", '{"command":["sleep","30"]}',
            ]))
            kill_process_id = kill_spawn["process_id"]
            killed = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.kill", "--params", json.dumps({"process_id": kill_process_id}),
            ]))
            expect(killed["killed"] is True and killed["signal_sent"] == "SIGKILL", "process.kill sends SIGKILL", json.dumps(killed))
            passed += 1

            wait_killed = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.wait", "--params", json.dumps({"process_id": kill_process_id, "timeout_s": 5}),
            ]))
            expect(wait_killed["exited"] is True and wait_killed.get("signal") == "SIGKILL", "killed process exits with SIGKILL", json.dumps(wait_killed))
            passed += 1

            process_list = parse_rpc_output(run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.list", "--params", "{}",
            ]))
            ids = {item["process_id"] for item in process_list["processes"]}
            expect({short_process_id, long_process_id, kill_process_id} <= ids, "process.list returns known adapter-managed processes", json.dumps(process_list))
            passed += 1

            unknown = run_cmd([
                str(pb_bin), "--endpoint", endpoint, "rpc", "process.wait", "--params", '{"process_id":"proc-missing","timeout_s":1}',
            ], check=False)
            expect(unknown.returncode != 0 and "unknown process_id: proc-missing" in unknown.stderr, "unknown process_id fails with invalid-params style error", unknown.stderr)
            passed += 1

            print(f"{passed}/13 checks pass")
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
            dump_process_output("pb-os-adapter", adapter_proc)
            dump_process_output("pb-runtimed", runtime_proc)


if __name__ == "__main__":
    raise SystemExit(main())
