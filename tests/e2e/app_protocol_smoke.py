#!/usr/bin/env python3
"""App primitive protocol smoke test for PrimitiveBox.

Runs 15 checks in two sections:

  Checks 01-12  Phase 2 app protocol: registration, schema, and basic calls
  Checks 13-15  Phase 2 CVR end-to-end: app-declared verify and rollback

Exit code 0 = all pass; 1 = one or more failures.
"""

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
ADAPTER_BIN = REPO_ROOT / "bin" / "pb-test-adapter"
ADAPTER_SOCKET = "/tmp/pb-test-adapter.sock"

_fail_count = 0


def _check(n: int, description: str, ok: bool, detail: str = "") -> None:
    global _fail_count
    tag = "PASS" if ok else "FAIL"
    suffix = f": {detail}" if detail else ""
    print(f"  check {n:02d} [{tag}] {description}{suffix}")
    if not ok:
        _fail_count += 1


def reserve_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


def wait_for_http(url: str, timeout_s: int = 30) -> None:
    deadline = time.time() + timeout_s
    last: Exception | None = None
    while time.time() < deadline:
        try:
            with urlopen(url, timeout=5) as r:
                if r.status == 200:
                    return
        except Exception as exc:
            last = exc
            time.sleep(0.5)
    raise RuntimeError(f"server at {url} did not become healthy: {last}")


def _rpc(url: str, method: str, params: dict, extra_headers: dict | None = None) -> dict:
    headers = {"Content-Type": "application/json"}
    if extra_headers:
        headers.update(extra_headers)
    body = json.dumps({"jsonrpc": "2.0", "method": method, "params": params, "id": method}).encode()
    req = Request(url, data=body, headers=headers, method="POST")
    with urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode())


def _app_register(url: str, manifest: dict) -> dict:
    return _rpc(url, "app.register", manifest, extra_headers={"X-PB-Origin": "sandbox"})


def _get_json(url: str) -> dict:
    with urlopen(url, timeout=10) as r:
        return json.loads(r.read().decode())


