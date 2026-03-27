#!/usr/bin/env python3
"""
Phase 4 data-pack end-to-end smoke test.

Validates the full Boxfile install flow for examples/data-pack in under 60 seconds:

  1. Build pb-data-adapter binary (make build)
  2. pb package install ./examples/data-pack  → local Boxfile install
  3. pb primitives list                        → data.schema / data.query / data.insert visible
  4. data.schema call                          → returns tables (products, orders)
  5. data.query SELECT                         → returns rows from products
  6. data.insert                               → inserts new row, returns last_insert_id
  7. data.query confirms inserted row          → count incremented
  8. pb package remove data-pack              → cleanup

Usage:
    python3 tests/e2e/data_pack_smoke.py

Set PB_ENDPOINT to override the default http://localhost:8080.
Requires a running pb server: ./bin/pb server start --workspace .
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import time
import urllib.request
import urllib.error
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
PB_BIN = REPO_ROOT / "bin" / "pb"
DATA_PACK_DIR = REPO_ROOT / "examples" / "data-pack"
PB_ENDPOINT = os.environ.get("PB_ENDPOINT", "http://localhost:8080")

SEED_SQL = DATA_PACK_DIR / "seed.sql"
DATA_PACK_DB = Path(".primitivebox/.pb/data-pack.db")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def run(args, check=True, capture=True, **kwargs):
    if capture:
        kwargs.setdefault("stdout", subprocess.PIPE)
        kwargs.setdefault("stderr", subprocess.PIPE)
    kwargs.setdefault("text", True)
    result = subprocess.run(args, cwd=str(REPO_ROOT), **kwargs)
    if check and result.returncode != 0:
        print(f"FAIL: {' '.join(str(a) for a in args)}")
        if capture:
            print(f"  stdout: {result.stdout.strip()}")
            print(f"  stderr: {result.stderr.strip()}")
        sys.exit(1)
    return result


def pb(*args, **kwargs):
    cmd = [str(PB_BIN), "--endpoint", PB_ENDPOINT] + [str(a) for a in args]
    return run(cmd, **kwargs)


def rpc_call(method, params=None):
    """POST a JSON-RPC call to /rpc and return the parsed response."""
    body = json.dumps({
        "jsonrpc": "2.0",
        "method": method,
        "params": params or {},
        "id": 1,
    }).encode()
    req = urllib.request.Request(
        f"{PB_ENDPOINT}/rpc",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.loads(resp.read())


def assert_no_rpc_error(resp, label):
    if resp.get("error"):
        print(f"FAIL [{label}]: RPC error {resp['error']}")
        sys.exit(1)


def assert_contains(text, substring, label):
    if substring not in text:
        print(f"FAIL [{label}]: expected {substring!r} in output")
        print(f"  got: {text[:500]}")
        sys.exit(1)
    print(f"  OK [{label}]: contains {substring!r}")


def check_server() -> bool:
    try:
        with urllib.request.urlopen(f"{PB_ENDPOINT}/health", timeout=3) as r:
            return r.status == 200
    except Exception:
        return False


def check_binary(name: str) -> bool:
    return (REPO_ROOT / "bin" / name).is_file()


# ---------------------------------------------------------------------------
# Steps
# ---------------------------------------------------------------------------

def step_build():
    print("\n[1] Building pb-data-adapter...")
    if not check_binary("pb-data-adapter"):
        run(["make", "build"], capture=False)
    if not check_binary("pb-data-adapter"):
        print("FAIL: pb-data-adapter not found after make build")
        sys.exit(1)
    print("  OK: pb-data-adapter built")


def step_seed_database():
    """Pre-populate the SQLite database used by the data adapter."""
    print("\n[2] Seeding SQLite database...")
    import sqlite3

    db_path = REPO_ROOT / ".primitivebox" / ".pb" / "data-pack.db"
    db_path.parent.mkdir(parents=True, exist_ok=True)

    seed = SEED_SQL.read_text()
    conn = sqlite3.connect(str(db_path))
    try:
        conn.executescript(seed)
        conn.commit()
    finally:
        conn.close()
    print(f"  OK: database seeded at {db_path}")


def step_install():
    print("\n[3] Installing data-pack from local Boxfile...")
    result = pb("package", "install", str(DATA_PACK_DIR), check=False)
    if result.returncode != 0:
        stderr = result.stderr.strip()
        if "already installed" in stderr.lower() or "already installed" in result.stdout.lower():
            print("  NOTE: already installed, continuing")
        else:
            print(f"FAIL: install returned {result.returncode}")
            print(f"  stdout: {result.stdout.strip()}")
            print(f"  stderr: {stderr}")
            sys.exit(1)
    else:
        print("  OK: data-pack installed")

    # Wait up to 5s for adapter to register
    deadline = time.time() + 5
    while time.time() < deadline:
        try:
            resp = rpc_call("data.schema")
            if not resp.get("error"):
                print("  OK: data.schema available")
                return
        except Exception:
            pass
        time.sleep(0.5)
    print("FAIL: data.schema did not become available within 5s")
    sys.exit(1)


def step_primitives_list():
    print("\n[4] Verifying primitives are listed...")
    result = pb("primitives", "list")
    out = result.stdout
    assert_contains(out, "data.schema", "primitives list")
    assert_contains(out, "data.query", "primitives list")
    assert_contains(out, "data.insert", "primitives list")
    print("  PASS")


def step_data_schema():
    print("\n[5] data.schema → tables")
    resp = rpc_call("data.schema")
    assert_no_rpc_error(resp, "data.schema")
    tables = resp.get("result", {}).get("tables", [])
    names = [t["name"] for t in tables]
    if "products" not in names or "orders" not in names:
        print(f"FAIL [data.schema]: expected products+orders in {names}")
        sys.exit(1)
    print(f"  OK: tables = {names}")


def step_data_query():
    print("\n[6] data.query → SELECT from products")
    resp = rpc_call("data.query", {"sql": "SELECT id, name, price FROM products ORDER BY id"})
    assert_no_rpc_error(resp, "data.query")
    result = resp.get("result", {})
    rows = result.get("rows", [])
    if len(rows) < 4:
        print(f"FAIL [data.query]: expected >= 4 rows, got {len(rows)}")
        sys.exit(1)
    print(f"  OK: {len(rows)} rows returned, columns = {result.get('columns')}")


def step_data_insert():
    print("\n[7] data.insert → new product")
    resp = rpc_call("data.insert", {
        "table": "products",
        "values": {"name": "Smoke Widget", "price": 1.99, "stock": 42},
    })
    assert_no_rpc_error(resp, "data.insert")
    result = resp.get("result", {})
    if not result.get("inserted"):
        print(f"FAIL [data.insert]: inserted=false in {result}")
        sys.exit(1)
    last_id = result.get("last_insert_id", 0)
    print(f"  OK: inserted row id={last_id}")
    return last_id


def step_verify_insert(last_id: int):
    print(f"\n[8] data.query → confirm inserted row id={last_id}")
    resp = rpc_call("data.query", {
        "sql": "SELECT id, name FROM products WHERE id = ?",
        "args": [str(last_id)],
    })
    assert_no_rpc_error(resp, "data.query after insert")
    rows = resp.get("result", {}).get("rows", [])
    if len(rows) != 1:
        print(f"FAIL [verify insert]: expected 1 row, got {rows}")
        sys.exit(1)
    print(f"  OK: row confirmed: {rows[0]}")


def step_remove():
    print("\n[9] pb package remove data-pack...")
    result = pb("package", "remove", "data-pack", check=False)
    if result.returncode != 0:
        print(f"  WARN: remove failed (non-fatal): {result.stderr.strip()}")
    else:
        print("  OK: data-pack removed")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    print(f"=== data-pack smoke test ===")
    print(f"Endpoint : {PB_ENDPOINT}")
    print(f"Repo root: {REPO_ROOT}")

    if not PB_BIN.is_file():
        print(f"ERROR: pb binary not found at {PB_BIN}. Run: make build")
        sys.exit(1)

    if not check_server():
        print(f"ERROR: pb server not running at {PB_ENDPOINT}")
        print("Start with: ./bin/pb server start --workspace .")
        sys.exit(1)

    step_build()
    step_seed_database()
    step_install()
    step_primitives_list()
    step_data_schema()
    step_data_query()
    last_id = step_data_insert()
    step_verify_insert(last_id)
    step_remove()

    print("\n=== data-pack smoke test PASSED ===")


if __name__ == "__main__":
    main()
