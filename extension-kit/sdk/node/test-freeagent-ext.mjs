import assert from "node:assert/strict";
import { test } from "node:test";
import { Writable } from "node:stream";
import { Extension } from "./freeagent-ext.mjs";

test("node SDK registers capabilities and handles context", async () => {
  const output = [];
  const stdout = new Writable({
    write(chunk, encoding, callback) {
      output.push(JSON.parse(chunk.toString()));
      callback();
    },
  });
  const ext = new Extension("com.example.node", { stdout });
  ext.context({ placement: "tail" }, (ctx) => [{ id: "hello", text: ctx.message, placement: "tail" }]);
  await ext.dispatchLine(JSON.stringify({
    jsonrpc: "2.0", id: "1", method: "initialize",
    params: { protocol: "freeagent-ext/1" },
  }));
  await ext.dispatchLine(JSON.stringify({
    jsonrpc: "2.0", id: "2", method: "context.prepare",
    params: { invocation: "i", message: { content: "hello" } },
  }));
  assert.deepEqual(output[0].result.provides, ["context"]);
  assert.equal(output[1].result.blocks[0].text, "hello");
});

test("node SDK accepts the Core tool.call contract", async () => {
  const output = [];
  const stdout = new Writable({ write(chunk, encoding, done) {
    output.push(JSON.parse(chunk.toString())); done();
  } });
  const ext = new Extension("com.example.node", { stdout });
  ext.tool("echo", { schema: { type: "object" }, readOnly: true }, (ctx, args) => {
    assert.equal(ctx.invocation, "tool-1");
    return { content: args.text, is_error: false };
  });
  await ext.dispatchLine(JSON.stringify({ jsonrpc: "2.0", id: "1", method: "initialize",
    params: { protocol: "freeagent-ext/1" } }));
  await ext.dispatchLine(JSON.stringify({ jsonrpc: "2.0", id: "2", method: "tool.call",
    params: { invocation: "tool-1", name: "echo", arguments: { text: "hello" } } }));
  assert.deepEqual(output[1].result, { content: "hello", is_error: false });
});
