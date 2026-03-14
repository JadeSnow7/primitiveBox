"""
Typed primitive wrappers for the PrimitiveBox Python SDK.

Each class provides a fluent API over the raw JSON-RPC calls.
"""

from typing import Any, Optional


class FSPrimitives:
    """File system primitives: read, write, list, diff."""

    def __init__(self, client):
        self._client = client

    def read(self, path: str, start_line: int = 0, end_line: int = 0) -> dict:
        """Read file content with optional line range."""
        params = {"path": path}
        if start_line > 0:
            params["start_line"] = start_line
        if end_line > 0:
            params["end_line"] = end_line
        return self._client.call("fs.read", params)

    def write(self, path: str, content: str = "", mode: str = "overwrite",
              search: str = "", replace: str = "", create_dirs: bool = False) -> dict:
        """Write file content via overwrite or search-and-replace."""
        params: dict[str, Any] = {"path": path, "mode": mode}
        if mode == "search_replace":
            params["search"] = search
            params["replace"] = replace
        else:
            params["content"] = content
        if create_dirs:
            params["create_dirs"] = True
        return self._client.call("fs.write", params)

    def list(self, path: str = ".", recursive: bool = False, pattern: str = "") -> dict:
        """List directory contents."""
        params: dict[str, Any] = {"path": path, "recursive": recursive}
        if pattern:
            params["pattern"] = pattern
        return self._client.call("fs.list", params)


class ShellPrimitives:
    """Shell execution primitives."""

    def __init__(self, client):
        self._client = client

    def exec(self, command: str, timeout_s: int = 30, env: Optional[dict] = None) -> dict:
        """Execute a shell command with timeout protection."""
        params: dict[str, Any] = {"command": command, "timeout_s": timeout_s}
        if env:
            params["env"] = env
        return self._client.call("shell.exec", params)


class StatePrimitives:
    """State and snapshot primitives."""

    def __init__(self, client):
        self._client = client

    def checkpoint(self, label: str = "") -> dict:
        """Create a workspace snapshot."""
        params = {}
        if label:
            params["label"] = label
        result = self._client.call("state.checkpoint", params)
        self._client._events.emit("checkpoint", result)
        return result

    def restore(self, checkpoint_id: str) -> dict:
        """Restore workspace to a previous snapshot."""
        return self._client.call("state.restore", {"checkpoint_id": checkpoint_id})

    def list(self) -> dict:
        """List all checkpoints."""
        return self._client.call("state.list", {})


class VerifyPrimitives:
    """Verification primitives."""

    def __init__(self, client):
        self._client = client

    def test(self, command: str = "pytest", timeout_s: int = 60) -> dict:
        """Run tests and return structured results."""
        return self._client.call("verify.test", {"command": command, "timeout_s": timeout_s})


class CodePrimitives:
    """Code analysis primitives."""

    def __init__(self, client):
        self._client = client

    def search(self, query: str, path: str = "", regex: bool = False,
               case_sensitive: bool = False, max_results: int = 50) -> dict:
        """Search code in workspace."""
        params: dict[str, Any] = {
            "query": query,
            "regex": regex,
            "case_sensitive": case_sensitive,
            "max_results": max_results,
        }
        if path:
            params["path"] = path
        return self._client.call("code.search", params)
