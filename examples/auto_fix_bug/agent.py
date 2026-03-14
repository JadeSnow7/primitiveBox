"""
Demo Agent: inspect a PrimitiveBox sandbox through the host gateway.

Usage:
    python agent.py [endpoint] [sandbox_id]

Example:
    python agent.py http://localhost:8080 sb-12345678
"""

from __future__ import annotations

import sys

from primitivebox import PrimitiveBoxClient


def main() -> None:
    endpoint = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080"
    sandbox_id = sys.argv[2] if len(sys.argv) > 2 else ""
    client = PrimitiveBoxClient(endpoint, sandbox_id=sandbox_id)

    client.on("checkpoint", lambda e: print(f"checkpoint: {e['data']['checkpoint_id']}"))
    client.on("fail", lambda e: print(f"call failed: {e}"))

    print("PrimitiveBox demo agent")
    print("=" * 40)
    print(f"Endpoint: {endpoint}")
    print(f"Sandbox:  {sandbox_id or '(host workspace mode)'}")

    print("\nHealth")
    print(client.health())

    print("\nAvailable primitives")
    for primitive in client.list_primitives():
        print(f"- {primitive.get('name')}: {primitive.get('description')}")

    print("\nWorkspace entries")
    entries = client.fs.list(".", recursive=False).get("data", {}).get("entries", [])
    for entry in entries[:10]:
        print(f"- {entry['path']}")

    print("\nCheckpoint")
    checkpoint = client.state.checkpoint("demo-run")
    print(checkpoint["data"]["checkpoint_id"])

    print("\nVerification")
    verify_result = client.verify.test("test -d /workspace")
    print(verify_result["data"]["summary"])


if __name__ == "__main__":
    main()
