import {
  CONTROL_SCOPE_ID_ENCODING,
  CONTROL_SCOPE_ID_ENCODING_HEADER,
  MAX_RESPONSE_BYTES,
  canonicalJSONString,
  credential,
  decodeControlError,
  domainDigest,
  exactLoopbackOrigin,
  encodeControlScopeID,
  scopeKey,
  type ControlSession,
  type ControlScope,
  type ControlView,
  type DigestRef,
  type ExpectedResourceRef,
  type PublishedBasis
} from "./contracts.ts";
import type { OverviewContext } from "./overview.ts";

export const MODULES_PATH = "/control/api/v1/modules";
export const MODULE_DISABLE_DRY_RUN_PATH =
  "/control/api/v1/modules/disable/dry-run";
export const MODULE_DISABLE_CONFIRMATION_PATH =
  "/control/api/v1/modules/disable/confirmation";
export const MODULE_DISABLE_MUTATE_PATH =
  "/control/api/v1/modules/disable/mutate";

export const MODULES_PAGE_LIMIT = 100;
export const MODULE_DISABLE_BODY_SCHEMA =
  "control-module-disable-dry-run-input/v1";

const MODULES_PAGE_SCHEMA = "control-http-modules-page/v1";
const MODULES_APPLICATION_PAGE_SCHEMA = "control-modules-page/v1";
const MODULE_DETAIL_SCHEMA = "control-module-detail/v1";
const MODULES_CURSOR_SCHEMA = "control-modules-cursor/v1";
const MODULES_SORT_VERSION = "control-modules-instance-id-binary/v1";
const MODULE_DISABLE_DRY_RUN_RESULT_SCHEMA =
  "control-module-disable-dry-run-result/v1";
const MODULE_DISABLE_EVALUATION_SCHEMA =
  "control-module-disable-evaluation/v1";
const MODULE_DISABLE_CONFIRMATION_RESULT_SCHEMA =
  "control-module-disable-confirmation-result/v1";
const MODULE_DISABLE_MUTATION_RESULT_SCHEMA =
  "control-module-disable-mutation-result/v1";
const OPERATION_REQUEST_SCHEMA = "control-operation-request/v1";
const OPERATION_RECEIPT_SCHEMA = "control-operation-receipt/v1";
const CONFIRMATION_STATEMENT_SCHEMA = "control-confirmation-statement/v1";

const DIGEST_PATTERN = /^[0-9a-f]{64}$/u;
const STRONG_ETAG_PATTERN = /^"[0-9a-f]{64}"$/u;
const RAW_URL_SEGMENT_PATTERN = /^[A-Za-z0-9_-]+$/u;
const MODULE_INSTANCE_ID_ENCODING = "base64url-utf8-v1";
const MODULE_INSTANCE_ID_ENCODING_HEADER =
  "X-FreeAgent-Module-Instance-ID-Encoding";
const DOTTED_IDENTIFIER_PATTERN =
  /^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)*$/u;
const VERSION_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9._+-]*[A-Za-z0-9])?$/u;

const MAX_OPAQUE_BYTES = 256;
const MAX_IDENTIFIER_BYTES = 128;
const MAX_VERSION_BYTES = 64;
const MAX_MANIFEST_ENTRIES = 256;
const MAX_VISIBLE_BINDINGS = 1 << 17;
const MAX_CURSOR_BYTES = 4096;
const MAX_JSON_DEPTH = 64;
const MAX_JSON_NODES = 1 << 16;
const MAX_TRANSPORT_CACHE_ENTRIES = 256;
const MAX_CONFIRMATION_CACHE_ENTRIES = 64;

export type ModulePortRef = {
  name: string;
  exact_version: string;
};

export type ModuleBindingTarget =
  | { kind: "PROFILE"; profile_id: string }
  | {
      kind: "WORKSPACE_CHANNEL_ENDPOINT";
      workspace_id: string;
      endpoint_id: string;
    };

export type ModuleBindingSummary = {
  target: ModuleBindingTarget;
  port: ModulePortRef;
  port_binding_index: number;
  config_ref: string;
  authority_ceiling_ref: string;
  static_context_refs: string[];
  failure_policy: "REQUIRED" | "OPTIONAL";
};

export type ModuleSummary = {
  instance_id: string;
  module_id: string;
  exact_version: string;
  artifact_digest: string;
  execution_class:
    | "DECLARATIVE"
    | "TRUSTED_IN_PROCESS"
    | "LOCAL_PROCESS"
    | "REMOTE"
    | "WASM";
  adapter_identity: string;
  activation_revision: number;
  provides: ModulePortRef[];
  visible_binding_count: number;
};

export type ModuleDetail = {
  summary: ModuleSummary;
  bindings: ModuleBindingSummary[];
};

export type ModulesPageResponse = {
  schema_version: "control-http-modules-page/v1";
  published_pointer: ExpectedResourceRef;
  basis: PublishedBasis;
  view: ControlView;
  view_snapshot_digest: string;
  source_revision: number;
  source_digest: string;
  sort_version: "control-modules-instance-id-binary/v1";
  filter_digest: string;
  items: ModuleSummary[];
  has_more: boolean;
  next_cursor?: string;
  projection_digest: string;
};

export type ModuleDetailResponse = {
  schema_version: "control-module-detail/v1";
  published_pointer: ExpectedResourceRef;
  basis: PublishedBasis;
  view: ControlView;
  view_snapshot_digest: string;
  source_revision: number;
  source_digest: string;
  module: ModuleDetail;
  projection_digest: string;
  strong_etag: string;
};

export type ModuleDisableBody = {
  schema_version: "control-module-disable-dry-run-input/v1";
  expected_pointer_revision: number;
  binding_target: { kind: "PROFILE"; profile_id: string };
  instance_id: string;
  port: { name: "context.provide"; exact_version: "v1" };
};

export type ModuleDisableBindingRemoval = ModuleBindingSummary & {
  target: { kind: "PROFILE"; profile_id: string };
  port: { name: "context.provide"; exact_version: "v1" };
  failure_policy: "OPTIONAL";
};

export type ModuleDisableProjection = {
  disposition: "ALREADY_APPLIED" | "NO_CHANGE" | "WOULD_APPLY";
  plan_digest: string;
  instance_id: string;
  precondition_basis: PublishedBasis;
  observed_basis: PublishedBasis;
  candidate_basis: PublishedBasis;
  candidate_state: "PROJECTED_NOT_RESERVED";
  binding_removal?: ModuleDisableBindingRemoval;
  catalog_change: "NONE" | "RETAIN_INSTANCE" | "REMOVE_INSTANCE";
};

export type ModuleDisableEvaluation = {
  schema_version: "control-module-disable-evaluation/v1";
  operation: "MODULE_DISABLE";
  input_digest: string;
  expected_ref: ExpectedResourceRef;
  projection: ModuleDisableProjection;
};

export type ControlOperationRequest = {
  schema_version: "control-operation-request/v1";
  principal_id: string;
  capability: "OPERATE_MODULES";
  scope: ControlScope;
  scope_digest: string;
  operation: "MODULE_DISABLE";
  intent: "DRY_RUN" | "MUTATE";
  idempotency_key_digest?: string;
  input_digest: string;
  operation_evaluation_digest?: string;
  expected_ref: ExpectedResourceRef;
  confirmation_digest?: string;
};

export type DomainReceiptRef = {
  kind: "MODULE_DISABLE";
  id: string;
  digest: string;
};

export type ControlOperationReceipt = {
  schema_version: "control-operation-receipt/v1";
  request_digest: string;
  intent: "DRY_RUN" | "MUTATE";
  idempotency_key_digest?: string;
  principal_id: string;
  scope_digest: string;
  operation: "MODULE_DISABLE";
  status: "DRY_RUN" | "NO_CHANGE" | "APPLIED";
  error_code: "NONE";
  pre_ref: ExpectedResourceRef;
  post_ref?: ExpectedResourceRef;
  domain_receipt?: DomainReceiptRef;
  replay_disposition: "NO_RETRY" | "RETURN_EXACT_RECEIPT";
  completed_at_unix_micros: number;
};

export type ControlConfirmationStatement = {
  schema_version: "control-confirmation-statement/v1";
  principal_id: string;
  capability: "OPERATE_MODULES";
  intent: "MUTATE";
  operation: "MODULE_DISABLE";
  scope: ControlScope;
  scope_digest: string;
  idempotency_key_digest: string;
  input_digest: string;
  operation_evaluation_digest: string;
  expected_ref: ExpectedResourceRef;
};

export type ModuleDisableDryRunResult = {
  schema_version: "control-module-disable-dry-run-result/v1";
  request: ControlOperationRequest;
  request_digest: string;
  receipt: ControlOperationReceipt;
  receipt_digest: string;
  projection: ModuleDisableProjection;
};

export type ModuleDisableConfirmationResult = {
  schema_version: "control-module-disable-confirmation-result/v1";
  request: ControlOperationRequest;
  request_digest: string;
  evaluation: ModuleDisableEvaluation;
  evaluation_digest: string;
  statement: ControlConfirmationStatement;
  statement_digest: string;
  confirmation_proof: string;
  expires_at_unix_micros: number;
};

export type ModuleDisableMutationResult = {
  schema_version: "control-module-disable-mutation-result/v1";
  request: ControlOperationRequest;
  request_digest: string;
  receipt: ControlOperationReceipt;
  receipt_digest: string;
};

export type ModuleContext = OverviewContext & {
  sessionEpoch: number;
  sessionExpiresAtUnixMicros?: number;
  publishedPointer?: ExpectedResourceRef;
  publishedBasis?: PublishedBasis;
};

export type ModulesQueryKey = readonly [
  "modules",
  string,
  string,
  string,
  string,
  number,
  string,
  "TENANT" | "WORKSPACE",
  string,
  string,
  string
];

export type ModuleDetailQueryKey = readonly [
  "module-detail",
  string,
  string,
  string,
  string,
  number,
  string,
  "TENANT" | "WORKSPACE",
  string,
  string,
  string
];

export class ControlModulesError extends Error {
  readonly status: number;
  readonly code: string;
  readonly correlationID: string;
  readonly retryAfterSeconds: number;
  readonly outcomeMayBeCommitted: boolean;

  constructor(
    message: string,
    status = 0,
    code = "TRANSPORT_ERROR",
    correlationID = "",
    retryAfterSeconds = 0,
    outcomeMayBeCommitted = false
  ) {
    super(message);
    this.name = "ControlModulesError";
    this.status = status;
    this.code = code;
    this.correlationID = correlationID;
    this.retryAfterSeconds = retryAfterSeconds;
    this.outcomeMayBeCommitted = outcomeMayBeCommitted;
  }
}

export const isModulesPermissionDenied = (
  error: unknown
): error is ControlModulesError =>
  error instanceof ControlModulesError &&
  (error.status === 403 ||
    error.code === "FORBIDDEN" ||
    error.code === "PERMISSION_DENIED");

export const isModulesSessionInvalid = (
  error: unknown
): error is ControlModulesError =>
  error instanceof ControlModulesError &&
  (error.status === 401 ||
    error.code === "UNAUTHENTICATED" ||
    error.code === "SESSION_EXPIRED");

