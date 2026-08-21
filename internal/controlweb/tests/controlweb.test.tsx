import assert from "node:assert/strict";
import { renderToStaticMarkup } from "react-dom/server";

import {
  CONTROL_SCOPE_ID_ENCODING,
  CONTROL_SCOPE_ID_ENCODING_HEADER,
  authorizedScopeSetDigest,
  canonicalJSONString,
  decodeHandoff,
  decodeOverview,
  decodeSessionExchange,
  domainDigest,
  validateOverviewDigests,
  type ControlScope,
  type OverviewResponse
} from "../src/contracts.ts";
import {
  buildScopeChoices,
  captureDetailSnapshot,
  clearOverviewTransportCache,
  ControlOverviewError,
  detailHash,
  fetchOverview,
  overviewHeaders,
  overviewQueryKey,
  overviewSearchResults,
  parseDetailHash,
  type OverviewContext
} from "../src/overview.ts";
import {
  canonicalBootstrapBody,
  canonicalResumeBody,
  decodeStoredResume,
  exchangeHandoff,
  readStoredResume,
  resumeSession,
  storeResume
} from "../src/session.ts";
import {
  FatalOverviewPanel,
  HandoffPanel,
  LoadingPanel,
  OverviewPage,
  PermissionPanel,
  SessionInvalidBoundary
} from "../src/ui.tsx";

const ORIGIN = "http://127.0.0.1:7331";
const OTHER_ORIGIN = "http://127.0.0.1:7332";
const FIXTURE_A = "A".repeat(43);
const FIXTURE_B = `${"B".repeat(42)}A`;
const DIGEST_A = "a".repeat(64);
const DIGEST_B = "b".repeat(64);
const DIGEST_C = "c".repeat(64);

const storage = new Map<string, string>();
Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: {
    location: { origin: ORIGIN, hash: "" },
    addEventListener: () => undefined,
    removeEventListener: () => undefined
  }
});
Object.defineProperty(globalThis, "sessionStorage", {
  configurable: true,
  value: {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => { storage.set(key, value); },
    removeItem: (key: string) => { storage.delete(key); }
  }
});

const tenantScope: ControlScope = {
  schema_version: "control-scope/v1",
  kind: "TENANT",
  tenant_id: "tenant-a"
};

const makeSessionResponse = async (scopes: ControlScope[] = [tenantScope]) => {
  const scopeSetDigest = await authorizedScopeSetDigest(scopes);
  return {
    schema_version: "control-bootstrap-session/v2",
    session: {
      schema_version: "control-session/v1",
      boot_id: "boot-a",
      session_id: "session-a",
      principal_id: "principal-a",
      capabilities: ["OBSERVE"],
      scope_set_digest: scopeSetDigest,
      authorization_revision: 3,
      issued_at_unix_micros: 1_800_000_000_000_000,
      expires_at_unix_micros: 1_800_000_100_000_000
    },
    authorized_scopes: scopes,
    csrf_token: FIXTURE_A,
    resume_credential: FIXTURE_B
  };
};

