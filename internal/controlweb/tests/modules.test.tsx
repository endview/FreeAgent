import assert from "node:assert/strict";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";

import {
  canonicalJSONString,
  domainDigest,
  scopeKey,
  type ControlScope,
  type ControlSession,
  type ExpectedResourceRef,
  type PublishedBasis
} from "../src/contracts.ts";
import {
  MODULE_DISABLE_CONFIRMATION_PATH,
  MODULE_DISABLE_DRY_RUN_PATH,
  MODULE_DISABLE_MUTATE_PATH,
  MODULES_PAGE_LIMIT,
  MODULES_PATH,
  canonicalModuleDisableBody,
  clearModulesOperationCache,
  clearModulesTransportCache,
  createIdempotencyKey,
  dryRunModuleDisable,
  fetchModuleDetail,
  fetchModulesPage,
  issueModuleDisableConfirmation,
  moduleDetailQueryKey,
  modulesQueryKey,
  mutateModuleDisable,
  withPublishedModulesBasis,
  type ModuleBindingSummary,
  type ModuleContext,
  type ModuleDisableBody,
  type ModuleDetailResponse,
  type ModuleSummary,
  type ModulesPageResponse
} from "../src/modules.ts";
import { ModulesPage, isModuleDisableCandidate } from "../src/modules-ui.tsx";
import type { ScopeChoice } from "../src/overview.ts";

const ORIGIN = "http://127.0.0.1:7331";
const OTHER_ORIGIN = "http://127.0.0.1:7332";
const CSRF = "A".repeat(43);
const PROOF = `${"B".repeat(42)}A`;
const DIGEST_A = "a".repeat(64);
const DIGEST_B = "b".repeat(64);
const DIGEST_C = "c".repeat(64);
const DIGEST_D = "d".repeat(64);
const DIGEST_E = "e".repeat(64);
const OBSERVED_AT = 1_800_000_002_000_000;

Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: {
    location: { origin: ORIGIN, hash: "#modules" },
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    setTimeout,
    clearTimeout
  }
});

const tenantScope: ControlScope = {
  schema_version: "control-scope/v1",
  kind: "TENANT",
  tenant_id: "tenant-a"
};

const sessionContextFields = Object.fromEntries([
  ["authorizationRevision", 3],
  ["csrfToken", CSRF]
]) as Pick<ModuleContext, "authorizationRevision" | "csrfToken">;

const context: ModuleContext = {
  origin: ORIGIN,
  bootID: "boot-a",
  sessionID: "session-a",
  principalID: "principal-a",
  ...sessionContextFields,
  scopeSetDigest: DIGEST_D,
  scope: tenantScope,
  sessionEpoch: 1,
  sessionExpiresAtUnixMicros: Date.now() * 1000 + 600_000_000
};

const sessionIdentityProjection = Object.fromEntries([
  ["boot_id", context.bootID],
  ["principal_id", context.principalID],
  ["authorization_revision", context.authorizationRevision],
  ["scope_set_digest", context.scopeSetDigest]
]) as Pick<
  ControlSession,
  "boot_id" | "principal_id" | "authorization_revision" | "scope_set_digest"
>;

const scopeDigest = await domainDigest(
  "freeagent.control-scope/v1",
  canonicalJSONString(tenantScope)
);

const basis: PublishedBasis = {
  tenant_id: "tenant-a",
  pointer_revision: 7,
  control: { id: "control-a", revision: 4, digest: DIGEST_A },
  catalog: { id: "catalog-a", revision: 5, digest: DIGEST_B }
};

const pointerFor = async (value: PublishedBasis): Promise<ExpectedResourceRef> => ({
  kind: "PUBLISHED_POINTER",
  resource_id: value.tenant_id,
  revision: value.pointer_revision,
  digest: await domainDigest(
    "freeagent.control-published-pointer-ref/v1",
    canonicalJSONString(value)
  )
});

const pointer = await pointerFor(basis);