export const isModulesStale = (error: unknown): error is ControlModulesError =>
  error instanceof ControlModulesError &&
  (error.code === "REVISION_CONFLICT" ||
    error.code === "CURSOR_STALE" ||
    error.code === "CURSOR_CONTEXT_MISSING" ||
    error.code === "INVALID_304");

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const exactKeys = (
  value: Record<string, unknown>,
  required: readonly string[],
  optional: readonly string[] = []
) => {
  const allowed = new Set([...required, ...optional]);
  const keys = Object.keys(value);
  return (
    required.every((key) => Object.hasOwn(value, key)) &&
    keys.every((key) => allowed.has(key))
  );
};

const safeInteger = (value: unknown, positive = false): value is number =>
  Number.isSafeInteger(value) && (value as number) >= (positive ? 1 : 0);

const boundedInteger = (
  value: unknown,
  maximum: number,
  positive = false
): value is number => safeInteger(value, positive) && (value as number) <= maximum;

const digest = (value: unknown): value is string =>
  typeof value === "string" && DIGEST_PATTERN.test(value);

const strongETag = (value: unknown): value is string =>
  typeof value === "string" && STRONG_ETAG_PATTERN.test(value);

const utf8Bytes = (value: string) => new TextEncoder().encode(value);
const byteLength = (value: string) => utf8Bytes(value).length;
const wellFormedUTF8 = (value: string) => {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(utf8Bytes(value)) === value;
  } catch {
    return false;
  }
};

const opaque = (value: unknown, maximum = MAX_OPAQUE_BYTES): value is string =>
  typeof value === "string" &&
  value.length > 0 &&
  wellFormedUTF8(value) &&
  byteLength(value) <= maximum &&
  value === value.trim() &&
  value === value.normalize("NFC") &&
  !/[\u0000-\u001f\u007f]/u.test(value);

const dottedIdentifier = (value: unknown): value is string =>
  typeof value === "string" &&
  byteLength(value) <= MAX_IDENTIFIER_BYTES &&
  DOTTED_IDENTIFIER_PATTERN.test(value);

const version = (value: unknown): value is string =>
  typeof value === "string" &&
  byteLength(value) <= MAX_VERSION_BYTES &&
  VERSION_PATTERN.test(value);

const sameCanonical = (left: unknown, right: unknown) =>
  canonicalJSONString(left) === canonicalJSONString(right);

const compareUTF8 = (left: string, right: string) => {
  const leftBytes = new TextEncoder().encode(left);
  const rightBytes = new TextEncoder().encode(right);
  const length = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < length; index += 1) {
    if (leftBytes[index] !== rightBytes[index]) {
      return leftBytes[index] - rightBytes[index];
    }
  }
  return leftBytes.length - rightBytes.length;
};

const rejectDuplicateObjectKeys = (text: string, label: string) => {
  let index = 0;
  let nodes = 0;
  const whitespace = () => {
    while (index < text.length && /[\t\n\r ]/u.test(text[index])) index += 1;
  };
  const readStringLexeme = () => {
    const start = index;
    index += 1;
    while (index < text.length) {
      if (text[index] === "\\") {
        index += 2;
        continue;
      }
      if (text[index] === '"') {
        index += 1;
        return JSON.parse(text.slice(start, index)) as string;
      }
      index += 1;
    }
    throw new Error(`${label} contains an unterminated JSON string`);
  };
  const value = (depth: number): void => {
    nodes += 1;
    if (depth > MAX_JSON_DEPTH || nodes > MAX_JSON_NODES) {
      throw new Error(`${label} exceeds JSON structural bounds`);
    }
    whitespace();
    if (text[index] === "{") {
      index += 1;
      whitespace();
      const keys = new Set<string>();
      if (text[index] === "}") {
        index += 1;
        return;
      }
      while (index < text.length) {
        if (text[index] !== '"') throw new Error(`${label} contains invalid JSON`);
        const key = readStringLexeme();
        if (keys.has(key)) {
          throw new Error(`${label} contains duplicate object key ${key}`);
        }
        keys.add(key);
        whitespace();
        if (text[index] !== ":") throw new Error(`${label} contains invalid JSON`);
        index += 1;
        value(depth + 1);
        whitespace();
        if (text[index] === "}") {
          index += 1;
          return;
        }
        if (text[index] !== ",") throw new Error(`${label} contains invalid JSON`);
        index += 1;
        whitespace();
      }
      throw new Error(`${label} contains invalid JSON`);
    }
    if (text[index] === "[") {
      index += 1;
      whitespace();
      if (text[index] === "]") {
        index += 1;
        return;
      }
      while (index < text.length) {
        value(depth + 1);
        whitespace();
        if (text[index] === "]") {
          index += 1;
          return;
        }
        if (text[index] !== ",") throw new Error(`${label} contains invalid JSON`);
        index += 1;
      }
      throw new Error(`${label} contains invalid JSON`);
    }
    if (text[index] === '"') {
      readStringLexeme();
      return;
    }
    const start = index;
    while (index < text.length && !/[\t\n\r ,\]}]/u.test(text[index])) index += 1;
    if (index === start) throw new Error(`${label} contains invalid JSON`);
  };
  value(0);
  whitespace();
  if (index !== text.length) throw new Error(`${label} contains trailing JSON material`);
};

const parseJSONRecord = (text: string, label: string) => {
  let decoded: unknown;
  try {
    rejectDuplicateObjectKeys(text, label);
    decoded = JSON.parse(text);
  } catch (error) {
    if (
      error instanceof Error &&
      (error.message.includes("duplicate object key") ||
        error.message.includes("structural bounds"))
    ) {
      throw error;
    }
    throw new Error(`${label} is not valid JSON`);
  }
  if (!isRecord(decoded)) throw new Error(`${label} must be one JSON object`);
  return decoded;
};

const decodeScope = (value: unknown): ControlScope => {
  if (!isRecord(value) || value.schema_version !== "control-scope/v1") {
    throw new Error("control scope is invalid");
  }
  if (value.kind === "TENANT") {
    if (
      !exactKeys(value, ["schema_version", "kind", "tenant_id"]) ||
      !opaque(value.tenant_id)
    ) {
      throw new Error("Tenant control scope is invalid");
    }
    return value as ControlScope;
  }
  if (
    value.kind !== "WORKSPACE" ||
    !exactKeys(value, ["schema_version", "kind", "tenant_id", "workspace_id"]) ||
    !opaque(value.tenant_id) ||
    !opaque(value.workspace_id)
  ) {
    throw new Error("Workspace control scope is invalid");
  }
  return value as ControlScope;
};

const decodeDigestRef = (value: unknown): DigestRef => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["id", "revision", "digest"]) ||
    !opaque(value.id) ||
    !safeInteger(value.revision, true) ||
    !digest(value.digest)
  ) {
    throw new Error("revisioned digest reference is invalid");
  }
  return value as DigestRef;
};

const decodeBasis = (value: unknown): PublishedBasis => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["tenant_id", "pointer_revision", "control", "catalog"]) ||
    !opaque(value.tenant_id) ||
    !safeInteger(value.pointer_revision, true)
  ) {
    throw new Error("published basis is invalid");
  }
  return {
    tenant_id: value.tenant_id,
    pointer_revision: value.pointer_revision,
    control: decodeDigestRef(value.control),
    catalog: decodeDigestRef(value.catalog)
  };
};

const decodeExpectedRef = (value: unknown): ExpectedResourceRef => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["kind", "resource_id", "revision", "digest"]) ||
    value.kind !== "PUBLISHED_POINTER" ||
    !opaque(value.resource_id) ||
    !safeInteger(value.revision, true) ||
    !digest(value.digest)
  ) {
    throw new Error("Published Pointer reference is invalid");
  }
  return value as ExpectedResourceRef;
};

const decodeView = (value: unknown): ControlView => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "schema_version",
      "scope",
      "scope_digest",
      "observed_at_unix_micros",
      "basis",
      "sections"
    ]) ||
    value.schema_version !== "control-view-snapshot/v1" ||
    !digest(value.scope_digest) ||
    !safeInteger(value.observed_at_unix_micros, true) ||
    !Array.isArray(value.sections) ||
    value.sections.length !== 1
  ) {
    throw new Error("Modules view is invalid");
  }
  const section = value.sections[0];
  if (
    !isRecord(section) ||
    !exactKeys(section, [
      "kind",
      "source_revision",
      "source_digest",
      "item_count",
      "truncated"
    ]) ||
    section.kind !== "MODULES" ||
    !safeInteger(section.source_revision, true) ||
    !digest(section.source_digest) ||
    !boundedInteger(section.item_count, MAX_VISIBLE_BINDINGS) ||
    section.truncated !== false
  ) {
    throw new Error("Modules view section is invalid");
  }
  return {
    schema_version: "control-view-snapshot/v1",
    scope: decodeScope(value.scope),
    scope_digest: value.scope_digest,
    observed_at_unix_micros: value.observed_at_unix_micros,
    basis: decodeBasis(value.basis),
    sections: [
      {
        kind: "MODULES",
        source_revision: section.source_revision,
        source_digest: section.source_digest,
        item_count: section.item_count,
        truncated: false
      }
    ]
  };
};

const decodePort = (value: unknown): ModulePortRef => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["name", "exact_version"]) ||
    !dottedIdentifier(value.name) ||
    !version(value.exact_version)
  ) {
    throw new Error("module Port reference is invalid");
  }
  return value as ModulePortRef;
};

const decodeTarget = (value: unknown): ModuleBindingTarget => {
  if (!isRecord(value)) throw new Error("module Binding target is invalid");
  if (value.kind === "PROFILE") {
    if (!exactKeys(value, ["kind", "profile_id"]) || !opaque(value.profile_id)) {
      throw new Error("Profile Binding target is invalid");
    }
    return value as ModuleBindingTarget;
  }
  if (
    value.kind !== "WORKSPACE_CHANNEL_ENDPOINT" ||
    !exactKeys(value, ["kind", "workspace_id", "endpoint_id"]) ||
    !opaque(value.workspace_id) ||
    !opaque(value.endpoint_id)
  ) {
    throw new Error("Workspace Channel Endpoint Binding target is invalid");
  }
  return value as ModuleBindingTarget;
};

const decodeDigestArray = (value: unknown, label: string) => {
  if (!Array.isArray(value) || value.length > MAX_MANIFEST_ENTRIES) {
    throw new Error(`${label} exceeds its bound`);
  }
  const result = value.map((entry) => {
    if (!digest(entry)) throw new Error(`${label} contains an invalid digest`);
    return entry;
  });
  if (new Set(result).size !== result.length) {
    throw new Error(`${label} contains duplicate digests`);
  }
  return result;
};

