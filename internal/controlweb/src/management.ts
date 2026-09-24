import {
  canonicalJSONString,
  domainDigest,
  scopeKey,
  type ControlScope
} from "./contracts.ts";
import {
  ControlModulesError,
  detachJSON,
  digest,
  exactKeys,
  fetchExact,
  httpStrongETag,
  opaque,
  parseJSONRecord,
  requireJSONSuccess,
  sameCanonical,
  safeInteger,
  scopeHeaders,
  strongETag,
  validateContext,
  type ModuleContext
} from "./modules.ts";

export const UNKNOWN_OUTCOMES_PATH = "/control/api/v1/unknown-outcomes";
export const STORE_MANAGEMENT_PATH = "/control/api/v1/store-management";
export const MANAGEMENT_PAGE_LIMIT = 100;

const UNKNOWN_LIST_SCHEMA = "control-unknown-outcome-list/v1";
const UNKNOWN_DETAIL_SCHEMA = "control-unknown-outcome-detail/v1";
const STORE_SCHEMA = "control-store-management/v1";
const RAW_SEGMENT = /^[A-Za-z0-9_-]+$/u;
const DOTTED_ID = /^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)*$/u;
const VERSION = /^[A-Za-z0-9](?:[A-Za-z0-9._+-]*[A-Za-z0-9])?$/u;

export type UnknownAttemptKind = "MODEL" | "ACTION" | "CHANNEL";

export type UnknownUsage = {
  input_tokens: number | null;
  cached_input_tokens: number | null;
  uncached_input_tokens: number | null;
  output_tokens: number | null;
  reasoning_tokens: number | null;
};

export type UnknownOutcome = {
  kind: UnknownAttemptKind;
  attempt_id: string;
  run_id: string;
  tenant_id: string;
  workspace_id: string;
  state: string;
  provider?: string;
  model?: string;
  provider_request_id?: string;
  external_operation_id?: string;
  endpoint_id?: string;
  error_classification?: string;
  unknown_reason?: string;
  has_reconciliation_evidence: boolean;
  reconciliation_evidence_ref?: string;
  revision: number;
  created_at_unix_micros: number;
  updated_at_unix_micros: number;
  usage: UnknownUsage;
};

export type UnknownOutcomeListResponse = {
  schema_version: typeof UNKNOWN_LIST_SCHEMA;
  scope: ControlScope;
  items: UnknownOutcome[];
  has_more: boolean;
  projection_digest: string;
  strong_etag: string;
};

export type UnknownOutcomeDetailResponse = {
  schema_version: typeof UNKNOWN_DETAIL_SCHEMA;
  scope: ControlScope;
  item: UnknownOutcome;
  projection_digest: string;
  strong_etag: string;
};

export type ArtifactAdmission = {
  admission_id: string;
  source_id: string;
  source_policy_id: string;
  source_policy_revision: number;
  snapshot_id: string;
  snapshot_observation_revision: number;
  entry_ordinal: number;
  module: { id: string; version: string };
  artifact_digest: string;
  manifest_ref: string;
  artifact_size_bytes: number;
  covered_file_count: number;
  admitted_at_unix_micros: number;
};

export type StoreManagementResponse = {
  schema_version: typeof STORE_SCHEMA;
  scope: ControlScope;
  verification: {
    store_instance_id: string;
    schema_identity: string;
    schema_version: number;
    schema_fingerprint: string;
    generator_id: string;
  };
  backup: {
    format_version: string;
    state: string;
    online_create: boolean;
    online_restore: boolean;
    restore_mode: string;
  };
  artifacts: ArtifactAdmission[];
  has_more: boolean;
  projection_digest: string;
  strong_etag: string;
};

type ReadCache<T> = { etag: string; data: T };

const unknownListCache = new Map<string, ReadCache<UnknownOutcomeListResponse>>();
const unknownDetailCache = new Map<string, ReadCache<UnknownOutcomeDetailResponse>>();
const storeCache = new Map<string, ReadCache<StoreManagementResponse>>();