const moduleSummary = (
  instanceID: string,
  visibleBindingCount = 1
): ModuleSummary => ({
  instance_id: instanceID,
  module_id: "freeagent.test.module",
  exact_version: "1.0.0",
  artifact_digest: DIGEST_C,
  execution_class: "DECLARATIVE",
  adapter_identity: "static-v1",
  activation_revision: 2,
  provides: [{ name: "context.provide", exact_version: "v1" }],
  visible_binding_count: visibleBindingCount
});

const binding = (
  index: number,
  configRef = DIGEST_C
): ModuleBindingSummary => ({
  target: { kind: "PROFILE", profile_id: "profile-a" },
  port: { name: "context.provide", exact_version: "v1" },
  port_binding_index: index,
  config_ref: configRef,
  authority_ceiling_ref: DIGEST_D,
  static_context_refs: index === 0 ? [] : [DIGEST_E],
  failure_policy: "OPTIONAL"
});

const compareInstance = (left: ModuleSummary, right: ModuleSummary) => {
  const leftBytes = new TextEncoder().encode(left.instance_id);
  const rightBytes = new TextEncoder().encode(right.instance_id);
  const length = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < length; index += 1) {
    if (leftBytes[index] !== rightBytes[index]) return leftBytes[index] - rightBytes[index];
  }
  return leftBytes.length - rightBytes.length;
};

type PageFixture = {
  value: ModulesPageResponse;
  body: string;
  etag: string;
};

const pageFixture = async ({
  items,
  total = items.length,
  hasMore = false,
  opaqueNext,
  afterInstanceID = ""
}: {
  items: ModuleSummary[];
  total?: number;
  hasMore?: boolean;
  opaqueNext?: string;
  afterInstanceID?: string;
}): Promise<PageFixture> => {
  const ordered = [...items].sort(compareInstance);
  const sourceDigest = await domainDigest(
    "freeagent.control-modules-source/v1",
    canonicalJSONString({ fixture: "modules", total })
  );
  const view = {
    schema_version: "control-view-snapshot/v1" as const,
    scope: tenantScope,
    scope_digest: scopeDigest,
    observed_at_unix_micros: OBSERVED_AT,
    basis,
    sections: [{
      kind: "MODULES",
      source_revision: basis.pointer_revision,
      source_digest: sourceDigest,
      item_count: total,
      truncated: false
    }]
  };
  const viewDigest = await domainDigest(
    "freeagent.control-view-snapshot/v1",
    canonicalJSONString(view)
  );
  const filterDigest = await domainDigest(
    "freeagent.control-modules-empty-filter/v1",
    canonicalJSONString({ filters: [] })
  );
  let decodedNext: Record<string, unknown> | undefined;
  if (hasMore) {
    const lastInstanceID = ordered.at(-1)?.instance_id ?? "";
    const positionDigest = await domainDigest(
      "freeagent.control-modules-position/v1",
      canonicalJSONString({
        sort_version: "control-modules-instance-id-binary/v1",
        last_instance_id: lastInstanceID
      })
    );
    decodedNext = {
      schema_version: "control-modules-cursor/v1",
      ...sessionIdentityProjection,
      scope: tenantScope,
      scope_digest: scopeDigest,
      collection: "MODULES",
      filter_digest: filterDigest,
      sort_version: "control-modules-instance-id-binary/v1",
      source_revision: basis.pointer_revision,
      source_digest: sourceDigest,
      view_snapshot_digest: viewDigest,
      observed_at_unix_micros: OBSERVED_AT,
      last_instance_id: lastInstanceID,
      position_digest: positionDigest
    };
  }
  const projection = {
    schema_version: "control-modules-page/v1",
    published_pointer: pointer,
    basis,
    view,
    view_snapshot_digest: viewDigest,
    source_revision: basis.pointer_revision,
    source_digest: sourceDigest,
    sort_version: "control-modules-instance-id-binary/v1",
    filter_digest: filterDigest,
    limit: MODULES_PAGE_LIMIT,
    ...(afterInstanceID === "" ? {} : { after_instance_id: afterInstanceID }),
    items: ordered,
    has_more: hasMore,
    ...(decodedNext === undefined ? {} : { next_cursor: decodedNext })
  };
  const projectionDigest = await domainDigest(
    "freeagent.control-modules-page/v1",
    canonicalJSONString(projection)
  );
  const applicationETag = `"${await domainDigest(
    "freeagent.control-modules-page-etag/v1",
    canonicalJSONString({
      ...sessionIdentityProjection,
      scope_digest: scopeDigest,
      projection_digest: projectionDigest
    })
  )}"`;
  const value = {
    schema_version: "control-http-modules-page/v1" as const,
    published_pointer: pointer,
    basis,
    view,
    view_snapshot_digest: viewDigest,
    source_revision: basis.pointer_revision,
    source_digest: sourceDigest,
    sort_version: "control-modules-instance-id-binary/v1" as const,
    filter_digest: filterDigest,
    items: ordered,
    has_more: hasMore,
    ...(hasMore ? { next_cursor: opaqueNext ?? "opaque_cursor" } : {}),
    projection_digest: projectionDigest
  } satisfies ModulesPageResponse;
  const body = JSON.stringify(value);
  const etag = `"${await domainDigest(
    "freeagent.control-http-modules-page-etag/v1",
    `${applicationETag}\n${body}`
  )}"`;
  return { value, body, etag };
};