const makeOverview = async (): Promise<OverviewResponse> => {
  const scopeDigest = await domainDigest(
    "freeagent.control-scope/v1",
    canonicalJSONString(tenantScope)
  );
  const basis = {
    tenant_id: "tenant-a",
    pointer_revision: 7,
    control: { id: "control-a", revision: 4, digest: DIGEST_A },
    catalog: { id: "catalog-a", revision: 5, digest: DIGEST_B }
  };
  const basisDigest = await domainDigest(
    "freeagent.control-published-pointer-ref/v1",
    canonicalJSONString(basis)
  );
  const workspaces = [{ id: "workspace-a", version: "1.0.0", digest: DIGEST_C }];
  const runs = [{
    tenant_id: "tenant-a",
    workspace_id: "workspace-a",
    run_id: "run-a",
    state: "TERMINATED",
    disposition: "TERMINATED",
    revision: 1,
    created_at_unix_micros: 1_800_000_000_000_000,
    updated_at_unix_micros: 1_800_000_001_000_000
  }];
  const collections = new Map<string, { items: unknown[]; truncated: false }>([
    ["LEARNING", { items: [], truncated: false }],
    ["MODULES", { items: [], truncated: false }],
    ["RUNS", { items: runs, truncated: false }],
    ["UNKNOWN", { items: [], truncated: false }],
    ["USAGE", { items: [], truncated: false }],
    ["WORKSPACES", { items: workspaces, truncated: false }]
  ]);
  const sections = [];
  for (const [kind, source] of collections) {
    sections.push({
      kind,
      source_revision: 7,
      source_digest: await domainDigest(
        "freeagent.control-overview-section-source/v1",
        canonicalJSONString({
          schema_version: "control-overview-section-source/v1",
          scope_digest: scopeDigest,
          kind,
          items: source.items,
          truncated: source.truncated
        })
      ),
      item_count: source.items.length,
      truncated: source.truncated
    });
  }
  const view = {
    schema_version: "control-view-snapshot/v1",
    scope: tenantScope,
    scope_digest: scopeDigest,
    observed_at_unix_micros: 1_800_000_002_000_000,
    basis,
    sections
  };
  const viewDigest = await domainDigest(
    "freeagent.control-view-snapshot/v1",
    canonicalJSONString(view)
  );
  const overview = {
    schema_version: "control-http-overview/v1",
    published_pointer: {
      kind: "PUBLISHED_POINTER",
      resource_id: "tenant-a",
      revision: 7,
      digest: basisDigest
    },
    basis,
    view,
    view_snapshot_digest: viewDigest,
    workspaces,
    workspaces_truncated: false,
    runs,
    runs_truncated: false,
    unknown: [],
    unknown_truncated: false,
    learning: [],
    learning_truncated: false,
    module_candidates: [],
    module_candidates_truncated: false,
    usage: [],
    usage_truncated: false,
    projection_digest: ""
  } as unknown as OverviewResponse;
  overview.projection_digest = await domainDigest(
    "freeagent.control-overview/v1",
    canonicalJSONString({
      ...overview,
      schema_version: "control-overview/v1",
      projection_digest: "",
      strong_etag: ""
    })
  );
  return overview;
};

const resignOverview = async (overview: OverviewResponse) => {
  const sources = new Map<string, { items: unknown[]; truncated: boolean }>([
    ["LEARNING", { items: overview.learning, truncated: overview.learning_truncated }],
    ["MODULES", { items: overview.module_candidates, truncated: overview.module_candidates_truncated }],
    ["RUNS", { items: overview.runs, truncated: overview.runs_truncated }],
    ["UNKNOWN", { items: overview.unknown, truncated: overview.unknown_truncated }],
    ["USAGE", { items: overview.usage, truncated: overview.usage_truncated }],
    ["WORKSPACES", { items: overview.workspaces, truncated: overview.workspaces_truncated }]
  ]);
  for (const section of overview.view.sections) {
    const source = sources.get(section.kind);
    assert.ok(source);
    section.item_count = source.items.length;
    section.truncated = source.truncated;
    section.source_digest = await domainDigest(
      "freeagent.control-overview-section-source/v1",
      canonicalJSONString({
        schema_version: "control-overview-section-source/v1",
        scope_digest: overview.view.scope_digest,
        kind: section.kind,
        items: source.items,
        truncated: source.truncated
      })
    );
  }
  overview.view_snapshot_digest = await domainDigest(
    "freeagent.control-view-snapshot/v1",
    canonicalJSONString(overview.view)
  );
  overview.projection_digest = await domainDigest(
    "freeagent.control-overview/v1",
    canonicalJSONString({
      ...overview,
      schema_version: "control-overview/v1",
      projection_digest: "",
      strong_etag: ""
    })
  );
};

assert.equal(
  canonicalBootstrapBody(FIXTURE_A),
  `{"capability":"${FIXTURE_A}","schema_version":"control-bootstrap-exchange/v1"}`
);
assert.equal(
  canonicalResumeBody(FIXTURE_B),
  `{"resume_credential":"${FIXTURE_B}","schema_version":"control-session-resume/v1"}`
);

const handoff = decodeHandoff(JSON.stringify({
  schema_version: "freeagent.control-bootstrap-handoff/v1",
  origin: ORIGIN,
  capability: FIXTURE_A,
  expires_at_unix_micros: Date.now() * 1000 + 1_000_000
}));
assert.equal(handoff.origin, ORIGIN);
assert.throws(() => decodeHandoff(JSON.stringify({ ...handoff, origin: OTHER_ORIGIN }), Infinity));