const decodeBinding = (value: unknown): ModuleBindingSummary => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "target",
      "port",
      "port_binding_index",
      "config_ref",
      "authority_ceiling_ref",
      "static_context_refs",
      "failure_policy"
    ]) ||
    !boundedInteger(value.port_binding_index, MAX_MANIFEST_ENTRIES - 1) ||
    !digest(value.config_ref) ||
    !digest(value.authority_ceiling_ref) ||
    (value.failure_policy !== "REQUIRED" && value.failure_policy !== "OPTIONAL")
  ) {
    throw new Error("module Binding is invalid");
  }
  return {
    target: decodeTarget(value.target),
    port: decodePort(value.port),
    port_binding_index: value.port_binding_index,
    config_ref: value.config_ref,
    authority_ceiling_ref: value.authority_ceiling_ref,
    static_context_refs: decodeDigestArray(
      value.static_context_refs,
      "module Binding static context references"
    ),
    failure_policy: value.failure_policy
  };
};

const EXECUTION_CLASSES = new Set([
  "DECLARATIVE",
  "TRUSTED_IN_PROCESS",
  "LOCAL_PROCESS",
  "REMOTE",
  "WASM"
]);

const decodeModuleSummary = (value: unknown): ModuleSummary => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "instance_id",
      "module_id",
      "exact_version",
      "artifact_digest",
      "execution_class",
      "adapter_identity",
      "activation_revision",
      "provides",
      "visible_binding_count"
    ]) ||
    !opaque(value.instance_id) ||
    !dottedIdentifier(value.module_id) ||
    !version(value.exact_version) ||
    !digest(value.artifact_digest) ||
    typeof value.execution_class !== "string" ||
    !EXECUTION_CLASSES.has(value.execution_class) ||
    !opaque(value.adapter_identity) ||
    !safeInteger(value.activation_revision, true) ||
    !Array.isArray(value.provides) ||
    value.provides.length < 1 ||
    value.provides.length > MAX_MANIFEST_ENTRIES ||
    !boundedInteger(value.visible_binding_count, MAX_VISIBLE_BINDINGS)
  ) {
    throw new Error("module summary is invalid");
  }
  const provides = value.provides.map(decodePort);
  if (new Set(provides.map((port) => `${port.name}\u0000${port.exact_version}`)).size !== provides.length) {
    throw new Error("module summary contains duplicate provided Ports");
  }
  return {
    instance_id: value.instance_id,
    module_id: value.module_id,
    exact_version: value.exact_version,
    artifact_digest: value.artifact_digest,
    execution_class: value.execution_class as ModuleSummary["execution_class"],
    adapter_identity: value.adapter_identity,
    activation_revision: value.activation_revision,
    provides,
    visible_binding_count: value.visible_binding_count
  };
};

const bindingOrderKey = (binding: ModuleBindingSummary) => [
  binding.target.kind,
  binding.target.kind === "PROFILE" ? binding.target.profile_id : "",
  binding.target.kind === "WORKSPACE_CHANNEL_ENDPOINT"
    ? binding.target.workspace_id
    : "",
  binding.target.kind === "WORKSPACE_CHANNEL_ENDPOINT"
    ? binding.target.endpoint_id
    : "",
  binding.port.name,
  binding.port.exact_version
];

const compareBinding = (left: ModuleBindingSummary, right: ModuleBindingSummary) => {
  const leftKey = bindingOrderKey(left);
  const rightKey = bindingOrderKey(right);
  for (let index = 0; index < leftKey.length; index += 1) {
    const compared = compareUTF8(leftKey[index], rightKey[index]);
    if (compared !== 0) return compared;
  }
  return left.port_binding_index - right.port_binding_index;
};

const decodeModuleDetail = (value: unknown, scope: ControlScope): ModuleDetail => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["summary", "bindings"]) ||
    !Array.isArray(value.bindings) ||
    value.bindings.length > MAX_VISIBLE_BINDINGS
  ) {
    throw new Error("module detail is invalid");
  }
  const summary = decodeModuleSummary(value.summary);
  const bindings = value.bindings.map(decodeBinding);
  if (summary.visible_binding_count !== bindings.length) {
    throw new Error("module detail Binding count does not match its summary");
  }
  if (bindings.some((binding, index) => index > 0 && compareBinding(bindings[index - 1], binding) >= 0)) {
    throw new Error("module detail Bindings are not in canonical order");
  }
  for (const binding of bindings) {
    if (scope.kind === "WORKSPACE") {
      if (
        binding.target.kind !== "WORKSPACE_CHANNEL_ENDPOINT" ||
        binding.target.workspace_id !== scope.workspace_id
      ) {
        throw new Error("module detail contains a Binding outside the requested Workspace");
      }
    }
  }
  if (scope.kind === "WORKSPACE" && bindings.length === 0) {
    throw new Error("Workspace module detail has no visible Binding");
  }
  return { summary, bindings };
};

const scopeDigest = (scope: ControlScope) =>
  domainDigest("freeagent.control-scope/v1", canonicalJSONString(scope));

const basisPointerDigest = (basis: PublishedBasis) =>
  domainDigest(
    "freeagent.control-published-pointer-ref/v1",
    canonicalJSONString(basis)
  );

const validatePointerBasis = async (
  pointer: ExpectedResourceRef,
  basis: PublishedBasis,
  scope: ControlScope
) => {
  if (
    basis.tenant_id !== scope.tenant_id ||
    pointer.kind !== "PUBLISHED_POINTER" ||
    pointer.resource_id !== scope.tenant_id ||
    pointer.revision !== basis.pointer_revision ||
    pointer.digest !== (await basisPointerDigest(basis))
  ) {
    throw new Error("Published Pointer does not bind the exact Modules basis");
  }
};

type ModulesEnvelope = {
  publishedPointer: ExpectedResourceRef;
  basis: PublishedBasis;
  view: ControlView;
  viewSnapshotDigest: string;
  sourceRevision: number;
  sourceDigest: string;
};

const validateModulesEnvelope = async (
  envelope: ModulesEnvelope,
  requestedScope: ControlScope
) => {
  if (
    scopeKey(envelope.view.scope) !== scopeKey(requestedScope) ||
    !sameCanonical(envelope.view.basis, envelope.basis) ||
    envelope.sourceRevision !== envelope.basis.pointer_revision ||
    envelope.view.sections[0].source_revision !== envelope.sourceRevision ||
    envelope.view.sections[0].source_digest !== envelope.sourceDigest
  ) {
    throw new Error("Modules response is outside its requested scope, basis, or source");
  }
  const expectedScopeDigest = await scopeDigest(requestedScope);
  if (envelope.view.scope_digest !== expectedScopeDigest) {
    throw new Error("Modules view scope digest is invalid");
  }
  await validatePointerBasis(envelope.publishedPointer, envelope.basis, requestedScope);
  const viewDigest = await domainDigest(
    "freeagent.control-view-snapshot/v1",
    canonicalJSONString(envelope.view)
  );
  if (viewDigest !== envelope.viewSnapshotDigest) {
    throw new Error("Modules view snapshot digest is invalid");
  }
};

const decodePage = (text: string, scope: ControlScope): ModulesPageResponse => {
  const value = parseJSONRecord(text, "Modules page response");
  if (
    !exactKeys(
      value,
      [
        "schema_version",
        "published_pointer",
        "basis",
        "view",
        "view_snapshot_digest",
        "source_revision",
        "source_digest",
        "sort_version",
        "filter_digest",
        "items",
        "has_more",
        "projection_digest"
      ],
      ["next_cursor"]
    ) ||
    value.schema_version !== MODULES_PAGE_SCHEMA ||
    !digest(value.view_snapshot_digest) ||
    !safeInteger(value.source_revision, true) ||
    !digest(value.source_digest) ||
    value.sort_version !== MODULES_SORT_VERSION ||
    !digest(value.filter_digest) ||
    !Array.isArray(value.items) ||
    value.items.length > MODULES_PAGE_LIMIT ||
    typeof value.has_more !== "boolean" ||
    !digest(value.projection_digest)
  ) {
    throw new Error("Modules page response envelope is invalid");
  }
  const hasCursor = Object.hasOwn(value, "next_cursor");
  if (
    value.has_more !== hasCursor ||
    (hasCursor &&
      (typeof value.next_cursor !== "string" ||
        byteLength(value.next_cursor) > MAX_CURSOR_BYTES ||
        !RAW_URL_SEGMENT_PATTERN.test(value.next_cursor))) ||
    (value.has_more && value.items.length !== MODULES_PAGE_LIMIT)
  ) {
    throw new Error("Modules page continuation is invalid");
  }
  const items = value.items.map(decodeModuleSummary);
  if (items.some((item, index) => index > 0 && compareUTF8(items[index - 1].instance_id, item.instance_id) >= 0)) {
    throw new Error("Modules page items are not in canonical keyset order");
  }
  return {
    schema_version: MODULES_PAGE_SCHEMA,
    published_pointer: decodeExpectedRef(value.published_pointer),
    basis: decodeBasis(value.basis),
    view: decodeView(value.view),
    view_snapshot_digest: value.view_snapshot_digest,
    source_revision: value.source_revision,
    source_digest: value.source_digest,
    sort_version: MODULES_SORT_VERSION,
    filter_digest: value.filter_digest,
    items,
    has_more: value.has_more,
    ...(hasCursor ? { next_cursor: value.next_cursor as string } : {}),
    projection_digest: value.projection_digest
  };
};

const decodeDetail = (text: string, scope: ControlScope): ModuleDetailResponse => {
  const value = parseJSONRecord(text, "Module detail response");
  if (
    !exactKeys(value, [
      "schema_version",
      "published_pointer",
      "basis",
      "view",
      "view_snapshot_digest",
      "source_revision",
      "source_digest",
      "module",
      "projection_digest",
      "strong_etag"
    ]) ||
    value.schema_version !== MODULE_DETAIL_SCHEMA ||
    !digest(value.view_snapshot_digest) ||
    !safeInteger(value.source_revision, true) ||
    !digest(value.source_digest) ||
    !digest(value.projection_digest) ||
    !strongETag(value.strong_etag)
  ) {
    throw new Error("Module detail response envelope is invalid");
  }
  return {
    schema_version: MODULE_DETAIL_SCHEMA,
    published_pointer: decodeExpectedRef(value.published_pointer),
    basis: decodeBasis(value.basis),
    view: decodeView(value.view),
    view_snapshot_digest: value.view_snapshot_digest,
    source_revision: value.source_revision,
    source_digest: value.source_digest,
    module: decodeModuleDetail(value.module, scope),
    projection_digest: value.projection_digest,
    strong_etag: value.strong_etag
  };
};

type SessionIdentityProjection = Pick<
  ControlSession,
  "boot_id" | "principal_id" | "authorization_revision" | "scope_set_digest"
>;

type DecodedModulesCursor = SessionIdentityProjection & {
  schema_version: "control-modules-cursor/v1";
  scope: ControlScope;
  scope_digest: string;
  collection: "MODULES";
  filter_digest: string;
  sort_version: "control-modules-instance-id-binary/v1";
  source_revision: number;
  source_digest: string;
  view_snapshot_digest: string;
  observed_at_unix_micros: number;
  last_instance_id: string;
  position_digest: string;
};

type CursorBinding = {
  afterInstanceID: string;
  seenCount: number;
  sourceRevision: number;
  sourceDigest: string;
  viewSnapshotDigest: string;
  observedAtUnixMicros: number;
  basis: PublishedBasis;
  publishedPointer: ExpectedResourceRef;
};