const detailFixture = async (
  summary: ModuleSummary,
  bindings: ModuleBindingSummary[],
  total = 1
): Promise<{ value: ModuleDetailResponse; body: string; etag: string }> => {
  const sourceDigest = await domainDigest(
    "freeagent.control-modules-source/v1",
    canonicalJSONString({ fixture: "detail", total })
  );
  const view = {
    schema_version: "control-view-snapshot/v1" as const,
    scope: tenantScope,
    scope_digest: scopeDigest,
    observed_at_unix_micros: OBSERVED_AT,
    basis,
    sections: [{
      kind: "MODULES",
      source_revision: basis.pointer_revision,
      source_digest: sourceDigest,
      item_count: total,
      truncated: false
    }]
  };
  const viewDigest = await domainDigest(
    "freeagent.control-view-snapshot/v1",
    canonicalJSONString(view)
  );
  const module = { summary, bindings };
  const projectionDigest = await domainDigest(
    "freeagent.control-module-detail/v1",
    canonicalJSONString({
      schema_version: "control-module-detail/v1",
      published_pointer: pointer,
      basis,
      view,
      view_snapshot_digest: viewDigest,
      source_revision: basis.pointer_revision,
      source_digest: sourceDigest,
      module
    })
  );
  const applicationETag = `"${await domainDigest(
    "freeagent.control-module-detail-etag/v1",
    canonicalJSONString({
      ...sessionIdentityProjection,
      scope_digest: scopeDigest,
      projection_digest: projectionDigest
    })
  )}"`;
  const value = {
    schema_version: "control-module-detail/v1" as const,
    published_pointer: pointer,
    basis,
    view,
    view_snapshot_digest: viewDigest,
    source_revision: basis.pointer_revision,
    source_digest: sourceDigest,
    module,
    projection_digest: projectionDigest,
    strong_etag: applicationETag
  } satisfies ModuleDetailResponse;
  const body = JSON.stringify(value);
  const etag = `"${await domainDigest(
    "freeagent.control-http-module-detail-etag/v1",
    `${applicationETag}\n${body}`
  )}"`;
  return { value, body, etag };
};

assert.deepEqual(modulesQueryKey(context), [
  "modules", ORIGIN, "boot-a", "session-a", "principal-a", 3, DIGEST_D,
  "TENANT", "tenant-a", "", ""
]);
assert.deepEqual(moduleDetailQueryKey(context, "instance-a"), [
  "module-detail", ORIGIN, "boot-a", "session-a", "principal-a", 3, DIGEST_D,
  "TENANT", "tenant-a", "", "instance-a"
]);
const generatedKey = createIdempotencyKey();
assert.match(generatedKey, /^[0-9a-f]{64}$/u);

