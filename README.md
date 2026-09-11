# FreeAgent

<center>
  <strong>A governed, local-first agent runtime built in Go</strong>
</center>

<center>
  FreeAgent keeps identity, authority, budgets, external effects, and recovery
  inside an auditable core, while Role, Knowledge, Memory, Skill, Action,
  Channel, and orchestration capabilities remain explicit, replaceable
  assembly units.
</center>

<center>
  <a href="README.md">English</a>
  ·
  <a href="README.zh-CN.md">简体中文</a>
  ·
  <a href="README.zh-TW.md">繁體中文</a>
</center>

<center>
  <img alt="FreeAgent preview, Go, platform, runtime, and license badges" src="docs/assets/readme-badges.svg">
</center>

## Why FreeAgent?

Most agent frameworks make the model the center of the system. FreeAgent
starts from a different premise: an agent is a governed runtime with durable
state, explicit authority, and recoverable effects.

The core owns the parts that are difficult to audit after the fact:

- workspace, agent, and profile identity;
- module assembly and authority ceilings;
- budgets, usage, and attempt ledgers;
- context compilation and conversation history;
- external effects and their `UNKNOWN` outcomes;
- backup, restore, and exact retry semantics.

Optional capabilities are assembled around that core. A default Pure Chat
profile can run with no optional module selected, while an explicit assembly
can add local RAG, bounded memory, trusted local actions, MCP tools, loopback
channels, model profiles, or narrow Remote/WASM action hosts.

```text
Control Plane + RuntimeCatalog
              |
              v
      Assembly Compiler
              |
              v
MemberExecutionSnapshot + RunManifest
              |
              v
          Universal Loop
     /         |          \
Context Port  Model Port  Action Port
     \         |          /
              v
Current Store / Attempt / Usage / History
              |
              v
     Gateway -> private executor
```

Modules contribute capabilities through exact ports and bindings. They do not
bypass workspace permissions, budgets, the gateway, the effect ledger, or final
persistence.

## Highlights

| Area | What FreeAgent provides |
| --- | --- |
| Governed assembly | Explicit Apply, Dry-run, Disable, CAS, authority ceilings, and exact runtime expectations for enabled modules. |
| Durable execution | Immutable runs and attempts, exact retry, persistent conversations, usage records, and no semantic replay of `UNKNOWN`. |
| Context control | Context compilation with an 85% summarization threshold and minimal, ordered history removal at 100%. |
| Optional capabilities | Local RAG, bounded memory, trusted local actions, MCP stdio tools, loopback channels, model profiles, and narrow Remote/WASM action hosts. |
| Recovery | Complete Current Store backup, verification, and restore across store state, artifacts, cursors, history, and module facts. |
| Local control | Optional loopback Control listener with a read-only Overview and a narrow, confirmed module-disable workflow. |
| Supply chain | Per-target archives, SPDX SBOM, unsigned provenance, and SHA-256 checksum manifests. |

FreeAgent is an early Developer Preview. The accepted development slices are
documented in [CURRENT_CAPABILITIES](docs/CURRENT_CAPABILITIES.md); they are
not a production-support, availability, compatibility, or upgrade SLA.

## Current status

The exact development-slice status is published below for compatibility with
the repository's documentation gate. `NEXT` markers are historical boundaries,
not an implementation claim. Full scope and evidence are maintained in
[CURRENT_CAPABILITIES](docs/CURRENT_CAPABILITIES.md).

```text
W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT
W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE
W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE
W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE
W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE
W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE
W2_U0_SIGNING_SOURCE_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE
W2_U1_SIGNING_SOURCE_POLICY_ACCEPTED_DEVELOPMENT_SLICE
W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE
W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE
W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT
W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE
W6_0_CONTROL_API_CONTRACT_NEXT
W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE
W6_1_APPLICATION_SERVICES_READ_API_NEXT
W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE
W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT
W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE
W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE
W6_2_DURABLE_RECEIPT_SCHEMA_NEXT
W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE
W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT
W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE
W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT
W6_4_MODULES_CONFIGURATION_UI_NEXT
W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT
```

## Download

The current Developer Preview is **v0.1.0-dev.2**. It packages the existing
development slices and does not add a new runtime feature wave.