const contextCacheKey = (context: ModuleContext) =>
  JSON.stringify([scopeKey(context.scope), ...contextIdentity(context)]);

const contextIdentity = (context: ModuleContext) => [
  context.origin,
  context.bootID,
  context.sessionID,
  context.principalID,
  context.authorizationRevision,
  context.scopeSetDigest,
  context.sessionEpoch
];

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const optionalText = (value: unknown, label: string): string | undefined => {
  if (value === undefined) return undefined;
  if (!opaque(value)) throw new Error(`${label} is invalid`);
  return value;
};

const opaqueOrEmpty = (value: unknown, label: string) => {
  if (value === "") return "";
  if (!opaque(value)) throw new Error(`${label} is invalid`);
  return value;
};

const decodeModuleRef = (value: unknown, label: string) => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["id", "version"]) ||
    typeof value.id !== "string" ||
    !DOTTED_ID.test(value.id) ||
    typeof value.version !== "string" ||
    !VERSION.test(value.version)
  ) {
    throw new Error(`${label} is invalid`);
  }
  return { id: value.id, version: value.version };
};

const decodeUsage = (value: unknown): UnknownUsage => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "input_tokens",
      "cached_input_tokens",
      "uncached_input_tokens",
      "output_tokens",
      "reasoning_tokens"
    ])
  ) {
    throw new Error("UNKNOWN usage is invalid");
  }
  const usageValue = (candidate: unknown) =>
    candidate === null ? null : safeInteger(candidate) ? candidate : (() => {
      throw new Error("UNKNOWN usage token is invalid");
    })();
  return {
    input_tokens: usageValue(value.input_tokens),
    cached_input_tokens: usageValue(value.cached_input_tokens),
    uncached_input_tokens: usageValue(value.uncached_input_tokens),
    output_tokens: usageValue(value.output_tokens),
    reasoning_tokens: usageValue(value.reasoning_tokens)
  };
};

const decodeUnknown = (value: unknown): UnknownOutcome => {
  const required = [
    "kind", "attempt_id", "run_id", "tenant_id", "workspace_id", "state",
    "has_reconciliation_evidence", "revision", "created_at_unix_micros",
    "updated_at_unix_micros", "usage"
  ];
  const optional = [
    "provider", "model", "provider_request_id", "external_operation_id",
    "endpoint_id", "error_classification", "unknown_reason",
    "reconciliation_evidence_ref"
  ];
  if (!isRecord(value) || !exactKeys(value, required, optional)) {
    throw new Error("UNKNOWN outcome is invalid");
  }
  if (
    (value.kind !== "MODEL" && value.kind !== "ACTION" && value.kind !== "CHANNEL") ||
    !opaque(value.attempt_id) ||
    !opaque(value.run_id) ||
    !opaque(value.tenant_id) ||
    typeof value.workspace_id !== "string" ||
    typeof value.state !== "string" ||
    value.state.length === 0 ||
    typeof value.has_reconciliation_evidence !== "boolean" ||
    !safeInteger(value.revision, true) ||
    !safeInteger(value.created_at_unix_micros, true) ||
    !safeInteger(value.updated_at_unix_micros, true)
  ) {
    throw new Error("UNKNOWN outcome facts are invalid");
  }
  return {
    kind: value.kind,
    attempt_id: value.attempt_id,
    run_id: value.run_id,
    tenant_id: value.tenant_id,
    workspace_id: opaqueOrEmpty(value.workspace_id, "UNKNOWN workspace ID"),
    state: value.state,
    provider: optionalText(value.provider, "UNKNOWN provider"),
    model: optionalText(value.model, "UNKNOWN model"),
    provider_request_id: optionalText(value.provider_request_id, "provider request ID"),
    external_operation_id: optionalText(value.external_operation_id, "external operation ID"),
    endpoint_id: optionalText(value.endpoint_id, "UNKNOWN endpoint"),
    error_classification: optionalText(value.error_classification, "UNKNOWN error classification"),
    unknown_reason: optionalText(value.unknown_reason, "UNKNOWN reason"),
    has_reconciliation_evidence: value.has_reconciliation_evidence,
    reconciliation_evidence_ref: optionalText(
      value.reconciliation_evidence_ref,
      "reconciliation evidence reference"
    ),
    revision: value.revision,
    created_at_unix_micros: value.created_at_unix_micros,
    updated_at_unix_micros: value.updated_at_unix_micros,
    usage: decodeUsage(value.usage)
  };
};

