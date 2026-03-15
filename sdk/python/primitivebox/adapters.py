"""
PrimitiveBox LLM Adapters

Converts the server's /primitives schema into native tool declaration formats
for popular LLM APIs:

    - OpenAI function_calling  (gpt-4o, gpt-4-turbo, etc.)
    - Anthropic tool_use       (claude-3-5-sonnet, claude-3-haiku, etc.)

Usage::

    from primitivebox import PrimitiveBoxClient
    from primitivebox.adapters import export_openai_tools, export_claude_tools

    client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-xxx")

    # OpenAI
    tools = export_openai_tools(client)
    # → list[dict] ready for openai.ChatCompletion(tools=tools)

    # Anthropic
    tools = export_claude_tools(client)
    # → list[dict] ready for anthropic.messages.create(tools=tools)
"""

from __future__ import annotations

import json
from typing import TYPE_CHECKING, Any, Optional

if TYPE_CHECKING:
    from .client import PrimitiveBoxClient


def export_openai_tools(client: "PrimitiveBoxClient", primitives: Optional[list] = None) -> list[dict]:
    """
    Build an OpenAI ``tools`` list from the primitives exposed by *client*.

    Each primitive schema is mapped to an OpenAI function declaration::

        tools = [
            {
                "type": "function",
                "function": {
                    "name": "<method>",          # dots replaced with underscores
                    "description": "...",
                    "parameters": { ... }        # JSON Schema object
                }
            },
            ...
        ]

    Args:
        client: A connected ``PrimitiveBoxClient``.
        primitives: Optional pre-fetched primitive list. If ``None``, calls
                    ``client.list_primitives()`` automatically.

    Returns:
        A list of OpenAI tool dicts ready for ``openai.chat.completions.create(tools=...)``.
    """
    if primitives is None:
        primitives = client.list_primitives()

    tools = []
    for prim in primitives:
        name = prim.get("name", "")
        description = prim.get("description", "")
        input_schema = prim.get("input")

        # Parse JSON schema if it's a string
        if isinstance(input_schema, str):
            try:
                input_schema = json.loads(input_schema)
            except json.JSONDecodeError:
                input_schema = {"type": "object", "properties": {}}
        elif input_schema is None:
            input_schema = {"type": "object", "properties": {}}

        # OpenAI requires function names without dots
        oai_name = name.replace(".", "_")

        tools.append({
            "type": "function",
            "function": {
                "name": oai_name,
                "description": f"[primitivebox:{name}] {description}",
                "parameters": input_schema,
            },
        })
    return tools


def export_claude_tools(client: "PrimitiveBoxClient", primitives: Optional[list] = None) -> list[dict]:
    """
    Build an Anthropic ``tools`` list from the primitives exposed by *client*.

    Each primitive schema is mapped to a Claude tool declaration::

        tools = [
            {
                "name": "fs_read",
                "description": "...",
                "input_schema": { "type": "object", "properties": { ... } }
            },
            ...
        ]

    Args:
        client: A connected ``PrimitiveBoxClient``.
        primitives: Optional pre-fetched primitive list.

    Returns:
        A list of Anthropic tool dicts ready for ``anthropic.messages.create(tools=...)``.
    """
    if primitives is None:
        primitives = client.list_primitives()

    tools = []
    for prim in primitives:
        name = prim.get("name", "")
        description = prim.get("description", "")
        input_schema = prim.get("input")

        if isinstance(input_schema, str):
            try:
                input_schema = json.loads(input_schema)
            except json.JSONDecodeError:
                input_schema = {"type": "object", "properties": {}}
        elif input_schema is None:
            input_schema = {"type": "object", "properties": {}}

        # Claude accepts dots in tool names via underscores
        claude_name = name.replace(".", "_")

        tools.append({
            "name": claude_name,
            "description": f"[primitivebox:{name}] {description}",
            "input_schema": input_schema,
        })
    return tools


def dispatch_tool_call(client: "PrimitiveBoxClient", tool_name: str, tool_input: dict[str, Any]) -> Any:
    """
    Dispatch a single LLM tool call back to the PrimitiveBox RPC server.

    Converts ``tool_name`` from underscore-format (``fs_read``) back to
    dot-format (``fs.read``) before making the call.

    Args:
        client: A connected ``PrimitiveBoxClient``.
        tool_name: The function name as returned by the LLM (underscores).
        tool_input: The parsed JSON arguments from the LLM response.

    Returns:
        The raw result from ``client.call()``.

    Example::

        for tool_call in response.tool_calls:
            result = dispatch_tool_call(client, tool_call.function.name, tool_call.function.arguments)
    """
    # Convert underscores back to dots for the first separator only
    # e.g. "fs_read" → "fs.read", "macro_safe_edit" → "macro.safe_edit"
    method = _underscore_to_primitive(tool_name)
    return client.call(method, tool_input)


def _underscore_to_primitive(name: str) -> str:
    """Convert LLM-safe underscore name back to primitive dot notation."""
    # Known compound names that use underscores in the primitive name itself
    _compound_prefixes = {
        "macro_safe_edit": "macro.safe_edit",
        "verify_test": "verify.test",
        "state_checkpoint": "state.checkpoint",
        "state_restore": "state.restore",
        "state_list": "state.list",
        "code_search": "code.search",
        "code_symbols": "code.symbols",
        "fs_read": "fs.read",
        "fs_write": "fs.write",
        "fs_list": "fs.list",
        "fs_diff": "fs.diff",
        "shell_exec": "shell.exec",
    }
    return _compound_prefixes.get(name, name.replace("_", ".", 1))