type ReadCache<T> = { etag: string; data: T };
type PublishedBinding = {
  basis: PublishedBasis;
  pointer: ExpectedResourceRef;
};

const pageCache = new Map<string, ReadCache<ModulesPageResponse>>();
const detailCache = new Map<string, ReadCache<ModuleDetailResponse>>();
const cursorBindings = new Map<string, CursorBinding>();
const publishedBindings = new Map<string, PublishedBinding>();
const confirmationBindings = new Map<string, string>();

const detachJSON = <T>(value: T): T =>
  JSON.parse(JSON.stringify(value)) as T;

const putBounded = <K, V>(map: Map<K, V>, key: K, value: V, maximum: number) => {
  map.delete(key);
  map.set(key, value);
  while (map.size > maximum) {
    const oldest = map.keys().next();
    if (oldest.done) break;
    map.delete(oldest.value);
  }
};

const contextIdentity = (context: ModuleContext) => [
  context.origin,
  context.bootID,
  context.sessionID,
  context.principalID,
  context.authorizationRevision,
  context.scopeSetDigest,
  context.scope.kind,
  context.scope.tenant_id,
  context.scope.workspace_id ?? ""
];

const sessionIdentityProjection = (
  context: ModuleContext
): SessionIdentityProjection =>
  Object.fromEntries([
    ["boot_id", context.bootID],
    ["principal_id", context.principalID],
    ["authorization_revision", context.authorizationRevision],
    ["scope_set_digest", context.scopeSetDigest]
  ]) as SessionIdentityProjection;

const contextCacheKey = (context: ModuleContext) =>
  JSON.stringify(contextIdentity(context));

const cursorCacheKey = (context: ModuleContext, cursor: string) =>
  JSON.stringify([...contextIdentity(context), cursor]);

const confirmationCacheKey = (
  context: ModuleContext,
  idempotencyKeyDigest: string,
  inputDigest: string,
  evaluationDigest: string
) =>
  JSON.stringify([
    ...contextIdentity(context),
    idempotencyKeyDigest,
    inputDigest,
    evaluationDigest
  ]);

export const modulesQueryKey = (
  context: ModuleContext,
  cursor = ""
): ModulesQueryKey => [
  "modules",
  context.origin,
  context.bootID,
  context.sessionID,
  context.principalID,
  context.authorizationRevision,
  context.scopeSetDigest,
  context.scope.kind,
  context.scope.tenant_id,
  context.scope.workspace_id ?? "",
  cursor
];

export const moduleDetailQueryKey = (
  context: ModuleContext,
  instanceID: string
): ModuleDetailQueryKey => [
  "module-detail",
  context.origin,
  context.bootID,
  context.sessionID,
  context.principalID,
  context.authorizationRevision,
  context.scopeSetDigest,
  context.scope.kind,
  context.scope.tenant_id,
  context.scope.workspace_id ?? "",
  instanceID
];

export const clearModulesOperationCache = () => {
  publishedBindings.clear();
  confirmationBindings.clear();
};

export const clearModulesTransportCache = () => {
  pageCache.clear();
  detailCache.clear();
  cursorBindings.clear();
  clearModulesOperationCache();
};

export const withPublishedModulesBasis = (
  context: ModuleContext,
  source: Pick<ModulesPageResponse | ModuleDetailResponse, "published_pointer" | "basis">
): ModuleContext => ({
  ...context,
  publishedPointer: source.published_pointer,
  publishedBasis: source.basis
});

const validateContext = (context: ModuleContext) => {
  if (
    !exactLoopbackOrigin(context.origin) ||
    typeof window === "undefined" ||
    context.origin !== window.location.origin
  ) {
    throw new ControlModulesError(
      "Modules origin does not match this control page",
      0,
      "CONTROL_ORIGIN_MISMATCH"
    );
  }
  let decodedScope: ControlScope;
  try {
    decodedScope = decodeScope(context.scope);
  } catch {
    throw new ControlModulesError(
      "Modules context is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  if (
    !opaque(context.bootID) ||
    !opaque(context.sessionID) ||
    !opaque(context.principalID) ||
    !safeInteger(context.authorizationRevision, true) ||
    !digest(context.scopeSetDigest) ||
    !credential(context.csrfToken) ||
    !safeInteger(context.sessionEpoch, true) ||
    !sameCanonical(decodedScope, context.scope) ||
    (context.sessionExpiresAtUnixMicros !== undefined &&
      !safeInteger(context.sessionExpiresAtUnixMicros, true))
  ) {
    throw new ControlModulesError(
      "Modules context is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
};

const scopeHeaders = (context: ModuleContext) => {
  const headers: Record<string, string> = {
    "X-FreeAgent-CSRF": context.csrfToken,
    "X-FreeAgent-Scope-Kind": context.scope.kind,
    [CONTROL_SCOPE_ID_ENCODING_HEADER]: CONTROL_SCOPE_ID_ENCODING,
    "X-FreeAgent-Tenant-ID": encodeControlScopeID(context.scope.tenant_id)
  };
  if (context.scope.kind === "WORKSPACE") {
    headers["X-FreeAgent-Workspace-ID"] = encodeControlScopeID(
      context.scope.workspace_id ?? ""
    );
  }
  return headers;
};

const readBoundedText = async (response: Response, label: string) => {
  const declared = response.headers.get("Content-Length");
  if (declared !== null) {
    if (!/^(?:0|[1-9][0-9]*)$/u.test(declared)) {
      throw new Error(`${label} has an invalid Content-Length`);
    }
    const parsed = Number(declared);
    if (!Number.isSafeInteger(parsed) || parsed > MAX_RESPONSE_BYTES) {
      throw new Error(`${label} exceeds the 1 MiB limit`);
    }
  }
  if (response.body === null) {
    const text = await response.text();
    if (byteLength(text) > MAX_RESPONSE_BYTES) {
      throw new Error(`${label} exceeds the 1 MiB limit`);
    }
    return text;
  }
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const next = await reader.read();
      if (next.done) break;
      total += next.value.byteLength;
      if (total > MAX_RESPONSE_BYTES) {
        await reader.cancel();
        throw new Error(`${label} exceeds the 1 MiB limit`);
      }
      chunks.push(next.value);
    }
  } finally {
    reader.releaseLock();
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    throw new Error(`${label} is not valid UTF-8`);
  }
};

const fetchExact = async (
  url: string,
  init: RequestInit,
  fetcher: typeof fetch,
  outcomeMayBeCommitted: boolean
) => {
  let response: Response;
  try {
    response = await fetcher(url, init);
  } catch (error) {
    const cancelled = init.signal?.aborted === true;
    throw new ControlModulesError(
      cancelled
        ? "the Modules request was cancelled"
        : "the Modules request did not return a response",
      0,
      cancelled ? "CANCELLED" : "TRANSPORT_ERROR",
      "",
      0,
      outcomeMayBeCommitted
    );
  }
  if (
    response.redirected ||
    response.type === "opaqueredirect" ||
    (response.url !== "" && response.url !== url)
  ) {
    throw new ControlModulesError(
      "the Modules request crossed its exact same-origin URL",
      response.status,
      "INVALID_RESPONSE",
      "",
      0,
      outcomeMayBeCommitted
    );
  }
  return response;
};

const readControlError = async (
  response: Response,
  outcomeMayBeCommitted: boolean
) => {
  let text: string;
  try {
    if (response.headers.get("Content-Type") !== "application/json") {
      throw new Error("invalid content type");
    }
    text = await readBoundedText(response, "Modules error response");
  } catch {
    return new ControlModulesError(
      "the control service returned an invalid error response",
      response.status,
      "INVALID_ERROR_RESPONSE",
      "",
      0,
      outcomeMayBeCommitted && response.status >= 500
    );
  }
  try {
    rejectDuplicateObjectKeys(text, "Modules error response");
  } catch {
    return new ControlModulesError(
      "the control service returned an invalid error response",
      response.status,
      "INVALID_ERROR_RESPONSE",
      "",
      0,
      outcomeMayBeCommitted && response.status >= 500
    );
  }
  const decoded = decodeControlError(text);
  if (decoded === null) {
    return new ControlModulesError(
      "the control service returned an invalid error response",
      response.status,
      "INVALID_ERROR_RESPONSE",
      "",
      0,
      outcomeMayBeCommitted && response.status >= 500
    );
  }
  return new ControlModulesError(
    decoded.message,
    response.status,
    decoded.code,
    decoded.correlation_id,
    decoded.retry_after_seconds ?? 0,
    outcomeMayBeCommitted && response.status >= 500
  );
};

const requireJSONSuccess = async (
  response: Response,
  label: string,
  outcomeMayBeCommitted: boolean,
  allowETag: boolean
) => {
  if (!response.ok) throw await readControlError(response, outcomeMayBeCommitted);
  if (
    response.headers.get("Content-Type") !== "application/json" ||
    (!allowETag && response.headers.get("ETag") !== null)
  ) {
    throw new ControlModulesError(
      `${label} returned invalid response headers`,
      response.status,
      "INVALID_RESPONSE",
      "",
      0,
      outcomeMayBeCommitted
    );
  }
  try {
    return await readBoundedText(response, label);
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error ? error.message : `${label} is unreadable`,
      response.status,
      "INVALID_RESPONSE",
      "",
      0,
      outcomeMayBeCommitted
    );
  }
};

const strongETagWire = async (
  context: ModuleContext,
  projectionDigest: string,
  computedScopeDigest: string,
  domain: string
) =>
  `"${await domainDigest(
    domain,
    canonicalJSONString({
      ...sessionIdentityProjection(context),
      scope_digest: computedScopeDigest,
      projection_digest: projectionDigest
    })
  )}"`;

const httpStrongETag = async (
  domain: string,
  applicationETag: string,
  exactBody: string
) => `"${await domainDigest(domain, `${applicationETag}\n${exactBody}`)}"`;

const emptyFilterDigest = () =>
  domainDigest(
    "freeagent.control-modules-empty-filter/v1",
    canonicalJSONString({ filters: [] })
  );

const decodedCursor = async (
  context: ModuleContext,
  page: ModulesPageResponse
): Promise<DecodedModulesCursor> => {
  const lastInstanceID = page.items[page.items.length - 1]?.instance_id;
  if (!opaque(lastInstanceID)) throw new Error("Modules continuation has no last item");
  const positionDigest = await domainDigest(
    "freeagent.control-modules-position/v1",
    canonicalJSONString({
      sort_version: MODULES_SORT_VERSION,
      last_instance_id: lastInstanceID
    })
  );
  return {
    schema_version: MODULES_CURSOR_SCHEMA,
    ...sessionIdentityProjection(context),
    scope: context.scope,
    scope_digest: page.view.scope_digest,
    collection: "MODULES",
    filter_digest: page.filter_digest,
    sort_version: MODULES_SORT_VERSION,
    source_revision: page.source_revision,
    source_digest: page.source_digest,
    view_snapshot_digest: page.view_snapshot_digest,
    observed_at_unix_micros: page.view.observed_at_unix_micros,
    last_instance_id: lastInstanceID,
    position_digest: positionDigest
  };
};

