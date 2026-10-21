import readline from "node:readline";

export const PROTOCOL = "freeagent-ext/1";

export class RPCError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "RPCError";
    this.code = code;
    this.message = message;
  }
}

class Host {
  constructor(extension) {
    this.extension = extension;
    this.nextId = 0;
    this.pending = new Map();
  }

  async call(method, params = {}) {
    const payload = { ...params };
    if (!payload.invocation) payload.invocation = this.extension.activeInvocation;
    if (!payload.invocation) throw new RPCError(-32005, "invocation is required");
    const id = `host-${++this.nextId}`;
    const result = new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
    });
    this.extension.send({ jsonrpc: "2.0", id, method, params: payload });
    return result;
  }

  resolve(message) {
    const id = String(message.id);
    const entry = this.pending.get(id);
    if (!entry) return false;
    this.pending.delete(id);
    if (message.error) {
      entry.reject(new RPCError(message.error.code ?? -32603,
        message.error.message ?? "host call failed"));
    } else {
      entry.resolve(message.result);
    }
    return true;
  }

  rejectAll(error) {
    for (const { reject } of this.pending.values()) reject(error);
    this.pending.clear();
  }

  async generate({ model = "current", messages = [], tools, schema, maxTokens } = {}) {
    const params = { model, messages };
    if (tools !== undefined) params.tools = tools;
    if (schema !== undefined) params.schema = schema;
    if (maxTokens !== undefined) params.max_tokens = maxTokens;
    return this.call("model.generate", params);
  }

  async embed({ model = "current", inputs = [] } = {}) {
    return this.call("model.embed", { model, inputs });
  }

  async delegate(plan) {
    return this.call("task.delegate", { plan });
  }

  async callTool(name, args = {}) {
    return this.call("tool.call", { name, arguments: args });
  }

  async report({ summary = "", items = [] } = {}) {
    return this.call("activity.report", { summary, items });
  }
}

export class Context {
  constructor(params, host) {
    const value = params ?? {};
    const message = value.message;
    this.raw = value;
    this.message = message && typeof message === "object"
      ? message.content ?? "" : message ?? "";
    this.history = value.history ?? [];
    this.workspace = value.workspace ?? "";
    this.dataDir = value.data_dir ?? "";
    this.settings = value.settings ?? {};
    this.invocation = value.invocation ?? "";
    this.phase = value.phase ?? "";
    this.purpose = value.purpose ?? "";
    this.step = value.step ?? 0;
    this.output = value.output ?? "";
    this.status = value.status ?? "";
    this.host = host;
  }

  get cancelled() {
    return this.host.extension.cancelled.has(this.invocation);
  }
}

export class Extension {
  constructor(id, { version = "0.1.0", stdin = process.stdin, stdout = process.stdout } = {}) {
    this.id = id;
    this.version = version;
    this.stdin = stdin;
    this.stdout = stdout;
    this.contextHandler = null;
    this.contextSpec = {};
    this.events = new Map();
    this.workflows = new Map();
    this.tools = new Map();
    this.activeInvocation = "";
    this.cancelled = new Set();
    this.host = new Host(this);
    this.initialized = false;
    this.readline = null;
  }

  context(options = {}, handler) {
    if (typeof options === "function") {
      handler = options;
      options = {};
    }
    this.contextHandler = handler;
    this.contextSpec = {
      phases: options.phases ?? ["turn"],
      purposes: options.purposes ?? [],
      max_chars: options.maxChars ?? options.max_chars ?? 0,
      placement: Array.isArray(options.placement)
        ? options.placement : [options.placement ?? "tail"],
    };
    return handler;
  }

  onEvent(name, handler) {
    this.events.set(name, handler);
    return handler;
  }

  workflow(name, options = {}, handler) {
    if (typeof options === "function") {
      handler = options;
      options = {};
    }
    this.workflows.set(name, {
      handler,
      inputSchema: options.inputSchema ?? options.input_schema,
      agentCallable: options.agentCallable ?? options.agent_callable ?? true,
    });
    return handler;
  }