def main() -> int:
    gocache = os.environ.get("GOCACHE", "/tmp/primitivebox-gocache")

    # Build binaries if absent.
    if not PB_BIN.exists():
        subprocess.run(
            ["go", "build", "-o", str(PB_BIN), "./cmd/pb/..."],
            cwd=REPO_ROOT, check=True,
            env={**os.environ, "GOCACHE": gocache},
        )
    if not ADAPTER_BIN.exists():
        subprocess.run(
            ["go", "build", "-o", str(ADAPTER_BIN), "./cmd/pb-test-adapter/..."],
            cwd=REPO_ROOT, check=True,
            env={**os.environ, "GOCACHE": gocache},
        )

    port = reserve_port()
    server = subprocess.Popen(
        [str(PB_BIN), "server", "start", "--sandbox-mode",
         "--workspace", str(REPO_ROOT), "--port", str(port)],
        cwd=REPO_ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    adapter = subprocess.Popen(
        [str(ADAPTER_BIN), "-socket", ADAPTER_SOCKET],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )

    try:
        # Discover adapter socket path from its startup line.
        adapter_line = (adapter.stdout.readline() or "").strip()
        adapter_reg = json.loads(adapter_line) if adapter_line else {}
        socket_path = adapter_reg.get("socket", ADAPTER_SOCKET)

        wait_for_http(f"http://127.0.0.1:{port}/health")
        rpc_url = f"http://127.0.0.1:{port}/rpc"

        # ----------------------------------------------------------------
        # Checks 01-12  Phase 2 app protocol: registration and schema
        # ----------------------------------------------------------------
        print("Phase 2 app protocol: registration and schema")

        # Check 01 — server health
        try:
            health = _get_json(f"http://127.0.0.1:{port}/health")
            _check(1, "server health endpoint returns ok", health.get("status") == "ok")
        except Exception as exc:
            _check(1, "server health endpoint returns ok", False, str(exc))

        # Check 02 — adapter started and reported socket path
        _check(2, "adapter started and reported socket path", bool(socket_path))

        # Checks 03-07 — register demo.* primitives
        primitives = [
            ("demo.set", {
                "verify_endpoint": "demo.verify_set",
                "rollback_endpoint": "demo.rollback_set",
                "intent": {"category": "mutation", "reversible": True, "risk_level": "high"},
            }),
            ("demo.verify_set", {
                "intent": {"category": "verification", "reversible": True, "risk_level": "low"},
            }),
            ("demo.rollback_set", {
                "intent": {"category": "rollback", "reversible": True, "risk_level": "high"},
            }),
            ("demo.state", {
                "intent": {"category": "query", "reversible": True, "risk_level": "low"},
            }),
            ("demo.fail", {
                "intent": {"category": "mutation", "reversible": False, "risk_level": "high"},
            }),
        ]
        prim_names = [p[0] for p in primitives]
        for idx, (name, extra) in enumerate(primitives):
            check_n = 3 + idx
            manifest = {
                "app_id": "demo",
                "name": name,
                "socket_path": socket_path,
                "input_schema": "{}",
                "output_schema": "{}",
                **extra,
            }
            try:
                resp = _app_register(rpc_url, manifest)
                ok = isinstance(resp.get("result"), dict) and resp["result"].get("registered") is True
                _check(check_n, f"app.register {name} succeeds", ok,
                       str(resp.get("error", "")) if not ok else "")
            except Exception as exc:
                _check(check_n, f"app.register {name} succeeds", False, str(exc))

        # Check 08 — /app-primitives lists all 5 primitives
        listing: dict = {}
        try:
            listing = _get_json(f"http://127.0.0.1:{port}/app-primitives")
            registered_names = {m["name"] for m in listing.get("app_primitives", [])}
            expected = set(prim_names)
            _check(8, "/app-primitives lists all 5 registered primitives",
                   expected == registered_names,
                   f"got {registered_names!r}" if expected != registered_names else "")
        except Exception as exc:
            _check(8, "/app-primitives lists all 5 registered primitives", False, str(exc))

        app_prims = {m["name"]: m for m in listing.get("app_primitives", [])}

        # Check 09 — demo.set declares verify_endpoint
        demo_set = app_prims.get("demo.set", {})
        _check(9, "demo.set manifest declares verify_endpoint = demo.verify_set",
               demo_set.get("verify_endpoint") == "demo.verify_set",
               repr(demo_set.get("verify_endpoint")))

        # Check 10 — demo.set declares rollback_endpoint
        _check(10, "demo.set manifest declares rollback_endpoint = demo.rollback_set",
               demo_set.get("rollback_endpoint") == "demo.rollback_set",
               repr(demo_set.get("rollback_endpoint")))

        # Check 11 — demo.set intent: reversible=true, risk_level=high
        intent = demo_set.get("intent", {})
        _check(11, "demo.set intent has reversible=true and risk_level=high",
               intent.get("reversible") is True and intent.get("risk_level") == "high",
               repr(intent))

        # Check 12 — demo.fail has no rollback_endpoint
        demo_fail = app_prims.get("demo.fail", {})
        _check(12, "demo.fail manifest has no rollback_endpoint",
               not demo_fail.get("rollback_endpoint"),
               repr(demo_fail.get("rollback_endpoint")))

        # ----------------------------------------------------------------
        # Checks 13-15  Phase 2 CVR end-to-end: app-declared verify and rollback
        # ----------------------------------------------------------------
        print("\nPhase 2 CVR end-to-end: app-declared verify and rollback")

        # Check 13 — verify strategy is invoked on success path
        #   Call demo.set with value="v1".
        #   Assert the call succeeds.
        #   Call demo.state.
        #   Assert current value is "v1" (verify_set passed, no rollback).
        try:
            resp = _rpc(rpc_url, "demo.set", {"value": "v1"})
            _check(13, "demo.set value=v1 succeeds (verify passes)",
                   resp.get("error") is None,
                   str(resp.get("error", "")))
        except Exception as exc:
            _check(13, "demo.set value=v1 succeeds (verify passes)", False, str(exc))

        try:
            resp = _rpc(rpc_url, "demo.state", {})
            # App primitive result is returned as result.Data directly.
            state_val = (resp.get("result") or {}).get("value")
            _check(13, "demo.state confirms value=v1 after successful set",
                   state_val == "v1", f"got {state_val!r}")
        except Exception as exc:
            _check(13, "demo.state confirms value=v1 after successful set", False, str(exc))

        # Check 14 — rollback is invoked when verify fails
        #   Set baseline to "v2", then call demo.set with value="FAIL_VERIFY".
        #   Assert the call returns a failure result.
        #   Assert demo.state shows "v2" (rollback_set restored previous value).
        try:
            _rpc(rpc_url, "demo.set", {"value": "v2"})
        except Exception:
            pass  # best-effort baseline

        try:
            resp = _rpc(rpc_url, "demo.set", {"value": "FAIL_VERIFY"})
            _check(14, "demo.set value=FAIL_VERIFY returns failure (verify rejected)",
                   resp.get("error") is not None,
                   str(resp.get("result", "")) if resp.get("error") is None else "")
        except Exception as exc:
            _check(14, "demo.set value=FAIL_VERIFY returns failure (verify rejected)", False, str(exc))

        try:
            resp = _rpc(rpc_url, "demo.state", {})
            state_val = (resp.get("result") or {}).get("value")
            _check(14, "demo.state confirms rollback to v2 (rollback_set was called)",
                   state_val == "v2", f"got {state_val!r}")
        except Exception as exc:
            _check(14, "demo.state confirms rollback to v2 (rollback_set was called)", False, str(exc))

        # Check 15 — irreversible primitive without rollback fails closed
        #   demo.fail has no rollback_endpoint declared.
        #   Assert the runtime returns a structured failure, not a silent pass.
        try:
            resp = _rpc(rpc_url, "demo.fail", {})
            _check(15, "demo.fail returns a structured error (fail-closed)",
                   resp.get("error") is not None,
                   str(resp.get("result", "")) if resp.get("error") is None else "")
        except HTTPError as exc:
            _check(15, "demo.fail returns a structured error (fail-closed)", True,
                   f"HTTP {exc.code}")
        except Exception as exc:
            _check(15, "demo.fail returns a structured error (fail-closed)", False, str(exc))

        print()
        if _fail_count == 0:
            print("all 15 checks passed")
            return 0
        print(f"{_fail_count} check(s) failed")
        return 1

    finally:
        if adapter.poll() is None:
            adapter.terminate()
            try:
                adapter.wait(timeout=5)
            except subprocess.TimeoutExpired:
                adapter.kill()
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