const validatePageDigests = async (
  context: ModuleContext,
  page: ModulesPageResponse,
  rawBody: string,
  responseETag: string,
  inputCursor: string,
  cursorBinding: CursorBinding | undefined
) => {
  await validateModulesEnvelope(
    {
      publishedPointer: page.published_pointer,
      basis: page.basis,
      view: page.view,
      viewSnapshotDigest: page.view_snapshot_digest,
      sourceRevision: page.source_revision,
      sourceDigest: page.source_digest
    },
    context.scope
  );
  const expectedFilter = await emptyFilterDigest();
  if (page.filter_digest !== expectedFilter) {
    throw new Error("Modules page filter digest is invalid");
  }
  const sectionCount = page.view.sections[0].item_count;
  const seenCount = cursorBinding?.seenCount ?? 0;
  if (
    sectionCount < seenCount + page.items.length ||
    (page.has_more
      ? sectionCount <= seenCount + page.items.length
      : sectionCount !== seenCount + page.items.length)
  ) {
    throw new Error("Modules page cardinality does not match its source view");
  }
  if (cursorBinding !== undefined) {
    if (
      cursorBinding.sourceRevision !== page.source_revision ||
      cursorBinding.sourceDigest !== page.source_digest ||
      cursorBinding.viewSnapshotDigest !== page.view_snapshot_digest ||
      cursorBinding.observedAtUnixMicros !== page.view.observed_at_unix_micros ||
      !sameCanonical(cursorBinding.basis, page.basis) ||
      !sameCanonical(cursorBinding.publishedPointer, page.published_pointer) ||
      (page.items.length > 0 &&
        compareUTF8(page.items[0].instance_id, cursorBinding.afterInstanceID) <= 0)
    ) {
      throw new Error("Modules page cursor does not bind the exact source snapshot");
    }
  }
  const nextCursor = page.has_more ? await decodedCursor(context, page) : undefined;
  const projection = {
    schema_version: MODULES_APPLICATION_PAGE_SCHEMA,
    published_pointer: page.published_pointer,
    basis: page.basis,
    view: page.view,
    view_snapshot_digest: page.view_snapshot_digest,
    source_revision: page.source_revision,
    source_digest: page.source_digest,
    sort_version: page.sort_version,
    filter_digest: page.filter_digest,
    limit: MODULES_PAGE_LIMIT,
    ...(inputCursor === ""
      ? {}
      : { after_instance_id: cursorBinding?.afterInstanceID }),
    items: page.items,
    has_more: page.has_more,
    ...(nextCursor === undefined ? {} : { next_cursor: nextCursor })
  };
  const projectionDigest = await domainDigest(
    "freeagent.control-modules-page/v1",
    canonicalJSONString(projection)
  );
  if (projectionDigest !== page.projection_digest) {
    throw new Error("Modules page projection digest is invalid");
  }
  const applicationETag = await strongETagWire(
    context,
    projectionDigest,
    page.view.scope_digest,
    "freeagent.control-modules-page-etag/v1"
  );
  const expectedHTTPETag = await httpStrongETag(
    "freeagent.control-http-modules-page-etag/v1",
    applicationETag,
    rawBody
  );
  if (responseETag !== expectedHTTPETag) {
    throw new Error("Modules page HTTP strong ETag is invalid");
  }
  return { nextCursor, seenCount };
};

const validateDetailDigests = async (
  context: ModuleContext,
  detail: ModuleDetailResponse,
  instanceID: string,
  rawBody: string,
  responseETag: string
) => {
  await validateModulesEnvelope(
    {
      publishedPointer: detail.published_pointer,
      basis: detail.basis,
      view: detail.view,
      viewSnapshotDigest: detail.view_snapshot_digest,
      sourceRevision: detail.source_revision,
      sourceDigest: detail.source_digest
    },
    context.scope
  );
  if (
    detail.module.summary.instance_id !== instanceID ||
    detail.view.sections[0].item_count < 1
  ) {
    throw new Error("Module detail does not bind the requested instance");
  }
  const projectionDigest = await domainDigest(
    "freeagent.control-module-detail/v1",
    canonicalJSONString({
      schema_version: MODULE_DETAIL_SCHEMA,
      published_pointer: detail.published_pointer,
      basis: detail.basis,
      view: detail.view,
      view_snapshot_digest: detail.view_snapshot_digest,
      source_revision: detail.source_revision,
      source_digest: detail.source_digest,
      module: detail.module
    })
  );
  if (projectionDigest !== detail.projection_digest) {
    throw new Error("Module detail projection digest is invalid");
  }
  const applicationETag = await strongETagWire(
    context,
    projectionDigest,
    detail.view.scope_digest,
    "freeagent.control-module-detail-etag/v1"
  );
  if (detail.strong_etag !== applicationETag) {
    throw new Error("Module detail application strong ETag is invalid");
  }
  const expectedHTTPETag = await httpStrongETag(
    "freeagent.control-http-module-detail-etag/v1",
    applicationETag,
    rawBody
  );
  if (responseETag !== expectedHTTPETag) {
    throw new Error("Module detail HTTP strong ETag is invalid");
  }
};

const storePublishedBinding = (
  context: ModuleContext,
  basis: PublishedBasis,
  pointer: ExpectedResourceRef
) => {
  putBounded(
    publishedBindings,
    contextCacheKey(context),
    { basis: detachJSON(basis), pointer: detachJSON(pointer) },
    MAX_TRANSPORT_CACHE_ENTRIES
  );
};

