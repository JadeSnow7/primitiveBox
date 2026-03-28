"""
PrimitiveBox Async Client

Provides an async JSON-RPC client for PrimitiveBox using ``httpx``.
Falls back gracefully if ``httpx`` is not installed (raises ImportError with
a clear install message).

Usage::

    from primitivebox.async_client import AsyncPrimitiveBoxClient

    async with AsyncPrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-xxx") as client:
        result = await client.fs.read("src/main.py")
        symbols = await client.code.symbols("src/main.py")
"""

from __future__ import annotations

import json
from typing import Any, AsyncIterator, Optional

try:
    import httpx
    _HTTPX_AVAILABLE = True
except ImportError:
    _HTTPX_AVAILABLE = False

from .events import EventEmitter
from .goals import AsyncGoalPrimitives
from .primitives import BrowserPrimitives, CodePrimitives, DBPrimitives, FSPrimitives, ShellPrimitives, StatePrimitives, VerifyPrimitives, MacroPrimitives


class AsyncPrimitiveBoxClient:
    """
    Async PrimitiveBox client using ``httpx.AsyncClient``.

    Requires ``httpx >= 0.20``::

        pip install httpx

    Can be used as an async context manager::

        async with AsyncPrimitiveBoxClient() as client:
            result = await client.fs.read("README.md")
    """

    def __init__(self, endpoint: str = "http://localhost:8080", sandbox_id: str = "", timeout: float = 120.0):
        if not _HTTPX_AVAILABLE:
            raise ImportError(
                "httpx is required for AsyncPrimitiveBoxClient. "
                "Install it with: pip install httpx"
            )
        self.endpoint = endpoint.rstrip("/")
        self.sandbox_id = sandbox_id
        self.timeout = timeout
        self._call_id = 0
        self._events = EventEmitter()
        self._http: Optional[httpx.AsyncClient] = None

        # Goal composition helpers (async via thread executor).
        self.goals = AsyncGoalPrimitives(self)

        # Bind primitive wrappers — they call self.call() which is async-aware
        self.fs = _AsyncPrimitiveGroup(self, FSPrimitives)
        self.shell = _AsyncPrimitiveGroup(self, ShellPrimitives)
        self.state = _AsyncPrimitiveGroup(self, StatePrimitives)
        self.verify = _AsyncPrimitiveGroup(self, VerifyPrimitives)
        self.code = _AsyncPrimitiveGroup(self, CodePrimitives)
        self.macro = _AsyncPrimitiveGroup(self, MacroPrimitives)
        self.db = _AsyncPrimitiveGroup(self, DBPrimitives)
        self.browser = _AsyncPrimitiveGroup(self, BrowserPrimitives)

    async def __aenter__(self) -> "AsyncPrimitiveBoxClient":
        self._http = httpx.AsyncClient(timeout=self.timeout, trust_env=False)
        return self

    async def __aexit__(self, *args: Any) -> None:
        if self._http:
            await self._http.aclose()
            self._http = None

    async def call(self, method: str, params: Optional[dict] = None, headers: Optional[dict[str, str]] = None) -> Any:
        """Make an async JSON-RPC 2.0 call."""
        if self._http is None:
            self._http = httpx.AsyncClient(timeout=self.timeout, trust_env=False)

        self._call_id += 1
        request = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params or {},
            "id": self._call_id,
        }

        response = await self._http.post(
            f"{self.endpoint}{self._rpc_path()}",
            content=json.dumps(request).encode(),
            headers={"Content-Type": "application/json", **(headers or {})},
        )
        response.raise_for_status()
        data = response.json()

        if "error" in data and data["error"] is not None:
            error = data["error"]
            self._events.emit("fail", {"method": method, "error": error})
            from .client import RPCError
            raise RPCError(error.get("code", -1), error.get("message", "Unknown error"), error.get("data"))

        self._events.emit("call", {"method": method, "result": data.get("result")})
        return data.get("result", {})

    async def stream_call(self, method: str, params: Optional[dict] = None) -> AsyncIterator[dict[str, Any]]:
        """Stream a JSON-RPC call over the gateway's SSE endpoint."""
        if self._http is None:
            self._http = httpx.AsyncClient(timeout=self.timeout, trust_env=False)

        self._call_id += 1
        request = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params or {},
            "id": self._call_id,
        }

        async with self._http.stream(
            "POST",
            f"{self.endpoint}{self._stream_path()}",
            content=json.dumps(request).encode(),
            headers={"Content-Type": "application/json"},
        ) as response:
            response.raise_for_status()
            event_name = "message"
            data_lines: list[str] = []
            async for raw_line in response.aiter_lines():
                if raw_line == "":
                    if data_lines:
                        yield {"event": event_name, "data": json.loads("\n".join(data_lines))}
                        event_name = "message"
                        data_lines = []
                    continue
                if raw_line.startswith("event:"):
                    event_name = raw_line.split(":", 1)[1].strip()
                    continue
                if raw_line.startswith("data:"):
                    data_lines.append(raw_line.split(":", 1)[1].strip())

            if data_lines:
                yield {"event": event_name, "data": json.loads("\n".join(data_lines))}

    async def health(self) -> dict:
        """Check gateway health."""
        if self._http is None:
            self._http = httpx.AsyncClient(timeout=self.timeout, trust_env=False)
        response = await self._http.get(f"{self.endpoint}{self._health_path()}")
        response.raise_for_status()
        return response.json()

    async def list_primitives(self) -> list:
        """List available primitives from the gateway."""
        if self._http is None:
            self._http = httpx.AsyncClient(timeout=self.timeout, trust_env=False)
        response = await self._http.get(f"{self.endpoint}{self._primitives_path()}")
        response.raise_for_status()
        return response.json().get("primitives", [])

    def on(self, event: str, callback) -> None:
        """Subscribe to events: 'call', 'fail'."""
        self._events.on(event, callback)

    def _rpc_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/rpc"
        return "/rpc"

    def _health_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/health"
        return "/health"

    def _stream_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/rpc/stream"
        return "/rpc/stream"

    def _primitives_path(self) -> str:
        if self.sandbox_id:
            return f"/sandboxes/{self.sandbox_id}/primitives"
        return "/primitives"


