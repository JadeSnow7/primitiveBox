from __future__ import annotations

import json
import os
import shutil
import socket
import sys
import tempfile
import time
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
SDK_ROOT = REPO_ROOT / "sdk" / "python"
if str(SDK_ROOT) not in sys.path:
    sys.path.insert(0, str(SDK_ROOT))

from primitivebox.html_layout import HTMLLayoutServer  # noqa: E402


class StubClient:
    def __init__(self) -> None:
        self.registrations: list[dict] = []

    def call(self, method: str, params: dict, headers: dict | None = None) -> dict:
        if method != "app.register":
            raise ValueError(f"unexpected method: {method}")
        self.registrations.append({"params": params, "headers": headers})
        return {"registered": True, "name": params["name"]}


def call_socket(method: str, params: dict) -> dict:
    request = {"jsonrpc": "2.0", "method": method, "params": params, "id": method}
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
        conn.connect(HTMLLayoutServer.SOCKET_PATH)
        conn.sendall((json.dumps(request) + "\n").encode("utf-8"))
        response = json.loads(conn.recv(65536).decode("utf-8"))
    if response["error"] is not None:
        raise RuntimeError(response["error"]["message"])
    return response["result"]


def main() -> None:
    sample_path = REPO_ROOT / "examples" / "html_layout" / "sample.html"
    work_dir = Path(tempfile.mkdtemp(prefix="html-layout-demo-"))
    work_path = work_dir / "sample.html"
    shutil.copyfile(sample_path, work_path)

    client = StubClient()
    server = HTMLLayoutServer(client)
    server.serve()
    time.sleep(0.1)

    steps = [
        ("style.read", {"path": str(work_path), "selector": "body"}),
        (
            "style.apply",
            {
                "path": str(work_path),
                "selector": "p",
                "styles": {"font-size": "17px", "color": "#1a1a1a"},
            },
        ),
        (
            "style.apply_tokens",
            {
                "path": str(work_path),
                "token_map": {"--brand-color": "#2563eb"},
            },
        ),
        (
            "verify.contrast",
            {"path": str(work_path), "selector": "p", "level": "AA"},
        ),
        ("verify.structure", {"path": str(work_path)}),
    ]

    try:
        print(json.dumps({"step": "app.register", "result": client.registrations}, ensure_ascii=False))
        for method, params in steps:
            result = call_socket(f"html.{method}", params)
            print(json.dumps({"step": method, "result": result}, ensure_ascii=False))
    finally:
        if os.path.exists(HTMLLayoutServer.SOCKET_PATH):
            os.unlink(HTMLLayoutServer.SOCKET_PATH)
        if work_path.exists():
            work_path.unlink()
        work_dir.rmdir()


if __name__ == "__main__":
    main()
