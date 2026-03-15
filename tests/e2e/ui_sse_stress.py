#!/usr/bin/env python3
"""High-frequency RPC driver for Inspector UI SSE verification."""

from __future__ import annotations

import json
import os
import threading
import time
from urllib.request import Request, urlopen

BASE_URL = os.environ.get("PB_BASE_URL", "http://127.0.0.1:8080")


def rpc(method: str, params: dict) -> None:
    req = Request(
        f"{BASE_URL}/rpc",
        data=json.dumps({"jsonrpc": "2.0", "method": method, "params": params, "id": time.time()}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urlopen(req, timeout=60):
        return


def worker(stop_at: float) -> None:
    while time.time() < stop_at:
        rpc("fs.list", {"path": ".", "recursive": False})
        rpc("code.search", {"query": "PrimitiveBox", "max_results": 5})
        rpc("shell.exec", {"command": "printf 'ui-stress\\n'", "timeout_s": 5})


def main() -> int:
    stop_at = time.time() + 20
    threads = [threading.Thread(target=worker, args=(stop_at,), daemon=True) for _ in range(4)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
