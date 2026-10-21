import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "sdk", "python"))
from freeagent_ext import Extension

ext = Extension("com.example.context")


@ext.context(phases=["turn"], max_chars=800, placement="tail")
async def prepare(ctx):
    if not ctx.message.strip():
        return []
    return [{"id": "hint", "text": "Replace this bounded context hint.",
             "placement": "tail", "label": "Template"}]


if __name__ == "__main__":
    raise SystemExit(ext.serve())
