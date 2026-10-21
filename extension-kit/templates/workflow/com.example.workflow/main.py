import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "sdk", "python"))
from freeagent_ext import Extension

ext = Extension("com.example.workflow")


@ext.workflow("run", input_schema={
    "type": "object",
    "properties": {"text": {"type": "string"}},
    "required": ["text"],
    "additionalProperties": False,
})
async def run(ctx, value):
    return {"output": "Template workflow: " + value.get("text", "")}


if __name__ == "__main__":
    raise SystemExit(ext.serve())
