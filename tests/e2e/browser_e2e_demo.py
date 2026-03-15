#!/usr/bin/env python3
"""Minimal browser primitive E2E flow."""

from __future__ import annotations

import http.server
import json
import socket
import socketserver
import subprocess
import threading
import time
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import Request, urlopen


REPO_ROOT = Path(__file__).resolve().parents[2]
PB_BIN = REPO_ROOT / "bin" / "pb"
SITE_DIR = REPO_ROOT / "tests" / "e2e" / "site"


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


def serve_site() -> tuple[socketserver.TCPServer, int]:
    handler = http.server.SimpleHTTPRequestHandler
    httpd = socketserver.TCPServer(("127.0.0.1", 0), handler)
    port = httpd.server_address[1]

    def _serve() -> None:
        cwd = Path.cwd()
        try:
            import os
            os.chdir(SITE_DIR)
            httpd.serve_forever()
        finally:
            os.chdir(cwd)

    thread = threading.Thread(target=_serve, daemon=True)
    thread.start()
    return httpd, port


def main() -> int:
    SITE_DIR.mkdir(parents=True, exist_ok=True)
    (SITE_DIR / "index.html").write_text(
        "<!doctype html><html><body><h1 id='title'>PrimitiveBox Browser Demo</h1>"
        "<button id='go' onclick=\"document.getElementById('title').innerText='Clicked';\">Go</button>"
        "</body></html>",
        encoding="utf-8",
    )

    httpd, port = serve_site()
    gateway_port = reserve_port()
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
        sandbox = json.loads(
            subprocess.run(
                [str(PB_BIN), "sandbox", "create", "--driver", "docker", "--image", "primitivebox-sandbox-browser:latest", "--mount", str(REPO_ROOT)],
                check=True,
                capture_output=True,
                text=True,
            ).stdout
        )
        sandbox_id = sandbox["id"]
        time.sleep(3)
        base_url = f"http://127.0.0.1:{gateway_port}/sandboxes/{sandbox_id}/rpc"

        goto = rpc(base_url, "browser.goto", {"url": f"http://host.docker.internal:{port}"})
        print(json.dumps(goto, indent=2))
        if "result" not in goto:
            events = get_json(
                f"http://127.0.0.1:{gateway_port}/api/v1/events?"
                + urlencode({"method": "browser.goto", "type": "rpc.error", "limit": 5})
            )
            print(json.dumps(events, indent=2))
            raise RuntimeError("browser.goto failed")
        session_id = goto["result"]["data"]["session_id"]
        print(json.dumps(rpc(base_url, "browser.extract", {"session_id": session_id, "selector": "#title"}), indent=2))
        print(json.dumps(rpc(base_url, "browser.click", {"session_id": session_id, "selector": "#go"}), indent=2))
        print(json.dumps(rpc(base_url, "browser.screenshot", {"session_id": session_id, "full_page": True}), indent=2)[:1000])
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
                print(output)
        httpd.shutdown()


if __name__ == "__main__":
    raise SystemExit(main())
