# FreeAgent Extension Kit

This directory is the standalone authoring kit for `freeagent-ext/1`.

Read the [lightweight Core development principles](../docs/EXTENSION_DEVELOPMENT_PRINCIPLES.md)
first: optional business policy stays with the capability that owns it, while
execution authority and reliable records remain in Core.

Start with the [extension development guide](../docs/EXTENSION_DEVELOPMENT_GUIDE.md)
for current CLI examples, installation, permissions, known SDK gaps, and
implemented optional state and delivery capabilities.

- `SPEC.md` defines the protocol and boundaries.
- `AUTHORING.md` defines implementation and acceptance rules.
- `schema/` contains the manifest and method schemas.
- `sdk/python/freeagent_ext.py` is a zero-dependency Python SDK.
- `sdk/node/freeagent-ext.mjs` is a zero-dependency Node SDK.
- `templates/` contains context, workflow, and tool starting points.
- `examples/` contains prompt-router, adaptive-memory, triage-cluster and a standalone Go context example (no SDK or interpreter runtime).

The method schemas include `operation.check`, which receives a bounded,
credential-redacted preview before a side-effecting tool call and can return
`allow`, `ask`, or `deny`. The Harness remains the owner of arguments,
approval, dispatch, and execution evidence.

The delivery schemas cover concrete `reply_parts` returned by `turn.finished`,
the `delivery.resources/propose/read/withdraw` Host methods and Core's
`delivery.check` callback. Expression algorithms and mode settings belong to
extensions; plain Core has no segmentation modes. Delivery requires explicit manifest opt-in and
authorized conversation rules. Review has a fresh, short-lived invocation
and allows only activity, read and withdrawal; it cannot run models/tools or
submit another candidate. See [SPEC](SPEC.md#scoped-replies-and-one-time-delivery).

From the repository root:

```text
freeagent ext check --path extension-kit/examples/prompt-router/com.freeagent.prompt-router
freeagent ext test --path extension-kit/examples/prompt-router/com.freeagent.prompt-router \
  --fixture extension-kit/examples/prompt-router/com.freeagent.prompt-router/fixture.json
```

The fixture Host is offline, but it still starts trusted local extension code;
it is not an OS sandbox. Live workflow execution uses
the configured Harness model and budgets:

```text
freeagent ext run --host live --path <extension> --workflow <name> --input <json>
```

Mock fixtures currently support context and workflow entrypoints, including
deterministic workflow Host responses. They do not exercise real Provider,
database, permission, or delivery behavior.

Current SDKs and the tool template use Core's `tool.call`, string `content` and
`is_error`. The v8.0 SDK mismatch was fixed for v8.0.1 and is covered by actual
Core-to-SDK stdio dispatch tests for Python and Node. `ext check` still only
probes discovery, so extensions also need a real tool-call test.

Bounded no-tool input analysis and scheduled generation are documented in [SUPPLEMENTAL_EXTENSION_EXECUTION](../docs/SUPPLEMENTAL_EXTENSION_EXECUTION.md). Both require declared capabilities, separate user authorization and the existing Run/Attempt budget; no enabled rules means no proactive timer.