const decodeUnknownList = (
  text: string,
  scope: ControlScope
): UnknownOutcomeListResponse => {
  const value = parseJSONRecord(text, "UNKNOWN outcome list response");
  if (
    !exactKeys(value, [
      "schema_version", "scope", "items", "has_more",
      "projection_digest", "strong_etag"
    ]) ||
    value.schema_version !== UNKNOWN_LIST_SCHEMA ||
    !sameCanonical(value.scope, scope) ||
    !Array.isArray(value.items) ||
    value.items.length > MANAGEMENT_PAGE_LIMIT ||
    typeof value.has_more !== "boolean" ||
    !digest(value.projection_digest) ||
    !strongETag(value.strong_etag)
  ) {
    throw new Error("UNKNOWN outcome list envelope is invalid");
  }
  const items = value.items.map(decodeUnknown);
  return {
    schema_version: UNKNOWN_LIST_SCHEMA,
    scope,
    items,
    has_more: value.has_more,
    projection_digest: value.projection_digest,
    strong_etag: value.strong_etag
  };
};

const decodeUnknownDetail = (
  text: string,
  scope: ControlScope,
  kind: UnknownAttemptKind,
  attemptID: string
): UnknownOutcomeDetailResponse => {
  const value = parseJSONRecord(text, "UNKNOWN outcome detail response");
  if (
    !exactKeys(value, [
      "schema_version", "scope", "item", "projection_digest", "strong_etag"
    ]) ||
    value.schema_version !== UNKNOWN_DETAIL_SCHEMA ||
    !sameCanonical(value.scope, scope) ||
    !digest(value.projection_digest) ||
    !strongETag(value.strong_etag)
  ) {
    throw new Error("UNKNOWN outcome detail envelope is invalid");
  }
  const item = decodeUnknown(value.item);
  if (item.kind !== kind || item.attempt_id !== attemptID) {
    throw new Error("UNKNOWN outcome detail does not bind the request");
  }
  return {
    schema_version: UNKNOWN_DETAIL_SCHEMA,
    scope,
    item,
    projection_digest: value.projection_digest,
    strong_etag: value.strong_etag
  };
};

const decodeArtifact = (value: unknown): ArtifactAdmission => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "admission_id", "source_id", "source_policy_id", "source_policy_revision",
      "snapshot_id", "snapshot_observation_revision", "entry_ordinal", "module",
      "artifact_digest", "manifest_ref", "artifact_size_bytes", "covered_file_count",
      "admitted_at_unix_micros"
    ]) ||
    !digest(value.admission_id) ||
    !opaque(value.source_id) ||
    !digest(value.source_policy_id) ||
    !safeInteger(value.source_policy_revision, true) ||
    !digest(value.snapshot_id) ||
    !safeInteger(value.snapshot_observation_revision, true) ||
    !safeInteger(value.entry_ordinal) ||
    !digest(value.artifact_digest) ||
    !digest(value.manifest_ref) ||
    !safeInteger(value.artifact_size_bytes, true) ||
    !safeInteger(value.covered_file_count, true) ||
    !safeInteger(value.admitted_at_unix_micros, true)
  ) {
    throw new Error("Artifact Admission is invalid");
  }
  return {
    admission_id: value.admission_id,
    source_id: value.source_id,
    source_policy_id: value.source_policy_id,
    source_policy_revision: value.source_policy_revision,
    snapshot_id: value.snapshot_id,
    snapshot_observation_revision: value.snapshot_observation_revision,
    entry_ordinal: value.entry_ordinal,
    module: decodeModuleRef(value.module, "Artifact module"),
    artifact_digest: value.artifact_digest,
    manifest_ref: value.manifest_ref,
    artifact_size_bytes: value.artifact_size_bytes,
    covered_file_count: value.covered_file_count,
    admitted_at_unix_micros: value.admitted_at_unix_micros
  };
};

