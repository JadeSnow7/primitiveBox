"""
PrimitiveBox Python SDK

A Python client for interacting with PrimitiveBox AI primitives.

Usage:
    from primitivebox import PrimitiveBoxClient

    client = PrimitiveBoxClient("http://localhost:8080")
    result = client.fs.read("src/main.py")

    sandbox_client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")
    sandbox_result = sandbox_client.fs.read("src/main.py")
"""

from .client import PrimitiveBoxClient, AsyncPrimitiveBoxClient
from .primitives import FSPrimitives, ShellPrimitives, StatePrimitives, VerifyPrimitives, CodePrimitives
from .events import EventEmitter

__version__ = "0.1.0"
__all__ = [
    "PrimitiveBoxClient",
    "AsyncPrimitiveBoxClient",
    "FSPrimitives",
    "ShellPrimitives",
    "StatePrimitives",
    "VerifyPrimitives",
    "CodePrimitives",
    "EventEmitter",
]
