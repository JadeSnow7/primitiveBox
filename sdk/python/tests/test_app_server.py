from __future__ import annotations

import json
import os
import queue
import socket
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest.mock import MagicMock
from unittest.mock import patch


REPO_ROOT = Path(__file__).resolve().parents[3]
SDK_ROOT = REPO_ROOT / "sdk" / "python"
if str(SDK_ROOT) not in sys.path:
    sys.path.insert(0, str(SDK_ROOT))

from primitivebox.app import AppServer  # noqa: E402


class TestAppServer(unittest.TestCase):
    def test_primitive_registration(self):
        """@primitive decorates a function and registers the manifest once."""
        mock_client = MagicMock()
        server = AppServer("myapp", mock_client)

        sock_path = short_socket_path()
        try:
            @server.primitive("greet", socket_path=sock_path, description="greet user")
            def greet(name: str) -> dict:
                return {"message": f"hello {name}"}

            self.assertEqual(greet("world")["message"], "hello world")
            mock_client.call.assert_called_once()
            call_args = mock_client.call.call_args
            self.assertEqual(call_args[0][0], "app.register")
            manifest = call_args[0][1]
            self.assertEqual(manifest["app_id"], "myapp")
            self.assertEqual(manifest["name"], "myapp.greet")
            self.assertEqual(manifest["socket_path"], sock_path)
        finally:
            if os.path.exists(sock_path):
                os.unlink(sock_path)

    def test_dispatch_via_socket(self):
        """serve() dispatches registered methods over the Unix socket."""
        mock_client = MagicMock()
        server = AppServer("myapp", mock_client)
        sock_path = short_socket_path()

        @server.primitive("greet", socket_path=sock_path)
        def greet(name: str) -> dict:
            return {"message": f"hello {name}"}

        runtime = FakeUnixSocketRuntime()
        try:
            with patch("socket.socket", runtime.socket):
                server.serve(sock_path)
                time.sleep(0.1)

                conn = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                conn.connect(sock_path)
                req = json.dumps({"id": 1, "method": "myapp.greet", "params": {"name": "world"}}) + "\n"
                conn.sendall(req.encode())
                resp = json.loads(conn.recv(4096).decode())
                conn.close()

                self.assertIsNone(resp["error"])
                self.assertEqual(resp["result"]["message"], "hello world")
        finally:
            if os.path.exists(sock_path):
                os.unlink(sock_path)

    def test_dispatch_method_not_found(self):
        """Unknown methods return the JSON-RPC not-found error code."""
        mock_client = MagicMock()
        server = AppServer("myapp", mock_client)
        sock_path = short_socket_path()
        runtime = FakeUnixSocketRuntime()
        try:
            with patch("socket.socket", runtime.socket):
                server.serve(sock_path)
                time.sleep(0.1)

                conn = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                conn.connect(sock_path)
                req = json.dumps({"id": 2, "method": "myapp.unknown", "params": {}}) + "\n"
                conn.sendall(req.encode())
                resp = json.loads(conn.recv(4096).decode())
                conn.close()

                self.assertIsNone(resp["result"])
                self.assertEqual(resp["error"]["code"], -32601)
        finally:
            if os.path.exists(sock_path):
                os.unlink(sock_path)


def short_socket_path() -> str:
    handle = tempfile.NamedTemporaryFile(dir="/tmp", prefix="pb-app-", suffix=".sock", delete=False)
    path = handle.name
    handle.close()
    if os.path.exists(path):
        os.unlink(path)
    return path


class _Pipe:
    def __init__(self) -> None:
        self.buffer = bytearray()
        self.closed = False
        self.cond = threading.Condition()


class FakeSocketFile:
    def __init__(self, sock: "FakeSocket", encoding: str = "utf-8") -> None:
        self._sock = sock
        self._encoding = encoding

    def readline(self) -> str:
        data = bytearray()
        while True:
            chunk = self._sock.recv(1)
            if not chunk:
                break
            data.extend(chunk)
            if data.endswith(b"\n"):
                break
        return data.decode(self._encoding)

    def close(self) -> None:
        return None

    def __enter__(self) -> "FakeSocketFile":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        return None


class FakeSocket:
    def __init__(self, runtime: "FakeUnixSocketRuntime") -> None:
        self._runtime = runtime
        self._accept_queue: queue.Queue[FakeSocket] = queue.Queue()
        self._bound_path: str | None = None
        self._incoming: _Pipe | None = None
        self._outgoing: _Pipe | None = None

    def bind(self, path: str) -> None:
        self._bound_path = path
        self._runtime.register(path, self)

    def listen(self, backlog: int) -> None:  # noqa: ARG002 - matches socket API
        return None

    def accept(self):
        conn = self._accept_queue.get(timeout=1)
        return conn, None

    def connect(self, path: str) -> None:
        server = self._runtime.get(path)
        client_side, server_side = self._runtime.connection_pair()
        self._incoming = client_side._incoming
        self._outgoing = client_side._outgoing
        server._accept_queue.put(server_side)

    def sendall(self, data: bytes) -> None:
        if self._outgoing is None:
            raise RuntimeError("socket not connected")
        with self._outgoing.cond:
            self._outgoing.buffer.extend(data)
            self._outgoing.cond.notify_all()

    def recv(self, size: int) -> bytes:
        if self._incoming is None:
            raise RuntimeError("socket not connected")
        with self._incoming.cond:
            while not self._incoming.buffer and not self._incoming.closed:
                self._incoming.cond.wait(timeout=1)
            if not self._incoming.buffer:
                return b""
            data = bytes(self._incoming.buffer[:size])
            del self._incoming.buffer[:size]
            return data

    def makefile(self, mode: str, encoding: str = "utf-8"):  # noqa: ARG002 - matches socket API
        return FakeSocketFile(self, encoding=encoding)

    def close(self) -> None:
        if self._bound_path is not None:
            self._runtime.unregister(self._bound_path)
        if self._outgoing is not None:
            with self._outgoing.cond:
                self._outgoing.closed = True
                self._outgoing.cond.notify_all()
        if self._incoming is not None:
            with self._incoming.cond:
                self._incoming.closed = True
                self._incoming.cond.notify_all()


class FakeUnixSocketRuntime:
    def __init__(self) -> None:
        self._servers: dict[str, FakeSocket] = {}
        self._lock = threading.Lock()

    def socket(self, family, socktype):  # noqa: ANN001 - matches socket.socket
        if family != socket.AF_UNIX or socktype != socket.SOCK_STREAM:
            raise ValueError("fake runtime only supports AF_UNIX/SOCK_STREAM")
        return FakeSocket(self)

    def register(self, path: str, server: FakeSocket) -> None:
        with self._lock:
            self._servers[path] = server

    def unregister(self, path: str) -> None:
        with self._lock:
            self._servers.pop(path, None)

    def get(self, path: str) -> FakeSocket:
        with self._lock:
            return self._servers[path]

    def connection_pair(self):
        client = FakeSocket(self)
        server = FakeSocket(self)
        client._incoming = _Pipe()
        client._outgoing = _Pipe()
        server._incoming = client._outgoing
        server._outgoing = client._incoming
        return client, server
