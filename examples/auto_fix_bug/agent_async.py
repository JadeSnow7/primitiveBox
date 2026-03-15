"""
Async Demo Agent: uses OpenAI gpt-4o, AsyncPrimitiveBoxClient, and tool adapters
to automatically solve bugs inside a sandbox.

Usage:
    export OPENAI_API_KEY="sk-..."
    python agent_async.py [endpoint] [sandbox_id]

Example:
    python agent_async.py http://localhost:8080 sb-12345678
"""

import asyncio
import json
import os
import sys

try:
    from openai import AsyncOpenAI
except ImportError:
    print("Please install openai: pip install openai")
    sys.exit(1)

from primitivebox.async_client import AsyncPrimitiveBoxClient
from primitivebox.adapters import export_openai_tools, _underscore_to_primitive


async def dispatch_tool_call(client: AsyncPrimitiveBoxClient, tool_name: str, tool_input: dict) -> dict:
    """Async wrapper for dispatching a tool call to the client."""
    method = _underscore_to_primitive(tool_name)
    return await client.call(method, tool_input)


async def main():
    api_key = os.environ.get("OPENAI_API_KEY")
    if not api_key:
        print("Please set OPENAI_API_KEY environment variable. Example: export OPENAI_API_KEY='sk-...'")
        sys.exit(1)

    endpoint = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080"
    sandbox_id = sys.argv[2] if len(sys.argv) > 2 else ""

    openai_client = AsyncOpenAI(api_key=api_key)

    async with AsyncPrimitiveBoxClient(endpoint, sandbox_id=sandbox_id) as pb:
        # Fetch primitives asynchronously, then export to OpenAI format
        primitives = await pb.list_primitives()
        tools = export_openai_tools(pb, primitives=primitives)

        print(f"Loaded {len(tools)} tools from PrimitiveBox gateway.")

        messages = [
            {
                "role": "system", 
                "content": (
                    "You are an autonomous AI software engineer. "
                    "Your workspace is connected to PrimitiveBox. "
                    "You should locate any bugs in the project, fix them, and ensure tests pass. "
                    "Use code_symbols and code_search to browse codebase structure. "
                    "Use macro_safe_edit to make file changes and automatically verify them. "
                    "Do NOT guess line numbers; always read file content first before editing."
                )
            },
            {
                "role": "user", 
                "content": "Please review the workspace. Identify the problem, fix it securely, and confirm success."
            }
        ]

        print("\n--- Starting Auto-Fix Loop ---")
        
        for step in range(15):  # Max 15 steps to prevent infinite loop
            print(f"\n[Step {step + 1}] LLM is thinking...")
            try:
                response = await openai_client.chat.completions.create(
                    model="gpt-4o",
                    messages=messages,
                    tools=tools,
                    tool_choice="auto"
                )
            except Exception as e:
                print(f"OpenAI API Error: {e}")
                break

            msg = response.choices[0].message
            messages.append(msg)

            if getattr(msg, 'content', None):
                print(f"\nAgent: {msg.content}")

            if not msg.tool_calls:
                print("\n✅ Task completed!")
                break

            for tc in msg.tool_calls:
                name = tc.function.name
                
                try:
                    arguments = json.loads(tc.function.arguments)
                except json.JSONDecodeError:
                    arguments = {}

                print(f"\n-> Tool Call: {name}({arguments})")
                
                try:
                    # Execute tool against PrimitiveBox
                    result = await dispatch_tool_call(pb, name, arguments)
                    result_str = json.dumps(result, indent=2)
                except Exception as e:
                    # Provide error context back to the LLM
                    result_str = f"Error executing tool: {e}"
                    print(f"<- Tool Error: {e}")
                
                print(f"<- Result: {result_str[:400]}... (truncated)" if len(result_str) > 400 else f"<- Result: {result_str}")
                
                # Append tool response
                messages.append({
                    "role": "tool",
                    "tool_call_id": tc.id,
                    "name": name,
                    "content": result_str
                })


if __name__ == "__main__":
    asyncio.run(main())
