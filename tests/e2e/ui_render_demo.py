#!/usr/bin/env python3
"""Inspector UI + SSE end-to-end verification."""

from __future__ import annotations

import json
import os
import socket
import subprocess
import threading
import time
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen


REPO_ROOT = Path(__file__).resolve().parents[2]
PB_BIN = REPO_ROOT / "bin" / "pb"


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


def rpc(url: str, method: str, params: dict) -> dict:
    req = Request(
        url,
        data=json.dumps({"jsonrpc": "2.0", "method": method, "params": params, "id": method}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urlopen(req, timeout=180) as response:
            return json.loads(response.read().decode("utf-8"))
    except HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} failed with HTTP {exc.code}: {body}") from exc


def collect_sse(base_url: str, seconds: int = 8) -> dict[str, int]:
    counts: dict[str, int] = {}
    req = Request(f"{base_url}/api/v1/events/stream", headers={"Accept": "text/event-stream"})
    deadline = time.time() + seconds
    with urlopen(req, timeout=seconds + 10) as response:
        while time.time() < deadline:
            raw = response.readline()
            if not raw:
                break
            line = raw.decode("utf-8", errors="replace").strip()
            if not line.startswith("event:"):
                continue
            event_type = line.split(":", 1)[1].strip()
            counts[event_type] = counts.get(event_type, 0) + 1
    return counts


def main() -> int:
    port = int(os.environ.get("PB_UI_PORT", "8080"))
    base_url = f"http://127.0.0.1:{port}"
    server = subprocess.Popen(
        [str(PB_BIN), "server", "start", "--workspace", str(REPO_ROOT), "--port", str(port), "--ui"],
        cwd=REPO_ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    stress: subprocess.Popen[str] | None = None
    sandbox_id = ""
    try:
        wait_for_http(f"{base_url}/health")
        with urlopen(f"{base_url}/", timeout=10) as response:
            html = response.read().decode("utf-8")
        if '<div id="root"></div>' not in html or "/assets/" not in html:
            raise RuntimeError("ui index page did not serve the embedded SPA shell")

        sandbox = json.loads(
            subprocess.run(
                [
                    str(PB_BIN),
                    "sandbox",
                    "create",
                    "--driver",
                    "docker",
                    "--image",
                    "primitivebox-sandbox-browser:latest",
                    "--mount",
                    str(REPO_ROOT),
                ],
                check=True,
                capture_output=True,
                text=True,
            ).stdout
        )
        sandbox_id = sandbox["id"]
        time.sleep(3)
        rpc_url = f"{base_url}/sandboxes/{sandbox_id}/rpc"

        goto = rpc(rpc_url, "browser.goto", {"url": f"http://host.docker.internal:{port}/"})
        print(json.dumps({"goto": goto}, indent=2))
        with urlopen(f"{base_url}/api/v1/sandboxes/{sandbox_id}", timeout=10) as response:
            print(response.read().decode("utf-8"))
        session_id = goto["result"]["data"]["session_id"]

        sse_counts: dict[str, int] = {}
        collector = threading.Thread(
            target=lambda: sse_counts.update(collect_sse(base_url)),
            daemon=True,
        )
        collector.start()

        stress = subprocess.Popen(
            ["python3", str(REPO_ROOT / "tests" / "e2e" / "ui_sse_stress.py")],
            cwd=REPO_ROOT,
            env={**os.environ, "PB_BASE_URL": base_url},
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )

        time.sleep(2)
        hero = rpc(rpc_url, "browser.extract", {"session_id": session_id, "selector": "h1"})
        time.sleep(4)
        console = rpc(rpc_url, "browser.extract", {"session_id": session_id, "selector": ".console-log"})
        metrics = rpc(rpc_url, "browser.extract", {"session_id": session_id, "selector": ".hero-metrics"})
        screenshot = rpc(rpc_url, "browser.screenshot", {"session_id": session_id, "full_page": True})

        if stress.wait(timeout=40) != 0:
            raise RuntimeError("ui stress driver failed")
        collector.join(timeout=15)

        screenshot_size = len(screenshot["result"]["data"]["image_base64"])
        print(json.dumps({"hero": hero, "console": console, "metrics": metrics}, indent=2))
        print(json.dumps({"sse_counts": sse_counts, "screenshot_base64_len": screenshot_size}, indent=2))

        console_text = console["result"]["data"]["text"]
        if "Waiting for the first SSE frame." in console_text:
            raise RuntimeError("ui console did not receive streamed events")
        if "rpc." not in console_text and "fs.list" not in console_text and "shell.exec" not in console_text:
            raise RuntimeError("ui console did not render expected rpc activity")
        if sse_counts.get("rpc.started", 0) == 0 and sse_counts.get("rpc.completed", 0) == 0:
            raise RuntimeError("sse endpoint did not stream rpc events under stress")
        if screenshot_size < 10_000:
            raise RuntimeError("ui screenshot payload was unexpectedly small")
        return 0
    finally:
        if stress and stress.poll() is None:
            stress.terminate()
            try:
                stress.wait(timeout=5)
            except subprocess.TimeoutExpired:
                stress.kill()
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
        if stress and stress.stdout is not None:
            output = stress.stdout.read().strip()
            if output:
                print(output)
        if server.stdout is not None:
            output = server.stdout.read().strip()
            if output:
                print(output)


if __name__ == "__main__":
    raise SystemExit(main())