const decodeStore = (
  text: string,
  scope: ControlScope
): StoreManagementResponse => {
  const value = parseJSONRecord(text, "Store management response");
  if (
    !exactKeys(value, [
      "schema_version", "scope", "verification", "backup", "artifacts",
      "has_more", "projection_digest", "strong_etag"
    ]) ||
    value.schema_version !== STORE_SCHEMA ||
    !sameCanonical(value.scope, scope) ||
    !isRecord(value.verification) ||
    !isRecord(value.backup) ||
    !Array.isArray(value.artifacts) ||
    value.artifacts.length > MANAGEMENT_PAGE_LIMIT ||
    typeof value.has_more !== "boolean" ||
    !digest(value.projection_digest) ||
    !strongETag(value.strong_etag)
  ) {
    throw new Error("Store management envelope is invalid");
  }
  const verification = value.verification;
  if (
    !exactKeys(verification, [
      "store_instance_id", "schema_identity", "schema_version",
      "schema_fingerprint", "generator_id"
    ]) ||
    !opaque(verification.store_instance_id) ||
    !opaque(verification.schema_identity) ||
    !safeInteger(verification.schema_version, true) ||
    !digest(verification.schema_fingerprint) ||
    !opaque(verification.generator_id)
  ) {
    throw new Error("Store verification is invalid");
  }
  const backup = value.backup;
  if (
    !exactKeys(backup, [
      "format_version", "state", "online_create", "online_restore", "restore_mode"
    ]) ||
    typeof backup.format_version !== "string" ||
    typeof backup.state !== "string" ||
    typeof backup.online_create !== "boolean" ||
    typeof backup.online_restore !== "boolean" ||
    typeof backup.restore_mode !== "string"
  ) {
    throw new Error("Backup management projection is invalid");
  }
  return {
    schema_version: STORE_SCHEMA,
    scope,
    verification: {
      store_instance_id: verification.store_instance_id,
      schema_identity: verification.schema_identity,
      schema_version: verification.schema_version,
      schema_fingerprint: verification.schema_fingerprint,
      generator_id: verification.generator_id
    },
    backup: {
      format_version: backup.format_version,
      state: backup.state,
      online_create: backup.online_create,
      online_restore: backup.online_restore,
      restore_mode: backup.restore_mode
    },
    artifacts: value.artifacts.map(decodeArtifact),
    has_more: value.has_more,
    projection_digest: value.projection_digest,
    strong_etag: value.strong_etag
  };
};

const managementURL = (context: ModuleContext, path: string, limit = MANAGEMENT_PAGE_LIMIT) =>
  `${context.origin}${path}?limit=${limit}`;

const readManagement = async <T>(
  context: ModuleContext,
  url: string,
  cache: Map<string, ReadCache<T>>,
  cacheKey: string,
  label: string,
  decode: (text: string) => T,
  validateProjection: (result: T) => Promise<void>,
  httpETagDomain: string,
  signal: AbortSignal | undefined,
  fetcher: typeof fetch
) => {
  type StrongETagResult = { strong_etag: string };
  validateContext(context);
  const cached = cache.get(cacheKey);
  const headers = scopeHeaders(context);
  if (cached !== undefined) headers["If-None-Match"] = cached.etag;
  const response = await fetchExact(url, {
    method: "GET",
    credentials: "include",
    redirect: "error",
    headers,
    signal
  }, fetcher, false);
  if (response.status === 304) {
    if (cached === undefined) {
      throw new ControlModulesError(
        `${label} returned 304 without a cached response`,
        response.status,
        "INVALID_304"
      );
    }
    return detachJSON(cached.data);
  }
  const text = await requireJSONSuccess(response, label, false, true);
  const etag = response.headers.get("ETag");
  if (!strongETag(etag)) {
    throw new ControlModulesError(`${label} omitted its ETag`, response.status, "INVALID_RESPONSE");
  }
  try {
    const result = decode(text);
    await validateProjection(result);
    if (
      await httpStrongETag(
        httpETagDomain,
        (result as StrongETagResult).strong_etag,
        text
      ) !== etag
    ) {
      throw new Error(`${label} HTTP ETag is invalid`);
    }
    cache.set(cacheKey, { etag, data: detachJSON(result) });
    return result;
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error ? error.message : `${label} is invalid`,
      response.status,
      "INVALID_RESPONSE"
    );
  }
};

