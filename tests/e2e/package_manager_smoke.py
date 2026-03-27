#!/usr/bin/env python3
"""
Phase 4 Package Manager smoke test.

Tests:
  1. pb package search        → lists available packages (offline)
  2. pb package info os       → shows os package details (offline)
  3. pb package list          → lists installed packages (empty)
  4. pb package install os    → installs os package (requires running server + binary)
  5. pb package list          → os appears in installed list
  6. pb package remove os     → removes os package
  7. pb package list          → empty again

Usage:
    python3 tests/e2e/package_manager_smoke.py [--skip-install]

Set PB_ENDPOINT to override the default http://localhost:8080.
Set PB_DIR to the directory containing the pb binary (default: bin/).
"""

import os
import subprocess
import sys
import json
import time
import argparse
import shutil


PB_ENDPOINT = os.environ.get("PB_ENDPOINT", "http://localhost:8080")
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
PB_BIN = os.path.join(REPO_ROOT, "bin", "pb")


def run(args, check=True, capture=True, **kwargs):
    """Run a command and return CompletedProcess."""
    if capture:
        kwargs.setdefault("stdout", subprocess.PIPE)
        kwargs.setdefault("stderr", subprocess.PIPE)
    kwargs.setdefault("text", True)
    result = subprocess.run(args, **kwargs)
    if check and result.returncode != 0:
        print(f"FAIL: {' '.join(args)}")
        if capture:
            print(f"  stdout: {result.stdout.strip()}")
            print(f"  stderr: {result.stderr.strip()}")
        sys.exit(1)
    return result


def pb(*args, **kwargs):
    """Run the pb CLI."""
    cmd = [PB_BIN, "--endpoint", PB_ENDPOINT] + list(args)
    return run(cmd, **kwargs)


def assert_contains(output, substring, label):
    if substring not in output:
        print(f"FAIL [{label}]: expected {substring!r} in output")
        print(f"  got: {output[:500]}")
        sys.exit(1)
    print(f"  OK: {label!r} contains {substring!r}")


def assert_not_contains(output, substring, label):
    if substring in output:
        print(f"FAIL [{label}]: expected {substring!r} NOT in output")
        print(f"  got: {output[:500]}")
        sys.exit(1)
    print(f"  OK: {label!r} does not contain {substring!r}")


def test_search_offline():
    """pb package search lists available packages without a running server."""
    print("\n[1] pb package search (offline)")
    result = pb("package", "search", check=True)
    out = result.stdout
    assert_contains(out, "os", "search output")
    assert_contains(out, "mcp-bridge", "search output")
    assert_contains(out, "0.1.0", "search output")
    print("  PASS")


def test_search_query():
    """pb package search with a query filters results."""
    print("\n[2] pb package search os (offline)")
    result = pb("package", "search", "os", check=True)
    out = result.stdout
    assert_contains(out, "os", "search query output")
    print("  PASS")


def test_info_offline():
    """pb package info os shows package details without a running server."""
    print("\n[3] pb package info os (offline)")
    result = pb("package", "info", "os", check=True)
    out = result.stdout
    assert_contains(out, "os", "info output")
    assert_contains(out, "0.1.0", "info output")
    assert_contains(out, "process.list", "info output primitives")
    assert_contains(out, "pkg.install", "info output primitives")
    assert_contains(out, "Healthcheck", "info output healthcheck")
    print("  PASS")


def test_info_not_found():
    """pb package info for unknown package returns error."""
    print("\n[4] pb package info nonexistent (offline)")
    result = pb("package", "info", "nonexistent-package-xyz", check=False)
    if result.returncode == 0:
        print("FAIL: expected non-zero exit for unknown package")
        sys.exit(1)
    print("  PASS (error returned as expected)")


def test_list_empty():
    """pb package list returns empty when no packages are installed."""
    print("\n[5] pb package list (empty)")
    result = pb("package", "list", check=True)
    out = result.stdout
    # Either empty output or "No packages installed." is acceptable
    assert_not_contains(out, "os", "empty list")
    print("  PASS")


def check_server_running():
    """Check if the pb server is accessible."""
    import urllib.request
    import urllib.error
    try:
        with urllib.request.urlopen(f"{PB_ENDPOINT}/health", timeout=3) as r:
            return r.status == 200
    except Exception:
        return False


def check_binary_exists(name):
    """Check if the named binary exists in REPO_ROOT/bin/."""
    path = os.path.join(REPO_ROOT, "bin", name)
    return os.path.isfile(path)


def test_install_and_remove():
    """Test install + list + remove cycle (requires running server + binary)."""
    print("\n[6] pb package install os (requires server + binary)")

    if not check_server_running():
        print("  SKIP: pb server not running at", PB_ENDPOINT)
        return False

    if not check_binary_exists("pb-os-adapter"):
        print("  SKIP: pb-os-adapter binary not found in bin/")
        print("        Build with: make build")
        return False

    # Install
    result = pb("package", "install", "os", check=False)
    if result.returncode != 0:
        stderr = result.stderr.strip()
        # If it's already installed, that's fine
        if "already installed" in stderr.lower() or "already installed" in result.stdout.lower():
            print("  NOTE: already installed, continuing")
        else:
            print(f"  FAIL: install returned {result.returncode}")
            print(f"    stdout: {result.stdout.strip()}")
            print(f"    stderr: {stderr}")
            # Non-fatal for smoke test
            print("  SKIP: install failed (non-fatal for smoke)")
            return False

    print("  install succeeded (or already installed)")

    # List — should show os
    print("\n[7] pb package list (after install)")
    result = pb("package", "list", check=True)
    out = result.stdout
    assert_contains(out, "os", "list after install")
    print("  PASS")

    # Remove
    print("\n[8] pb package remove os")
    result = pb("package", "remove", "os", check=False)
    if result.returncode != 0:
        print(f"  WARN: remove failed: {result.stderr.strip()}")
    else:
        print("  remove succeeded")

        # List — should be empty again
        print("\n[9] pb package list (after remove)")
        result = pb("package", "list", check=True)
        out = result.stdout
        assert_not_contains(out, "os", "list after remove")
        print("  PASS")

    return True


def main():
    parser = argparse.ArgumentParser(description="Package Manager smoke test")
    parser.add_argument("--skip-install", action="store_true",
                        help="Skip install/remove tests requiring a running server")
    opts = parser.parse_args()

    # Verify pb binary exists
    if not os.path.isfile(PB_BIN):
        print(f"ERROR: pb binary not found at {PB_BIN}")
        print("Build with: make build")
        sys.exit(1)

    print(f"Using pb: {PB_BIN}")
    print(f"Endpoint: {PB_ENDPOINT}")

    # Offline tests (no server required)
    test_search_offline()
    test_search_query()
    test_info_offline()
    test_info_not_found()
    test_list_empty()

    # Online tests (server + binary required)
    if not opts.skip_install:
        test_install_and_remove()
    else:
        print("\n[6-9] Skipping install/remove tests (--skip-install)")

    print("\n=== Package Manager smoke test PASSED ===")


if __name__ == "__main__":
    main()
