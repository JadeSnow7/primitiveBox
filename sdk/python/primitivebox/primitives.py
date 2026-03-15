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

    def diff(self, path: str = "", staged: bool = False) -> dict:
        """Show uncommitted workspace changes as a unified diff against the last Git checkpoint."""
        params: dict[str, Any] = {"staged": staged}
        if path:
            params["path"] = path
        return self._client.call("fs.diff", params)

    def stream_diff(self, path: str = "", staged: bool = False):
        """Stream fs.diff progress/events over SSE."""
        params: dict[str, Any] = {"staged": staged}
        if path:
            params["path"] = path
        return self._client.stream_call("fs.diff", params)


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

    def stream_exec(self, command: str, timeout_s: int = 30, env: Optional[dict] = None):
        """Stream shell.exec output over SSE."""
        params: dict[str, Any] = {"command": command, "timeout_s": timeout_s}
        if env:
            params["env"] = env
        return self._client.stream_call("shell.exec", params)


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

    def symbols(self, path: str, kinds: Optional[list] = None) -> dict:
        """
        Extract top-level symbols (functions, classes, methods) from a source file.

        Returns a structural outline without requiring a full file read,
        making it LLM-context-efficient.

        Args:
            path: File path relative to workspace.
            kinds: Optional list to filter by kind: ``['function', 'class', 'method']``.
                   Returns all kinds if not provided.

        Returns:
            Dict with ``symbols`` list, ``language`` string, and ``total`` count.
        """
        params: dict[str, Any] = {"path": path}
        if kinds:
            params["kinds"] = kinds
        return self._client.call("code.symbols", params)


class DBPrimitives:
    """Read-only database primitives."""

    def __init__(self, client):
        self._client = client

    def schema(self, connection: dict[str, Any]) -> dict:
        """Inspect schema metadata for a sqlite or postgres connection."""
        return self._client.call("db.schema", {"connection": connection})

    def query_readonly(self, connection: dict[str, Any], query: str, max_rows: int = 100) -> dict:
        """Run a capped, read-only SQL query."""
        return self._client.call(
            "db.query_readonly",
            {
                "connection": connection,
                "query": query,
                "max_rows": max_rows,
            },
        )


class BrowserPrimitives:
    """Sandbox-local browser automation primitives."""

    def __init__(self, client):
        self._client = client

    def goto(self, url: str, session_id: str = "", timeout_s: int = 30) -> dict:
        """Navigate to a URL and create or resume a browser session."""
        params: dict[str, Any] = {"url": url, "timeout_s": timeout_s}
        if session_id:
            params["session_id"] = session_id
        return self._client.call("browser.goto", params)

    def extract(self, session_id: str, selector: str) -> dict:
        """Extract text content from the current page."""
        return self._client.call("browser.extract", {"session_id": session_id, "selector": selector})

    def click(self, session_id: str, selector: str, timeout_s: int = 10) -> dict:
        """Click a selector in the active page."""
        return self._client.call(
            "browser.click",
            {"session_id": session_id, "selector": selector, "timeout_s": timeout_s},
        )

    def screenshot(self, session_id: str, full_page: bool = True) -> dict:
        """Capture a base64-encoded PNG screenshot."""
        return self._client.call(
            "browser.screenshot",
            {"session_id": session_id, "full_page": full_page},
        )


class MacroPrimitives:
    """Compound macro primitives for atomic multi-step operations."""

    def __init__(self, client):
        self._client = client

    def safe_edit(
        self,
        path: str,
        test_command: str,
        content: str = "",
        mode: str = "overwrite",
        search: str = "",
        replace: str = "",
        checkpoint_label: str = "",
    ) -> dict:
        """
        Atomically perform: checkpoint → write → verify → restore-on-failure.

        This replaces 4 separate RPC calls with a single HTTP round-trip.
        On test failure the workspace is automatically rolled back to the
        pre-edit checkpoint.

        Args:
            path: Target file path (relative to workspace).
            test_command: Command to run after the edit (e.g. ``"pytest tests/"``).
            content: New file contents (overwrite mode).
            mode: ``"overwrite"`` or ``"search_replace"``.
            search: Text to find (``search_replace`` mode only).
            replace: Replacement text (``search_replace`` mode only).
            checkpoint_label: Optional label for the auto-created checkpoint.

        Returns:
            Dict with ``passed``, ``rolled_back``, ``checkpoint_id``,
            ``test_output``, and ``diff``.
        """
        params: dict[str, Any] = {
            "path": path,
            "test_command": test_command,
            "mode": mode,
        }
        if mode == "search_replace":
            params["search"] = search
            params["replace"] = replace
        else:
            params["content"] = content
        if checkpoint_label:
            params["checkpoint_label"] = checkpoint_label
        return self._client.call("macro.safe_edit", params)
