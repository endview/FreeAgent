"""Zero-dependency Python SDK for freeagent-ext/1.

The SDK deliberately keeps the transport small. FreeAgent owns permissions,
budgets, cancellation, process lifetime, and execution records.
"""

import asyncio
import inspect
import json
import sys


PROTOCOL = "freeagent-ext/1"


class RPCError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


class Host:
    def __init__(self, extension):
        self._extension = extension
        self._next_id = 0

    async def call(self, method, params=None):
        payload = dict(params or {})
        payload.setdefault("invocation", self._extension._active_invocation)
        return self._call_sync(method, payload)

    async def generate(self, model="current", messages=None, tools=None,
                       schema=None, max_tokens=None):
        params = {"model": model, "messages": messages or []}
        if tools is not None:
            params["tools"] = tools
        if schema is not None:
            params["schema"] = schema
        if max_tokens is not None:
            params["max_tokens"] = max_tokens
        return await self.call("model.generate", params)

    async def embed(self, model="current", inputs=None):
        return await self.call("model.embed", {
            "model": model, "inputs": inputs or [],
        })

    async def delegate(self, plan):
        return await self.call("task.delegate", {"plan": plan})

    async def call_tool(self, name, arguments=None):
        return await self.call("tool.call", {
            "name": name, "args": arguments or {},
        })

    async def report(self, summary="", items=None):
        return await self.call("activity.report", {
            "summary": summary, "items": items or [],
        })

    def _call_sync(self, method, params):
        self._next_id += 1
        request_id = "host-%d" % self._next_id
        self._extension._send({
            "jsonrpc": "2.0", "id": request_id,
            "method": method, "params": params,
        })
        while True:
            line = self._extension._stdin.readline()
            if not line:
                raise RPCError(-32006, "host connection closed")
            message = json.loads(line)
            if message.get("id") == request_id and "method" not in message:
                error = message.get("error")
                if error:
                    raise RPCError(error.get("code", -32603),
                                   error.get("message", "host call failed"))
                return message.get("result")
            if message.get("method") == "$/cancel":
                invocation = (message.get("params") or {}).get("invocation")
                self._extension._cancelled.add(invocation)
                continue
            # A second Harness request cannot be safely dispatched while a
            # handler is synchronously waiting for a Host response.
            if "id" in message:
                self._extension._send_error(
                    message.get("id"), -32601, "method not found")


class Context:
    def __init__(self, params, host):
        params = params or {}
        self.raw = params
        message = params.get("message")
        if isinstance(message, dict):
            self.message = message.get("content", "")
        else:
            self.message = message or ""
        self.history = params.get("history") or []
        self.workspace = params.get("workspace", "")
        self.settings = params.get("settings") or {}
        self.data_dir = params.get("data_dir", "")
        self.invocation = params.get("invocation", "")
        self.phase = params.get("phase", "")
        self.purpose = params.get("purpose", "")
        self.step = params.get("step", 0)
        self.output = params.get("output", "")
        self.status = params.get("status", "")
        self.host = host

    @property
    def cancelled(self):
        return self.invocation in self.host._extension._cancelled


