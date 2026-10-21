# FreeAgent Extension Kit

This directory is the standalone authoring kit for `freeagent-ext/1`.

- `SPEC.md` defines the protocol and boundaries.
- `AUTHORING.md` defines implementation and acceptance rules.
- `schema/` contains the manifest and method schemas.
- `sdk/python/freeagent_ext.py` is a zero-dependency Python SDK.
- `sdk/node/freeagent-ext.mjs` is a zero-dependency Node SDK.
- `templates/` contains context, workflow, and tool starting points.
- `examples/` contains prompt-router, adaptive-memory, and triage-cluster.

The method schemas include `operation.check`, which receives a bounded,
credential-redacted preview before a side-effecting tool call and can return
`allow`, `ask`, or `deny`. The Harness remains the owner of arguments,
approval, dispatch, and execution evidence.

From the repository root:

```text
freeagent ext check --path extension-kit/examples/prompt-router/com.freeagent.prompt-router
freeagent ext run --path extension-kit/examples/prompt-router/com.freeagent.prompt-router \
  --fixture extension-kit/examples/prompt-router/com.freeagent.prompt-router/fixture.json
```

The fixture runner is offline and safe to replay. Live workflow execution uses
the configured Harness model and budgets:

```text
freeagent ext run --host live --path <extension> --workflow <name> --input <json>
```

Mock fixture host replay is intentionally limited to the currently implemented
context path. Workflow fixtures are included as executable contract examples
for the Host API shape and become runnable when the mock Host replay is enabled
for that entrypoint.
