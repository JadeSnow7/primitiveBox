"""
PrimitiveBox CVR Event Stream Demo
===================================
Run alongside run_demo.py to see real-time CVR events via SSE.

Usage:
    python3 stream_demo.py
"""

import os
from primitivebox import PrimitiveBoxClient

PB_HOST = os.getenv("PB_HOST", "http://localhost:8080")
pb = PrimitiveBoxClient(PB_HOST)

ICONS = {
    "primitive.start": "▶",
    "primitive.complete": "✓",
    "cvr.checkpoint": "📌",
    "cvr.verify.pass": "✅",
    "cvr.verify.fail": "❌",
    "cvr.recover.rollback": "↩",
    "cvr.recover.retry": "🔄",
    "cvr.recover.escalate": "⚠",
}

print("─" * 50)
print("  PrimitiveBox CVR Event Stream")
print("─" * 50)
print("  Listening for events...\n")

try:
    for event in pb.events.stream():
        etype = event.get("type", "unknown")
        icon = ICONS.get(etype, "·")

        if etype == "primitive.start":
            prim = event.get("primitive", "?")
            risk = event.get("risk_level", "?")
            print(f"  {icon} {prim}  (risk: {risk})")

        elif etype == "primitive.complete":
            prim = event.get("primitive", "?")
            dur = event.get("duration_ms", "?")
            print(f"  {icon} {prim} done ({dur}ms)")

        elif etype == "cvr.checkpoint":
            cid = event.get("checkpoint_id", "?")
            label = event.get("label", "")
            print(f"  {icon} Checkpoint: {cid} ({label})")

        elif etype in ("cvr.verify.pass", "cvr.verify.fail"):
            summary = event.get("summary", "")
            print(f"  {icon} Verify: {summary}")

        elif etype.startswith("cvr.recover"):
            action = event.get("action", "?")
            reason = event.get("reason", "")
            print(f"  {icon} Recover: {action} — {reason}")

        else:
            detail = str(event)[:100]
            print(f"  {icon} {etype}: {detail}")

except KeyboardInterrupt:
    print("\n\n  Stream stopped.")