export const clearManagementTransportCache = () => {
  unknownListCache.clear();
  unknownDetailCache.clear();
  storeCache.clear();
};

export const managementQueryKey = (context: ModuleContext) =>
  ["management", ...contextIdentity(context), scopeKey(context.scope)] as const;

export const unknownDetailQueryKey = (
  context: ModuleContext,
  kind: UnknownAttemptKind,
  attemptID: string
) => ["management-unknown-detail", ...managementQueryKey(context), kind, attemptID] as const;

export const fetchUnknownOutcomes = (
  context: ModuleContext,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
) => {
  const key = contextCacheKey(context);
  const url = managementURL(context, UNKNOWN_OUTCOMES_PATH);
  return readManagement(
    context, url, unknownListCache, key, "UNKNOWN outcome list response",
    (text) => decodeUnknownList(text, context.scope),
    async (result) => {
      const expected = await domainDigest(
        "freeagent.control-management-projection/v1",
        canonicalJSONString({
          scope: result.scope,
          items: result.items,
          has_more: result.has_more
        })
      );
      if (expected !== result.projection_digest) {
        throw new Error("UNKNOWN outcome list projection digest is invalid");
      }
    },
    "freeagent.control-http-unknown-outcome-list-etag/v1",
    signal, fetcher
  );
};

export const fetchUnknownOutcomeDetail = (
  context: ModuleContext,
  kind: UnknownAttemptKind,
  attemptID: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
) => {
  if (
    !RAW_SEGMENT.test(attemptID) ||
    !opaque(attemptID) ||
    (kind !== "MODEL" && kind !== "ACTION" && kind !== "CHANNEL")
  ) {
    throw new ControlModulesError("UNKNOWN outcome identity is invalid", 0, "INVALID_CLIENT_INPUT");
  }
  const key = JSON.stringify([contextCacheKey(context), kind, attemptID]);
  const url = `${context.origin}${UNKNOWN_OUTCOMES_PATH}/${kind}/${attemptID}`;
  return readManagement(
    context, url, unknownDetailCache, key, "UNKNOWN outcome detail response",
    (text) => decodeUnknownDetail(text, context.scope, kind, attemptID),
    async (result) => {
      const expected = await domainDigest(
        "freeagent.control-management-projection/v1",
        canonicalJSONString({ scope: result.scope, item: result.item })
      );
      if (expected !== result.projection_digest) {
        throw new Error("UNKNOWN outcome detail projection digest is invalid");
      }
    },
    "freeagent.control-http-unknown-outcome-detail-etag/v1",
    signal, fetcher
  );
};

export const fetchStoreManagement = (
  context: ModuleContext,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
) => {
  const key = contextCacheKey(context);
  const url = managementURL(context, STORE_MANAGEMENT_PATH);
  return readManagement(
    context, url, storeCache, key, "Store management response",
    (text) => decodeStore(text, context.scope),
    async (result) => {
      const expected = await domainDigest(
        "freeagent.control-management-projection/v1",
        canonicalJSONString({
          scope: result.scope,
          verification: result.verification,
          backup: result.backup,
          artifacts: result.artifacts,
          has_more: result.has_more
        })
      );
      if (expected !== result.projection_digest) {
        throw new Error("Store management projection digest is invalid");
      }
    },
    "freeagent.control-http-store-management-etag/v1",
    signal, fetcher
  );
};
