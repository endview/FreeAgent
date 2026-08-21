# FreeAgent examples

这里的 bootstrap 与 artifact 是可演进教程，不是冻结的 SDK 兼容夹具。模块作者应先阅读
[`MODULE_DEVELOPMENT_V1`](../docs/MODULE_DEVELOPMENT_V1.md)；稳定、原创的 v1 合成夹具位于
`sdk/moduleapi/testdata/compat/v1/`。

## W1 Pure Chat Conversation 开发切片

默认 Echo seed 可以演示已接受的受限 Conversation 纵链。Conversation 必须先显式创建；
每个用户回合形成一个新 Run，并提交调用方观察到的精确 revision/head：

```powershell
New-Item -ItemType Directory -Force data-conversation | Out-Null

go run ./cmd/freeagent init `
  --db data-conversation/current.sqlite `
  --artifact-root data-conversation/artifacts `
  --seed examples/current-v1.bootstrap.seed.json

$conversation = "example-conversation-1"
$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")

go run ./cmd/freeagent conversation-create `
  --db data-conversation/current.sqlite `
  --conversation $conversation

$first = go run ./cmd/freeagent chat `
  --db data-conversation/current.sqlite `
  --artifact-root data-conversation/artifacts `
  --conversation $conversation `
  --conversation-revision 0 `
  --message "turn one" `
  --request-id conversation-example-turn-1 `
  --deadline $deadline | ConvertFrom-Json

$second = go run ./cmd/freeagent chat `
  --db data-conversation/current.sqlite `
  --artifact-root data-conversation/artifacts `
  --conversation $conversation `
  --conversation-revision $first.conversation_revision `
  --conversation-head-run $first.run_id `
  --message "turn two" `
  --request-id conversation-example-turn-2 `
  --deadline $deadline | ConvertFrom-Json

go run ./cmd/freeagent conversation-get `
  --db data-conversation/current.sqlite `
  --conversation $conversation
```

Only a successful head can be continued. A stale revision/head fails closed
instead of forking, and restored history always consists of complete
USER/ASSISTANT predecessor pairs. Context summary and ordered drop keep each
pair indivisible. Conversation scope, revision, head, Run projections and
history closure are included in backup verification and restore; the restored
head can accept the next turn without any model call during backup.

The deterministic local acceptance test extends this same path to 50 turns,
including process re-entry, backup/verify/restore after turn 25, exact re-entry
at turns 25 and 50, a final verified bundle, 50 closed Run/Attempt/Usage rows,
and zero Action, Memory, or Channel access. This is local deterministic
evidence, not real DeepSeek 50-turn acceptance.

The loopback `POST /v1/chat` endpoint can continue an existing Conversation by
accepting `conversation_id`, numeric `conversation_revision`, and—after the
first turn—`conversation_head_run_id`. It does not create Conversations; use
`conversation-create` before starting `serve`. This development slice is
limited to ordinary single-Agent, single-model Pure Chat. Conversation remains
mutually exclusive with Composite and is not claimed for Action or Channel.
It is not W1 completion, production multi-turn readiness, or real DeepSeek
50-turn acceptance.

## Controlled DeepSeek single-Agent Pure Chat

`current-v1.deepseek.bootstrap.seed.json` is a separate model-provider
assembly for ordinary single-Agent Pure Chat. It contains exactly one trusted
model module binding and deliberately installs no optional Role, RAG, Memory,
Skill, MCP, Tool, Action, Channel or Composite module.

```powershell
New-Item -ItemType Directory -Force data-deepseek | Out-Null

go run ./cmd/freeagent init `
  --db data-deepseek/current.sqlite `
  --artifact-root data-deepseek/artifacts `
  --seed examples/current-v1.deepseek.bootstrap.seed.json

$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")
go run ./cmd/freeagent chat `
  --db data-deepseek/current.sqlite `
  --artifact-root data-deepseek/artifacts `
  --profile deepseek-chat `
  --message "hello" `
  --request-id deepseek-example-1 `
  --deadline $deadline `
  --enable-deepseek
```

The adapter is default-off, fixes the official DeepSeek URL, accepts only its
compiled artifact/model-build allowlist, issues at most one POST for a model
Attempt, and never semantically replays `MODEL_UNKNOWN`. The API key is read
at dispatch time from `FREEAGENT_DEEPSEEK_API_KEY` by default; only an
environment-variable name may be selected with `--deepseek-api-key-env`.
`serve` exposes the same two explicit runtime flags. The same Profile can be
selected by the restricted Conversation path above, but this section proves
only controlled Provider wiring. Real DeepSeek 50-turn acceptance is still
pending and W1 is not complete.

## S2 optional Action provider

`current-v1.action.bootstrap.seed.json` is a separate Action-enabled assembly.
It binds the deterministic local `text.stats` module to the standard
`action.provider/v1` Port. The default `current-v1.bootstrap.seed.json`
remains unchanged and does not install, load, describe or expose an Action.

```powershell
New-Item -ItemType Directory -Force data-action | Out-Null

go run ./cmd/freeagent init `
  --db data-action/current.sqlite `
  --artifact-root data-action/artifacts `
  --seed examples/current-v1.action.bootstrap.seed.json

$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")

go run ./cmd/freeagent chat `
  --db data-action/current.sqlite `
  --artifact-root data-action/artifacts `
  --profile action-chat `
  --message "Count this text." `
  --request-id action-example-1 `
  --deadline $deadline