export const fetchModulesPage = async (
  context: ModuleContext,
  cursor?: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<ModulesPageResponse> => {
  validateContext(context);
  const cursorValue = cursor ?? "";
  if (
    cursorValue !== "" &&
    (byteLength(cursorValue) > MAX_CURSOR_BYTES ||
      !RAW_URL_SEGMENT_PATTERN.test(cursorValue))
  ) {
    throw new ControlModulesError(
      "Modules cursor is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  const cursorBinding =
    cursorValue === ""
      ? undefined
      : cursorBindings.get(cursorCacheKey(context, cursorValue));
  if (cursorValue !== "" && cursorBinding === undefined) {
    throw new ControlModulesError(
      "Modules cursor is not bound to this live session and scope",
      0,
      "CURSOR_CONTEXT_MISSING"
    );
  }
  const queryKey = modulesQueryKey(context, cursorValue);
  const cacheKey = JSON.stringify(queryKey);
  const cached = pageCache.get(cacheKey);
  const headers = scopeHeaders(context);
  if (cached !== undefined) headers["If-None-Match"] = cached.etag;
  const query =
    cursorValue === ""
      ? `limit=${MODULES_PAGE_LIMIT}`
      : `limit=${MODULES_PAGE_LIMIT}&cursor=${cursorValue}`;
  const url = `${context.origin}${MODULES_PATH}?${query}`;
  const response = await fetchExact(
    url,
    {
      method: "GET",
      credentials: "include",
      redirect: "error",
      headers,
      signal
    },
    fetcher,
    false
  );
  if (response.status === 304) {
    if (cached === undefined) {
      throw new ControlModulesError(
        "Modules page returned 304 without a local representation",
        304,
        "INVALID_304"
      );
    }
    let body: string;
    try {
      body = await readBoundedText(response, "Modules page 304 response");
    } catch {
      body = "invalid";
    }
    if (
      response.headers.get("ETag") !== cached.etag ||
      response.headers.get("Content-Type") !== null ||
      body !== ""
    ) {
      throw new ControlModulesError(
        "Modules page returned a malformed 304 response",
        304,
        "INVALID_304"
      );
    }
    return detachJSON(cached.data);
  }
  const text = await requireJSONSuccess(response, "Modules page response", false, true);
  const etag = response.headers.get("ETag");
  if (!strongETag(etag)) {
    throw new ControlModulesError(
      "Modules page omitted its exact strong ETag",
      response.status,
      "INVALID_RESPONSE"
    );
  }
  let page: ModulesPageResponse;
  let validation: Awaited<ReturnType<typeof validatePageDigests>>;
  try {
    page = decodePage(text, context.scope);
    validation = await validatePageDigests(
      context,
      page,
      text,
      etag,
      cursorValue,
      cursorBinding
    );
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error ? error.message : "Modules page response is invalid",
      response.status,
      "INVALID_RESPONSE"
    );
  }
  putBounded(
    pageCache,
    cacheKey,
    { etag, data: detachJSON(page) },
    MAX_TRANSPORT_CACHE_ENTRIES
  );
  storePublishedBinding(context, page.basis, page.published_pointer);
  if (page.next_cursor !== undefined && validation.nextCursor !== undefined) {
    putBounded(
      cursorBindings,
      cursorCacheKey(context, page.next_cursor),
      {
        afterInstanceID: validation.nextCursor.last_instance_id,
        seenCount: validation.seenCount + page.items.length,
        sourceRevision: page.source_revision,
        sourceDigest: page.source_digest,
        viewSnapshotDigest: page.view_snapshot_digest,
        observedAtUnixMicros: page.view.observed_at_unix_micros,
        basis: detachJSON(page.basis),
        publishedPointer: detachJSON(page.published_pointer)
      },
      MAX_TRANSPORT_CACHE_ENTRIES
    );
  }
  return page;
};

const moduleInstancePathSegment = (value: string) => {
  if (!opaque(value)) return null;
  const bytes = utf8Bytes(value);
  const binary = [...bytes].map((byte) => String.fromCharCode(byte)).join("");
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/u, "");
};

export const fetchModuleDetail = async (
  context: ModuleContext,
  instanceID: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<ModuleDetailResponse> => {
  validateContext(context);
  const instanceSegment = moduleInstancePathSegment(instanceID);
  if (instanceSegment === null) {
    throw new ControlModulesError(
      "Module instance ID is not valid on the exact detail path",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  const key = moduleDetailQueryKey(context, instanceID);
  const cacheKey = JSON.stringify(key);
  const cached = detailCache.get(cacheKey);
  const headers = scopeHeaders(context);
  headers[MODULE_INSTANCE_ID_ENCODING_HEADER] = MODULE_INSTANCE_ID_ENCODING;
  if (cached !== undefined) headers["If-None-Match"] = cached.etag;
  const url = `${context.origin}${MODULES_PATH}/${instanceSegment}`;
  const response = await fetchExact(
    url,
    {
      method: "GET",
      credentials: "include",
      redirect: "error",
      headers,
      signal
    },
    fetcher,
    false
  );
  if (response.status === 304) {
    if (cached === undefined) {
      throw new ControlModulesError(
        "Module detail returned 304 without a local representation",
        304,
        "INVALID_304"
      );
    }
    let body: string;
    try {
      body = await readBoundedText(response, "Module detail 304 response");
    } catch {
      body = "invalid";
    }
    if (
      response.headers.get("ETag") !== cached.etag ||
      response.headers.get("Content-Type") !== null ||
      body !== ""
    ) {
      throw new ControlModulesError(
        "Module detail returned a malformed 304 response",
        304,
        "INVALID_304"
      );
    }
    return detachJSON(cached.data);
  }
  const text = await requireJSONSuccess(response, "Module detail response", false, true);
  const etag = response.headers.get("ETag");
  if (!strongETag(etag)) {
    throw new ControlModulesError(
      "Module detail omitted its exact strong ETag",
      response.status,
      "INVALID_RESPONSE"
    );
  }
  let detail: ModuleDetailResponse;
  try {
    detail = decodeDetail(text, context.scope);
    await validateDetailDigests(context, detail, instanceID, text, etag);
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error ? error.message : "Module detail response is invalid",
      response.status,
      "INVALID_RESPONSE"
    );
  }
  putBounded(
    detailCache,
    cacheKey,
    { etag, data: detachJSON(detail) },
    MAX_TRANSPORT_CACHE_ENTRIES
  );
  storePublishedBinding(context, detail.basis, detail.published_pointer);
  return detail;
};

const validateDisableBody = (body: ModuleDisableBody): ModuleDisableBody => {
  if (
    !isRecord(body) ||
    !exactKeys(body, [
      "schema_version",
      "expected_pointer_revision",
      "binding_target",
      "instance_id",
      "port"
    ]) ||
    body.schema_version !== MODULE_DISABLE_BODY_SCHEMA ||
    !safeInteger(body.expected_pointer_revision, true) ||
    !opaque(body.instance_id) ||
    !isRecord(body.binding_target) ||
    !exactKeys(body.binding_target, ["kind", "profile_id"]) ||
    body.binding_target.kind !== "PROFILE" ||
    !opaque(body.binding_target.profile_id) ||
    !isRecord(body.port) ||
    !exactKeys(body.port, ["name", "exact_version"]) ||
    body.port.name !== "context.provide" ||
    body.port.exact_version !== "v1"
  ) {
    throw new ControlModulesError(
      "MODULE_DISABLE body is outside the narrow Profile context.provide/v1 contract",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  return body;
};

export const canonicalModuleDisableBody = (body: ModuleDisableBody) =>
  canonicalJSONString(validateDisableBody(body));

export const createIdempotencyKey = () => {
  let bytes: Uint8Array;
  try {
    bytes = crypto.getRandomValues(new Uint8Array(32));
  } catch {
    throw new ControlModulesError(
      "secure randomness is unavailable for the idempotency key",
      0,
      "SECURE_RANDOM_UNAVAILABLE"
    );
  }
  try {
    const key = [...bytes]
      .map((byte) => byte.toString(16).padStart(2, "0"))
      .join("");
    if (key.length !== 64 || !/^[0-9a-f]{64}$/u.test(key)) {
      throw new ControlModulesError(
        "secure idempotency key generation failed closed",
        0,
        "SECURE_RANDOM_UNAVAILABLE"
      );
    }
    return key;
  } finally {
    bytes.fill(0);
  }
};

const validateIdempotencyKey = (key: string) => {
  if (
    typeof key !== "string" ||
    byteLength(key) < 16 ||
    byteLength(key) > 128 ||
    !/^[!-~]+$/u.test(key)
  ) {
    throw new ControlModulesError(
      "Idempotency-Key must contain 16-128 non-space printable ASCII bytes",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
};

const inputDigestFor = (canonicalBody: string) =>
  domainDigest(
    "freeagent.control-module-disable-dry-run-input/v1",
    canonicalBody
  );

const idempotencyDigestFor = (key: string) =>
  domainDigest("freeagent.control-idempotency-key/v1", key);

const planDigestFor = (context: ModuleContext, body: ModuleDisableBody) =>
  domainDigest(
    "freeagent.module-apply-plan/v1",
    canonicalJSONString({
      schema_version: "module-apply-plan/v1",
      desired_state: "DISABLED",
      tenant_id: context.scope.tenant_id,
      expected_pointer_revision: body.expected_pointer_revision,
      binding_target: body.binding_target,
      instance_id: body.instance_id,
      port: body.port
    })
  );

const resolveMutationBasis = async (
  context: ModuleContext,
  body: ModuleDisableBody
): Promise<PublishedBinding> => {
  if (context.scope.kind !== "TENANT") {
    throw new ControlModulesError(
      "MODULE_DISABLE requires exact Tenant scope",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  let binding: PublishedBinding | undefined;
  if (context.publishedPointer !== undefined || context.publishedBasis !== undefined) {
    if (context.publishedPointer === undefined || context.publishedBasis === undefined) {
      throw new ControlModulesError(
        "MODULE_DISABLE requires the Published Pointer and its full basis together",
        0,
        "INVALID_CLIENT_INPUT"
      );
    }
    try {
      binding = {
        pointer: decodeExpectedRef(context.publishedPointer),
        basis: decodeBasis(context.publishedBasis)
      };
    } catch {
      throw new ControlModulesError(
        "MODULE_DISABLE Published Pointer basis is invalid",
        0,
        "INVALID_CLIENT_INPUT"
      );
    }
  } else {
    binding = publishedBindings.get(contextCacheKey(context));
  }
  if (binding === undefined) {
    throw new ControlModulesError(
      "MODULE_DISABLE requires a freshly validated Modules Published Pointer basis",
      0,
      "PUBLISHED_BASIS_REQUIRED"
    );
  }
  try {
    await validatePointerBasis(binding.pointer, binding.basis, context.scope);
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error ? error.message : "Published Pointer basis is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  if (binding.pointer.revision !== body.expected_pointer_revision) {
    throw new ControlModulesError(
      "MODULE_DISABLE body revision does not match its exact Published Pointer",
      0,
      "REVISION_CONFLICT"
    );
  }
  return binding;
};

const sameRef = (left: ExpectedResourceRef, right: ExpectedResourceRef) =>
  left.kind === right.kind &&
  left.resource_id === right.resource_id &&
  left.revision === right.revision &&
  left.digest === right.digest;

const nextBasis = (before: PublishedBasis, after: PublishedBasis) =>
  before.tenant_id === after.tenant_id &&
  after.pointer_revision === before.pointer_revision + 1 &&
  after.control.revision === before.control.revision + 1 &&
  after.catalog.revision === before.catalog.revision + 1 &&
  after.control.id !== before.control.id &&
  after.control.digest !== before.control.digest &&
  after.catalog.id !== before.catalog.id &&
  after.catalog.digest !== before.catalog.digest;

const decodeDisableProjection = (value: unknown): ModuleDisableProjection => {
  if (
    !isRecord(value) ||
    !exactKeys(
      value,
      [
        "disposition",
        "plan_digest",
        "instance_id",
        "precondition_basis",
        "observed_basis",
        "candidate_basis",
        "candidate_state",
        "catalog_change"
      ],
      ["binding_removal"]
    ) ||
    (value.disposition !== "ALREADY_APPLIED" &&
      value.disposition !== "NO_CHANGE" &&
      value.disposition !== "WOULD_APPLY") ||
    !digest(value.plan_digest) ||
    !opaque(value.instance_id) ||
    value.candidate_state !== "PROJECTED_NOT_RESERVED" ||
    (value.catalog_change !== "NONE" &&
      value.catalog_change !== "RETAIN_INSTANCE" &&
      value.catalog_change !== "REMOVE_INSTANCE")
  ) {
    throw new Error("MODULE_DISABLE projection is invalid");
  }
  let removal: ModuleDisableBindingRemoval | undefined;
  if (Object.hasOwn(value, "binding_removal")) {
    const decoded = decodeBinding(value.binding_removal);
    if (
      decoded.target.kind !== "PROFILE" ||
      decoded.port.name !== "context.provide" ||
      decoded.port.exact_version !== "v1" ||
      decoded.failure_policy !== "OPTIONAL"
    ) {
      throw new Error("MODULE_DISABLE Binding removal is outside the narrow contract");
    }
    removal = decoded as ModuleDisableBindingRemoval;
  }
  return {
    disposition: value.disposition,
    plan_digest: value.plan_digest,
    instance_id: value.instance_id,
    precondition_basis: decodeBasis(value.precondition_basis),
    observed_basis: decodeBasis(value.observed_basis),
    candidate_basis: decodeBasis(value.candidate_basis),
    candidate_state: "PROJECTED_NOT_RESERVED",
    ...(removal === undefined ? {} : { binding_removal: removal }),
    catalog_change: value.catalog_change
  };
};

const validateDisableProjection = async (
  context: ModuleContext,
  projection: ModuleDisableProjection,
  body: ModuleDisableBody,
  expected: ExpectedResourceRef
) => {
  if (
    projection.plan_digest !== (await planDigestFor(context, body)) ||
    projection.instance_id !== body.instance_id ||
    projection.precondition_basis.tenant_id !== context.scope.tenant_id ||
    projection.observed_basis.tenant_id !== context.scope.tenant_id ||
    projection.candidate_basis.tenant_id !== context.scope.tenant_id
  ) {
    throw new Error("MODULE_DISABLE projection does not bind its exact input");
  }
  const preconditionRef: ExpectedResourceRef = {
    kind: "PUBLISHED_POINTER",
    resource_id: projection.precondition_basis.tenant_id,
    revision: projection.precondition_basis.pointer_revision,
    digest: await basisPointerDigest(projection.precondition_basis)
  };
  if (!sameRef(preconditionRef, expected)) {
    throw new Error("MODULE_DISABLE projection precondition is invalid");
  }
  const removal = projection.binding_removal;
  switch (projection.disposition) {
    case "NO_CHANGE":
      if (
        !sameCanonical(projection.precondition_basis, projection.observed_basis) ||
        !sameCanonical(projection.observed_basis, projection.candidate_basis) ||
        projection.catalog_change !== "NONE" ||
        removal !== undefined
      ) {
        throw new Error("MODULE_DISABLE NO_CHANGE projection is invalid");
      }
      break;
    case "ALREADY_APPLIED":
      if (
        !nextBasis(projection.precondition_basis, projection.observed_basis) ||
        !sameCanonical(projection.observed_basis, projection.candidate_basis) ||
        projection.catalog_change !== "NONE" ||
        removal !== undefined
      ) {
        throw new Error("MODULE_DISABLE ALREADY_APPLIED projection is invalid");
      }
      break;
    case "WOULD_APPLY":
      if (
        !sameCanonical(projection.precondition_basis, projection.observed_basis) ||
        !nextBasis(projection.observed_basis, projection.candidate_basis) ||
        projection.catalog_change === "NONE" ||
        removal === undefined ||
        !sameCanonical(removal.target, body.binding_target) ||
        !sameCanonical(removal.port, body.port)
      ) {
        throw new Error("MODULE_DISABLE WOULD_APPLY projection is invalid");
      }
      break;
  }
};

type OperationInputs = {
  intent: "DRY_RUN" | "MUTATE";
  inputDigest: string;
  expected: ExpectedResourceRef;
  idempotencyKeyDigest?: string;
  evaluationDigest?: string;
};

const confirmationFromRequest = (
  request: ControlOperationRequest
): ControlConfirmationStatement => ({
  schema_version: CONFIRMATION_STATEMENT_SCHEMA,
  principal_id: request.principal_id,
  capability: "OPERATE_MODULES",
  intent: "MUTATE",
  operation: "MODULE_DISABLE",
  scope: request.scope,
  scope_digest: request.scope_digest,
  idempotency_key_digest: request.idempotency_key_digest ?? "",
  input_digest: request.input_digest,
  operation_evaluation_digest: request.operation_evaluation_digest ?? "",
  expected_ref: request.expected_ref
});

const decodeOperationRequest = async (
  value: unknown,
  context: ModuleContext,
  inputs: OperationInputs
) => {
  if (!isRecord(value)) throw new Error("Control operation Request is invalid");
  const commonKeys = [
    "schema_version",
    "principal_id",
    "capability",
    "scope",
    "scope_digest",
    "operation",
    "intent",
    "input_digest",
    "expected_ref"
  ];
  const mutationKeys = [
    "idempotency_key_digest",
    "operation_evaluation_digest",
    "confirmation_digest"
  ];
  if (
    !exactKeys(value, inputs.intent === "MUTATE" ? [...commonKeys, ...mutationKeys] : commonKeys) ||
    value.schema_version !== OPERATION_REQUEST_SCHEMA ||
    value.principal_id !== context.principalID ||
    value.capability !== "OPERATE_MODULES" ||
    value.operation !== "MODULE_DISABLE" ||
    value.intent !== inputs.intent ||
    !digest(value.scope_digest) ||
    value.input_digest !== inputs.inputDigest
  ) {
    throw new Error("Control operation Request is outside its exact call input");
  }
  const scope = decodeScope(value.scope);
  const expected = decodeExpectedRef(value.expected_ref);
  if (
    scopeKey(scope) !== scopeKey(context.scope) ||
    value.scope_digest !== (await scopeDigest(context.scope)) ||
    !sameRef(expected, inputs.expected)
  ) {
    throw new Error("Control operation Request scope or precondition is invalid");
  }
  const request: ControlOperationRequest = {
    schema_version: OPERATION_REQUEST_SCHEMA,
    principal_id: value.principal_id,
    capability: "OPERATE_MODULES",
    scope,
    scope_digest: value.scope_digest,
    operation: "MODULE_DISABLE",
    intent: inputs.intent,
    ...(inputs.intent === "MUTATE"
      ? {
          idempotency_key_digest: value.idempotency_key_digest as string
        }
      : {}),
    input_digest: value.input_digest,
    ...(inputs.intent === "MUTATE"
      ? {
          operation_evaluation_digest:
            value.operation_evaluation_digest as string
        }
      : {}),
    expected_ref: expected,
    ...(inputs.intent === "MUTATE"
      ? { confirmation_digest: value.confirmation_digest as string }
      : {})
  };
  if (inputs.intent === "MUTATE") {
    if (
      value.idempotency_key_digest !== inputs.idempotencyKeyDigest ||
      value.operation_evaluation_digest !== inputs.evaluationDigest ||
      !digest(value.confirmation_digest)
    ) {
      throw new Error("Control mutation Request digests do not bind the call");
    }
    const confirmationDigest = await domainDigest(
      "freeagent.control-confirmation-statement/v1",
      canonicalJSONString(confirmationFromRequest(request))
    );
    if (confirmationDigest !== request.confirmation_digest) {
      throw new Error("Control mutation Request confirmation digest is invalid");
    }
  }
  const requestDigest = await domainDigest(
    "freeagent.control-operation-request/v1",
    canonicalJSONString(request)
  );
  return { request, requestDigest };
};

const decodeDomainReceipt = (value: unknown): DomainReceiptRef => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["kind", "id", "digest"]) ||
    value.kind !== "MODULE_DISABLE" ||
    !opaque(value.id) ||
    !digest(value.digest)
  ) {
    throw new Error("MODULE_DISABLE domain receipt reference is invalid");
  }
  return value as DomainReceiptRef;
};

const decodeOperationReceipt = async (
  value: unknown,
  request: ControlOperationRequest,
  requestDigest: string,
  expected: ExpectedResourceRef,
  idempotencyKeyDigest?: string
) => {
  if (!isRecord(value)) throw new Error("Control operation Receipt is invalid");
  const dryRun = request.intent === "DRY_RUN";
  const base = [
    "schema_version",
    "request_digest",
    "intent",
    "principal_id",
    "scope_digest",
    "operation",
    "status",
    "error_code",
    "pre_ref",
    "replay_disposition",
    "completed_at_unix_micros"
  ];
  const mutationRequired = [...base, "idempotency_key_digest", "post_ref"];
  const mutationOptional = ["domain_receipt"];
  if (
    !exactKeys(value, dryRun ? base : mutationRequired, dryRun ? [] : mutationOptional) ||
    value.schema_version !== OPERATION_RECEIPT_SCHEMA ||
    value.request_digest !== requestDigest ||
    value.intent !== request.intent ||
    value.principal_id !== request.principal_id ||
    value.scope_digest !== request.scope_digest ||
    value.operation !== "MODULE_DISABLE" ||
    value.error_code !== "NONE" ||
    !safeInteger(value.completed_at_unix_micros, true)
  ) {
    throw new Error("Control operation Receipt does not bind its Request");
  }
  const preRef = decodeExpectedRef(value.pre_ref);
  if (!sameRef(preRef, expected)) {
    throw new Error("Control operation Receipt precondition is invalid");
  }
  let receipt: ControlOperationReceipt;
  if (dryRun) {
    if (value.status !== "DRY_RUN" || value.replay_disposition !== "NO_RETRY") {
      throw new Error("MODULE_DISABLE Dry-run Receipt outcome is invalid");
    }
    receipt = {
      schema_version: OPERATION_RECEIPT_SCHEMA,
      request_digest: requestDigest,
      intent: "DRY_RUN",
      principal_id: request.principal_id,
      scope_digest: request.scope_digest,
      operation: "MODULE_DISABLE",
      status: "DRY_RUN",
      error_code: "NONE",
      pre_ref: preRef,
      replay_disposition: "NO_RETRY",
      completed_at_unix_micros: value.completed_at_unix_micros
    };
  } else {
    if (
      value.idempotency_key_digest !== idempotencyKeyDigest ||
      (value.status !== "NO_CHANGE" && value.status !== "APPLIED") ||
      value.replay_disposition !== "RETURN_EXACT_RECEIPT"
    ) {
      throw new Error("MODULE_DISABLE mutation Receipt outcome is invalid");
    }
    const postRef = decodeExpectedRef(value.post_ref);
    let domainReceipt: DomainReceiptRef | undefined;
    if (value.status === "NO_CHANGE") {
      if (!sameRef(postRef, preRef) || Object.hasOwn(value, "domain_receipt")) {
        throw new Error("MODULE_DISABLE NO_CHANGE Receipt is invalid");
      }
    } else {
      if (
        postRef.kind !== preRef.kind ||
        postRef.resource_id !== preRef.resource_id ||
        postRef.revision !== preRef.revision + 1 ||
        postRef.digest === preRef.digest ||
        !Object.hasOwn(value, "domain_receipt")
      ) {
        throw new Error("MODULE_DISABLE APPLIED transition is invalid");
      }
      domainReceipt = decodeDomainReceipt(value.domain_receipt);
    }
    receipt = {
      schema_version: OPERATION_RECEIPT_SCHEMA,
      request_digest: requestDigest,
      intent: "MUTATE",
      idempotency_key_digest: idempotencyKeyDigest,
      principal_id: request.principal_id,
      scope_digest: request.scope_digest,
      operation: "MODULE_DISABLE",
      status: value.status,
      error_code: "NONE",
      pre_ref: preRef,
      post_ref: postRef,
      ...(domainReceipt === undefined ? {} : { domain_receipt: domainReceipt }),
      replay_disposition: "RETURN_EXACT_RECEIPT",
      completed_at_unix_micros: value.completed_at_unix_micros
    };
  }
  const receiptDigest = await domainDigest(
    "freeagent.control-operation-receipt/v1",
    canonicalJSONString(receipt)
  );
  return { receipt, receiptDigest };
};

const decodeEvaluation = async (
  value: unknown,
  context: ModuleContext,
  body: ModuleDisableBody,
  inputDigest: string,
  expected: ExpectedResourceRef
) => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "schema_version",
      "operation",
      "input_digest",
      "expected_ref",
      "projection"
    ]) ||
    value.schema_version !== MODULE_DISABLE_EVALUATION_SCHEMA ||
    value.operation !== "MODULE_DISABLE" ||
    value.input_digest !== inputDigest
  ) {
    throw new Error("MODULE_DISABLE evaluation is invalid");
  }
  const expectedRef = decodeExpectedRef(value.expected_ref);
  if (!sameRef(expectedRef, expected)) {
    throw new Error("MODULE_DISABLE evaluation precondition is invalid");
  }
  const projection = decodeDisableProjection(value.projection);
  await validateDisableProjection(context, projection, body, expected);
  const evaluation: ModuleDisableEvaluation = {
    schema_version: MODULE_DISABLE_EVALUATION_SCHEMA,
    operation: "MODULE_DISABLE",
    input_digest: inputDigest,
    expected_ref: expectedRef,
    projection
  };
  const evaluationDigest = await domainDigest(
    "freeagent.control-module-disable-evaluation/v1",
    canonicalJSONString(evaluation)
  );
  return { evaluation, evaluationDigest };
};

const decodeStatement = async (
  value: unknown,
  request: ControlOperationRequest
) => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "schema_version",
      "principal_id",
      "capability",
      "intent",
      "operation",
      "scope",
      "scope_digest",
      "idempotency_key_digest",
      "input_digest",
      "operation_evaluation_digest",
      "expected_ref"
    ])
  ) {
    throw new Error("Control confirmation Statement is invalid");
  }
  const expected = confirmationFromRequest(request);
  const statement: ControlConfirmationStatement = {
    schema_version:
      value.schema_version as ControlConfirmationStatement["schema_version"],
    principal_id: value.principal_id as string,
    capability: value.capability as ControlConfirmationStatement["capability"],
    intent: value.intent as ControlConfirmationStatement["intent"],
    operation: value.operation as ControlConfirmationStatement["operation"],
    scope: decodeScope(value.scope),
    scope_digest: value.scope_digest as string,
    idempotency_key_digest: value.idempotency_key_digest as string,
    input_digest: value.input_digest as string,
    operation_evaluation_digest: value.operation_evaluation_digest as string,
    expected_ref: decodeExpectedRef(value.expected_ref)
  };
  if (!sameCanonical(statement, expected)) {
    throw new Error("Control confirmation Statement does not bind its Request");
  }
  const statementDigest = await domainDigest(
    "freeagent.control-confirmation-statement/v1",
    canonicalJSONString(statement)
  );
  return { statement, statementDigest };
};