const unicodeScopes: ControlScope[] = [
  { schema_version: "control-scope/v1", kind: "TENANT", tenant_id: "\uE000" },
  { schema_version: "control-scope/v1", kind: "TENANT", tenant_id: "\u{10000}" }
];
const unicodeResponse = await makeSessionResponse(unicodeScopes);
const decodedUnicode = await decodeSessionExchange(
  JSON.stringify(unicodeResponse),
  "control-bootstrap-session/v2"
);
assert.deepEqual(decodedUnicode.authorized_scopes, unicodeScopes);
await assert.rejects(
  decodeSessionExchange(
    JSON.stringify({ ...unicodeResponse, authorized_scopes: [...unicodeScopes].reverse() }),
    "control-bootstrap-session/v2"
  ),
  /canonical server order/u
);

const sessionResponse = await makeSessionResponse();
await assert.rejects(
  decodeSessionExchange(
    JSON.stringify(sessionResponse).replace(
      '"schema_version":"control-bootstrap-session/v2"',
      '"schema_version":"control-bootstrap-session/v2","schema_version":"control-bootstrap-session/v2"'
    ),
    "control-bootstrap-session/v2"
  ),
  /duplicate object key/u
);
let bootstrapCalls = 0;
const bootstrapExchange = await exchangeHandoff(handoff, async (url, init) => {
  bootstrapCalls += 1;
  assert.equal(url, `${ORIGIN}/control/bootstrap`);
  assert.equal(init?.credentials, "same-origin");
  assert.equal(init?.redirect, "error");
  assert.equal(init?.body, canonicalBootstrapBody(FIXTURE_A));
  return new Response(JSON.stringify(sessionResponse), {
    status: 200,
    headers: {
      "Content-Type": "application/json",
      "Set-Cookie": "freeagent_control_session=opaque; Path=/control/; HttpOnly; SameSite=Strict"
    }
  });
});
assert.equal(bootstrapCalls, 1);
assert.equal(bootstrapExchange.session.session_id, "session-a");

let wrongOriginCalls = 0;
await assert.rejects(
  exchangeHandoff({ ...handoff, origin: OTHER_ORIGIN }, async () => {
    wrongOriginCalls += 1;
    throw new Error("must not run");
  }),
  /does not match/u
);
assert.equal(wrongOriginCalls, 0);
await assert.rejects(
  resumeSession(OTHER_ORIGIN, FIXTURE_B, async () => {
    wrongOriginCalls += 1;
    throw new Error("must not run");
  }),
  /stored session metadata is invalid/u
);
assert.equal(wrongOriginCalls, 0);

storage.clear();
assert.equal(storeResume(ORIGIN, FIXTURE_B), true);
const stored = readStoredResume();
assert.deepEqual(stored, {
  schema_version: "freeagent.control-resume-storage/v1",
  origin: ORIGIN,
  resume_credential: FIXTURE_B
});
assert.equal(decodeStoredResume(`{"origin":"${ORIGIN}","resume_credential":"${FIXTURE_B}"}`), null);
assert.throws(() => storeResume(OTHER_ORIGIN, FIXTURE_B), /metadata is invalid/u);

const resumedWire = {
  ...sessionResponse,
  schema_version: "control-session-resumed/v1"
};
let resumeCalls = 0;
const resumed = await resumeSession(ORIGIN, FIXTURE_B, async (url, init) => {
  resumeCalls += 1;
  assert.equal(url, `${ORIGIN}/control/session/resume`);
  assert.equal(init?.credentials, "include");
  assert.equal(init?.body, canonicalResumeBody(FIXTURE_B));
  return new Response(JSON.stringify(resumedWire), {
    status: 200,
    headers: { "Content-Type": "application/json" }
  });
});
assert.equal(resumeCalls, 1);
assert.equal(resumed.schema_version, "control-session-resumed/v1");

const fixture = await makeOverview();
const decodedOverview = decodeOverview(JSON.stringify(fixture), tenantScope);
await validateOverviewDigests(decodedOverview);
const revisionZeroFixture = structuredClone(fixture);
revisionZeroFixture.learning = [{
  proposal_id: DIGEST_A,
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  kind: "KNOWLEDGE",
  state: "SUBMITTED",
  revision: 0,
  created_at_unix_micros: 1_800_000_000_000_000,
  updated_at_unix_micros: 1_800_000_000_000_000
}];
revisionZeroFixture.usage = [{
  attempt_id: "attempt-zero",
  run_id: "run-a",
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  revision: 0,
  input_tokens: null,
  cached_input_tokens: null,
  uncached_input_tokens: null,
  output_tokens: null,
  reasoning_tokens: null,
  reconciliation_status: "PENDING",
  updated_at_unix_micros: 1_800_000_000_000_000
}];
assert.doesNotThrow(() => decodeOverview(JSON.stringify(revisionZeroFixture), tenantScope));

