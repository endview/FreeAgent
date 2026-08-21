# Current specifications

Only these files are current long-lived coding specifications:

- [`CORE_RUNTIME_V1`](CORE_RUNTIME_V1.md)
- [`CURRENT_STORE_V1`](CURRENT_STORE_V1.md)
- [`CONTROL_API_V1`](CONTROL_API_V1.md) — current narrow Control status:
  `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
  W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`

W6-1 means only a default-off dual-listener path, process-local bootstrap/session,
scope-filtered Modules list/detail, and an effect-free `MODULE_DISABLE` Dry-run with a
process-local `DRY_RUN` receipt. Authenticated GET and non-safe methods both require the
exact session plus its bound CSRF header. It does not include a Control mutation,
SSE, UI, second Store/writer, or background worker. Its historical 32-table identity
is preserved as W6-1 evidence, not the current Store identity.

Its historical marker remains
`W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`.

The W6-2 audit/confirmation historical atom accepted canonical-frozen
`ControlConfirmationStatementV1`, `ModuleDisableEvaluationV1`, and a process-local
32-byte confirmation proof registry (at most two minutes and never beyond the current
session absolute expiry; global 256/session 8), with historical
marker `W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_DURABLE_RECEIPT_SCHEMA_NEXT` and 32-table Store evidence.

The historical W6-2 receipt atom adds exactly one append-only
`control_operation_receipts` table, exact resolver, `NO_CHANGE`-only public commit,
existing-row semantic verification, and Backup Create/Verify/Restore integration.
Its historical marker is
`W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`.
That receipt atom's historical Store identity is 33 tables / fingerprint
`51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10` /
67,998-byte migration / SHA-256
`8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`.
The historical mutation-wiring marker is
`W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE /
W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`. That accepted atom still exposes exactly two routes only when
`--enable-control` is explicit: confirmation and mutate for the TENANT/PROFILE,
`context.provide/v1`, existing-`OPTIONAL`, DECLARATIVE/static, trusted-instruction,
deny-all `MODULE_DISABLE` slice. Durable lookup precedes proof/current-basis checks;
`NO_CHANGE` and `APPLIED` receipts are durable, and APPLIED publication commits in the
same Store transaction. SQLite `UNKNOWN` is unreachable. Verification covers existing
APPLIED rows and tamper but, without an external completeness anchor, does not claim
detection of arbitrary whole-row receipt deletion. Forced receipt-insert failure proves
publication rollback. Control remains default-off, with no other mutation, SSE, UI,
worker, or second Store/writer. Its historical next marker was
`W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`.

The accepted W6-3 atom keeps Control default-off, embeds the exact read-only Web
Shell, and exposes only the scope-filtered `GET /control/api/v1/overview` in
addition to the existing routes. The bounded projection covers Workspaces, Runs,
UNKNOWN, Learning, Module Candidates, and Usage, with current-basis, section,
projection, and strong-ETag verification. That historical Store identity is 41 tables /
23 indexes / 56 triggers, fingerprint
`87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`,
and a 143,588-byte migration with SHA-256
`5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`.
The accepted historical W6-4 marker is
`W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`. That atom adds a strict Modules list/detail UI and the only existing
`MODULE_DISABLE` dry-run → confirmation → mutate consumer. It adds no operation,
route, service, Store seam, Schema, writer, or publication owner; raw proof and
idempotency material remain memory-only, and durable success is followed only by
verified refetch. Its historical next marker is
`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`.

The accepted W6-5 atom adds only the default-off trusted-local Operator CLI
`module-artifact-ingress`. Source and artifact roots are transient trusted CLI inputs;
the caller selects an exact Store-owned current Snapshot entry and supplies no package
path, URL, signature, upload, HTTP request, or UI input. Only unsigned
`LOCAL_DIRECTORY + DENY` entries are eligible. The filesystem durably publishes the
fully verified object first, by digest and without replacement; the sole Current Store
then commits an immutable Artifact and append-only Admission in one transaction. The
object remains inert: no install, activation, binding, grant, review, apply, or execution.
An ingress whose same-digest target is absent always publishes directories as `0700` and
files as `0600`. Reuse of an existing same-digest target requires full bytes/mode
verification and exactly one root-global closure: either every directory is `0700` and
every file is `0600`, or every directory is `0700`, only the unique executable exactly
bound by the canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor is `0700`, and
all other files are `0600`. Both closures reject special/setid/sticky and every group/world
permission. When an artifact root is shared across Stores, this mode decision is independent
of any one Store's Installation. The second closure is existing physical compatibility only;
it grants the current Store no installation, activation, or execution authority. Here
`inert` means no Store authority and no execution by this slice, not the absence of an
OS executable bit.
Backup closes over the deduplicated union of installed and ingressed artifacts and can
restore an uninstalled object offline after its Source is gone. The current Store is
43 tables / 25 explicit indexes / 64 triggers, fingerprint
`47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`, with a
150,301-byte migration whose SHA-256 is
`6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`.
The current next marker is `W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`.

One-time stop, rebuild, recovery, and release gates are maintained in
[`CUTOVER_ACCEPTANCE`](../CUTOVER_ACCEPTANCE.md); it is an acceptance checklist,
not a fourth coding specification.

The concise current development-status index is
[`CURRENT_CAPABILITIES`](../CURRENT_CAPABILITIES.md). It points to these
authorities but is not an additional coding specification.

`CAPABILITY_PRESERVATION.md` remains in this directory only as an explicitly
historical S0 semantic source. It is non-normative and cannot authorize a
Runtime, Store, module, or production capability.
