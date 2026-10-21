import asyncio
import json
import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "sdk", "python"))
from freeagent_ext import Extension, RPCError


DEFAULT_CANDIDATES = [
    {"id": "docs", "text": "Review the documentation and identify missing acceptance evidence."},
    {"id": "tests", "text": "Run focused tests and investigate failures without changing unrelated code."},
    {"id": "ui", "text": "Inspect the browser workflow and report layout or interaction regressions."},
]
VERDICT_SCHEMA = {
    "type": "object",
    "properties": {"relevant": {"type": "boolean"}},
    "required": ["relevant"],
    "additionalProperties": False,
}
PLAN_SCHEMA = {
    "type": "object",
    "properties": {"objective": {"type": "string"}, "children": {"type": "array"}},
    "required": ["objective", "children"],
    "additionalProperties": True,
}
ext = Extension("com.freeagent.triage-cluster", version="0.1.0")


def candidates(ctx):
    path = os.path.join(ctx.data_dir, "candidates.json")
    try:
        with open(path, "r", encoding="utf-8") as stream:
            value = json.load(stream)
        if isinstance(value, list) and value:
            return value[:32]
    except (FileNotFoundError, OSError, ValueError):
        pass
    return DEFAULT_CANDIDATES


def cosine(left, right):
    denominator = math.sqrt(sum(x * x for x in left) * sum(x * x for x in right))
    return sum(a * b for a, b in zip(left, right)) / denominator if denominator else 0


@ext.workflow("triage", input_schema={
    "type": "object",
    "properties": {"task": {"type": "string", "minLength": 1}},
    "required": ["task"],
    "additionalProperties": False,
})
async def triage(ctx, value):
    task = value["task"]
    items = candidates(ctx)
    embedding = await ctx.host.embed(model="current",
                                     inputs=[task] + [item["text"] for item in items])
    vectors = embedding["vectors"]
    ranked = sorted(
        zip(items, vectors[1:]),
        key=lambda pair: cosine(vectors[0], pair[1]),
        reverse=True,
    )[:8]

    async def verdict(item):
        result = await ctx.host.generate(
            model="current",
            messages=[
                {"role": "system", "content": "Return JSON only. Decide whether the candidate is relevant."},
                {"role": "user", "content": "Task: " + task + "\nCandidate: " + item["text"]},
            ],
            schema=VERDICT_SCHEMA,
            max_tokens=30,
        )
        return item, bool(result.get("json", {}).get("relevant"))

    selected = [item for item, relevant in await asyncio.gather(
        *[verdict(item) for item, _ in ranked]
    ) if relevant]
    if not selected:
        selected = [item for item, _ in ranked[:1]]

    plan_result = await ctx.host.generate(
        model="current",
        messages=[{
            "role": "user",
            "content": json.dumps({"task": task, "candidates": selected}, ensure_ascii=False),
        }],
        schema=PLAN_SCHEMA,
        max_tokens=500,
    )
    plan = plan_result.get("json")
    if not isinstance(plan, dict):
        raise RPCError(-32603, "planner did not return a JSON plan")
    result = await ctx.host.delegate(plan)
    await ctx.host.report(summary="Triage completed",
                          items=[{"selected": len(selected)}])
    return {"output": result.get("synthesis", result)}


if __name__ == "__main__":
    raise SystemExit(ext.serve())