const decodeDryRunResult = async (
  text: string,
  context: ModuleContext,
  body: ModuleDisableBody,
  inputDigest: string,
  expected: ExpectedResourceRef
) => {
  const value = parseJSONRecord(text, "MODULE_DISABLE Dry-run response");
  if (
    !exactKeys(value, [
      "schema_version",
      "request",
      "request_digest",
      "receipt",
      "receipt_digest",
      "projection"
    ]) ||
    value.schema_version !== MODULE_DISABLE_DRY_RUN_RESULT_SCHEMA ||
    !digest(value.request_digest) ||
    !digest(value.receipt_digest)
  ) {
    throw new Error("MODULE_DISABLE Dry-run response envelope is invalid");
  }
  const { request, requestDigest } = await decodeOperationRequest(
    value.request,
    context,
    { intent: "DRY_RUN", inputDigest, expected }
  );
  if (value.request_digest !== requestDigest) {
    throw new Error("MODULE_DISABLE Dry-run Request digest is invalid");
  }
  const { receipt, receiptDigest } = await decodeOperationReceipt(
    value.receipt,
    request,
    requestDigest,
    expected
  );
  if (value.receipt_digest !== receiptDigest) {
    throw new Error("MODULE_DISABLE Dry-run Receipt digest is invalid");
  }
  const projection = decodeDisableProjection(value.projection);
  await validateDisableProjection(context, projection, body, expected);
  return {
    schema_version: MODULE_DISABLE_DRY_RUN_RESULT_SCHEMA,
    request,
    request_digest: requestDigest,
    receipt,
    receipt_digest: receiptDigest,
    projection
  } satisfies ModuleDisableDryRunResult;
};

