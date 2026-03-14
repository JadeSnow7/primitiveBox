from __future__ import annotations

import json
import sys
from pathlib import Path
from urllib.error import HTTPError

import pytest


REPO_ROOT = Path(__file__).resolve().parents[3]
SDK_ROOT = REPO_ROOT / "sdk" / "python"
if str(SDK_ROOT) not in sys.path:
    sys.path.insert(0, str(SDK_ROOT))

from primitivebox import AsyncPrimitiveBoxClient, PrimitiveBoxClient  # noqa: E402
from primitivebox.client import RPCError  # noqa: E402


def test_health_and_fs_read(monkeypatch: pytest.MonkeyPatch) -> None:
    responses = {
        "http://localhost:8080/health": {"status": "ok"},
        "http://localhost:8080/rpc": {
            "jsonrpc": "2.0",
            "result": {
                "data": {
                    "content": "hello\n",
                    "total_lines": 2,
                    "encoding": "utf-8",
                }
            },
            "id": 1,
        },
    }
    monkeypatch.setattr("urllib.request.urlopen", make_urlopen(responses))

    client = PrimitiveBoxClient("http://localhost:8080")
    health = client.health()
    result = client.fs.read("main.txt")

    assert health["status"] == "ok"
    assert result["data"]["content"] == "hello\n"
    assert result["data"]["total_lines"] == 2


def test_rpc_error_propagation(monkeypatch: pytest.MonkeyPatch) -> None:
    responses = {
        "http://localhost:8080/rpc": {
            "jsonrpc": "2.0",
            "error": {
                "code": -32602,
                "message": "invalid line range",
            },
            "id": 1,
        },
    }
    monkeypatch.setattr("urllib.request.urlopen", make_urlopen(responses))

    client = PrimitiveBoxClient("http://localhost:8080")
    with pytest.raises(RPCError) as exc_info:
        client.fs.read("main.txt", start_line=2, end_line=1)

    assert exc_info.value.code == -32602
    assert exc_info.value.message == "invalid line range"


def test_checkpoint_event(monkeypatch: pytest.MonkeyPatch) -> None:
    responses = {
        "http://localhost:8080/rpc": {
            "jsonrpc": "2.0",
            "result": {
                "data": {
                    "checkpoint_id": "abc123",
                    "timestamp": "2026-03-14T00:00:00Z",
                }
            },
            "id": 1,
        },
    }
    monkeypatch.setattr("urllib.request.urlopen", make_urlopen(responses))

    client = PrimitiveBoxClient("http://localhost:8080")
    events: list[dict] = []
    client.on("checkpoint", events.append)

    result = client.state.checkpoint("sdk-test")

    assert result["data"]["checkpoint_id"] == "abc123"
    assert events == [result]


def test_sandbox_client_routes_to_gateway_paths(monkeypatch: pytest.MonkeyPatch) -> None:
    responses = {
        "http://localhost:8080/sandboxes/sb-123/health": {"status": "ok"},
        "http://localhost:8080/sandboxes/sb-123/primitives": {
            "primitives": [{"name": "fs.read"}]
        },
        "http://localhost:8080/sandboxes/sb-123/rpc": {
            "jsonrpc": "2.0",
            "result": {
                "data": {
                    "content": "inside sandbox",
                    "total_lines": 1,
                    "encoding": "utf-8",
                }
            },
            "id": 1,
        },
    }
    monkeypatch.setattr("urllib.request.urlopen", make_urlopen(responses))

    client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-123")
    assert client.health()["status"] == "ok"
    assert client.list_primitives()[0]["name"] == "fs.read"
    result = client.fs.read("main.txt")
    assert result["data"]["content"] == "inside sandbox"


def test_async_client_is_fail_fast() -> None:
    with pytest.raises(NotImplementedError):
        AsyncPrimitiveBoxClient()


class FakeResponse:
    def __init__(self, payload: dict):
        self.status = 200
        self._payload = json.dumps(payload).encode("utf-8")

    def read(self) -> bytes:
        return self._payload

    def __enter__(self) -> "FakeResponse":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        return None


def make_urlopen(responses: dict[str, dict]):
    def _urlopen(request, timeout=0):  # noqa: ARG001 - signature matches stdlib usage
        url = request if isinstance(request, str) else request.full_url
        if url not in responses:
            raise HTTPError(url, 404, "not found", hdrs=None, fp=None)
        return FakeResponse(responses[url])

    return _urlopen
