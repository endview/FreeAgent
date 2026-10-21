import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "sdk", "python"))

from freeagent_ext import Extension


ROUTES = {
    "code": "For code work, state the smallest verified change and include test evidence.",
    "writing": "For writing work, preserve the user's meaning and return a clean final draft.",
    "data": "For data work, state the source, transformation, and uncertainty separately.",
}

KEYWORDS = {
    "code": ("error", "function", "code", "bug", "test"),
    "writing": ("rewrite", "draft", "writing"),
    "data": ("table", "statistics", "data", "csv"),
}

ext = Extension("com.freeagent.prompt-router", version="0.1.0")


@ext.context(phases=["turn"], max_chars=1200, placement="system")
async def prepare(ctx):
    text = str(ctx.message).lower()
    for category, words in KEYWORDS.items():
        if any(word.lower() in text for word in words):
            return [{
                "id": "route",
                "text": ROUTES[category],
                "placement": "system",
                "label": "Route: " + category,
            }]
    return []


if __name__ == "__main__":
    raise SystemExit(ext.serve())
