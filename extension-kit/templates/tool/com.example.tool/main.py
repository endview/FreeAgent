import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "sdk", "python"))
from freeagent_ext import Extension

ext = Extension("com.example.tool")


@ext.tool("echo", schema={
    "type": "object",
    "properties": {"text": {"type": "string"}},
    "required": ["text"],
    "additionalProperties": False,
}, read_only=True, description="Return the supplied text.")
async def echo(ctx, value):
    return {"content": [{"type": "text", "text": value.get("text", "")}]}


if __name__ == "__main__":
    raise SystemExit(ext.serve())
