"""
PrimitiveBox Python SDK Client

Provides synchronous clients for communicating with the PrimitiveBox JSON-RPC
host gateway over HTTP, including SSE-based streaming calls.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.request
from typing import Any, Iterator, Optional

from .events import EventEmitter
from .primitives import (
    BrowserPrimitives,
    CodePrimitives,
    DBPrimitives,
    FSPrimitives,
    MacroPrimitives,
    ShellPrimitives,
    StatePrimitives,
    VerifyPrimitives,
)


class PrimitiveBoxClient:
    """Synchronous PrimitiveBox client using JSON-RPC 2.0 over HTTP."""

    def __init__(self, endpoint: str = "http://localhost:8080", sandbox_id: str = ""):
        self.endpoint = endpoint.rstrip("/")
        self.sandbox_id = sandbox_id
        self._call_id = 0
        self._events = EventEmitter()

        self.fs = FSPrimitives(self)
        self.shell = ShellPrimitives(self)
        self.state = StatePrimitives(self)
        self.verify = VerifyPrimitives(self)
        self.code = CodePrimitives(self)
        self.macro = MacroPrimitives(self)
        self.db = DBPrimitives(self)
        self.browser = BrowserPrimitives(self)

    def call(self, method: str, params: Optional[dict] = None) -> Any:
        self._call_id += 1

        request = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params or {},
            "id": self._call_id,
        }

        result = self._request_json(self._rpc_path(), method="POST", payload=request, timeout=120)
        if "error" in result and result["error"] is not None:
            error = result["error"]
            self._events.emit("fail", {"method": method, "error": error})
            raise RPCError(error.get("code", -1), error.get("message", "Unknown error"), error.get("data"))

        self._events.emit("call", {"method": method, "result": result.get("result")})
        return result.get("result", {})

    def stream_call(self, method: str, params: Optional[dict] = None) -> Iterator[dict[str, Any]]:
        """Stream a JSON-RPC call over the gateway's SSE endpoint."""
        self._call_id += 1
        payload = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params or {},
            "id": self._call_id,
        }

        req = urllib.request.Request(
            f"{self.endpoint}{self._stream_path()}",
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )

        try:
            with urllib.request.urlopen(req, timeout=120) as response:
                yield from _iter_sse(response)
        except urllib.error.HTTPError as e:
            message = e.read().decode("utf-8", errors="replace")
            raise ConnectionError(f"PrimitiveBox stream failed ({e.code}) at {self._stream_path()}: {message}") from e
        except urllib.error.URLError as e:
            raise ConnectionError(f"Cannot connect to PrimitiveBox at {self.endpoint}: {e}") from e

    def on(self, event: str, callback) -> None:
        """Subscribe to events: 'call', 'fail', 'checkpoint'."""
        self._events.on(event, callback)

    def health(self) -> dict:
        return self._request_json(self._health_path(), timeout=5)

    def list_primitives(self) -> list:
        data = self._request_json(self._primitives_path(), timeout=5)
        return data.get("primitives", [])

    def auto_fix(self, test_command: str = "pytest", max_retries: int = 3) -> dict:
        for attempt in range(1, max_retries + 1):
            checkpoint = self.state.checkpoint(f"auto-fix-attempt-{attempt}")
            test_result = self.verify.test(test_command)
            if test_result.get("data", {}).get("passed", False):
                return {
                    "passed": True,
                    "attempts": attempt,
                    "message": f"Tests passed on attempt {attempt}",
                }

            self.state.restore(checkpoint.get("data", {}).get("checkpoint_id", "latest"))

        return {
            "passed": False,
            "attempts": max_retries,
            "message": f"Tests still failing after {max_retries} attempts",
        }

    def _rpc_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/rpc"
        return "/rpc"

    def _stream_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/rpc/stream"
        return "/rpc/stream"

    def _health_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/health"
        return "/health"

    def _primitives_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/primitives"
        return "/primitives"

    def _request_json(self, path: str, *, method: str = "GET", payload: Optional[dict] = None, timeout: int = 30) -> dict:
        data = None
        headers = {}
        if payload is not None:
            data = json.dumps(payload).encode("utf-8")
            headers["Content-Type"] = "application/json"

        req = urllib.request.Request(
            f"{self.endpoint}{path}",
            data=data,
            headers=headers,
            method=method,
        )

        try:
            with urllib.request.urlopen(req, timeout=timeout) as response:
                return json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            message = e.read().decode("utf-8", errors="replace")
            raise ConnectionError(f"PrimitiveBox request failed ({e.code}) at {path}: {message}") from e
        except urllib.error.URLError as e:
            raise ConnectionError(f"Cannot connect to PrimitiveBox at {self.endpoint}: {e}") from e


def _iter_sse(response) -> Iterator[dict[str, Any]]:
    event_name = "message"
    data_lines: list[str] = []

    for raw_line in response:
        line = raw_line.decode("utf-8").rstrip("\n")
        if line == "":
            if data_lines:
                data = "\n".join(data_lines)
                yield {"event": event_name, "data": json.loads(data)}
                event_name = "message"
                data_lines = []
            continue
        if line.startswith("event:"):
            event_name = line.split(":", 1)[1].strip()
            continue
        if line.startswith("data:"):
            data_lines.append(line.split(":", 1)[1].strip())

    if data_lines:
        yield {"event": event_name, "data": json.loads("\n".join(data_lines))}


class RPCError(Exception):
    """Raised when the JSON-RPC server returns an error."""

    def __init__(self, code: int, message: str, data: Any = None):
        self.code = code
        self.message = message
        self.data = data
        super().__init__(f"[{code}] {message}")