const zeroBindingPage = await pageFixture({
  items: [moduleSummary("instance-zero", 0)]
});
clearModulesTransportCache();
let listCalls = 0;
const zeroPage = await fetchModulesPage(context, undefined, undefined, async (url, init) => {
  listCalls += 1;
  assert.equal(url, `${ORIGIN}${MODULES_PATH}?limit=${MODULES_PAGE_LIMIT}`);
  assert.equal(init?.method, "GET");
  assert.equal(init?.credentials, "include");
  assert.equal(new Headers(init?.headers).get("X-FreeAgent-CSRF"), CSRF);
  assert.equal(new Headers(init?.headers).has("If-None-Match"), false);
  return new Response(zeroBindingPage.body, {
    status: 200,
    headers: { "Content-Type": "application/json", ETag: zeroBindingPage.etag }
  });
});
assert.equal(zeroPage.items[0].visible_binding_count, 0);
const cachedZeroPage = await fetchModulesPage(context, undefined, undefined, async (_url, init) => {
  listCalls += 1;
  assert.equal(new Headers(init?.headers).get("If-None-Match"), zeroBindingPage.etag);
  return new Response(null, { status: 304, headers: { ETag: zeroBindingPage.etag } });
});
assert.deepEqual(cachedZeroPage, zeroPage);
assert.equal(listCalls, 2);

clearModulesTransportCache();
await assert.rejects(
  fetchModulesPage({ ...context, origin: OTHER_ORIGIN }, undefined, undefined, async () => {
    throw new Error("must not fetch");
  }),
  /origin does not match/u
);

const revisionZero = structuredClone(zeroBindingPage.value);
revisionZero.basis.control.revision = 0;
await assert.rejects(
  fetchModulesPage(context, undefined, undefined, async () => new Response(
    JSON.stringify(revisionZero),
    {
      status: 200,
      headers: { "Content-Type": "application/json", ETag: zeroBindingPage.etag }
    }
  )),
  /revisioned digest reference is invalid/u
);

