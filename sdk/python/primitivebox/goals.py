"""
Goal composition helpers for the PrimitiveBox Python SDK.

Provides create/get/replay wrappers around the /api/v1/goals REST endpoints.
Both sync (GoalPrimitives) and async (AsyncGoalPrimitives) variants are available.
"""

from __future__ import annotations

import asyncio
import json
import urllib.error
import urllib.request
from typing import Any, Optional


class GoalPrimitives:
    """Synchronous goal composition helpers."""

    def __init__(self, client: Any) -> None:
        self._client = client

    def _base_url(self) -> str:
        return f"{self._client.endpoint}/api/v1/goals"

    def _request(self, method: str, path: str, body: Optional[dict] = None) -> Any:
        url = f"{self._base_url()}{path}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(
            url,
            data=data,
            method=method,
            headers={"Content-Type": "application/json"},
        )
        try:
            with urllib.request.urlopen(req) as resp:
                return json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"goal API error {e.code}: {e.read().decode()}") from e

    def create(
        self,
        description: str,
        packages: Optional[list[str]] = None,
        sandbox_ids: Optional[list[str]] = None,
    ) -> dict:
        """Create a new composition goal."""
        return self._request("POST", "", {
            "description": description,
            "packages": packages or [],
            "sandbox_ids": sandbox_ids or [],
        })

    def get(self, goal_id: str) -> dict:
        """Fetch a goal by ID, including verification truth such as check_type, check_params, verdict, evidence, and running/passed/failed status."""
        return self._request("GET", f"/{goal_id}")

    def list(self) -> list[dict]:
        """List all goals."""
        result = self._request("GET", "")
        return result.get("goals", [])

    def replay(self, goal_id: str, mode: str = "full") -> dict:
        """Trigger a goal replay. mode: 'full' | 'skip_passed' | 'step_by_step'."""
        return self._request("POST", f"/{goal_id}/replay", {"mode": mode})

    def execute(self, goal_id: str) -> dict:
        """Launch background execution of a goal. Returns immediately with 202."""
        return self._request("POST", f"/{goal_id}/execute")

    def list_bindings(self, goal_id: str) -> list[dict]:
        """List cross-package bindings for a goal."""
        result = self._request("GET", f"/{goal_id}/bindings")
        return result.get("bindings", [])

    def approve(self, goal_id: str, review_id: str) -> dict:
        """Approve a pending review, allowing execution to proceed."""
        return self._request("POST", f"/{goal_id}/approve", {"review_id": review_id})

    def reject(self, goal_id: str, review_id: str, reason: str = "") -> dict:
        """Reject a pending review, marking the goal as failed."""
        return self._request("POST", f"/{goal_id}/reject", {"review_id": review_id, "reason": reason})

    def resume(self, goal_id: str) -> dict:
        """Resume a paused goal after all reviews have been decided."""
        return self._request("POST", f"/{goal_id}/resume")


class AsyncGoalPrimitives:
    """Async goal composition helpers backed by GoalPrimitives via a thread executor."""

    def __init__(self, client: Any) -> None:
        self._sync = GoalPrimitives(client)

    def _run(self, fn, *args, **kwargs):
        loop = asyncio.get_event_loop()
        return loop.run_in_executor(None, lambda: fn(*args, **kwargs))

    async def create(
        self,
        description: str,
        packages: Optional[list[str]] = None,
        sandbox_ids: Optional[list[str]] = None,
    ) -> dict:
        """Create a new composition goal."""
        return await self._run(self._sync.create, description, packages, sandbox_ids)

    async def get(self, goal_id: str) -> dict:
        """Fetch a goal by ID, including verification truth such as check_type, check_params, verdict, evidence, and running/passed/failed status."""
        return await self._run(self._sync.get, goal_id)

    async def list(self) -> list[dict]:
        """List all goals."""
        return await self._run(self._sync.list)

    async def replay(self, goal_id: str, mode: str = "full") -> dict:
        """Trigger a goal replay. mode: 'full' | 'skip_passed' | 'step_by_step'."""
        return await self._run(self._sync.replay, goal_id, mode)

    async def execute(self, goal_id: str) -> dict:
        """Launch background execution of a goal. Returns immediately with 202."""
        return await self._run(self._sync.execute, goal_id)

    async def list_bindings(self, goal_id: str) -> list[dict]:
        """List cross-package bindings for a goal."""
        return await self._run(self._sync.list_bindings, goal_id)

    async def approve(self, goal_id: str, review_id: str) -> dict:
        """Approve a pending review, allowing execution to proceed."""
        return await self._run(self._sync.approve, goal_id, review_id)

    async def reject(self, goal_id: str, review_id: str, reason: str = "") -> dict:
        """Reject a pending review, marking the goal as failed."""
        return await self._run(self._sync.reject, goal_id, review_id, reason)

    async def resume(self, goal_id: str) -> dict:
        """Resume a paused goal after all reviews have been decided."""
        return await self._run(self._sync.resume, goal_id)