const validIntersectionFixture = structuredClone(fixture);
validIntersectionFixture.runs[0].state = "WAITING_RECONCILIATION";
validIntersectionFixture.runs[0].disposition = "WAITING_RECONCILIATION";
validIntersectionFixture.unknown = [{
  kind: "MODEL",
  resource_id: "attempt-a",
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  run_id: "run-a",
  revision: 1,
  updated_at_unix_micros: 1_800_000_001_000_000
}];
validIntersectionFixture.usage = [{
  attempt_id: "attempt-a",
  run_id: "run-a",
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  revision: 1,
  input_tokens: null,
  cached_input_tokens: null,
  uncached_input_tokens: null,
  output_tokens: null,
  reasoning_tokens: null,
  reconciliation_status: "PENDING_RECONCILIATION",
  updated_at_unix_micros: 1_800_000_001_000_000
}];
await resignOverview(validIntersectionFixture);
assert.doesNotThrow(() => decodeOverview(JSON.stringify(validIntersectionFixture), tenantScope));
await validateOverviewDigests(validIntersectionFixture);

const assertSelfConsistentOverviewRejected = async (
  mutate: (overview: OverviewResponse) => void,
  pattern: RegExp
) => {
  const changed = structuredClone(validIntersectionFixture);
  mutate(changed);
  await resignOverview(changed);
  await validateOverviewDigests(changed);
  assert.throws(() => decodeOverview(JSON.stringify(changed), tenantScope), pattern);
};

await assertSelfConsistentOverviewRejected((value) => {
  value.runs[0].revision = 0;
}, /run semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.unknown[0].revision = 2;
}, /unknown semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.runs[0].state = "TERMINATED";
  value.runs[0].disposition = "TERMINATED";
}, /unknown semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.runs[0].workspace_id = "workspace-b";
}, /unknown semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.usage[0].reconciliation_status = "PROVIDER_REPORTED";
}, /usage semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.usage[0].revision = 2;
}, /usage semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.runs.push({ ...value.runs[0], updated_at_unix_micros: 1_800_000_000_999_999 });
}, /run semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.unknown.push({
    ...value.unknown[0],
    updated_at_unix_micros: value.unknown[0].updated_at_unix_micros - 1
  });
}, /unknown semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.usage.push({
    ...value.usage[0],
    updated_at_unix_micros: value.usage[0].updated_at_unix_micros - 1
  });
}, /usage semantics/u);
await assertSelfConsistentOverviewRejected((value) => {
  value.runs_truncated = true;
}, /truncation proof/u);

for (const [kind, revision] of [
  ["MODEL", 1],
  ["ACTION", 1],
  ["ACTION", 2],
  ["CHANNEL_SEND", 1],
  ["CHANNEL_SEND", 9],
  ["LEARNING_PROPOSAL", 2],
  ["LEARNING_TASK", 2]
] as const) {
  const legalUnknown = structuredClone(fixture);
  legalUnknown.unknown = [{
    kind,
    resource_id: kind.startsWith("LEARNING_") ? DIGEST_A : `resource-${kind.toLowerCase()}`,
    tenant_id: "tenant-a",
    workspace_id: "workspace-a",
    run_id: "run-not-in-runs-section",
    revision,
    updated_at_unix_micros: 1_800_000_001_000_000
  }];
  await resignOverview(legalUnknown);
  assert.doesNotThrow(() => decodeOverview(JSON.stringify(legalUnknown), tenantScope));
}
for (const [kind, revision] of [
  ["MODEL", 2],
  ["ACTION", 3],
  ["CHANNEL_SEND", 0],
  ["LEARNING_PROPOSAL", 1],
  ["LEARNING_TASK", 3]
] as const) {
  const impossibleUnknown = structuredClone(fixture);
  impossibleUnknown.unknown = [{
    kind,
    resource_id: kind.startsWith("LEARNING_") ? DIGEST_A : `resource-${kind.toLowerCase()}`,
    tenant_id: "tenant-a",
    workspace_id: "workspace-a",
    run_id: "run-not-in-runs-section",
    revision,
    updated_at_unix_micros: 1_800_000_001_000_000
  }];
  await resignOverview(impossibleUnknown);
  await validateOverviewDigests(impossibleUnknown);
  assert.throws(
    () => decodeOverview(JSON.stringify(impossibleUnknown), tenantScope),
    /unknown semantics/u
  );
}