class Extension:
    def __init__(self, extension_id, version="0.1.0", stdin=None, stdout=None):
        self.id = extension_id
        self.version = version
        self._stdin = stdin or sys.stdin
        self._stdout = stdout or sys.stdout
        self._context_handler = None
        self._context_spec = {}
        self._events = {}
        self._workflows = {}
        self._tools = {}
        self._active_invocation = ""
        self._cancelled = set()
        self._host = Host(self)
        self._initialized = False

    def context(self, phases=("turn",), max_chars=0, placement="tail"):
        if isinstance(phases, str):
            phases = [phases]
        if isinstance(placement, str):
            placement = [placement]

        def register(handler):
            self._context_handler = handler
            self._context_spec = {
                "phases": list(phases),
                "max_chars": max_chars,
                "placement": list(placement),
            }
            return handler

        return register

    def on_event(self, name):
        def register(handler):
            self._events[name] = handler
            return handler

        return register

    def workflow(self, name, input_schema=None, agent_callable=True):
        def register(handler):
            self._workflows[name] = {
                "handler": handler,
                "input_schema": input_schema,
                "agent_callable": agent_callable,
            }
            return handler

        return register

    def tool(self, name, schema=None, read_only=False, description=""):
        def register(handler):
            self._tools[name] = {
                "handler": handler,
                "schema": schema or {"type": "object"},
                "read_only": read_only,
                "description": description,
            }
            return handler

        return register

    def serve(self):
        for raw in self._stdin:
            if not raw.strip():
                continue
            request = {}
            try:
                request = json.loads(raw)
                if not isinstance(request, dict):
                    raise RPCError(-32600, "invalid request")
                result = self._dispatch(request)
                if "id" in request:
                    self._send_result(request["id"], result)
            except RPCError as error:
                if "id" in locals().get("request", {}):
                    self._send_error(request.get("id"), error.code, error.message)
            except Exception:
                if "id" in locals().get("request", {}):
                    self._send_error(request.get("id"), -32603, "internal error")
        return 0

    def _dispatch(self, request):
        method = request.get("method")
        params = request.get("params") or {}
        if not isinstance(method, str):
            raise RPCError(-32600, "method is required")
        if method == "initialize":
            if params.get("protocol") not in (None, PROTOCOL):
                raise RPCError(-32600, "unsupported protocol")
            self._initialized = True
            return {
                "id": self.id,
                "version": self.version,
                "provides": self._provides(),
                "events": sorted(self._events),
                "workflows": sorted(self._workflows),
            }
        if method == "$/cancel":
            self._cancelled.add(params.get("invocation"))
            return None
        if method == "shutdown":
            return None
        if not self._initialized:
            raise RPCError(-32600, "extension is not initialized")
        if method == "context.prepare":
            return self._invoke_context(params)
        if method == "turn.finished" or method == "operation.check":
            return self._invoke_event(method, params)
        if method == "workflow.run":
            return self._invoke_workflow(params)
        if method == "tools/list":
            return {"tools": self._tool_definitions()}
        if method == "tools/call":
            return self._invoke_tool(params)
        raise RPCError(-32601, "method not found")

    def _invoke_context(self, params):
        if self._context_handler is None:
            return {"blocks": []}
        return self._invoke(self._context_handler, Context(params, self._host),
                            default={"blocks": []}, context_result=True)

    def _invoke_event(self, method, params):
        handler = self._events.get(method)
        if handler is None:
            return {}
        return self._invoke(handler, Context(params, self._host), default={})

    def _invoke_workflow(self, params):
        name = params.get("name")
        entry = self._workflows.get(name)
        if entry is None:
            raise RPCError(-32601, "workflow not found")
        return self._invoke(entry["handler"], Context(params, self._host),
                            params.get("input") or {}, default={})

    def _invoke_tool(self, params):
        name = params.get("name")
        entry = self._tools.get(name)
        if entry is None:
            raise RPCError(-32601, "tool not found")
        return self._invoke(entry["handler"], Context(params, self._host),
                            params.get("arguments") or {}, default={})

    def _invoke(self, handler, *args, default=None, context_result=False):
        self._active_invocation = args[0].invocation if args and isinstance(args[0], Context) else ""
        try:
            result = handler(*args)
            if inspect.isawaitable(result):
                result = asyncio.run(result)
            if result is None:
                result = default
            if context_result and isinstance(result, list):
                result = {"blocks": result}
            if not isinstance(result, dict):
                raise RPCError(-32603, "handler returned an object of the wrong type")
            return result
        finally:
            self._active_invocation = ""

    def _provides(self):
        result = []
        if self._context_handler is not None:
            result.append("context")
        if self._workflows:
            result.append("workflow")
        if self._tools:
            result.append("tools")
        return result

    def _tool_definitions(self):
        result = []
        for name, entry in self._tools.items():
            item = {
                "name": name,
                "description": entry["description"],
                "inputSchema": entry["schema"],
            }
            if entry["read_only"]:
                item["readOnly"] = True
            result.append(item)
        return result

    def _send_result(self, request_id, result):
        self._send({"jsonrpc": "2.0", "id": request_id, "result": result})

    def _send_error(self, request_id, code, message):
        self._send({
            "jsonrpc": "2.0", "id": request_id,
            "error": {"code": code, "message": message},
        })

    def _send(self, message):
        self._stdout.write(json.dumps(message, ensure_ascii=False,
                                      separators=(",", ":")) + "\n")
        self._stdout.flush()


__all__ = ["Context", "Extension", "Host", "PROTOCOL", "RPCError"]
