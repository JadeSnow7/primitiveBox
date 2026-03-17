"""AppServer SDK: @primitive decorator for registering app primitives."""

from __future__ import annotations

import functools
import json
import os
import socket
import threading
from typing import Any, Callable, Optional

_ERR_NOT_FOUND = -32601
_ERR_INVALID = -32600
_ERR_INTERNAL = -32603


class AppServer:
    def __init__(self, app_id: str, client):
        self._app_id = app_id
        self._client = client
        self._handlers: dict[str, Callable[..., Any]] = {}
        self._counter = 0
        self._lock = threading.Lock()

    def _next_id(self) -> int:
        with self._lock:
            self._counter += 1
            return self._counter

    def primitive(
        self,
        name: str,
        *,
        description: str = "",
        input_schema: Optional[dict] = None,
        output_schema: Optional[dict] = None,
        socket_path: str,
        reversible: bool = True,
        risk_level: str = "low",
        category: str = "mutation",
    ):
        """Register a function as an app primitive."""

        def decorator(fn: Callable[..., Any]) -> Callable[..., Any]:
            full_name = f"{self._app_id}.{name}"
            self._handlers[full_name] = fn
            manifest = {
                "app_id": self._app_id,
                "name": full_name,
                "description": description or (fn.__doc__ or "").strip(),
                "input_schema": json.dumps(input_schema or {}),
                "output_schema": json.dumps(output_schema or {}),
                "socket_path": socket_path,
                "intent": {
                    "reversible": reversible,
                    "risk_level": risk_level,
                    "category": category,
                    "affected_scopes": [],
                },
            }
            self._client.call("app.register", manifest, headers={"X-PB-Origin": "sandbox"})

            @functools.wraps(fn)
            def wrapper(*args, **kwargs):
                return fn(*args, **kwargs)

            return wrapper

        return decorator

    def serve(self, socket_path: str) -> None:
        """Start a newline-delimited JSON-RPC server on a Unix socket."""
        if os.path.exists(socket_path):
            os.unlink(socket_path)

        server_sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        server_sock.bind(socket_path)
        server_sock.listen(16)

        def _handle(conn: socket.socket) -> None:
            try:
                with conn.makefile("r", encoding="utf-8") as f:
                    line = f.readline()
                if not line:
                    return

                try:
                    req = json.loads(line)
                except json.JSONDecodeError:
                    resp = {
                        "id": 0,
                        "result": None,
                        "error": {"code": _ERR_INVALID, "message": "invalid request"},
                    }
                    conn.sendall((json.dumps(resp) + "\n").encode())
                    return

                req_id = req.get("id", 0)
                method = req.get("method", "")
                params = req.get("params", {})
                if not isinstance(method, str) or not method:
                    resp = {
                        "id": req_id,
                        "result": None,
                        "error": {"code": _ERR_INVALID, "message": "invalid request"},
                    }
                    conn.sendall((json.dumps(resp) + "\n").encode())
                    return

                handler = self._handlers.get(method)
                if handler is None:
                    resp = {
                        "id": req_id,
                        "result": None,
                        "error": {"code": _ERR_NOT_FOUND, "message": f"method not found: {method}"},
                    }
                else:
                    try:
                        if isinstance(params, dict):
                            result = handler(**params)
                        else:
                            result = handler(params)
                        resp = {"id": req_id, "result": result, "error": None}
                    except Exception as exc:  # noqa: BLE001 - JSON-RPC internal error contract
                        resp = {
                            "id": req_id,
                            "result": None,
                            "error": {"code": _ERR_INTERNAL, "message": str(exc)},
                        }

                conn.sendall((json.dumps(resp) + "\n").encode())
            except Exception:
                pass
            finally:
                conn.close()

        def _serve_loop() -> None:
            while True:
                try:
                    conn, _ = server_sock.accept()
                except Exception:
                    break
                threading.Thread(target=_handle, args=(conn,), daemon=True).start()

        threading.Thread(target=_serve_loop, daemon=True).start()