```

The selected Profile freezes the model-visible Action definition at admission.
The production registry then loads the implementation only by its exact local
module, version, artifact digest and adapter identity. `Describe` and `Prepare`
cannot execute the Action; the private executor remains reachable only through
the persisted Core Gateway permit. This example uses no external effect and
the normal chain is model request → `text.stats` → final model response.

The Action seed is only a bootstrap shortcut. W2-E2 also verifies installing the
same compiled `text.stats` handler into an ordinary Store initialized from
`current-v1.bootstrap.seed.json`; the installation path does not use or merge the
Action seed. For a fresh Store whose pointer revision is `1`, save this exact
single-line canonical plan (adjust the expected revision if the Store has moved):

```json
{"binding":{"authority_ceiling":{"allowed_provider_action_ids":["text.stats"],"allowed_workspace_ids":["local-chat"],"max_effect_class":"none","max_result_bytes":256,"schema_version":"action-authority-ceiling/v1","tenant_id":"default"},"config":{"actions":[{"local_effect_class":"none","max_result_bytes":256,"provider_action_id":"text.stats","public_action_id":"text.stats"}],"parameters":{},"schema_version":"action-binding-config/v1"},"failure_policy":"REQUIRED","port_binding_index":0},"desired_state":"ENABLED","expected_pointer_revision":1,"instance_id":"text-stats-unified","module":{"artifact_digest":"2331b8b5209f4acffd7fef96f55ce1e0841bc3c4e39d90581efb941ecae9b955","artifact_size_bytes":1936,"exact_version":"1.0.0","expected_runtime_request":{"mode":"TRUSTED_IN_PROCESS","protocol":"go-in-process/v1"},"id":"freeagent.builtin.action.text_stats"},"port":{"exact_version":"v1","name":"action.provider"},"profile_id":"pure-chat","schema_version":"module-apply-plan/v1","tenant_id":"default"}
```

Then run the same read-only prediction and production Apply surfaces:

```powershell
go run ./cmd/freeagent module-dry-run `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-text-stats.json `
  --artifact examples/bootstrap-artifacts/freeagent.builtin.action.text_stats/1.0.0 `
  --allow-trusted-in-process-artifact 2331b8b5209f4acffd7fef96f55ce1e0841bc3c4e39d90581efb941ecae9b955

go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-text-stats.json `
  --artifact examples/bootstrap-artifacts/freeagent.builtin.action.text_stats/1.0.0 `
  --allow-trusted-in-process-artifact 2331b8b5209f4acffd7fef96f55ce1e0841bc3c4e39d90581efb941ecae9b955
```

`expected_runtime_request` is an Operator-owned expectation, not a trust grant.
Core still selects the exact `action.provider/v1 + TRUSTED_IN_PROCESS/
go-in-process/v1 + action-binding-config/v1` handler. The digest flag is accepted
only by this compiled handler and does not authorize arbitrary in-process code.
Dry-run and Apply do not execute the Action; a selected future Run still reaches
it only through `ActionProposal → DispatchAttempt → Gateway → private executor`.

## S2 local RAG

`current-v1.rag.bootstrap.seed.json` keeps the default Pure Chat assembly and
adds one deterministic local lexical Knowledge provider. It is independent of
`current-v1.bootstrap.seed.json`; selecting the RAG example does not change the
default seed.

From the repository root, initialize a new Store and run one query:

```powershell
New-Item -ItemType Directory -Force data-rag | Out-Null

go run ./cmd/freeagent init `
  --db data-rag/current.sqlite `
  --artifact-root data-rag/artifacts `
  --seed examples/current-v1.rag.bootstrap.seed.json

$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")

go run ./cmd/freeagent chat `
  --db data-rag/current.sqlite `
  --artifact-root data-rag/artifacts `
  --message "How does FreeAgent shared knowledge relate to Agent and Workspace definitions?" `
  --request-id rag-example-1 `
  --deadline $deadline
```

The Knowledge artifact requests only
`TRUSTED_IN_PROCESS/go-in-process/v1` and provides only
`context.provide/v1`. Retrieved text remains untrusted USER data and is limited
to the explicit Tenant, Workspace, Agent and Task authority scope in the seed.
The repository-root AGPL-3.0 license covers the example; the artifact does not
duplicate a license file.

## S2 optional Agent Memory

`current-v1.memory.bootstrap.seed.json` is a separate, explicit assembly. It
adds one deterministic local Memory provider to `context.provide/v1`, exact
Tenant + Agent + Workspace authority, and an empty revision-1 genesis. The
default `current-v1.bootstrap.seed.json` remains Pure Chat: it installs no
Memory binding, artifact, adapter, or snapshot.

The adapter is loaded lazily by its exact artifact digest plus adapter identity.
After a successful model terminal transition, the next immutable Agent Memory
revision is appended in the same Store transaction as the model outcome. A
retry of the same request verifies the existing revision and cannot append it
again.

```powershell
New-Item -ItemType Directory -Force data-memory | Out-Null

go run ./cmd/freeagent init `
  --db data-memory/current.sqlite `
  --artifact-root data-memory/artifacts `
  --seed examples/current-v1.memory.bootstrap.seed.json

$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")

go run ./cmd/freeagent chat `
  --db data-memory/current.sqlite `
  --artifact-root data-memory/artifacts `
  --profile memory-chat `
  --message "Build a Go backend API." `
  --request-id memory-example-1 `
  --deadline $deadline
```

Memory-derived text is projected only through the same untrusted USER-data
envelope used by other dynamic context. The adapter receives no Store handle;
Core selects and bounds the exact immutable snapshot before invocation.