clearModulesTransportCache();
const duplicateBody = zeroBindingPage.body.replace(
  /^\{/u,
  '{"schema_version":"control-http-modules-page/v1",'
);
await assert.rejects(
  fetchModulesPage(context, undefined, undefined, async () => new Response(
    duplicateBody,
    {
      status: 200,
      headers: { "Content-Type": "application/json", ETag: zeroBindingPage.etag }
    }
  )),
  /duplicate object key schema_version/u
);

const firstHundred = Array.from({ length: 100 }, (_, index) =>
  moduleSummary(`instance-${String(index).padStart(3, "0")}`, 0)
);
const firstPage = await pageFixture({
  items: firstHundred,
  total: 101,
  hasMore: true,
  opaqueNext: "opaque_cursor_page_two"
});
const secondPage = await pageFixture({
  items: [moduleSummary("instance-100", 0)],
  total: 101,
  afterInstanceID: "instance-099"
});
clearModulesTransportCache();
await fetchModulesPage(context, undefined, undefined, async () => new Response(
  firstPage.body,
  { status: 200, headers: { "Content-Type": "application/json", ETag: firstPage.etag } }
));
clearModulesOperationCache();
const pageTwo = await fetchModulesPage(
  context,
  "opaque_cursor_page_two",
  undefined,
  async (url) => {
    assert.equal(
      url,
      `${ORIGIN}${MODULES_PATH}?limit=${MODULES_PAGE_LIMIT}&cursor=opaque_cursor_page_two`
    );
    return new Response(secondPage.body, {
      status: 200,
      headers: { "Content-Type": "application/json", ETag: secondPage.etag }
    });
  }
);
assert.equal(pageTwo.items[0].instance_id, "instance-100");
clearModulesTransportCache();
await assert.rejects(
  fetchModulesPage(context, "opaque_cursor_page_two", undefined, async () => {
    throw new Error("must not fetch");
  }),
  /not bound to this live session/u
);

const selectedBindings = [binding(0), binding(1, DIGEST_E)];
const selectedSummary = moduleSummary("instance-a", selectedBindings.length);
const detail = await detailFixture(selectedSummary, selectedBindings);
clearModulesTransportCache();
const decodedDetail = await fetchModuleDetail(
  context,
  "instance-a",
  undefined,
  async (url, init) => {
    assert.equal(url, `${ORIGIN}${MODULES_PATH}/aW5zdGFuY2UtYQ`);
    assert.equal(init?.method, "GET");
    assert.equal(
      new Headers(init?.headers).get("X-FreeAgent-Module-Instance-ID-Encoding"),
      "base64url-utf8-v1"
    );
    return new Response(detail.body, {
      status: 200,
      headers: { "Content-Type": "application/json", ETag: detail.etag }
    });
  }
);
assert.equal(decodedDetail.module.bindings.length, 2);

const unicodeInstanceID = "模块一";
const unicodeSummary = moduleSummary(unicodeInstanceID, 0);
const unicodeDetail = await detailFixture(unicodeSummary, []);
clearModulesTransportCache();
const decodedUnicodeDetail = await fetchModuleDetail(
  context,
  unicodeInstanceID,
  undefined,
  async (url, init) => {
    assert.equal(url, `${ORIGIN}${MODULES_PATH}/5qih5Z2X5LiA`);
    assert.doesNotThrow(() => new Request(url, init));
    return new Response(unicodeDetail.body, {
      status: 200,
      headers: { "Content-Type": "application/json", ETag: unicodeDetail.etag }
    });
  }
);
assert.equal(decodedUnicodeDetail.module.summary.instance_id, unicodeInstanceID);

for (const [instanceID, encodedSegment] of [
  [".", "Lg"],
  ["..", "Li4"],
  ["module/one", "bW9kdWxlL29uZQ"],
  ["~u~YWJj", "fnV-WVdKag"],
  ["a\u0080b", "YcKAYg"]
] as const) {
  const summary = moduleSummary(instanceID, 0);
  const fixture = await detailFixture(summary, []);
  clearModulesTransportCache();
  const value = await fetchModuleDetail(context, instanceID, undefined, async (url, init) => {
    assert.equal(url, `${ORIGIN}${MODULES_PATH}/${encodedSegment}`);
    assert.equal(
      new Headers(init?.headers).get("X-FreeAgent-Module-Instance-ID-Encoding"),
      "base64url-utf8-v1"
    );
    return new Response(fixture.body, {
      status: 200,
      headers: { "Content-Type": "application/json", ETag: fixture.etag }
    });
  });
  assert.equal(value.module.summary.instance_id, instanceID);
}

const unicodeScopeContext: ModuleContext = {
  ...context,
  scope: {
    schema_version: "control-scope/v1",
    kind: "WORKSPACE",
    tenant_id: "租户甲",
    workspace_id: "工作区一"
  }
};
await assert.rejects(
  fetchModulesPage(unicodeScopeContext, undefined, undefined, async (url, init) => {
    assert.doesNotThrow(() => new Request(url, init));
    const headers = new Headers(init?.headers);
    assert.equal(headers.get("X-FreeAgent-Scope-ID-Encoding"), "base64url-utf8-v1");
    assert.equal(headers.get("X-FreeAgent-Tenant-ID"), "56ef5oi355Sy");
    assert.equal(headers.get("X-FreeAgent-Workspace-ID"), "5bel5L2c5Yy65LiA");
    throw new Error("stop after validating encoded headers");
  }),
  /did not return a response/u
);

const body: ModuleDisableBody = {
  schema_version: "control-module-disable-dry-run-input/v1",
  expected_pointer_revision: basis.pointer_revision,
  binding_target: { kind: "PROFILE", profile_id: "profile-a" },
  instance_id: "instance-a",
  port: { name: "context.provide", exact_version: "v1" }
};
assert.equal(
  canonicalModuleDisableBody(body),
  '{"binding_target":{"kind":"PROFILE","profile_id":"profile-a"},"expected_pointer_revision":7,"instance_id":"instance-a","port":{"exact_version":"v1","name":"context.provide"},"schema_version":"control-module-disable-dry-run-input/v1"}'
);

const operationFixtures = async (key: string, expiresAt: number) => {
  const inputDigest = await domainDigest(
    "freeagent.control-module-disable-dry-run-input/v1",
    canonicalModuleDisableBody(body)
  );
  const planDigest = await domainDigest(
    "freeagent.module-apply-plan/v1",
    canonicalJSONString({
      schema_version: "module-apply-plan/v1",
      desired_state: "DISABLED",
      tenant_id: tenantScope.tenant_id,
      expected_pointer_revision: basis.pointer_revision,
      binding_target: body.binding_target,
      instance_id: body.instance_id,
      port: body.port
    })
  );
  const candidateBasis: PublishedBasis = {
    tenant_id: "tenant-a",
    pointer_revision: 8,
    control: { id: "control-b", revision: 5, digest: DIGEST_C },
    catalog: { id: "catalog-b", revision: 6, digest: DIGEST_D }
  };
  const removal = { ...binding(0), target: body.binding_target, port: body.port };
  const projection = {
    disposition: "WOULD_APPLY",
    plan_digest: planDigest,
    instance_id: body.instance_id,
    precondition_basis: basis,
    observed_basis: basis,
    candidate_basis: candidateBasis,
    candidate_state: "PROJECTED_NOT_RESERVED",
    binding_removal: removal,
    catalog_change: "RETAIN_INSTANCE"
  };
  const dryRequest = {
    schema_version: "control-operation-request/v1",
    principal_id: context.principalID,
    capability: "OPERATE_MODULES",
    scope: tenantScope,
    scope_digest: scopeDigest,
    operation: "MODULE_DISABLE",
    intent: "DRY_RUN",
    input_digest: inputDigest,
    expected_ref: pointer
  };
  const dryRequestDigest = await domainDigest(
    "freeagent.control-operation-request/v1",
    canonicalJSONString(dryRequest)
  );
  const dryReceipt = {
    schema_version: "control-operation-receipt/v1",
    request_digest: dryRequestDigest,
    intent: "DRY_RUN",
    principal_id: context.principalID,
    scope_digest: scopeDigest,
    operation: "MODULE_DISABLE",
    status: "DRY_RUN",
    error_code: "NONE",
    pre_ref: pointer,
    replay_disposition: "NO_RETRY",
    completed_at_unix_micros: 1_800_000_003_000_000
  };
  const dryReceiptDigest = await domainDigest(
    "freeagent.control-operation-receipt/v1",
    canonicalJSONString(dryReceipt)
  );
  const dryRun = {
    schema_version: "control-module-disable-dry-run-result/v1",
    request: dryRequest,
    request_digest: dryRequestDigest,
    receipt: dryReceipt,
    receipt_digest: dryReceiptDigest,
    projection
  };
  const evaluation = {
    schema_version: "control-module-disable-evaluation/v1",
    operation: "MODULE_DISABLE",
    input_digest: inputDigest,
    expected_ref: pointer,
    projection
  };
  const evaluationDigest = await domainDigest(
    "freeagent.control-module-disable-evaluation/v1",
    canonicalJSONString(evaluation)
  );
  const keyDigest = await domainDigest("freeagent.control-idempotency-key/v1", key);
  const statement = {
    schema_version: "control-confirmation-statement/v1",
    principal_id: context.principalID,
    capability: "OPERATE_MODULES",
    intent: "MUTATE",
    operation: "MODULE_DISABLE",
    scope: tenantScope,
    scope_digest: scopeDigest,
    idempotency_key_digest: keyDigest,
    input_digest: inputDigest,
    operation_evaluation_digest: evaluationDigest,
    expected_ref: pointer
  };
  const statementDigest = await domainDigest(
    "freeagent.control-confirmation-statement/v1",
    canonicalJSONString(statement)
  );
  const mutationRequest = {
    schema_version: "control-operation-request/v1",
    principal_id: context.principalID,
    capability: "OPERATE_MODULES",
    scope: tenantScope,
    scope_digest: scopeDigest,
    operation: "MODULE_DISABLE",
    intent: "MUTATE",
    idempotency_key_digest: keyDigest,
    input_digest: inputDigest,
    operation_evaluation_digest: evaluationDigest,
    expected_ref: pointer,
    confirmation_digest: statementDigest
  };
  const mutationRequestDigest = await domainDigest(
    "freeagent.control-operation-request/v1",
    canonicalJSONString(mutationRequest)
  );
  const confirmation = {
    schema_version: "control-module-disable-confirmation-result/v1",
    request: mutationRequest,
    request_digest: mutationRequestDigest,
    evaluation,
    evaluation_digest: evaluationDigest,
    statement,
    statement_digest: statementDigest,
    confirmation_proof: PROOF,
    expires_at_unix_micros: expiresAt
  };
  const mutationReceipt = {
    schema_version: "control-operation-receipt/v1",
    request_digest: mutationRequestDigest,
    intent: "MUTATE",
    idempotency_key_digest: keyDigest,
    principal_id: context.principalID,
    scope_digest: scopeDigest,
    operation: "MODULE_DISABLE",
    status: "NO_CHANGE",
    error_code: "NONE",
    pre_ref: pointer,
    post_ref: pointer,
    replay_disposition: "RETURN_EXACT_RECEIPT",
    completed_at_unix_micros: 1_800_000_004_000_000
  };
  const mutationReceiptDigest = await domainDigest(
    "freeagent.control-operation-receipt/v1",
    canonicalJSONString(mutationReceipt)
  );
  const mutation = {
    schema_version: "control-module-disable-mutation-result/v1",
    request: mutationRequest,
    request_digest: mutationRequestDigest,
    receipt: mutationReceipt,
    receipt_digest: mutationReceiptDigest
  };
  return { dryRun, confirmation, mutation, evaluationDigest };
};

const operationKey = "K".repeat(32);
const operation = await operationFixtures(
  operationKey,
  Date.now() * 1000 + 60_000_000
);
const operationContext = withPublishedModulesBasis(context, detail.value);
const expectedBody = canonicalModuleDisableBody(body);
const expectedIfMatch = `"${pointer.digest}"`;

const dry = await dryRunModuleDisable(
  operationContext,
  body,
  undefined,
  async (url, init) => {
    const headers = new Headers(init?.headers);
    assert.equal(url, `${ORIGIN}${MODULE_DISABLE_DRY_RUN_PATH}`);
    assert.equal(init?.method, "POST");
    assert.equal(init?.body, expectedBody);
    assert.equal(headers.get("If-Match"), expectedIfMatch);
    assert.equal(headers.get("Idempotency-Key"), null);
    assert.equal(headers.get("X-FreeAgent-Confirmation"), null);
    return new Response(JSON.stringify(operation.dryRun), {
      status: 200,
      headers: { "Content-Type": "application/json" }
    });
  }
);
assert.equal(dry.projection.disposition, "WOULD_APPLY");

const confirmation = await issueModuleDisableConfirmation(
  operationContext,
  body,
  operationKey,
  undefined,
  async (url, init) => {
    const headers = new Headers(init?.headers);
    assert.equal(url, `${ORIGIN}${MODULE_DISABLE_CONFIRMATION_PATH}`);
    assert.equal(headers.get("Idempotency-Key"), operationKey);
    assert.equal(headers.get("X-FreeAgent-Operation-Evaluation-Digest"), null);
    assert.equal(headers.get("X-FreeAgent-Confirmation"), null);
    return new Response(JSON.stringify(operation.confirmation), {
      status: 200,
      headers: { "Content-Type": "application/json" }
    });
  }
);
assert.equal(confirmation.confirmation_proof, PROOF);

let mutationCalls = 0;
const mutationFetcher = async (url: RequestInfo | URL, init?: RequestInit) => {
  mutationCalls += 1;
  const headers = new Headers(init?.headers);
  assert.equal(url, `${ORIGIN}${MODULE_DISABLE_MUTATE_PATH}`);
  assert.equal(init?.body, expectedBody);
  assert.equal(headers.get("Idempotency-Key"), operationKey);
  assert.equal(
    headers.get("X-FreeAgent-Operation-Evaluation-Digest"),
    operation.evaluationDigest
  );
  assert.equal(
    headers.get("X-FreeAgent-Confirmation"),
    mutationCalls === 1 ? PROOF : null
  );
  return new Response(JSON.stringify(operation.mutation), {
    status: 200,
    headers: { "Content-Type": "application/json" }
  });
};
const mutation = await mutateModuleDisable(
  operationContext,
  body,
  operationKey,
  operation.evaluationDigest,
  PROOF,
  undefined,
  mutationFetcher
);
assert.equal(mutation.receipt.status, "NO_CHANGE");
await mutateModuleDisable(
  operationContext,
  body,
  operationKey,
  operation.evaluationDigest,
  undefined,
  undefined,
  mutationFetcher
);
assert.equal(mutationCalls, 2);

const expiredOperation = await operationFixtures(
  "L".repeat(32),
  Date.now() * 1000 - 1
);
await assert.rejects(
  issueModuleDisableConfirmation(
    operationContext,
    body,
    "L".repeat(32),
    undefined,
    async () => new Response(JSON.stringify(expiredOperation.confirmation), {
      status: 200,
      headers: { "Content-Type": "application/json" }
    })
  ),
  /already expired/u
);

const session = {
  schema_version: "control-session/v1",
  ...sessionIdentityProjection,
  session_id: context.sessionID,
  capabilities: ["OBSERVE", "OPERATE_MODULES"],
  issued_at_unix_micros: Date.now() * 1000 - 60_000_000,
  expires_at_unix_micros: context.sessionExpiresAtUnixMicros ?? 0
} satisfies ControlSession;
const choices: ScopeChoice[] = [{
  key: scopeKey(tenantScope),
  label: "Tenant / tenant-a",
  scope: tenantScope,
  source: "authorized"
}];
const uiPage = await pageFixture({ items: [selectedSummary] });

const renderModules = (capabilities: ControlSession["capabilities"]) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } }
  });
  queryClient.setQueryData(modulesQueryKey(context), {
    pages: [uiPage.value],
    pageParams: [""]
  });
  queryClient.setQueryData(moduleDetailQueryKey(context, "instance-a"), detail.value);
  const html = renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <ModulesPage
        session={{ ...session, capabilities }}
        context={context}
        scopeChoices={choices}
        selectedScopeKey={choices[0].key}
        onScopeChange={() => undefined}
      />
    </QueryClientProvider>
  );
  queryClient.clear();
  return html;
};