const proposalIntersectionFixture = structuredClone(fixture);
proposalIntersectionFixture.unknown = [{
  kind: "LEARNING_PROPOSAL",
  resource_id: DIGEST_A,
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  run_id: "review-run-not-in-runs-section",
  revision: 2,
  updated_at_unix_micros: 1_800_000_001_000_000
}];
proposalIntersectionFixture.learning = [{
  proposal_id: DIGEST_A,
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  kind: "KNOWLEDGE",
  state: "REVIEW_UNKNOWN",
  revision: 2,
  created_at_unix_micros: 1_800_000_000_000_000,
  updated_at_unix_micros: 1_800_000_001_000_000
}];
await resignOverview(proposalIntersectionFixture);
assert.doesNotThrow(() => decodeOverview(JSON.stringify(proposalIntersectionFixture), tenantScope));
await validateOverviewDigests(proposalIntersectionFixture);
const duplicateLearning = structuredClone(proposalIntersectionFixture);
duplicateLearning.unknown = [];
duplicateLearning.learning.push({
  ...duplicateLearning.learning[0],
  updated_at_unix_micros: duplicateLearning.learning[0].updated_at_unix_micros - 1
});
await resignOverview(duplicateLearning);
await validateOverviewDigests(duplicateLearning);
assert.throws(
  () => decodeOverview(JSON.stringify(duplicateLearning), tenantScope),
  /learning semantics/u
);
const proposalMismatch = structuredClone(proposalIntersectionFixture);
proposalMismatch.learning[0].state = "APPROVED";
await resignOverview(proposalMismatch);
await validateOverviewDigests(proposalMismatch);
assert.throws(
  () => decodeOverview(JSON.stringify(proposalMismatch), tenantScope),
  /learning semantics/u
);

const impossibleLearningRevision = structuredClone(fixture);
impossibleLearningRevision.learning = [{
  proposal_id: DIGEST_A,
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  kind: "KNOWLEDGE",
  state: "SUBMITTED",
  revision: 1,
  created_at_unix_micros: 1_800_000_000_000_000,
  updated_at_unix_micros: 1_800_000_000_000_000
}];
await resignOverview(impossibleLearningRevision);
await validateOverviewDigests(impossibleLearningRevision);
assert.throws(
  () => decodeOverview(JSON.stringify(impossibleLearningRevision), tenantScope),
  /learning semantics/u
);

for (const [state, revision] of [
  ["REVIEW_PENDING", 1],
  ["APPROVED", 2],
  ["APPROVED", 3],
  ["REJECTED", 2],
  ["REVIEW_FAILED", 3]
] as const) {
  const legalLearning = structuredClone(fixture);
  legalLearning.learning = [{
    proposal_id: DIGEST_A,
    tenant_id: "tenant-a",
    workspace_id: "workspace-a",
    kind: "SKILL",
    state,
    revision,
    created_at_unix_micros: 1_800_000_000_000_000,
    updated_at_unix_micros: 1_800_000_001_000_000
  }];
  await resignOverview(legalLearning);
  assert.doesNotThrow(() => decodeOverview(JSON.stringify(legalLearning), tenantScope));
}

const usageItem = (
  reconciliationStatus: string,
  revision: number,
  outputTokens: number | null = null
) => ({
  attempt_id: "usage-matrix-attempt",
  run_id: "usage-matrix-run",
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  revision,
  input_tokens: null,
  cached_input_tokens: null,
  uncached_input_tokens: null,
  output_tokens: outputTokens,
  reasoning_tokens: null,
  reconciliation_status: reconciliationStatus,
  updated_at_unix_micros: 1_800_000_001_000_000
});
for (const [status, revision, outputTokens] of [
  ["PENDING", 0, null],
  ["PENDING_RECONCILIATION", 1, 1],
  ["PROVIDER_REPORTED", 1, 1],
  ["PROVIDER_REPORTED", 2, 1],
  ["NO_USAGE_REPORTED", 0, null],
  ["NO_USAGE_REPORTED", 1, null],
  ["NO_USAGE_REPORTED", 2, null]
] as const) {
  const legalUsage = structuredClone(fixture);
  legalUsage.usage = [usageItem(status, revision, outputTokens)];
  await resignOverview(legalUsage);
  assert.doesNotThrow(() => decodeOverview(JSON.stringify(legalUsage), tenantScope));
}
for (const [status, revision, outputTokens] of [
  ["PENDING", 1, null],
  ["PENDING", 0, 1],
  ["PENDING_RECONCILIATION", 2, null],
  ["PROVIDER_REPORTED", 3, null],
  ["NO_USAGE_REPORTED", 3, null],
  ["NO_USAGE_REPORTED", 1, 1]
] as const) {
  const impossibleUsage = structuredClone(fixture);
  impossibleUsage.usage = [usageItem(status, revision, outputTokens)];
  await resignOverview(impossibleUsage);
  await validateOverviewDigests(impossibleUsage);
  assert.throws(
    () => decodeOverview(JSON.stringify(impossibleUsage), tenantScope),
    /usage semantics/u
  );
}