  tool(name, options = {}, handler) {
    if (typeof options === "function") {
      handler = options;
      options = {};
    }
    this.tools.set(name, {
      handler,
      schema: options.schema ?? { type: "object" },
      readOnly: options.readOnly ?? options.read_only ?? false,
      description: options.description ?? "",
    });
    return handler;
  }

  async serve() {
    this.readline = readline.createInterface({ input: this.stdin, crlfDelay: Infinity });
    return new Promise((resolve) => {
      this.readline.on("line", (line) => {
        if (!line.trim()) return;
        void this.dispatchLine(line);
      });
      this.readline.on("close", () => {
        this.host.rejectAll(new RPCError(-32006, "host connection closed"));
        resolve(0);
      });
    });
  }

  async dispatchLine(line) {
    let request;
    try {
      request = JSON.parse(line);
      if (!request || typeof request !== "object") throw new RPCError(-32600, "invalid request");
      if (request.id !== undefined && !request.method && this.host.resolve(request)) return;
      if (request.method === "$/cancel") {
        this.cancelled.add(request.params?.invocation);
        return;
      }
      const result = await this.dispatch(request);
      if (request.id !== undefined) this.send({ jsonrpc: "2.0", id: request.id, result });
    } catch (error) {
      if (request?.id === undefined) return;
      const rpc = error instanceof RPCError
        ? error : new RPCError(-32603, "internal error");
      this.send({ jsonrpc: "2.0", id: request.id,
        error: { code: rpc.code, message: rpc.message } });
    }
  }

  async dispatch(request) {
    const method = request.method;
    const params = request.params ?? {};
    if (method === "initialize") {
      if (params.protocol && params.protocol !== PROTOCOL) {
        throw new RPCError(-32600, "unsupported protocol");
      }
      this.initialized = true;
      return {
        id: this.id,
        version: this.version,
        provides: this.provides(),
        events: [...this.events.keys()].sort(),
        workflows: [...this.workflows.keys()].sort(),
      };
    }
    if (method === "shutdown") return null;
    if (!this.initialized) throw new RPCError(-32600, "extension is not initialized");
    if (method === "context.prepare") return this.invoke(this.contextHandler, params, { blocks: [] }, true);
    if (method === "turn.finished" || method === "operation.check") {
      return this.invoke(this.events.get(method), params, {});
    }
    if (method === "workflow.run") {
      const item = this.workflows.get(params.name);
      if (!item) throw new RPCError(-32601, "workflow not found");
      return this.invoke(item.handler, params, {});
    }
    if (method === "tools/list") return { tools: this.toolDefinitions() };
    if (method === "tools/call") {
      const item = this.tools.get(params.name);
      if (!item) throw new RPCError(-32601, "tool not found");
      return this.invoke(item.handler, params, {});
    }
    throw new RPCError(-32601, "method not found");
  }

  async invoke(handler, params, fallback, contextResult = false) {
    if (!handler) return fallback;
    const context = new Context(params, this.host);
    this.activeInvocation = context.invocation;
    try {
      let value = await handler(context, params.input ?? params.arguments ?? {});
      if (value === undefined || value === null) value = fallback;
      if (contextResult && Array.isArray(value)) value = { blocks: value };
      if (!value || typeof value !== "object" || Array.isArray(value)) {
        throw new RPCError(-32603, "handler returned an object of the wrong type");
      }
      return value;
    } finally {
      this.activeInvocation = "";
    }
  }

  provides() {
    const value = [];
    if (this.contextHandler) value.push("context");
    if (this.workflows.size) value.push("workflow");
    if (this.tools.size) value.push("tools");
    return value;
  }

  toolDefinitions() {
    return [...this.tools.entries()].map(([name, item]) => ({
      name,
      description: item.description,
      inputSchema: item.schema,
      ...(item.readOnly ? { readOnly: true } : {}),
    }));
  }

  send(value) {
    this.stdout.write(`${JSON.stringify(value)}\n`);
    if (typeof this.stdout.flush === "function") this.stdout.flush();
  }
}