const readOnlyHTML = renderModules(["OBSERVE"]);
assert.match(readOnlyHTML, /Read-only Modules session/u);
assert.doesNotMatch(readOnlyHTML, /Review disable/u);

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } }
});
queryClient.setQueryData(modulesQueryKey(context), {
  pages: [uiPage.value],
  pageParams: [""]
});
queryClient.setQueryData(moduleDetailQueryKey(context, "instance-a"), detail.value);
const operatedHTML = renderToStaticMarkup(
  <QueryClientProvider client={queryClient}>
    <ModulesPage
      session={session}
      context={context}
      scopeChoices={choices}
      selectedScopeKey={choices[0].key}
      onScopeChange={() => undefined}
    />
  </QueryClientProvider>
);
queryClient.clear();
assert.match(operatedHTML, /instance-a/u);
assert.match(operatedHTML, /Select a module/u);
assert.doesNotMatch(operatedHTML, /Review disable/u);
assert.equal(isModuleDisableCandidate(selectedSummary, selectedBindings[0]), true);
assert.equal(isModuleDisableCandidate(selectedSummary, selectedBindings[1]), true);
assert.doesNotMatch(operatedHTML, new RegExp(operationKey, "u"));
assert.doesNotMatch(operatedHTML, new RegExp(PROOF, "u"));
delete (globalThis as { window?: unknown }).window;

console.log(
  "PASS control-web Modules tests: zero binding, cursor, 304, strict JSON, ETag, disable chain, expiry, exact retry, and SSR authority"
);