const moduleFixture = structuredClone(fixture);
moduleFixture.module_candidates = [{
  review_id: DIGEST_A,
  candidate_id: DIGEST_B,
  tenant_id: "tenant-a",
  binding_target_kind: "PROFILE",
  current_instance_id: "instance-a",
  target_instance_id: "instance-b",
  current_module_id: "vendor.module",
  current_exact_version: "build-a",
  current_artifact_digest: DIGEST_A,
  target_module_id: "vendor.module",
  target_exact_version: "build-b",
  target_artifact_digest: DIGEST_B,
  conclusion: "WOULD_APPLY",
  created_at_unix_micros: 1_800_000_001_000_000
}];
await resignOverview(moduleFixture);
assert.doesNotThrow(() => decodeOverview(JSON.stringify(moduleFixture), tenantScope));
for (const mutate of [
  (value: OverviewResponse) => { value.module_candidates[0].target_instance_id = "instance-a"; },
  (value: OverviewResponse) => { value.module_candidates[0].target_module_id = "other.module"; },
  (value: OverviewResponse) => { value.module_candidates[0].target_exact_version = "build-a"; },
  (value: OverviewResponse) => { value.module_candidates[0].target_artifact_digest = DIGEST_A; },
  (value: OverviewResponse) => {
    value.module_candidates.push({
      ...value.module_candidates[0],
      created_at_unix_micros: value.module_candidates[0].created_at_unix_micros - 1
    });
  }
]) {
  const impossibleModule = structuredClone(moduleFixture);
  mutate(impossibleModule);
  await resignOverview(impossibleModule);
  await validateOverviewDigests(impossibleModule);
  assert.throws(
    () => decodeOverview(JSON.stringify(impossibleModule), tenantScope),
    /candidate semantics/u
  );
}

const basisTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
basisTamper.basis.catalog.digest = DIGEST_C;
assert.throws(
  () => decodeOverview(JSON.stringify(basisTamper), tenantScope),
  /scope or basis/u
);
const pointerTamper = structuredClone(fixture);
pointerTamper.published_pointer.digest = DIGEST_C;
await assert.rejects(validateOverviewDigests(pointerTamper), /published pointer/u);
const sectionTamper = structuredClone(fixture);
sectionTamper.runs[0].state = "TAMPERED";
await assert.rejects(validateOverviewDigests(sectionTamper), /RUNS section digest/u);
const projectionTamper = structuredClone(fixture);
projectionTamper.projection_digest = DIGEST_C;
await assert.rejects(validateOverviewDigests(projectionTamper), /projection digest/u);

const runTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
runTamper.runs[0].state = "NOT_A_RUN_STATE";
assert.throws(() => decodeOverview(JSON.stringify(runTamper), tenantScope), /run semantics/u);
const ownershipTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
ownershipTamper.runs[0].tenant_id = "tenant-b";
assert.throws(() => decodeOverview(JSON.stringify(ownershipTamper), tenantScope), /ownership/u);
const runOrderTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
runOrderTamper.runs.push({ ...runOrderTamper.runs[0], run_id: "run-z" });
assert.throws(() => decodeOverview(JSON.stringify(runOrderTamper), tenantScope), /order/u);
const unknownTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
unknownTamper.unknown.push({
  kind: "NOT_AN_UNKNOWN_KIND",
  resource_id: "unknown-a",
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  run_id: "run-a",
  revision: 1,
  updated_at_unix_micros: 1_800_000_001_000_000
});
assert.throws(() => decodeOverview(JSON.stringify(unknownTamper), tenantScope), /unknown semantics/u);
const learningTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
learningTamper.learning.push({
  proposal_id: DIGEST_A,
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  kind: "NOT_A_LEARNING_KIND",
  state: "SUBMITTED",
  revision: 1,
  created_at_unix_micros: 1_800_000_000_000_000,
  updated_at_unix_micros: 1_800_000_001_000_000
});
assert.throws(() => decodeOverview(JSON.stringify(learningTamper), tenantScope), /learning semantics/u);
const candidateTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
candidateTamper.module_candidates.push({
  review_id: DIGEST_A,
  candidate_id: DIGEST_B,
  tenant_id: "tenant-a",
  binding_target_kind: "NOT_A_TARGET",
  current_instance_id: "instance-a",
  target_instance_id: "instance-b",
  current_module_id: "module-a",
  current_exact_version: "1.0.0",
  current_artifact_digest: DIGEST_A,
  target_module_id: "module-a",
  target_exact_version: "1.1.0",
  target_artifact_digest: DIGEST_B,
  conclusion: "WOULD_APPLY",
  created_at_unix_micros: 1_800_000_001_000_000
});
assert.throws(() => decodeOverview(JSON.stringify(candidateTamper), tenantScope), /candidate semantics/u);
const usageTamper = JSON.parse(JSON.stringify(fixture)) as OverviewResponse;
usageTamper.usage.push({
  attempt_id: "attempt-a",
  run_id: "run-a",
  tenant_id: "tenant-a",
  workspace_id: "workspace-a",
  revision: 1,
  input_tokens: 9,
  cached_input_tokens: 2,
  uncached_input_tokens: 3,
  output_tokens: 4,
  reasoning_tokens: 1,
  reconciliation_status: "PROVIDER_REPORTED",
  updated_at_unix_micros: 1_800_000_001_000_000
});
assert.throws(() => decodeOverview(JSON.stringify(usageTamper), tenantScope), /tokens/u);

const context: OverviewContext = {
  origin: ORIGIN,
  bootID: "boot-a",
  sessionID: "session-a",
  principalID: "principal-a",
  authorizationRevision: 3,
  scopeSetDigest: sessionResponse.session.scope_set_digest,
  csrfToken: FIXTURE_A,
  scope: tenantScope
};
assert.deepEqual(overviewQueryKey(context), [
  "overview", ORIGIN, "boot-a", "session-a", "principal-a", 3,
  sessionResponse.session.scope_set_digest, "TENANT", "tenant-a", ""
]);
assert.deepEqual(overviewHeaders(FIXTURE_A, tenantScope), {
  "X-FreeAgent-CSRF": FIXTURE_A,
  "X-FreeAgent-Scope-Kind": "TENANT",
  [CONTROL_SCOPE_ID_ENCODING_HEADER]: CONTROL_SCOPE_ID_ENCODING,
  "X-FreeAgent-Tenant-ID": "dGVuYW50LWE"
});
assert.doesNotThrow(() => new Headers(overviewHeaders(FIXTURE_A, {
  schema_version: "control-scope/v1",
  kind: "WORKSPACE",
  tenant_id: "租户甲",
  workspace_id: "工作区一"
})));

clearOverviewTransportCache();
let overviewCalls = 0;
const fetcher = async (_url: RequestInfo | URL, init?: RequestInit) => {
  overviewCalls += 1;
  assert.equal(init?.method, "GET");
  assert.equal(init?.credentials, "include");
  assert.equal(init?.body, undefined);
  assert.equal(new Headers(init?.headers).has("Content-Type"), false);
  if (overviewCalls === 1) {
    assert.equal(new Headers(init?.headers).has("If-None-Match"), false);
    return new Response(JSON.stringify(fixture), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        ETag: `"${DIGEST_A}"`
      }
    });
  }
  assert.equal(new Headers(init?.headers).get("If-None-Match"), `"${DIGEST_A}"`);
  return new Response(null, { status: 304, headers: { ETag: `"${DIGEST_A}"` } });
};
const firstOverview = await fetchOverview(context, undefined, fetcher);
const cachedOverview = await fetchOverview(context, undefined, fetcher);
assert.equal(firstOverview, cachedOverview);
assert.equal(overviewCalls, 2);