| Target | Archive |
| --- | --- |
| Windows AMD64 | `freeagent-v0.1.0-dev.2-windows-amd64.zip` |
| Windows ARM64 | `freeagent-v0.1.0-dev.2-windows-arm64.zip` |
| Linux AMD64 | `freeagent-v0.1.0-dev.2-linux-amd64.tar.gz` |
| Linux ARM64 | `freeagent-v0.1.0-dev.2-linux-arm64.tar.gz` |
| Darwin AMD64, build-only | `freeagent-v0.1.0-dev.2-darwin-amd64-build-only.tar.gz` |
| Darwin ARM64, build-only | `freeagent-v0.1.0-dev.2-darwin-arm64-build-only.tar.gz` |

Download the archive and adjacent `SHA256SUMS` from
[GitHub Releases](https://github.com/endview/FreeAgent/releases), verify the
checksum, extract into a new directory, and confirm the version output before
use. Windows and Linux AMD64 packages require native install validation.
ARM64 packages are cross-built, and Darwin packages are not claimed as
native-validated in this preview.

See [INSTALL](docs/INSTALL.md) for the full installation and upgrade guidance.

## Five-minute offline preview

The bundled quickstart uses a deterministic echo model. It needs no API key,
network access, or paid provider.

```powershell
$FreeAgent = (Resolve-Path .\bin\freeagent-windows-amd64.exe).Path
$Runtime = Join-Path $PWD 'data\quickstart'
New-Item -ItemType Directory -Force $Runtime | Out-Null

& $FreeAgent init `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --seed .\config\current-v1.bootstrap.seed.json

& $FreeAgent conversation-create `
  --db (Join-Path $Runtime 'current.sqlite') `
  --conversation preview-conversation

$Deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString('o')
$Turn1 = & $FreeAgent chat `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --conversation preview-conversation `
  --conversation-revision 0 `
  --message 'remember: the package works offline' `
  --request-id preview-turn-1 `
  --deadline $Deadline | ConvertFrom-Json

$Turn2 = & $FreeAgent chat `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --conversation preview-conversation `
  --conversation-revision $Turn1.conversation_revision `
  --conversation-head-run $Turn1.run_id `
  --message 'what did I ask you to remember?' `
  --request-id preview-turn-2 `
  --deadline $Deadline | ConvertFrom-Json
```

The second turn continues only from the exact successful head returned by the
first turn. A stale revision or head fails closed instead of forking the
conversation.

The complete walkthrough, including backup, verification, restore, and the
local-only service, is in [QUICKSTART](docs/QUICKSTART.md).

## Security boundaries

- HTTP chat and optional Control surfaces bind only to literal loopback addresses.
- Control is disabled by default and uses a process-local session with an owner-only bootstrap handoff.
- Runtime databases, artifact roots, backups, and handoff files are operator-owned.
- Stop all writers before backup or restore; do not run two writers against one store.
- The current module system is not a general untrusted plug-in host.
- Archives, SBOMs, provenance, and checksum manifests are unsigned in this Developer Preview.
- Real DeepSeek access is opt-in, requires a separately supplied secret, may incur fees, and is not part of the offline package smoke test.

Read [KNOWN_LIMITATIONS](docs/KNOWN_LIMITATIONS.md) before enabling Control
or running a real model provider. Security reporting guidance is in
[SECURITY](SECURITY.md).

## Build from source

Requirements:

- Go 1.26.5 or later
- PowerShell, bash, or another shell that can set environment variables and send HTTP requests

```powershell
go test -count=1 -timeout=30m ./...
go vet ./...
go run ./cmd/freeagent --help
```

Repository development uses ordinary Go tooling plus local release gates for
documentation, licensing, public-tree contents, install smoke, race testing,
and cross-platform builds.

## Documentation

- [Quickstart](docs/QUICKSTART.md)
- [Install guide](docs/INSTALL.md)
- [Current capabilities](docs/CURRENT_CAPABILITIES.md)
- [Project status](docs/PROJECT_STATUS.md)
- [Core runtime specification](docs/specs/CORE_RUNTIME_V1.md)
- [Current store specification](docs/specs/CURRENT_STORE_V1.md)
- [Control API specification](docs/specs/CONTROL_API_V1.md)
- [Module development](docs/MODULE_DEVELOPMENT_V1.md)
- [Release supply chain](docs/RELEASE_SUPPLY_CHAIN.md)
- [Known limitations](docs/KNOWN_LIMITATIONS.md)
- [Contributing](CONTRIBUTING.md)
- [Security](SECURITY.md)

## License

Licensed as `AGPL-3.0-only`; see [LICENSE](LICENSE).
