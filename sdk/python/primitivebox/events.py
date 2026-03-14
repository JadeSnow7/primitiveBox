"""
Event system for PrimitiveBox SDK.

Supports subscribing to primitive execution events:
- 'call': Fired after every successful primitive call
- 'fail': Fired when a primitive call fails
- 'checkpoint': Fired when a checkpoint is created
"""

from typing import Callable, Any


class EventEmitter:
    """Simple event emitter for SDK callbacks."""

    def __init__(self):
        self._listeners: dict[str, list[Callable]] = {}

    def on(self, event: str, callback: Callable[[Any], None]) -> None:
        """Register a callback for an event."""
        if event not in self._listeners:
            self._listeners[event] = []
        self._listeners[event].append(callback)

    def off(self, event: str, callback: Callable[[Any], None]) -> None:
        """Remove a callback for an event."""
        if event in self._listeners:
            self._listeners[event] = [cb for cb in self._listeners[event] if cb != callback]

    def emit(self, event: str, data: Any = None) -> None:
        """Emit an event, calling all registered callbacks."""
        for callback in self._listeners.get(event, []):
            try:
                callback(data)
            except Exception:
                pass  # Don't let callback errors break the SDK

    def clear(self) -> None:
        """Remove all event listeners."""
        self._listeners.clear()
