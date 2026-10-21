import json
import os
import re
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "sdk", "python"))
from freeagent_ext import Extension


MAX_ITEMS = 64
WORD_RE = re.compile(r"[A-Za-z0-9_\u4e00-\u9fff]+")
ext = Extension("com.freeagent.adaptive-memory", version="0.1.0")


def memory_path(ctx):
    os.makedirs(ctx.data_dir, mode=0o700, exist_ok=True)
    return os.path.join(ctx.data_dir, "memory.json")


def load(ctx):
    try:
        with open(memory_path(ctx), "r", encoding="utf-8") as stream:
            value = json.load(stream)
        return value if isinstance(value, list) else []
    except FileNotFoundError:
        return []
    except (OSError, ValueError):
        return []


def save(ctx, items):
    path = memory_path(ctx)
    fd, temporary = tempfile.mkstemp(prefix=".memory-", dir=ctx.data_dir)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(items[-MAX_ITEMS:], stream, ensure_ascii=False, separators=(",", ":"))
            stream.write("\n")
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.remove(temporary)


def words(text):
    return set(WORD_RE.findall(str(text).lower()))


@ext.context(phases=["turn"], max_chars=1200, placement="tail")
async def recall(ctx):
    query = words(ctx.message)
    if not query:
        return []
    ranked = []
    for item in load(ctx):
        text = str(item.get("text", ""))
        score = len(query & words(text))
        if score:
            ranked.append((score, item))
    ranked.sort(key=lambda pair: (-pair[0], pair[1].get("id", "")))
    return [{
        "id": "memory-" + str(item.get("id", index)),
        "text": item.get("text", ""),
        "placement": "tail",
        "label": "Memory",
        "priority": min(100, score * 20),
    } for index, (score, item) in enumerate(ranked[:5])]


@ext.on_event("turn.finished")
async def learn(ctx):
    text = str(ctx.message).strip()
    if not text or ctx.status != "completed":
        return {"activity": {"summary": "No memory written", "items": []}}
    items = load(ctx)
    normalized = text[:800]
    existing = next((item for item in items if item.get("text") == normalized), None)
    if existing:
        existing["hits"] = int(existing.get("hits", 0)) + 1
        action = "refreshed"
    else:
        items.append({"id": len(items) + 1, "text": normalized, "hits": 1})
        action = "added"
    save(ctx, items)
    return {"activity": {
        "summary": "Memory " + action,
        "items": [{"text": normalized, "action": action}],
    }}


if __name__ == "__main__":
    raise SystemExit(ext.serve())
