import io
import json
import unittest

from freeagent_ext import Extension


def lines(value):
    return [json.loads(item) for item in value.getvalue().splitlines()]


class ExtensionSDKTest(unittest.TestCase):
    def test_context_and_host_call(self):
        incoming = io.StringIO(
            '{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocol":"freeagent-ext/1"}}\n'
            '{"jsonrpc":"2.0","id":"2","method":"context.prepare","params":{"invocation":"turn-1","message":{"content":"hello"}}}\n'
            '{"jsonrpc":"2.0","id":"host-1","result":{"content":"mocked"}}\n'
        )
        outgoing = io.StringIO()
        extension = Extension("com.example.sdk", stdin=incoming, stdout=outgoing)

        @extension.context(phases=["turn"], placement="tail")
        async def prepare(ctx):
            result = await ctx.host.generate(messages=[{"role": "user", "content": ctx.message}])
            return [{"id": "mock", "text": result["content"]}]

        extension.serve()
        output = lines(outgoing)
        self.assertEqual(output[0]["result"]["provides"], ["context"])
        self.assertEqual(output[1]["method"], "model.generate")
        self.assertEqual(output[1]["params"]["invocation"], "turn-1")
        self.assertEqual(output[2]["result"]["blocks"][0]["text"], "mocked")

    def test_workflow_and_tool_registration(self):
        incoming = io.StringIO(
            '{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocol":"freeagent-ext/1"}}\n'
            '{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}\n'
            '{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"length","arguments":{"text":"abc"}}}\n'
            '{"jsonrpc":"2.0","id":"4","method":"workflow.run","params":{"invocation":"w","name":"hello","input":{}}}\n'
        )
        outgoing = io.StringIO()
        extension = Extension("com.example.sdk", stdin=incoming, stdout=outgoing)

        @extension.workflow("hello")
        async def hello(ctx, value):
            return {"output": "workflow"}

        @extension.tool("length", schema={"type": "object"}, read_only=True)
        async def length(ctx, value):
            return {"content": [{"type": "text", "text": str(len(value["text"]))}]}

        extension.serve()
        output = lines(outgoing)
        self.assertEqual(set(output[0]["result"]["provides"]), {"workflow", "tools"})
        self.assertTrue(output[1]["result"]["tools"][0]["readOnly"])
        self.assertEqual(output[2]["result"]["content"][0]["text"], "3")
        self.assertEqual(output[3]["result"]["output"], "workflow")


if __name__ == "__main__":
    unittest.main()