const decodeConfirmationResult = async (
  text: string,
  context: ModuleContext,
  body: ModuleDisableBody,
  inputDigest: string,
  expected: ExpectedResourceRef,
  idempotencyKeyDigest: string
) => {
  const value = parseJSONRecord(text, "MODULE_DISABLE confirmation response");
  if (
    !exactKeys(value, [
      "schema_version",
      "request",
      "request_digest",
      "evaluation",
      "evaluation_digest",
      "statement",
      "statement_digest",
      "confirmation_proof",
      "expires_at_unix_micros"
    ]) ||
    value.schema_version !== MODULE_DISABLE_CONFIRMATION_RESULT_SCHEMA ||
    !digest(value.request_digest) ||
    !digest(value.evaluation_digest) ||
    !digest(value.statement_digest) ||
    !credential(value.confirmation_proof) ||
    !safeInteger(value.expires_at_unix_micros, true)
  ) {
    throw new Error("MODULE_DISABLE confirmation response envelope is invalid");
  }
  const { evaluation, evaluationDigest } = await decodeEvaluation(
    value.evaluation,
    context,
    body,
    inputDigest,
    expected
  );
  if (value.evaluation_digest !== evaluationDigest) {
    throw new Error("MODULE_DISABLE confirmation evaluation digest is invalid");
  }
  const { request, requestDigest } = await decodeOperationRequest(
    value.request,
    context,
    {
      intent: "MUTATE",
      inputDigest,
      expected,
      idempotencyKeyDigest,
      evaluationDigest
    }
  );
  if (value.request_digest !== requestDigest) {
    throw new Error("MODULE_DISABLE confirmation Request digest is invalid");
  }
  const { statement, statementDigest } = await decodeStatement(value.statement, request);
  if (
    value.statement_digest !== statementDigest ||
    request.confirmation_digest !== statementDigest
  ) {
    throw new Error("MODULE_DISABLE confirmation Statement digest is invalid");
  }
  const nowMicros = Date.now() * 1000;
  if (value.expires_at_unix_micros <= nowMicros) {
    throw new Error("MODULE_DISABLE confirmation is already expired");
  }
  if (value.expires_at_unix_micros > nowMicros + 125_000_000) {
    throw new Error("MODULE_DISABLE confirmation expiry exceeds its two-minute bound");
  }
  if (
    context.sessionExpiresAtUnixMicros !== undefined &&
    value.expires_at_unix_micros > context.sessionExpiresAtUnixMicros
  ) {
    throw new Error("MODULE_DISABLE confirmation outlives its session");
  }
  return {
    schema_version: MODULE_DISABLE_CONFIRMATION_RESULT_SCHEMA,
    request,
    request_digest: requestDigest,
    evaluation,
    evaluation_digest: evaluationDigest,
    statement,
    statement_digest: statementDigest,
    confirmation_proof: value.confirmation_proof,
    expires_at_unix_micros: value.expires_at_unix_micros
  } satisfies ModuleDisableConfirmationResult;
};

const decodeMutationResult = async (
  text: string,
  context: ModuleContext,
  inputDigest: string,
  expected: ExpectedResourceRef,
  idempotencyKeyDigest: string,
  evaluationDigest: string
) => {
  const value = parseJSONRecord(text, "MODULE_DISABLE mutation response");
  if (
    !exactKeys(value, [
      "schema_version",
      "request",
      "request_digest",
      "receipt",
      "receipt_digest"
    ]) ||
    value.schema_version !== MODULE_DISABLE_MUTATION_RESULT_SCHEMA ||
    !digest(value.request_digest) ||
    !digest(value.receipt_digest)
  ) {
    throw new Error("MODULE_DISABLE mutation response envelope is invalid");
  }
  const { request, requestDigest } = await decodeOperationRequest(
    value.request,
    context,
    {
      intent: "MUTATE",
      inputDigest,
      expected,
      idempotencyKeyDigest,
      evaluationDigest
    }
  );
  if (value.request_digest !== requestDigest) {
    throw new Error("MODULE_DISABLE mutation Request digest is invalid");
  }
  const { receipt, receiptDigest } = await decodeOperationReceipt(
    value.receipt,
    request,
    requestDigest,
    expected,
    idempotencyKeyDigest
  );
  if (value.receipt_digest !== receiptDigest) {
    throw new Error("MODULE_DISABLE mutation Receipt digest is invalid");
  }
  return {
    schema_version: MODULE_DISABLE_MUTATION_RESULT_SCHEMA,
    request,
    request_digest: requestDigest,
    receipt,
    receipt_digest: receiptDigest
  } satisfies ModuleDisableMutationResult;
};

type OperationPreparation = {
  canonicalBody: string;
  inputDigest: string;
  expected: ExpectedResourceRef;
  headers: Record<string, string>;
};

const prepareOperation = async (
  context: ModuleContext,
  body: ModuleDisableBody
): Promise<OperationPreparation> => {
  validateContext(context);
  const canonicalBody = canonicalModuleDisableBody(body);
  if (byteLength(canonicalBody) > MAX_RESPONSE_BYTES) {
    throw new ControlModulesError(
      "MODULE_DISABLE body exceeds the 1 MiB limit",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  const binding = await resolveMutationBasis(context, body);
  return {
    canonicalBody,
    inputDigest: await inputDigestFor(canonicalBody),
    expected: binding.pointer,
    headers: {
      ...scopeHeaders(context),
      "Content-Type": "application/json",
      "If-Match": `"${binding.pointer.digest}"`
    }
  };
};

export const dryRunModuleDisable = async (
  context: ModuleContext,
  body: ModuleDisableBody,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<ModuleDisableDryRunResult> => {
  const prepared = await prepareOperation(context, body);
  const url = `${context.origin}${MODULE_DISABLE_DRY_RUN_PATH}`;
  const response = await fetchExact(
    url,
    {
      method: "POST",
      credentials: "include",
      redirect: "error",
      headers: prepared.headers,
      body: prepared.canonicalBody,
      signal
    },
    fetcher,
    false
  );
  const text = await requireJSONSuccess(
    response,
    "MODULE_DISABLE Dry-run response",
    false,
    false
  );
  try {
    return await decodeDryRunResult(
      text,
      context,
      body,
      prepared.inputDigest,
      prepared.expected
    );
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error ? error.message : "MODULE_DISABLE Dry-run response is invalid",
      response.status,
      "INVALID_RESPONSE"
    );
  }
};

export const issueModuleDisableConfirmation = async (
  context: ModuleContext,
  body: ModuleDisableBody,
  key: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<ModuleDisableConfirmationResult> => {
  validateIdempotencyKey(key);
  const prepared = await prepareOperation(context, body);
  const idempotencyKeyDigest = await idempotencyDigestFor(key);
  const url = `${context.origin}${MODULE_DISABLE_CONFIRMATION_PATH}`;
  const response = await fetchExact(
    url,
    {
      method: "POST",
      credentials: "include",
      redirect: "error",
      headers: {
        ...prepared.headers,
        "Idempotency-Key": key
      },
      body: prepared.canonicalBody,
      signal
    },
    fetcher,
    false
  );
  const text = await requireJSONSuccess(
    response,
    "MODULE_DISABLE confirmation response",
    false,
    false
  );
  let result: ModuleDisableConfirmationResult;
  try {
    result = await decodeConfirmationResult(
      text,
      context,
      body,
      prepared.inputDigest,
      prepared.expected,
      idempotencyKeyDigest
    );
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error
        ? error.message
        : "MODULE_DISABLE confirmation response is invalid",
      response.status,
      "INVALID_RESPONSE"
    );
  }
  putBounded(
    confirmationBindings,
    confirmationCacheKey(
      context,
      idempotencyKeyDigest,
      prepared.inputDigest,
      result.evaluation_digest
    ),
    result.statement_digest,
    MAX_CONFIRMATION_CACHE_ENTRIES
  );
  return result;
};

export const mutateModuleDisable = async (
  context: ModuleContext,
  body: ModuleDisableBody,
  key: string,
  evaluationDigest: string,
  proof?: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<ModuleDisableMutationResult> => {
  validateIdempotencyKey(key);
  if (!digest(evaluationDigest) || (proof !== undefined && !credential(proof))) {
    throw new ControlModulesError(
      "MODULE_DISABLE mutation evaluation digest or proof is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  const prepared = await prepareOperation(context, body);
  const idempotencyKeyDigest = await idempotencyDigestFor(key);
  if (
    proof !== undefined &&
    !confirmationBindings.has(
      confirmationCacheKey(
        context,
        idempotencyKeyDigest,
        prepared.inputDigest,
        evaluationDigest
      )
    )
  ) {
    throw new ControlModulesError(
      "MODULE_DISABLE proof is not bound to a confirmation issued in this live memory context",
      0,
      "CONFIRMATION_CONTEXT_MISSING"
    );
  }
  const headers: Record<string, string> = {
    ...prepared.headers,
    "Idempotency-Key": key,
    "X-FreeAgent-Operation-Evaluation-Digest": evaluationDigest
  };
  if (proof !== undefined) headers["X-FreeAgent-Confirmation"] = proof;
  const url = `${context.origin}${MODULE_DISABLE_MUTATE_PATH}`;
  const response = await fetchExact(
    url,
    {
      method: "POST",
      credentials: "include",
      redirect: "error",
      headers,
      body: prepared.canonicalBody,
      signal
    },
    fetcher,
    true
  );
  const text = await requireJSONSuccess(
    response,
    "MODULE_DISABLE mutation response",
    true,
    false
  );
  try {
    return await decodeMutationResult(
      text,
      context,
      prepared.inputDigest,
      prepared.expected,
      idempotencyKeyDigest,
      evaluationDigest
    );
  } catch (error) {
    throw new ControlModulesError(
      error instanceof Error
        ? error.message
        : "MODULE_DISABLE mutation response is invalid",
      response.status,
      "INVALID_RESPONSE",
      "",
      0,
      true
    );
  }
};