clearOverviewTransportCache();
let malformed304Calls = 0;
await fetchOverview(context, undefined, async () => {
  malformed304Calls += 1;
  return new Response(JSON.stringify(fixture), {
    status: 200,
    headers: { "Content-Type": "application/json", ETag: `"${DIGEST_A}"` }
  });
});
await assert.rejects(
  fetchOverview(context, undefined, async () => {
    malformed304Calls += 1;
    return new Response(null, { status: 304, headers: { ETag: `"${DIGEST_B}"` } });
  }),
  /malformed 304/u
);
assert.equal(malformed304Calls, 2);

let wrongOverviewCalls = 0;
await assert.rejects(
  fetchOverview({ ...context, origin: OTHER_ORIGIN }, undefined, async () => {
    wrongOverviewCalls += 1;
    throw new Error("must not run");
  }),
  /origin does not match/u
);
assert.equal(wrongOverviewCalls, 0);

const choices = buildScopeChoices(
  [tenantScope],
  new Map([["tenant-a", Array.from({ length: 257 }, (_, index) => ({
    id: `workspace-${String(index).padStart(3, "0")}`,
    version: "1.0.0",
    digest: DIGEST_A
  }))]])
);
assert.equal(choices.filter((choice) => choice.source === "tenant-workspace").length, 256);
assert.equal(choices.some((choice) => choice.scope.workspace_id === "workspace-255"), true);
assert.equal(choices.some((choice) => choice.scope.workspace_id === "workspace-256"), false);

const search = overviewSearchResults(fixture, "terminated");
assert.equal(search.length, 1);
assert.equal(search[0].id, "run-a");
const link = { section: "runs" as const, id: "run-a" };
assert.deepEqual(parseDetailHash(detailHash(link)), link);
const frozenDetail = captureDetailSnapshot(fixture, link);
const switchedFixture = structuredClone(fixture);
switchedFixture.runs = [];
assert.equal(frozenDetail.result?.id, "run-a");
assert.equal(frozenDetail.scope.kind, "TENANT");
assert.equal(captureDetailSnapshot(switchedFixture, link).result, null);
assert.equal(parseDetailHash("#detail=invalid"), null);
assert.equal(parseDetailHash(`#detail=runs:${"a".repeat(2048)}`), null);

const handoffHTML = renderToStaticMarkup(
  <HandoffPanel busy={false} error="" onFile={() => undefined} />
);
assert.match(handoffHTML, /Choose handoff JSON/u);
assert.match(renderToStaticMarkup(<LoadingPanel />), /Loading Overview/u);
assert.match(renderToStaticMarkup(<PermissionPanel principalID="principal-a" />), /permission denied/u);
const expiredBoundaryHTML = renderToStaticMarkup(
  <SessionInvalidBoundary
    error={new ControlOverviewError("expired", 401, "SESSION_EXPIRED")}
  >
    <span>cached authorized secret</span>
  </SessionInvalidBoundary>
);
assert.doesNotMatch(expiredBoundaryHTML, /cached authorized secret/u);
assert.match(expiredBoundaryHTML, /Closing an expired session/u);
assert.match(
  renderToStaticMarkup(
    <FatalOverviewPanel
      permissionDenied={false}
      message="unavailable"
      correlationID="correlation-a"
      onRetry={() => undefined}
    />
  ),
  /Retry read/u
);
const scopeChoices = buildScopeChoices([tenantScope], new Map([["tenant-a", fixture.workspaces]]));
const overviewHTML = renderToStaticMarkup(
  <OverviewPage
    session={sessionResponse.session as never}
    overview={fixture}
    scopeChoices={scopeChoices}
    selectedScopeKey={scopeChoices[0].key}
    search=""
    stale={true}
    refreshing={false}
    backgroundError="refresh failed"
    detailSnapshot={frozenDetail}
    onScopeChange={() => undefined}
    onSearchChange={() => undefined}
    onRefresh={() => undefined}
  />
);
assert.match(overviewHTML, /Authorized scope/u);
assert.match(overviewHTML, /workspace-a/u);
assert.match(overviewHTML, /Current response detail/u);
assert.match(overviewHTML, /Control generation/u);
assert.match(overviewHTML, /Catalog generation/u);
assert.match(overviewHTML, /Frozen tenant scope: tenant-a/u);
assert.match(overviewHTML, /prior verified response remains visible as stale/u);

console.log("PASS control-web tests: session, selector, query, 304, digests, search, hash, and SSR states");
