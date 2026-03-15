"""
PrimitiveBox Python SDK

A Python client for interacting with PrimitiveBox AI primitives.

Usage::

    from primitivebox import PrimitiveBoxClient
    from primitivebox.adapters import export_openai_tools

    client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-xxx")
    result = client.fs.read("src/main.py")
    symbols = client.code.symbols("src/main.py")
    tools = export_openai_tools(client)  # OpenAI tools list
"""

from .client import PrimitiveBoxClient
from .async_client import AsyncPrimitiveBoxClient
from .primitives import BrowserPrimitives, CodePrimitives, DBPrimitives, FSPrimitives, MacroPrimitives, ShellPrimitives, StatePrimitives, VerifyPrimitives
from .events import EventEmitter
from . import adapters

__version__ = "0.3.0"
__all__ = [
    "PrimitiveBoxClient",
    "AsyncPrimitiveBoxClient",
    "FSPrimitives",
    "ShellPrimitives",
    "StatePrimitives",
    "VerifyPrimitives",
    "CodePrimitives",
    "MacroPrimitives",
    "DBPrimitives",
    "BrowserPrimitives",
    "EventEmitter",
    "adapters",
]
