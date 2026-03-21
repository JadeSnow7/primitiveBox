"""
PrimitiveBox CVR Event Stream Demo
==================================
Run alongside run_demo.py to watch live gateway events over SSE.

Usage:
    export PB_HOST="http://127.0.0.1:8091"
    python3 examples/auto_fix_bug_demo/stream_demo.py
"""

from __future__ import annotations

import json
import os
import urllib.request


PB_HOST = os.getenv("PB_HOST", "http://127.0.0.1:8091").rstrip("/")

ICONS = {
    "rpc.started": "▶",
    "rpc.completed": "✓",
    "rpc.error": "✗",
    "checkpoint.created": "📌",
    "checkpoint.restored": "↩",
    "shell.output": "·",
}


def iter_sse(url: str):
    req = urllib.request.Request(url, headers={"Accept": "text/event-stream"})
    with urllib.request.urlopen(req, timeout=300) as response:
        event_name = "message"
        data_lines: list[str] = []
        for raw_line in response:
            line = raw_line.decode("utf-8").rstrip("\n")
            if line == "":
                if data_lines:
                    yield event_name, json.loads("\n".join(data_lines))
                    event_name = "message"
                    data_lines = []
                continue
            if line.startswith("event:"):
                event_name = line.split(":", 1)[1].strip()
            elif line.startswith("data:"):
                data_lines.append(line.split(":", 1)[1].strip())


print("─" * 50)
print("  PrimitiveBox CVR Event Stream")
print("─" * 50)
print(f"  Listening on {PB_HOST}/api/v1/events/stream\n")

try:
    for event_type, payload in iter_sse(PB_HOST + "/api/v1/events/stream"):
        icon = ICONS.get(event_type, "·")
        method = payload.get("method", "")
        message = payload.get("message", "")

        if event_type == "shell.output":
            stream = payload.get("stream", "stdout")
            print(f"  {icon} {stream}: {message}")
        elif event_type in {"checkpoint.created", "checkpoint.restored"}:
            print(f"  {icon} {event_type}: {message}")
        elif method:
            print(f"  {icon} {event_type}: {method}")
        else:
            print(f"  {icon} {event_type}: {message}")
except KeyboardInterrupt:
    print("\nStream stopped.")