class _AsyncPrimitiveGroup:
    """
    Wraps synchronous primitive classes so their methods return coroutines.

    Intercepts method calls on a sync primitive group (e.g., FSPrimitives)
    and returns async wrappers that call their parent async_client.call().
    """

    def __init__(self, async_client: AsyncPrimitiveBoxClient, sync_cls: type):
        # Create a dummy sync proxy to introspect the method list
        self._async_client = async_client
        self._sync_cls = sync_cls

        # Install async method wrappers for each sync method
        from .client import PrimitiveBoxClient as _SyncClient

        class _FakeSyncClient:
            def call(self_, method: str, params: dict) -> None:  # type: ignore
                pass

        sync_instance = sync_cls(_FakeSyncClient())  # type: ignore[arg-type]
        for attr_name in dir(sync_instance):
            if attr_name.startswith("_"):
                continue
            if attr_name.startswith("stream_"):
                continue
            attr = getattr(sync_instance, attr_name)
            if callable(attr):
                setattr(self, attr_name, self._make_async(sync_instance, attr_name))

    def _make_async(self, sync_instance: Any, method_name: str):
        """Return an async wrapper that infers the JSON-RPC method name and params."""
        import inspect

        original = getattr(sync_instance, method_name)
        sig = inspect.signature(original)

        async def _wrapper(*args: Any, **kwargs: Any) -> Any:
            # Bind args to reproduce the params dict that the sync method would build
            bound = sig.bind(*args, **kwargs)
            bound.apply_defaults()
            params = dict(bound.arguments)
            # Determine the RPC method from the sync implementation
            # (FSPrimitives.read → "fs.read" via client.call("fs.read", ...))
            # We call the REAL async path directly.
            # Build params by delegating to the sync version with a spy client.
            captured: dict = {}

            class _SpyClient:
                def call(self_, method: str, p: dict) -> None:  # type: ignore
                    captured["method"] = method
                    captured["params"] = p

            spy = sync_instance._client.__class__  # type: ignore[attr-defined]
            spy_instance = _SpyClient()
            sync_spy = self._sync_cls(spy_instance)  # type: ignore[arg-type]
            try:
                getattr(sync_spy, method_name)(*args, **kwargs)
            except Exception:
                pass
            if not captured:
                raise RuntimeError(f"Could not capture RPC call for {method_name}")
            return await self._async_client.call(captured["method"], captured["params"])

        _wrapper.__name__ = method_name
        return _wrapper
