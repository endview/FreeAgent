import { j as jsxRuntimeExports, r as reactExports, c as clientExports } from "./react.js";
import { u as useQueryClient, a as useInfiniteQuery, b as useQuery, Q as QueryClient, c as QueryClientProvider } from "./tanstack-query.js";
const HANDOFF_SCHEMA = "freeagent.control-bootstrap-handoff/v1";
const BOOTSTRAP_RESPONSE_SCHEMA = "control-bootstrap-session/v2";
const RESUME_RESPONSE_SCHEMA = "control-session-resumed/v1";
const SESSION_SCHEMA = "control-session/v1";
const SCOPE_SCHEMA = "control-scope/v1";
const OVERVIEW_SCHEMA = "control-http-overview/v1";
const MAX_HANDOFF_BYTES = 4 * 1024;
const MAX_RESPONSE_BYTES = 1 << 20;
const MAX_AUTHORIZED_SCOPES = 256;
const MAX_OVERVIEW_WORKSPACES = 256;
const MAX_OVERVIEW_ITEMS = 12;
const OPAQUE_VALUE_PATTERN = /^[A-Za-z0-9_-]{43}$/u;
const DIGEST_PATTERN$1 = /^[0-9a-f]{64}$/u;
const LOOPBACK_ORIGIN_PATTERN = /^http:\/\/127\.0\.0\.1:([1-9][0-9]{0,4})$/u;
const CAPABILITIES = /* @__PURE__ */ new Set([
  "OBSERVE",
  "OPERATE_MODULES",
  "REVIEW_LEARNING",
  "RUN_LEARNING"
]);
const isRecord$1 = (value) => typeof value === "object" && value !== null && !Array.isArray(value);
const exactKeys$1 = (value, required, optional = []) => {
  const allowed = /* @__PURE__ */ new Set([...required, ...optional]);
  const keys = Object.keys(value);
  return required.every((key) => Object.hasOwn(value, key)) && keys.every((key) => allowed.has(key));
};
const safeInteger$1 = (value, positive = false) => Number.isSafeInteger(value) && value >= (positive ? 1 : 0);
const wellFormedUTF8$1 = (value) => {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(
      new TextEncoder().encode(value)
    ) === value;
  } catch {
    return false;
  }
};
const opaque$1 = (value, maximum = 256) => typeof value === "string" && value.length > 0 && wellFormedUTF8$1(value) && new TextEncoder().encode(value).length <= maximum && value === value.trim() && value === value.normalize("NFC") && !/[\u0000-\u001f\u007f]/u.test(value);
const CONTROL_SCOPE_ID_ENCODING = "base64url-utf8-v1";
const CONTROL_SCOPE_ID_ENCODING_HEADER = "X-FreeAgent-Scope-ID-Encoding";
const encodeControlScopeID = (value) => {
  if (!opaque$1(value)) throw new Error("Control scope ID is invalid");
  const bytes = new TextEncoder().encode(value);
  const binary = [...bytes].map((byte) => String.fromCharCode(byte)).join("");
  const encoded = btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/u, "");
  if (encoded === "") throw new Error("Control scope ID encoding failed");
  return encoded;
};
const digest$1 = (value) => typeof value === "string" && DIGEST_PATTERN$1.test(value);
const credential = (value) => {
  if (typeof value !== "string" || !OPAQUE_VALUE_PATTERN.test(value)) return false;
  try {
    const standard = value.replaceAll("-", "+").replaceAll("_", "/") + "=";
    const decoded = atob(standard);
    if (decoded.length !== 32) return false;
    const rebuilt = btoa(decoded).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/u, "");
    return rebuilt === value;
  } catch {
    return false;
  }
};
const exactLoopbackOrigin = (value) => {
  if (typeof value !== "string") return false;
  const match = LOOPBACK_ORIGIN_PATTERN.exec(value);
  if (match === null) return false;
  const port = Number(match[1]);
  if (!Number.isInteger(port) || port < 1 || port > 65535 || String(port) !== match[1]) {
    return false;
  }
  try {
    const parsed = new URL(value);
    return parsed.origin === value && parsed.hostname === "127.0.0.1";
  } catch {
    return false;
  }
};
const rejectDuplicateObjectKeys$1 = (text, label) => {
  let index = 0;
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
        return JSON.parse(text.slice(start, index));
      }
      index += 1;
    }
    throw new Error(`${label} contains an unterminated JSON string`);
  };
  const value = () => {
    whitespace();
    if (text[index] === "{") {
      index += 1;
      whitespace();
      const keys = /* @__PURE__ */ new Set();
      if (text[index] === "}") {
        index += 1;
        return;
      }
      while (index < text.length) {
        if (text[index] !== '"') throw new Error(`${label} contains invalid JSON`);
        const key = readStringLexeme();
        if (keys.has(key)) throw new Error(`${label} contains duplicate object key ${key}`);
        keys.add(key);
        whitespace();
        if (text[index] !== ":") throw new Error(`${label} contains invalid JSON`);
        index += 1;
        value();
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
        value();
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
    while (index < text.length && !/[\t\n\r ,\]}]/u.test(text[index])) index += 1;
  };
  value();
  whitespace();
  if (index !== text.length) throw new Error(`${label} contains trailing JSON material`);
};
const parseJSONRecord$1 = (text, label) => {
  let decoded;
  try {
    rejectDuplicateObjectKeys$1(text, label);
    decoded = JSON.parse(text);
  } catch (error) {
    if (error instanceof Error && error.message.includes("duplicate object key")) {
      throw error;
    }
    throw new Error(`${label} is not valid JSON`);
  }
  if (!isRecord$1(decoded)) throw new Error(`${label} must be one JSON object`);
  return decoded;
};
const decodeHandoff = (text, nowUnixMicros = Date.now() * 1e3) => {
  if (new TextEncoder().encode(text).length > MAX_HANDOFF_BYTES) {
    throw new Error("handoff file exceeds the 4 KiB limit");
  }
  const value = parseJSONRecord$1(text, "handoff file");
  if (!exactKeys$1(value, [
    "schema_version",
    "origin",
    "capability",
    "expires_at_unix_micros"
  ]) || value.schema_version !== HANDOFF_SCHEMA || !exactLoopbackOrigin(value.origin) || !credential(value.capability) || !safeInteger$1(value.expires_at_unix_micros, true) || value.expires_at_unix_micros <= nowUnixMicros) {
    throw new Error("handoff file is invalid or expired");
  }
  return value;
};
const decodeScope$1 = (value) => {
  if (!isRecord$1(value)) throw new Error("authorized scope is not an object");
  if (value.schema_version !== SCOPE_SCHEMA || !opaque$1(value.tenant_id) || value.kind !== "TENANT" && value.kind !== "WORKSPACE") {
    throw new Error("authorized scope is invalid");
  }
  if (value.kind === "TENANT") {
    if (!exactKeys$1(value, ["schema_version", "kind", "tenant_id"], ["workspace_id"])) {
      throw new Error("tenant scope has unexpected properties");
    }
    if (Object.hasOwn(value, "workspace_id") && value.workspace_id !== "") {
      throw new Error("tenant scope cannot name a workspace");
    }
    return {
      schema_version: SCOPE_SCHEMA,
      kind: "TENANT",
      tenant_id: value.tenant_id
    };
  }
  if (!exactKeys$1(value, ["schema_version", "kind", "tenant_id", "workspace_id"]) || !opaque$1(value.workspace_id)) {
    throw new Error("workspace scope is invalid");
  }
  return value;
};
const scopeKey = (scope) => `${scope.kind}\0${scope.tenant_id}\0${scope.workspace_id ?? ""}`;
const scopeOrderKey = (scope) => `${scope.tenant_id}\0${scope.kind}\0${scope.workspace_id ?? ""}`;
const compareUTF8$1 = (left, right) => {
  const leftBytes = new TextEncoder().encode(left);
  const rightBytes = new TextEncoder().encode(right);
  const length = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < length; index += 1) {
    if (leftBytes[index] !== rightBytes[index]) return leftBytes[index] - rightBytes[index];
  }
  return leftBytes.length - rightBytes.length;
};
const canonicalJSONString = (value) => {
  if (value === null || typeof value === "boolean" || typeof value === "string") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) throw new Error("canonical JSON contains an unsafe number");
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalJSONString).join(",")}]`;
  }
  if (isRecord$1(value)) {
    return `{${Object.keys(value).sort().filter((key) => value[key] !== void 0).map((key) => `${JSON.stringify(key)}:${canonicalJSONString(value[key])}`).join(",")}}`;
  }
  throw new Error("canonical JSON contains an unsupported value");
};
const domainDigest = async (domain, canonical) => {
  const domainBytes = new TextEncoder().encode(`${domain}\0${canonical}`);
  const bytes = new Uint8Array(await crypto.subtle.digest("SHA-256", domainBytes));
  return [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
};
const authorizedScopeSetDigest = async (scopes) => domainDigest(
  "freeagent.control-scope-set/v1",
  canonicalJSONString({ schema_version: "control-scope-set/v1", scopes })
);
const decodeSession = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, [
    "schema_version",
    "boot_id",
    "session_id",
    "principal_id",
    "capabilities",
    "scope_set_digest",
    "authorization_revision",
    "issued_at_unix_micros",
    "expires_at_unix_micros"
  ]) || value.schema_version !== SESSION_SCHEMA || !opaque$1(value.boot_id) || !opaque$1(value.session_id) || !opaque$1(value.principal_id) || !Array.isArray(value.capabilities) || value.capabilities.length < 1 || value.capabilities.length > 32 || !value.capabilities.every(
    (entry) => typeof entry === "string" && CAPABILITIES.has(entry)
  ) || value.capabilities.some((entry, index, all) => index > 0 && all[index - 1] >= entry) || !digest$1(value.scope_set_digest) || !safeInteger$1(value.authorization_revision, true) || !safeInteger$1(value.issued_at_unix_micros, true) || !safeInteger$1(value.expires_at_unix_micros, true) || value.expires_at_unix_micros <= value.issued_at_unix_micros || value.expires_at_unix_micros - value.issued_at_unix_micros > 8 * 60 * 60 * 1e6) {
    throw new Error("control session metadata is invalid");
  }
  return value;
};
const decodeSessionExchange = async (text, expectedSchema) => {
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) {
    throw new Error("session response exceeds the 1 MiB limit");
  }
  const value = parseJSONRecord$1(text, "session response");
  if (!exactKeys$1(value, [
    "schema_version",
    "session",
    "authorized_scopes",
    "csrf_token",
    "resume_credential"
  ]) || value.schema_version !== expectedSchema || !credential(value.csrf_token) || !credential(value.resume_credential) || !Array.isArray(value.authorized_scopes) || value.authorized_scopes.length < 1 || value.authorized_scopes.length > MAX_AUTHORIZED_SCOPES) {
    throw new Error("session response envelope is invalid");
  }
  const session = decodeSession(value.session);
  const scopes = value.authorized_scopes.map(decodeScope$1);
  if (scopes.some(
    (scope, index) => index > 0 && compareUTF8$1(scopeOrderKey(scopes[index - 1]), scopeOrderKey(scope)) >= 0
  )) {
    throw new Error("authorized scopes are not in canonical server order");
  }
  if (await authorizedScopeSetDigest(scopes) !== session.scope_set_digest) {
    throw new Error("authorized scope-set digest does not match the session");
  }
  return {
    schema_version: expectedSchema,
    session,
    authorized_scopes: scopes,
    csrf_token: value.csrf_token,
    resume_credential: value.resume_credential
  };
};
const decodeDigestRef$1 = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, ["id", "revision", "digest"]) || !opaque$1(value.id) || !safeInteger$1(value.revision) || !digest$1(value.digest)) {
    throw new Error("revisioned digest reference is invalid");
  }
  return value;
};
const decodeBasis$1 = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, ["tenant_id", "pointer_revision", "control", "catalog"]) || !opaque$1(value.tenant_id) || !safeInteger$1(value.pointer_revision, true)) {
    throw new Error("published basis is invalid");
  }
  return {
    tenant_id: value.tenant_id,
    pointer_revision: value.pointer_revision,
    control: decodeDigestRef$1(value.control),
    catalog: decodeDigestRef$1(value.catalog)
  };
};
const decodeExpectedRef$1 = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, ["kind", "resource_id", "revision", "digest"]) || !opaque$1(value.kind) || !opaque$1(value.resource_id) || !safeInteger$1(value.revision, true) || !digest$1(value.digest)) {
    throw new Error("published pointer reference is invalid");
  }
  return value;
};
const decodeViewSection = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, ["kind", "source_revision", "source_digest", "item_count", "truncated"]) || !opaque$1(value.kind) || !safeInteger$1(value.source_revision, true) || !digest$1(value.source_digest) || !safeInteger$1(value.item_count) || typeof value.truncated !== "boolean") {
    throw new Error("view section is invalid");
  }
  return value;
};
const decodeView$1 = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, [
    "schema_version",
    "scope",
    "scope_digest",
    "observed_at_unix_micros",
    "basis",
    "sections"
  ]) || value.schema_version !== "control-view-snapshot/v1" || !digest$1(value.scope_digest) || !safeInteger$1(value.observed_at_unix_micros, true) || !Array.isArray(value.sections) || value.sections.length < 1 || value.sections.length > 32) {
    throw new Error("control view is invalid");
  }
  const sections = value.sections.map(decodeViewSection);
  if (sections.some((section, index) => index > 0 && sections[index - 1].kind >= section.kind)) {
    throw new Error("view sections are not in canonical order");
  }
  return {
    schema_version: "control-view-snapshot/v1",
    scope: decodeScope$1(value.scope),
    scope_digest: value.scope_digest,
    observed_at_unix_micros: value.observed_at_unix_micros,
    basis: decodeBasis$1(value.basis),
    sections
  };
};
const decodeWorkspace = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, ["id", "version", "digest"]) || !opaque$1(value.id) || !opaque$1(value.version, 64) || !digest$1(value.digest)) {
    throw new Error("workspace reference is invalid");
  }
  return value;
};
const decodeRun = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(
    value,
    [
      "tenant_id",
      "workspace_id",
      "run_id",
      "state",
      "revision",
      "created_at_unix_micros",
      "updated_at_unix_micros"
    ],
    ["disposition"]
  ) || !opaque$1(value.tenant_id) || !opaque$1(value.workspace_id) || !opaque$1(value.run_id) || !opaque$1(value.state) || value.disposition !== void 0 && !opaque$1(value.disposition) || !safeInteger$1(value.revision) || !safeInteger$1(value.created_at_unix_micros, true) || !safeInteger$1(value.updated_at_unix_micros, true) || value.updated_at_unix_micros < value.created_at_unix_micros) throw new Error("run item is invalid");
  return value;
};
const decodeUnknown = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, [
    "kind",
    "resource_id",
    "tenant_id",
    "workspace_id",
    "run_id",
    "revision",
    "updated_at_unix_micros"
  ]) || !opaque$1(value.kind) || !opaque$1(value.resource_id) || !opaque$1(value.tenant_id) || !opaque$1(value.workspace_id) || !opaque$1(value.run_id) || !safeInteger$1(value.revision) || !safeInteger$1(value.updated_at_unix_micros, true)) throw new Error("unknown item is invalid");
  return value;
};
const decodeLearning = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, [
    "proposal_id",
    "tenant_id",
    "workspace_id",
    "kind",
    "state",
    "revision",
    "created_at_unix_micros",
    "updated_at_unix_micros"
  ]) || !digest$1(value.proposal_id) || !opaque$1(value.tenant_id) || !opaque$1(value.workspace_id) || !opaque$1(value.kind) || !opaque$1(value.state) || !safeInteger$1(value.revision) || !safeInteger$1(value.created_at_unix_micros, true) || !safeInteger$1(value.updated_at_unix_micros, true) || value.updated_at_unix_micros < value.created_at_unix_micros) throw new Error("learning item is invalid");
  return value;
};
const decodeCandidate = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(
    value,
    [
      "review_id",
      "candidate_id",
      "tenant_id",
      "binding_target_kind",
      "current_instance_id",
      "target_instance_id",
      "current_module_id",
      "current_exact_version",
      "current_artifact_digest",
      "target_module_id",
      "target_exact_version",
      "target_artifact_digest",
      "conclusion",
      "created_at_unix_micros"
    ],
    ["workspace_id"]
  ) || !digest$1(value.review_id) || !digest$1(value.candidate_id) || !opaque$1(value.tenant_id) || value.workspace_id !== void 0 && !opaque$1(value.workspace_id) || !opaque$1(value.binding_target_kind) || !opaque$1(value.current_instance_id) || !opaque$1(value.target_instance_id) || !opaque$1(value.current_module_id) || !opaque$1(value.current_exact_version, 64) || !digest$1(value.current_artifact_digest) || !opaque$1(value.target_module_id) || !opaque$1(value.target_exact_version, 64) || !digest$1(value.target_artifact_digest) || !opaque$1(value.conclusion) || !safeInteger$1(value.created_at_unix_micros, true)) throw new Error("module candidate is invalid");
  return value;
};
const nullableTokens = (value) => value === null || safeInteger$1(value);
const decodeUsage = (value) => {
  if (!isRecord$1(value) || !exactKeys$1(value, [
    "attempt_id",
    "run_id",
    "tenant_id",
    "workspace_id",
    "revision",
    "input_tokens",
    "cached_input_tokens",
    "uncached_input_tokens",
    "output_tokens",
    "reasoning_tokens",
    "reconciliation_status",
    "updated_at_unix_micros"
  ]) || !opaque$1(value.attempt_id) || !opaque$1(value.run_id) || !opaque$1(value.tenant_id) || !opaque$1(value.workspace_id) || !safeInteger$1(value.revision) || !nullableTokens(value.input_tokens) || !nullableTokens(value.cached_input_tokens) || !nullableTokens(value.uncached_input_tokens) || !nullableTokens(value.output_tokens) || !nullableTokens(value.reasoning_tokens) || !opaque$1(value.reconciliation_status) || !safeInteger$1(value.updated_at_unix_micros, true)) throw new Error("usage item is invalid");
  return value;
};
const ownsItem = (scope, tenantID, workspaceID) => tenantID === scope.tenant_id && opaque$1(workspaceID) && (scope.kind === "TENANT" || workspaceID === scope.workspace_id);
const newerFirst = (previousTime, previousID, currentTime, currentID) => previousTime > currentTime || previousTime === currentTime && compareUTF8$1(previousID, currentID) > 0;
const validateOverviewSemantics = (overview, scope) => {
  const sourceTime = overview.view.observed_at_unix_micros;
  const requireExactTruncationProof = (truncated, length) => !truncated || length === MAX_OVERVIEW_ITEMS;
  if (!requireExactTruncationProof(overview.runs_truncated, overview.runs.length) || !requireExactTruncationProof(overview.unknown_truncated, overview.unknown.length) || !requireExactTruncationProof(overview.learning_truncated, overview.learning.length) || !requireExactTruncationProof(
    overview.module_candidates_truncated,
    overview.module_candidates.length
  ) || !requireExactTruncationProof(overview.usage_truncated, overview.usage.length)) throw new Error("overview truncation proof is invalid");
  for (let index = 0; index < overview.workspaces.length; index += 1) {
    const workspace = overview.workspaces[index];
    if (scope.kind === "WORKSPACE" && workspace.id !== scope.workspace_id || index > 0 && compareUTF8$1(overview.workspaces[index - 1].id, workspace.id) >= 0) throw new Error("overview workspace order or ownership is invalid");
  }
  const validRunProjection = (item) => item.state === "ADMITTED" && (item.disposition === void 0 || item.disposition === "WAITING_EXTERNAL") || item.revision >= 1 && item.state === "WAITING_RECONCILIATION" && item.disposition === "WAITING_RECONCILIATION" || item.revision >= 1 && item.state === "TERMINATED" && item.disposition === "TERMINATED";
  const runsByID = /* @__PURE__ */ new Map();
  for (let index = 0; index < overview.runs.length; index += 1) {
    const item = overview.runs[index];
    if (runsByID.has(item.run_id) || !ownsItem(scope, item.tenant_id, item.workspace_id) || !validRunProjection(item) || item.updated_at_unix_micros > sourceTime || index > 0 && !newerFirst(
      overview.runs[index - 1].updated_at_unix_micros,
      overview.runs[index - 1].run_id,
      item.updated_at_unix_micros,
      item.run_id
    )) throw new Error("overview run semantics, ownership, or order is invalid");
    runsByID.set(item.run_id, item);
  }
  const unknownKinds = /* @__PURE__ */ new Set([
    "MODEL",
    "ACTION",
    "CHANNEL_SEND",
    "LEARNING_PROPOSAL",
    "LEARNING_TASK"
  ]);
  const validUnknownRevision = (item) => {
    switch (item.kind) {
      case "MODEL":
        return item.revision === 1;
      case "ACTION":
        return item.revision === 1 || item.revision === 2;
      case "CHANNEL_SEND":
        return item.revision >= 1;
      case "LEARNING_PROPOSAL":
      case "LEARNING_TASK":
        return item.revision === 2;
      default:
        return false;
    }
  };
  const unknownIdentities = /* @__PURE__ */ new Set();
  const modelUnknownByAttempt = /* @__PURE__ */ new Map();
  const proposalUnknownByID = /* @__PURE__ */ new Map();
  for (let index = 0; index < overview.unknown.length; index += 1) {
    const item = overview.unknown[index];
    const previous = overview.unknown[index - 1];
    const resourceOrder = index === 0 ? 0 : compareUTF8$1(previous.resource_id, item.resource_id);
    const resourceIDValid = item.kind === "LEARNING_PROPOSAL" || item.kind === "LEARNING_TASK" ? digest$1(item.resource_id) : opaque$1(item.resource_id);
    const identity = `${item.kind}\0${item.resource_id}`;
    const intersectingRun = runsByID.get(item.run_id);
    const ordered = index === 0 || previous.updated_at_unix_micros > item.updated_at_unix_micros || previous.updated_at_unix_micros === item.updated_at_unix_micros && (resourceOrder > 0 || resourceOrder === 0 && compareUTF8$1(previous.kind, item.kind) > 0);
    if (!unknownKinds.has(item.kind) || unknownIdentities.has(identity) || !resourceIDValid || !validUnknownRevision(item) || !ownsItem(scope, item.tenant_id, item.workspace_id) || item.updated_at_unix_micros > sourceTime || intersectingRun !== void 0 && (intersectingRun.tenant_id !== item.tenant_id || intersectingRun.workspace_id !== item.workspace_id || intersectingRun.state !== "WAITING_RECONCILIATION" || intersectingRun.disposition !== "WAITING_RECONCILIATION") || !ordered) {
      throw new Error("overview unknown semantics, ownership, or order is invalid");
    }
    unknownIdentities.add(identity);
    if (item.kind === "MODEL") modelUnknownByAttempt.set(item.resource_id, item);
    if (item.kind === "LEARNING_PROPOSAL") proposalUnknownByID.set(item.resource_id, item);
  }
  const learningKinds = /* @__PURE__ */ new Set(["KNOWLEDGE", "SKILL"]);
  const validLearningProjection = (item) => {
    switch (item.state) {
      case "SUBMITTED":
        return item.revision === 0;
      case "REVIEW_PENDING":
        return item.revision === 1;
      case "REVIEW_UNKNOWN":
        return item.revision === 2;
      case "APPROVED":
      case "REJECTED":
      case "REVIEW_FAILED":
        return item.revision === 2 || item.revision === 3;
      default:
        return false;
    }
  };
  const learningIdentities = /* @__PURE__ */ new Set();
  for (let index = 0; index < overview.learning.length; index += 1) {
    const item = overview.learning[index];
    const intersectingUnknown = proposalUnknownByID.get(item.proposal_id);
    if (learningIdentities.has(item.proposal_id) || !learningKinds.has(item.kind) || !validLearningProjection(item) || !ownsItem(scope, item.tenant_id, item.workspace_id) || item.updated_at_unix_micros > sourceTime || intersectingUnknown !== void 0 && (item.state !== "REVIEW_UNKNOWN" || item.revision !== intersectingUnknown.revision || item.tenant_id !== intersectingUnknown.tenant_id || item.workspace_id !== intersectingUnknown.workspace_id || item.updated_at_unix_micros !== intersectingUnknown.updated_at_unix_micros) || index > 0 && !newerFirst(
      overview.learning[index - 1].updated_at_unix_micros,
      overview.learning[index - 1].proposal_id,
      item.updated_at_unix_micros,
      item.proposal_id
    )) throw new Error("overview learning semantics, ownership, or order is invalid");
    learningIdentities.add(item.proposal_id);
  }
  const conclusions = /* @__PURE__ */ new Set(["WOULD_APPLY", "CONFLICT", "UNSUPPORTED"]);
  const moduleReviewIdentities = /* @__PURE__ */ new Set();
  for (let index = 0; index < overview.module_candidates.length; index += 1) {
    const item = overview.module_candidates[index];
    const validTarget = item.binding_target_kind === "PROFILE" ? scope.kind === "TENANT" && item.workspace_id === void 0 : item.binding_target_kind === "WORKSPACE_CHANNEL_ENDPOINT" && opaque$1(item.workspace_id) && (scope.kind === "TENANT" || item.workspace_id === scope.workspace_id);
    if (moduleReviewIdentities.has(item.review_id) || item.tenant_id !== scope.tenant_id || !validTarget || !conclusions.has(item.conclusion) || item.current_instance_id === item.target_instance_id || item.current_module_id !== item.target_module_id || item.current_exact_version === item.target_exact_version || item.current_artifact_digest === item.target_artifact_digest || item.created_at_unix_micros > sourceTime || index > 0 && !newerFirst(
      overview.module_candidates[index - 1].created_at_unix_micros,
      overview.module_candidates[index - 1].review_id,
      item.created_at_unix_micros,
      item.review_id
    )) throw new Error("overview module candidate semantics, ownership, or order is invalid");
    moduleReviewIdentities.add(item.review_id);
  }
  const usageIdentities = /* @__PURE__ */ new Set();
  for (let index = 0; index < overview.usage.length; index += 1) {
    const item = overview.usage[index];
    const allTokensNull = item.input_tokens === null && item.cached_input_tokens === null && item.uncached_input_tokens === null && item.output_tokens === null && item.reasoning_tokens === null;
    const validUsageProjection = item.reconciliation_status === "PENDING" && item.revision === 0 && allTokensNull || item.reconciliation_status === "PENDING_RECONCILIATION" && item.revision === 1 || item.reconciliation_status === "PROVIDER_REPORTED" && (item.revision === 1 || item.revision === 2) || item.reconciliation_status === "NO_USAGE_REPORTED" && item.revision <= 2 && allTokensNull;
    const usageRelationValid = item.input_tokens === null || item.cached_input_tokens === null || item.uncached_input_tokens === null || item.input_tokens === item.cached_input_tokens + item.uncached_input_tokens;
    const intersectingUnknown = modelUnknownByAttempt.get(item.attempt_id);
    if (usageIdentities.has(item.attempt_id) || !validUsageProjection || !usageRelationValid || !ownsItem(scope, item.tenant_id, item.workspace_id) || item.updated_at_unix_micros > sourceTime || intersectingUnknown !== void 0 && (item.reconciliation_status !== "PENDING_RECONCILIATION" || item.revision !== intersectingUnknown.revision || item.run_id !== intersectingUnknown.run_id || item.tenant_id !== intersectingUnknown.tenant_id || item.workspace_id !== intersectingUnknown.workspace_id || item.updated_at_unix_micros !== intersectingUnknown.updated_at_unix_micros) || index > 0 && !newerFirst(
      overview.usage[index - 1].updated_at_unix_micros,
      overview.usage[index - 1].attempt_id,
      item.updated_at_unix_micros,
      item.attempt_id
    )) throw new Error("overview usage semantics, ownership, tokens, or order is invalid");
    usageIdentities.add(item.attempt_id);
  }
};
const decodeBoundedArray = (value, maximum, decoder, label) => {
  if (!Array.isArray(value) || value.length > maximum) {
    throw new Error(`${label} collection exceeds its bound`);
  }
  return value.map(decoder);
};
const decodeOverview = (text, requestedScope) => {
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) {
    throw new Error("overview response exceeds the 1 MiB limit");
  }
  const value = parseJSONRecord$1(text, "overview response");
  const keys = [
    "schema_version",
    "published_pointer",
    "basis",
    "view",
    "view_snapshot_digest",
    "workspaces",
    "workspaces_truncated",
    "runs",
    "runs_truncated",
    "unknown",
    "unknown_truncated",
    "learning",
    "learning_truncated",
    "module_candidates",
    "module_candidates_truncated",
    "usage",
    "usage_truncated",
    "projection_digest"
  ];
  if (!exactKeys$1(value, keys) || value.schema_version !== OVERVIEW_SCHEMA || value.workspaces_truncated !== false || typeof value.runs_truncated !== "boolean" || typeof value.unknown_truncated !== "boolean" || typeof value.learning_truncated !== "boolean" || typeof value.module_candidates_truncated !== "boolean" || typeof value.usage_truncated !== "boolean" || !digest$1(value.view_snapshot_digest) || !digest$1(value.projection_digest)) throw new Error("overview response envelope is invalid");
  const basis = decodeBasis$1(value.basis);
  const view = decodeView$1(value.view);
  if (scopeKey(view.scope) !== scopeKey(requestedScope) || basis.tenant_id !== requestedScope.tenant_id || canonicalJSONString(view.basis) !== canonicalJSONString(basis)) throw new Error("overview response is outside the requested scope or basis");
  const overview = {
    schema_version: OVERVIEW_SCHEMA,
    published_pointer: decodeExpectedRef$1(value.published_pointer),
    basis,
    view,
    view_snapshot_digest: value.view_snapshot_digest,
    workspaces: decodeBoundedArray(value.workspaces, MAX_OVERVIEW_WORKSPACES, decodeWorkspace, "workspace"),
    workspaces_truncated: false,
    runs: decodeBoundedArray(value.runs, MAX_OVERVIEW_ITEMS, decodeRun, "run"),
    runs_truncated: value.runs_truncated,
    unknown: decodeBoundedArray(value.unknown, MAX_OVERVIEW_ITEMS, decodeUnknown, "unknown"),
    unknown_truncated: value.unknown_truncated,
    learning: decodeBoundedArray(value.learning, MAX_OVERVIEW_ITEMS, decodeLearning, "learning"),
    learning_truncated: value.learning_truncated,
    module_candidates: decodeBoundedArray(value.module_candidates, MAX_OVERVIEW_ITEMS, decodeCandidate, "candidate"),
    module_candidates_truncated: value.module_candidates_truncated,
    usage: decodeBoundedArray(value.usage, MAX_OVERVIEW_ITEMS, decodeUsage, "usage"),
    usage_truncated: value.usage_truncated,
    projection_digest: value.projection_digest
  };
  validateOverviewSemantics(overview, requestedScope);
  return overview;
};
const sectionSource = (overview, kind) => {
  switch (kind) {
    case "WORKSPACES":
      return { items: overview.workspaces, truncated: overview.workspaces_truncated };
    case "RUNS":
      return { items: overview.runs, truncated: overview.runs_truncated };
    case "UNKNOWN":
      return { items: overview.unknown, truncated: overview.unknown_truncated };
    case "LEARNING":
      return { items: overview.learning, truncated: overview.learning_truncated };
    case "MODULES":
      return { items: overview.module_candidates, truncated: overview.module_candidates_truncated };
    case "USAGE":
      return { items: overview.usage, truncated: overview.usage_truncated };
    default:
      return null;
  }
};
const validateOverviewDigests = async (overview) => {
  const scopeDigest2 = await domainDigest(
    "freeagent.control-scope/v1",
    canonicalJSONString(overview.view.scope)
  );
  if (scopeDigest2 !== overview.view.scope_digest) {
    throw new Error("overview view scope digest is invalid");
  }
  const basisDigest = await domainDigest(
    "freeagent.control-published-pointer-ref/v1",
    canonicalJSONString(overview.basis)
  );
  if (overview.published_pointer.kind !== "PUBLISHED_POINTER" || overview.published_pointer.resource_id !== overview.basis.tenant_id || overview.published_pointer.revision !== overview.basis.pointer_revision || overview.published_pointer.digest !== basisDigest) {
    throw new Error("overview published pointer does not bind its exact basis");
  }
  const viewDigest = await domainDigest(
    "freeagent.control-view-snapshot/v1",
    canonicalJSONString(overview.view)
  );
  if (viewDigest !== overview.view_snapshot_digest) {
    throw new Error("overview view snapshot digest is invalid");
  }
  for (const section of overview.view.sections) {
    const source = sectionSource(overview, section.kind);
    if (source === null || section.source_revision !== overview.basis.pointer_revision || section.item_count !== source.items.length || section.truncated !== source.truncated) {
      throw new Error(`overview ${section.kind} section metadata is invalid`);
    }
    const sourceDigest = await domainDigest(
      "freeagent.control-overview-section-source/v1",
      canonicalJSONString({
        schema_version: "control-overview-section-source/v1",
        scope_digest: overview.view.scope_digest,
        kind: section.kind,
        items: source.items,
        truncated: source.truncated
      })
    );
    if (sourceDigest !== section.source_digest) {
      throw new Error(`overview ${section.kind} section digest is invalid`);
    }
  }
  const expectedSectionKinds = ["LEARNING", "MODULES", "RUNS", "UNKNOWN", "USAGE", "WORKSPACES"];
  if (overview.view.sections.map((section) => section.kind).join("\n") !== expectedSectionKinds.join("\n")) {
    throw new Error("overview section set is incomplete");
  }
  const projectionDigest = await domainDigest(
    "freeagent.control-overview/v1",
    canonicalJSONString({
      schema_version: "control-overview/v1",
      published_pointer: overview.published_pointer,
      basis: overview.basis,
      view: overview.view,
      view_snapshot_digest: overview.view_snapshot_digest,
      workspaces: overview.workspaces,
      workspaces_truncated: overview.workspaces_truncated,
      runs: overview.runs,
      runs_truncated: overview.runs_truncated,
      unknown: overview.unknown,
      unknown_truncated: overview.unknown_truncated,
      learning: overview.learning,
      learning_truncated: overview.learning_truncated,
      module_candidates: overview.module_candidates,
      module_candidates_truncated: overview.module_candidates_truncated,
      usage: overview.usage,
      usage_truncated: overview.usage_truncated,
      projection_digest: "",
      strong_etag: ""
    })
  );
  if (projectionDigest !== overview.projection_digest) {
    throw new Error("overview projection digest is invalid");
  }
};
const decodeControlError = (text) => {
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) return null;
  let value;
  try {
    value = parseJSONRecord$1(text, "control error");
  } catch {
    return null;
  }
  if (!exactKeys$1(
    value,
    ["schema_version", "code", "correlation_id", "message"],
    ["retry_after_seconds"]
  ) || value.schema_version !== "control-error/v1" || !opaque$1(value.code) || !opaque$1(value.correlation_id) || !opaque$1(value.message, 1024) || value.retry_after_seconds !== void 0 && !safeInteger$1(value.retry_after_seconds)) return null;
  return value;
};
const OVERVIEW_PATH = "/control/api/v1/overview";
const STRONG_ETAG_PATTERN$1 = /^"[0-9a-f]{64}"$/u;
class ControlOverviewError extends Error {
  status;
  code;
  correlationID;
  retryAfterSeconds;
  constructor(message, status = 0, code = "TRANSPORT_ERROR", correlationID = "", retryAfterSeconds = 0) {
    super(message);
    this.name = "ControlOverviewError";
    this.status = status;
    this.code = code;
    this.correlationID = correlationID;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}
const overviewQueryKey = (context) => [
  "overview",
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
const overviewHeaders = (csrfToken, scope, etag) => {
  const headers = {
    "X-FreeAgent-CSRF": csrfToken,
    "X-FreeAgent-Scope-Kind": scope.kind,
    [CONTROL_SCOPE_ID_ENCODING_HEADER]: CONTROL_SCOPE_ID_ENCODING,
    "X-FreeAgent-Tenant-ID": encodeControlScopeID(scope.tenant_id)
  };
  if (scope.kind === "WORKSPACE") {
    headers["X-FreeAgent-Workspace-ID"] = encodeControlScopeID(scope.workspace_id ?? "");
  }
  if (etag !== void 0) headers["If-None-Match"] = etag;
  return headers;
};
const etagCache = /* @__PURE__ */ new Map();
const cacheKey = (key) => JSON.stringify(key);
const clearOverviewTransportCache = () => etagCache.clear();
const readError = async (response) => {
  let text = "";
  try {
    text = await response.text();
  } catch {
    return new ControlOverviewError("the control service returned an unreadable error", response.status);
  }
  const decoded = decodeControlError(text);
  if (decoded === null) {
    return new ControlOverviewError("the control service returned an invalid error", response.status);
  }
  return new ControlOverviewError(
    decoded.message,
    response.status,
    decoded.code,
    decoded.correlation_id,
    decoded.retry_after_seconds ?? 0
  );
};
const fetchOverview = async (context, signal, fetcher = fetch) => {
  if (!exactLoopbackOrigin(context.origin) || context.origin !== window.location.origin) {
    throw new ControlOverviewError(
      "overview origin does not match this control page",
      0,
      "CONTROL_ORIGIN_MISMATCH"
    );
  }
  const key = overviewQueryKey(context);
  const stored = etagCache.get(cacheKey(key));
  const response = await fetcher(`${context.origin}${OVERVIEW_PATH}`, {
    method: "GET",
    credentials: "include",
    redirect: "error",
    headers: overviewHeaders(context.csrfToken, context.scope, stored?.etag),
    signal
  });
  if (response.status === 304) {
    if (stored === void 0) {
      throw new ControlOverviewError("overview returned 304 without a local representation", 304, "INVALID_304");
    }
    const body = await response.text();
    if (response.headers.get("ETag") !== stored.etag || response.headers.get("Content-Type") !== null || body !== "") {
      throw new ControlOverviewError(
        "overview returned a malformed 304 response",
        304,
        "INVALID_304"
      );
    }
    return stored.data;
  }
  if (!response.ok) throw await readError(response);
  if (response.headers.get("Content-Type") !== "application/json") {
    throw new ControlOverviewError("overview returned an invalid content type", response.status, "INVALID_RESPONSE");
  }
  const etag = response.headers.get("ETag");
  if (etag === null || !STRONG_ETAG_PATTERN$1.test(etag)) {
    throw new ControlOverviewError("overview omitted its exact strong ETag", response.status, "INVALID_RESPONSE");
  }
  const text = await response.text();
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) {
    throw new ControlOverviewError("overview response exceeds the 1 MiB limit", response.status, "INVALID_RESPONSE");
  }
  let data;
  try {
    data = decodeOverview(text, context.scope);
    await validateOverviewDigests(data);
  } catch (error) {
    throw new ControlOverviewError(
      error instanceof Error ? error.message : "overview response is invalid",
      response.status,
      "INVALID_RESPONSE"
    );
  }
  etagCache.set(cacheKey(key), { etag, data });
  return data;
};
const isPermissionDenied = (error) => error instanceof ControlOverviewError && (error.status === 403 || error.code === "FORBIDDEN" || error.code === "PERMISSION_DENIED");
const isSessionInvalid = (error) => error instanceof ControlOverviewError && (error.status === 401 || error.code === "UNAUTHENTICATED" || error.code === "SESSION_EXPIRED");
const buildScopeChoices = (authorizedScopes, workspacesByTenant) => {
  const choices = /* @__PURE__ */ new Map();
  for (const scope of authorizedScopes) {
    const key = scopeKey(scope);
    choices.set(key, {
      key,
      label: scope.kind === "TENANT" ? `Tenant · ${scope.tenant_id}` : `Workspace · ${scope.tenant_id} / ${scope.workspace_id ?? ""}`,
      scope,
      source: "authorized"
    });
    if (scope.kind !== "TENANT") continue;
    const workspaces = workspacesByTenant.get(scope.tenant_id) ?? [];
    for (const workspace of workspaces.slice(0, 256)) {
      const derived = {
        schema_version: "control-scope/v1",
        kind: "WORKSPACE",
        tenant_id: scope.tenant_id,
        workspace_id: workspace.id
      };
      const derivedKey = scopeKey(derived);
      if (!choices.has(derivedKey)) {
        choices.set(derivedKey, {
          key: derivedKey,
          label: `Workspace · ${scope.tenant_id} / ${workspace.id}`,
          scope: derived,
          source: "tenant-workspace"
        });
      }
    }
  }
  return [...choices.values()].sort((left, right) => left.label.localeCompare(right.label, "en"));
};
const overviewSearchResults = (overview, rawSearch) => {
  const rows = [
    ...overview.workspaces.map((item) => ({
      section: "workspaces",
      id: item.id,
      title: item.id,
      summary: `Workspace ${item.version}`,
      value: item
    })),
    ...overview.runs.map((item) => ({
      section: "runs",
      id: item.run_id,
      title: item.run_id,
      summary: `${item.state}${item.disposition ? ` · ${item.disposition}` : ""}`,
      value: item
    })),
    ...overview.unknown.map((item) => ({
      section: "unknown",
      id: `${item.kind}:${item.resource_id}`,
      title: item.resource_id,
      summary: item.kind,
      value: item
    })),
    ...overview.learning.map((item) => ({
      section: "learning",
      id: item.proposal_id,
      title: item.proposal_id,
      summary: `${item.kind} · ${item.state}`,
      value: item
    })),
    ...overview.module_candidates.map((item) => ({
      section: "module-candidates",
      id: item.review_id,
      title: item.target_module_id,
      summary: `${item.current_exact_version} → ${item.target_exact_version} · ${item.conclusion}`,
      value: item
    })),
    ...overview.usage.map((item) => ({
      section: "usage",
      id: item.attempt_id,
      title: item.attempt_id,
      summary: `${item.reconciliation_status} · run ${item.run_id}`,
      value: item
    }))
  ];
  const search = rawSearch.trim().toLocaleLowerCase("en");
  if (search === "") return rows;
  return rows.filter(
    (row) => `${row.section}
${row.title}
${row.summary}
${JSON.stringify(row.value)}`.toLocaleLowerCase("en").includes(search)
  );
};
const DETAIL_SECTIONS = /* @__PURE__ */ new Set([
  "workspaces",
  "runs",
  "unknown",
  "learning",
  "module-candidates",
  "usage"
]);
const detailHash = (detail) => `#detail=${encodeURIComponent(`${detail.section}:${detail.id}`)}`;
const parseDetailHash = (hash) => {
  if (hash.length > 2048 || !hash.startsWith("#detail=")) return null;
  let decoded;
  try {
    decoded = decodeURIComponent(hash.slice("#detail=".length));
  } catch {
    return null;
  }
  const separator = decoded.indexOf(":");
  if (separator < 1) return null;
  const section = decoded.slice(0, separator);
  const id = decoded.slice(separator + 1);
  if (!DETAIL_SECTIONS.has(section) || id === "" || new TextEncoder().encode(id).length > 1024 || /[\u0000-\u001f\u007f]/u.test(id)) return null;
  return { section, id };
};
const captureDetailSnapshot = (overview, link) => ({
  link,
  result: overviewSearchResults(overview, "").find(
    (item) => item.section === link.section && item.id === link.id
  ) ?? null,
  scope: overview.view.scope
});
const MODULES_PATH = "/control/api/v1/modules";
const MODULE_DISABLE_DRY_RUN_PATH = "/control/api/v1/modules/disable/dry-run";
const MODULE_DISABLE_CONFIRMATION_PATH = "/control/api/v1/modules/disable/confirmation";
const MODULE_DISABLE_MUTATE_PATH = "/control/api/v1/modules/disable/mutate";
const MODULES_PAGE_LIMIT = 100;
const MODULE_DISABLE_BODY_SCHEMA = "control-module-disable-dry-run-input/v1";
const MODULES_PAGE_SCHEMA = "control-http-modules-page/v1";
const MODULES_APPLICATION_PAGE_SCHEMA = "control-modules-page/v1";
const MODULE_DETAIL_SCHEMA = "control-module-detail/v1";
const MODULES_CURSOR_SCHEMA = "control-modules-cursor/v1";
const MODULES_SORT_VERSION = "control-modules-instance-id-binary/v1";
const MODULE_DISABLE_DRY_RUN_RESULT_SCHEMA = "control-module-disable-dry-run-result/v1";
const MODULE_DISABLE_EVALUATION_SCHEMA = "control-module-disable-evaluation/v1";
const MODULE_DISABLE_CONFIRMATION_RESULT_SCHEMA = "control-module-disable-confirmation-result/v1";
const MODULE_DISABLE_MUTATION_RESULT_SCHEMA = "control-module-disable-mutation-result/v1";
const OPERATION_REQUEST_SCHEMA = "control-operation-request/v1";
const OPERATION_RECEIPT_SCHEMA = "control-operation-receipt/v1";
const CONFIRMATION_STATEMENT_SCHEMA = "control-confirmation-statement/v1";
const DIGEST_PATTERN = /^[0-9a-f]{64}$/u;
const STRONG_ETAG_PATTERN = /^"[0-9a-f]{64}"$/u;
const RAW_URL_SEGMENT_PATTERN = /^[A-Za-z0-9_-]+$/u;
const MODULE_INSTANCE_ID_ENCODING = "base64url-utf8-v1";
const MODULE_INSTANCE_ID_ENCODING_HEADER = "X-FreeAgent-Module-Instance-ID-Encoding";
const DOTTED_IDENTIFIER_PATTERN = /^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)*$/u;
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
class ControlModulesError extends Error {
  status;
  code;
  correlationID;
  retryAfterSeconds;
  outcomeMayBeCommitted;
  constructor(message, status = 0, code = "TRANSPORT_ERROR", correlationID = "", retryAfterSeconds = 0, outcomeMayBeCommitted = false) {
    super(message);
    this.name = "ControlModulesError";
    this.status = status;
    this.code = code;
    this.correlationID = correlationID;
    this.retryAfterSeconds = retryAfterSeconds;
    this.outcomeMayBeCommitted = outcomeMayBeCommitted;
  }
}
const isModulesPermissionDenied = (error) => error instanceof ControlModulesError && (error.status === 403 || error.code === "FORBIDDEN" || error.code === "PERMISSION_DENIED");
const isModulesSessionInvalid = (error) => error instanceof ControlModulesError && (error.status === 401 || error.code === "UNAUTHENTICATED" || error.code === "SESSION_EXPIRED");
const isModulesStale = (error) => error instanceof ControlModulesError && (error.code === "REVISION_CONFLICT" || error.code === "CURSOR_STALE" || error.code === "CURSOR_CONTEXT_MISSING" || error.code === "INVALID_304");
const isRecord = (value) => typeof value === "object" && value !== null && !Array.isArray(value);
const exactKeys = (value, required, optional = []) => {
  const allowed = /* @__PURE__ */ new Set([...required, ...optional]);
  const keys = Object.keys(value);
  return required.every((key) => Object.hasOwn(value, key)) && keys.every((key) => allowed.has(key));
};
const safeInteger = (value, positive = false) => Number.isSafeInteger(value) && value >= (positive ? 1 : 0);
const boundedInteger = (value, maximum, positive = false) => safeInteger(value, positive) && value <= maximum;
const digest = (value) => typeof value === "string" && DIGEST_PATTERN.test(value);
const strongETag = (value) => typeof value === "string" && STRONG_ETAG_PATTERN.test(value);
const utf8Bytes = (value) => new TextEncoder().encode(value);
const byteLength = (value) => utf8Bytes(value).length;
const wellFormedUTF8 = (value) => {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(utf8Bytes(value)) === value;
  } catch {
    return false;
  }
};
const opaque = (value, maximum = MAX_OPAQUE_BYTES) => typeof value === "string" && value.length > 0 && wellFormedUTF8(value) && byteLength(value) <= maximum && value === value.trim() && value === value.normalize("NFC") && !/[\u0000-\u001f\u007f]/u.test(value);
const dottedIdentifier = (value) => typeof value === "string" && byteLength(value) <= MAX_IDENTIFIER_BYTES && DOTTED_IDENTIFIER_PATTERN.test(value);
const version = (value) => typeof value === "string" && byteLength(value) <= MAX_VERSION_BYTES && VERSION_PATTERN.test(value);
const sameCanonical = (left, right) => canonicalJSONString(left) === canonicalJSONString(right);
const compareUTF8 = (left, right) => {
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
const rejectDuplicateObjectKeys = (text, label) => {
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
        return JSON.parse(text.slice(start, index));
      }
      index += 1;
    }
    throw new Error(`${label} contains an unterminated JSON string`);
  };
  const value = (depth) => {
    nodes += 1;
    if (depth > MAX_JSON_DEPTH || nodes > MAX_JSON_NODES) {
      throw new Error(`${label} exceeds JSON structural bounds`);
    }
    whitespace();
    if (text[index] === "{") {
      index += 1;
      whitespace();
      const keys = /* @__PURE__ */ new Set();
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
const parseJSONRecord = (text, label) => {
  let decoded;
  try {
    rejectDuplicateObjectKeys(text, label);
    decoded = JSON.parse(text);
  } catch (error) {
    if (error instanceof Error && (error.message.includes("duplicate object key") || error.message.includes("structural bounds"))) {
      throw error;
    }
    throw new Error(`${label} is not valid JSON`);
  }
  if (!isRecord(decoded)) throw new Error(`${label} must be one JSON object`);
  return decoded;
};
const decodeScope = (value) => {
  if (!isRecord(value) || value.schema_version !== "control-scope/v1") {
    throw new Error("control scope is invalid");
  }
  if (value.kind === "TENANT") {
    if (!exactKeys(value, ["schema_version", "kind", "tenant_id"]) || !opaque(value.tenant_id)) {
      throw new Error("Tenant control scope is invalid");
    }
    return value;
  }
  if (value.kind !== "WORKSPACE" || !exactKeys(value, ["schema_version", "kind", "tenant_id", "workspace_id"]) || !opaque(value.tenant_id) || !opaque(value.workspace_id)) {
    throw new Error("Workspace control scope is invalid");
  }
  return value;
};
const decodeDigestRef = (value) => {
  if (!isRecord(value) || !exactKeys(value, ["id", "revision", "digest"]) || !opaque(value.id) || !safeInteger(value.revision, true) || !digest(value.digest)) {
    throw new Error("revisioned digest reference is invalid");
  }
  return value;
};
const decodeBasis = (value) => {
  if (!isRecord(value) || !exactKeys(value, ["tenant_id", "pointer_revision", "control", "catalog"]) || !opaque(value.tenant_id) || !safeInteger(value.pointer_revision, true)) {
    throw new Error("published basis is invalid");
  }
  return {
    tenant_id: value.tenant_id,
    pointer_revision: value.pointer_revision,
    control: decodeDigestRef(value.control),
    catalog: decodeDigestRef(value.catalog)
  };
};
const decodeExpectedRef = (value) => {
  if (!isRecord(value) || !exactKeys(value, ["kind", "resource_id", "revision", "digest"]) || value.kind !== "PUBLISHED_POINTER" || !opaque(value.resource_id) || !safeInteger(value.revision, true) || !digest(value.digest)) {
    throw new Error("Published Pointer reference is invalid");
  }
  return value;
};
const decodeView = (value) => {
  if (!isRecord(value) || !exactKeys(value, [
    "schema_version",
    "scope",
    "scope_digest",
    "observed_at_unix_micros",
    "basis",
    "sections"
  ]) || value.schema_version !== "control-view-snapshot/v1" || !digest(value.scope_digest) || !safeInteger(value.observed_at_unix_micros, true) || !Array.isArray(value.sections) || value.sections.length !== 1) {
    throw new Error("Modules view is invalid");
  }
  const section = value.sections[0];
  if (!isRecord(section) || !exactKeys(section, [
    "kind",
    "source_revision",
    "source_digest",
    "item_count",
    "truncated"
  ]) || section.kind !== "MODULES" || !safeInteger(section.source_revision, true) || !digest(section.source_digest) || !boundedInteger(section.item_count, MAX_VISIBLE_BINDINGS) || section.truncated !== false) {
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
const decodePort = (value) => {
  if (!isRecord(value) || !exactKeys(value, ["name", "exact_version"]) || !dottedIdentifier(value.name) || !version(value.exact_version)) {
    throw new Error("module Port reference is invalid");
  }
  return value;
};
const decodeTarget = (value) => {
  if (!isRecord(value)) throw new Error("module Binding target is invalid");
  if (value.kind === "PROFILE") {
    if (!exactKeys(value, ["kind", "profile_id"]) || !opaque(value.profile_id)) {
      throw new Error("Profile Binding target is invalid");
    }
    return value;
  }
  if (value.kind !== "WORKSPACE_CHANNEL_ENDPOINT" || !exactKeys(value, ["kind", "workspace_id", "endpoint_id"]) || !opaque(value.workspace_id) || !opaque(value.endpoint_id)) {
    throw new Error("Workspace Channel Endpoint Binding target is invalid");
  }
  return value;
};
const decodeDigestArray = (value, label) => {
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
const decodeBinding = (value) => {
  if (!isRecord(value) || !exactKeys(value, [
    "target",
    "port",
    "port_binding_index",
    "config_ref",
    "authority_ceiling_ref",
    "static_context_refs",
    "failure_policy"
  ]) || !boundedInteger(value.port_binding_index, MAX_MANIFEST_ENTRIES - 1) || !digest(value.config_ref) || !digest(value.authority_ceiling_ref) || value.failure_policy !== "REQUIRED" && value.failure_policy !== "OPTIONAL") {
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
const EXECUTION_CLASSES = /* @__PURE__ */ new Set([
  "DECLARATIVE",
  "TRUSTED_IN_PROCESS",
  "LOCAL_PROCESS",
  "REMOTE",
  "WASM"
]);
const decodeModuleSummary = (value) => {
  if (!isRecord(value) || !exactKeys(value, [
    "instance_id",
    "module_id",
    "exact_version",
    "artifact_digest",
    "execution_class",
    "adapter_identity",
    "activation_revision",
    "provides",
    "visible_binding_count"
  ]) || !opaque(value.instance_id) || !dottedIdentifier(value.module_id) || !version(value.exact_version) || !digest(value.artifact_digest) || typeof value.execution_class !== "string" || !EXECUTION_CLASSES.has(value.execution_class) || !opaque(value.adapter_identity) || !safeInteger(value.activation_revision, true) || !Array.isArray(value.provides) || value.provides.length < 1 || value.provides.length > MAX_MANIFEST_ENTRIES || !boundedInteger(value.visible_binding_count, MAX_VISIBLE_BINDINGS)) {
    throw new Error("module summary is invalid");
  }
  const provides = value.provides.map(decodePort);
  if (new Set(provides.map((port) => `${port.name}\0${port.exact_version}`)).size !== provides.length) {
    throw new Error("module summary contains duplicate provided Ports");
  }
  return {
    instance_id: value.instance_id,
    module_id: value.module_id,
    exact_version: value.exact_version,
    artifact_digest: value.artifact_digest,
    execution_class: value.execution_class,
    adapter_identity: value.adapter_identity,
    activation_revision: value.activation_revision,
    provides,
    visible_binding_count: value.visible_binding_count
  };
};
const bindingOrderKey = (binding) => [
  binding.target.kind,
  binding.target.kind === "PROFILE" ? binding.target.profile_id : "",
  binding.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" ? binding.target.workspace_id : "",
  binding.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" ? binding.target.endpoint_id : "",
  binding.port.name,
  binding.port.exact_version
];
const compareBinding = (left, right) => {
  const leftKey = bindingOrderKey(left);
  const rightKey = bindingOrderKey(right);
  for (let index = 0; index < leftKey.length; index += 1) {
    const compared = compareUTF8(leftKey[index], rightKey[index]);
    if (compared !== 0) return compared;
  }
  return left.port_binding_index - right.port_binding_index;
};
const decodeModuleDetail = (value, scope) => {
  if (!isRecord(value) || !exactKeys(value, ["summary", "bindings"]) || !Array.isArray(value.bindings) || value.bindings.length > MAX_VISIBLE_BINDINGS) {
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
      if (binding.target.kind !== "WORKSPACE_CHANNEL_ENDPOINT" || binding.target.workspace_id !== scope.workspace_id) {
        throw new Error("module detail contains a Binding outside the requested Workspace");
      }
    }
  }
  if (scope.kind === "WORKSPACE" && bindings.length === 0) {
    throw new Error("Workspace module detail has no visible Binding");
  }
  return { summary, bindings };
};
const scopeDigest = (scope) => domainDigest("freeagent.control-scope/v1", canonicalJSONString(scope));
const basisPointerDigest = (basis) => domainDigest(
  "freeagent.control-published-pointer-ref/v1",
  canonicalJSONString(basis)
);
const validatePointerBasis = async (pointer, basis, scope) => {
  if (basis.tenant_id !== scope.tenant_id || pointer.kind !== "PUBLISHED_POINTER" || pointer.resource_id !== scope.tenant_id || pointer.revision !== basis.pointer_revision || pointer.digest !== await basisPointerDigest(basis)) {
    throw new Error("Published Pointer does not bind the exact Modules basis");
  }
};
const validateModulesEnvelope = async (envelope, requestedScope) => {
  if (scopeKey(envelope.view.scope) !== scopeKey(requestedScope) || !sameCanonical(envelope.view.basis, envelope.basis) || envelope.sourceRevision !== envelope.basis.pointer_revision || envelope.view.sections[0].source_revision !== envelope.sourceRevision || envelope.view.sections[0].source_digest !== envelope.sourceDigest) {
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
const decodePage = (text, scope) => {
  const value = parseJSONRecord(text, "Modules page response");
  if (!exactKeys(
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
  ) || value.schema_version !== MODULES_PAGE_SCHEMA || !digest(value.view_snapshot_digest) || !safeInteger(value.source_revision, true) || !digest(value.source_digest) || value.sort_version !== MODULES_SORT_VERSION || !digest(value.filter_digest) || !Array.isArray(value.items) || value.items.length > MODULES_PAGE_LIMIT || typeof value.has_more !== "boolean" || !digest(value.projection_digest)) {
    throw new Error("Modules page response envelope is invalid");
  }
  const hasCursor = Object.hasOwn(value, "next_cursor");
  if (value.has_more !== hasCursor || hasCursor && (typeof value.next_cursor !== "string" || byteLength(value.next_cursor) > MAX_CURSOR_BYTES || !RAW_URL_SEGMENT_PATTERN.test(value.next_cursor)) || value.has_more && value.items.length !== MODULES_PAGE_LIMIT) {
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
    ...hasCursor ? { next_cursor: value.next_cursor } : {},
    projection_digest: value.projection_digest
  };
};
const decodeDetail = (text, scope) => {
  const value = parseJSONRecord(text, "Module detail response");
  if (!exactKeys(value, [
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
  ]) || value.schema_version !== MODULE_DETAIL_SCHEMA || !digest(value.view_snapshot_digest) || !safeInteger(value.source_revision, true) || !digest(value.source_digest) || !digest(value.projection_digest) || !strongETag(value.strong_etag)) {
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
const pageCache = /* @__PURE__ */ new Map();
const detailCache = /* @__PURE__ */ new Map();
const cursorBindings = /* @__PURE__ */ new Map();
const publishedBindings = /* @__PURE__ */ new Map();
const confirmationBindings = /* @__PURE__ */ new Map();
const detachJSON = (value) => JSON.parse(JSON.stringify(value));
const putBounded = (map, key, value, maximum) => {
  map.delete(key);
  map.set(key, value);
  while (map.size > maximum) {
    const oldest = map.keys().next();
    if (oldest.done) break;
    map.delete(oldest.value);
  }
};
const contextIdentity$1 = (context) => [
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
const sessionIdentityProjection = (context) => Object.fromEntries([
  ["boot_id", context.bootID],
  ["principal_id", context.principalID],
  ["authorization_revision", context.authorizationRevision],
  ["scope_set_digest", context.scopeSetDigest]
]);
const contextCacheKey = (context) => JSON.stringify(contextIdentity$1(context));
const cursorCacheKey = (context, cursor) => JSON.stringify([...contextIdentity$1(context), cursor]);
const confirmationCacheKey = (context, idempotencyKeyDigest, inputDigest, evaluationDigest) => JSON.stringify([
  ...contextIdentity$1(context),
  idempotencyKeyDigest,
  inputDigest,
  evaluationDigest
]);
const modulesQueryKey = (context, cursor = "") => [
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
const moduleDetailQueryKey = (context, instanceID) => [
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
const clearModulesOperationCache = () => {
  publishedBindings.clear();
  confirmationBindings.clear();
};
const clearModulesTransportCache = () => {
  pageCache.clear();
  detailCache.clear();
  cursorBindings.clear();
  clearModulesOperationCache();
};
const withPublishedModulesBasis = (context, source) => ({
  ...context,
  publishedPointer: source.published_pointer,
  publishedBasis: source.basis
});
const validateContext = (context) => {
  if (!exactLoopbackOrigin(context.origin) || typeof window === "undefined" || context.origin !== window.location.origin) {
    throw new ControlModulesError(
      "Modules origin does not match this control page",
      0,
      "CONTROL_ORIGIN_MISMATCH"
    );
  }
  let decodedScope;
  try {
    decodedScope = decodeScope(context.scope);
  } catch {
    throw new ControlModulesError(
      "Modules context is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  if (!opaque(context.bootID) || !opaque(context.sessionID) || !opaque(context.principalID) || !safeInteger(context.authorizationRevision, true) || !digest(context.scopeSetDigest) || !credential(context.csrfToken) || !safeInteger(context.sessionEpoch, true) || !sameCanonical(decodedScope, context.scope) || context.sessionExpiresAtUnixMicros !== void 0 && !safeInteger(context.sessionExpiresAtUnixMicros, true)) {
    throw new ControlModulesError(
      "Modules context is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
};
const scopeHeaders = (context) => {
  const headers = {
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
const readBoundedText = async (response, label) => {
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
  const chunks = [];
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
const fetchExact = async (url, init, fetcher, outcomeMayBeCommitted) => {
  let response;
  try {
    response = await fetcher(url, init);
  } catch (error) {
    const cancelled = init.signal?.aborted === true;
    throw new ControlModulesError(
      cancelled ? "the Modules request was cancelled" : "the Modules request did not return a response",
      0,
      cancelled ? "CANCELLED" : "TRANSPORT_ERROR",
      "",
      0,
      outcomeMayBeCommitted
    );
  }
  if (response.redirected || response.type === "opaqueredirect" || response.url !== "" && response.url !== url) {
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
const readControlError = async (response, outcomeMayBeCommitted) => {
  let text;
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
const requireJSONSuccess = async (response, label, outcomeMayBeCommitted, allowETag) => {
  if (!response.ok) throw await readControlError(response, outcomeMayBeCommitted);
  if (response.headers.get("Content-Type") !== "application/json" || !allowETag && response.headers.get("ETag") !== null) {
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
const strongETagWire = async (context, projectionDigest, computedScopeDigest, domain) => `"${await domainDigest(
  domain,
  canonicalJSONString({
    ...sessionIdentityProjection(context),
    scope_digest: computedScopeDigest,
    projection_digest: projectionDigest
  })
)}"`;
const httpStrongETag = async (domain, applicationETag, exactBody) => `"${await domainDigest(domain, `${applicationETag}
${exactBody}`)}"`;
const emptyFilterDigest = () => domainDigest(
  "freeagent.control-modules-empty-filter/v1",
  canonicalJSONString({ filters: [] })
);
const decodedCursor = async (context, page) => {
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
const validatePageDigests = async (context, page, rawBody, responseETag, inputCursor, cursorBinding) => {
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
  if (sectionCount < seenCount + page.items.length || (page.has_more ? sectionCount <= seenCount + page.items.length : sectionCount !== seenCount + page.items.length)) {
    throw new Error("Modules page cardinality does not match its source view");
  }
  if (cursorBinding !== void 0) {
    if (cursorBinding.sourceRevision !== page.source_revision || cursorBinding.sourceDigest !== page.source_digest || cursorBinding.viewSnapshotDigest !== page.view_snapshot_digest || cursorBinding.observedAtUnixMicros !== page.view.observed_at_unix_micros || !sameCanonical(cursorBinding.basis, page.basis) || !sameCanonical(cursorBinding.publishedPointer, page.published_pointer) || page.items.length > 0 && compareUTF8(page.items[0].instance_id, cursorBinding.afterInstanceID) <= 0) {
      throw new Error("Modules page cursor does not bind the exact source snapshot");
    }
  }
  const nextCursor = page.has_more ? await decodedCursor(context, page) : void 0;
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
    ...inputCursor === "" ? {} : { after_instance_id: cursorBinding?.afterInstanceID },
    items: page.items,
    has_more: page.has_more,
    ...nextCursor === void 0 ? {} : { next_cursor: nextCursor }
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
const validateDetailDigests = async (context, detail, instanceID, rawBody, responseETag) => {
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
  if (detail.module.summary.instance_id !== instanceID || detail.view.sections[0].item_count < 1) {
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
const storePublishedBinding = (context, basis, pointer) => {
  putBounded(
    publishedBindings,
    contextCacheKey(context),
    { basis: detachJSON(basis), pointer: detachJSON(pointer) },
    MAX_TRANSPORT_CACHE_ENTRIES
  );
};
const fetchModulesPage = async (context, cursor, signal, fetcher = fetch) => {
  validateContext(context);
  const cursorValue = cursor ?? "";
  if (cursorValue !== "" && (byteLength(cursorValue) > MAX_CURSOR_BYTES || !RAW_URL_SEGMENT_PATTERN.test(cursorValue))) {
    throw new ControlModulesError(
      "Modules cursor is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  const cursorBinding = cursorValue === "" ? void 0 : cursorBindings.get(cursorCacheKey(context, cursorValue));
  if (cursorValue !== "" && cursorBinding === void 0) {
    throw new ControlModulesError(
      "Modules cursor is not bound to this live session and scope",
      0,
      "CURSOR_CONTEXT_MISSING"
    );
  }
  const queryKey = modulesQueryKey(context, cursorValue);
  const cacheKey2 = JSON.stringify(queryKey);
  const cached = pageCache.get(cacheKey2);
  const headers = scopeHeaders(context);
  if (cached !== void 0) headers["If-None-Match"] = cached.etag;
  const query = cursorValue === "" ? `limit=${MODULES_PAGE_LIMIT}` : `limit=${MODULES_PAGE_LIMIT}&cursor=${cursorValue}`;
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
    if (cached === void 0) {
      throw new ControlModulesError(
        "Modules page returned 304 without a local representation",
        304,
        "INVALID_304"
      );
    }
    let body;
    try {
      body = await readBoundedText(response, "Modules page 304 response");
    } catch {
      body = "invalid";
    }
    if (response.headers.get("ETag") !== cached.etag || response.headers.get("Content-Type") !== null || body !== "") {
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
  let page;
  let validation;
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
    cacheKey2,
    { etag, data: detachJSON(page) },
    MAX_TRANSPORT_CACHE_ENTRIES
  );
  storePublishedBinding(context, page.basis, page.published_pointer);
  if (page.next_cursor !== void 0 && validation.nextCursor !== void 0) {
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
const moduleInstancePathSegment = (value) => {
  if (!opaque(value)) return null;
  const bytes = utf8Bytes(value);
  const binary = [...bytes].map((byte) => String.fromCharCode(byte)).join("");
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/u, "");
};
const fetchModuleDetail = async (context, instanceID, signal, fetcher = fetch) => {
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
  const cacheKey2 = JSON.stringify(key);
  const cached = detailCache.get(cacheKey2);
  const headers = scopeHeaders(context);
  headers[MODULE_INSTANCE_ID_ENCODING_HEADER] = MODULE_INSTANCE_ID_ENCODING;
  if (cached !== void 0) headers["If-None-Match"] = cached.etag;
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
    if (cached === void 0) {
      throw new ControlModulesError(
        "Module detail returned 304 without a local representation",
        304,
        "INVALID_304"
      );
    }
    let body;
    try {
      body = await readBoundedText(response, "Module detail 304 response");
    } catch {
      body = "invalid";
    }
    if (response.headers.get("ETag") !== cached.etag || response.headers.get("Content-Type") !== null || body !== "") {
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
  let detail;
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
    cacheKey2,
    { etag, data: detachJSON(detail) },
    MAX_TRANSPORT_CACHE_ENTRIES
  );
  storePublishedBinding(context, detail.basis, detail.published_pointer);
  return detail;
};
const validateDisableBody = (body) => {
  if (!isRecord(body) || !exactKeys(body, [
    "schema_version",
    "expected_pointer_revision",
    "binding_target",
    "instance_id",
    "port"
  ]) || body.schema_version !== MODULE_DISABLE_BODY_SCHEMA || !safeInteger(body.expected_pointer_revision, true) || !opaque(body.instance_id) || !isRecord(body.binding_target) || !exactKeys(body.binding_target, ["kind", "profile_id"]) || body.binding_target.kind !== "PROFILE" || !opaque(body.binding_target.profile_id) || !isRecord(body.port) || !exactKeys(body.port, ["name", "exact_version"]) || body.port.name !== "context.provide" || body.port.exact_version !== "v1") {
    throw new ControlModulesError(
      "MODULE_DISABLE body is outside the narrow Profile context.provide/v1 contract",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  return body;
};
const canonicalModuleDisableBody = (body) => canonicalJSONString(validateDisableBody(body));
const createIdempotencyKey = () => {
  let bytes;
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
    const key = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
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
const validateIdempotencyKey = (key) => {
  if (typeof key !== "string" || byteLength(key) < 16 || byteLength(key) > 128 || !/^[!-~]+$/u.test(key)) {
    throw new ControlModulesError(
      "Idempotency-Key must contain 16-128 non-space printable ASCII bytes",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
};
const inputDigestFor = (canonicalBody) => domainDigest(
  "freeagent.control-module-disable-dry-run-input/v1",
  canonicalBody
);
const idempotencyDigestFor = (key) => domainDigest("freeagent.control-idempotency-key/v1", key);
const planDigestFor = (context, body) => domainDigest(
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
const resolveMutationBasis = async (context, body) => {
  if (context.scope.kind !== "TENANT") {
    throw new ControlModulesError(
      "MODULE_DISABLE requires exact Tenant scope",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  let binding;
  if (context.publishedPointer !== void 0 || context.publishedBasis !== void 0) {
    if (context.publishedPointer === void 0 || context.publishedBasis === void 0) {
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
  if (binding === void 0) {
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
const sameRef = (left, right) => left.kind === right.kind && left.resource_id === right.resource_id && left.revision === right.revision && left.digest === right.digest;
const nextBasis = (before, after) => before.tenant_id === after.tenant_id && after.pointer_revision === before.pointer_revision + 1 && after.control.revision === before.control.revision + 1 && after.catalog.revision === before.catalog.revision + 1 && after.control.id !== before.control.id && after.control.digest !== before.control.digest && after.catalog.id !== before.catalog.id && after.catalog.digest !== before.catalog.digest;
const decodeDisableProjection = (value) => {
  if (!isRecord(value) || !exactKeys(
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
  ) || value.disposition !== "ALREADY_APPLIED" && value.disposition !== "NO_CHANGE" && value.disposition !== "WOULD_APPLY" || !digest(value.plan_digest) || !opaque(value.instance_id) || value.candidate_state !== "PROJECTED_NOT_RESERVED" || value.catalog_change !== "NONE" && value.catalog_change !== "RETAIN_INSTANCE" && value.catalog_change !== "REMOVE_INSTANCE") {
    throw new Error("MODULE_DISABLE projection is invalid");
  }
  let removal;
  if (Object.hasOwn(value, "binding_removal")) {
    const decoded = decodeBinding(value.binding_removal);
    if (decoded.target.kind !== "PROFILE" || decoded.port.name !== "context.provide" || decoded.port.exact_version !== "v1" || decoded.failure_policy !== "OPTIONAL") {
      throw new Error("MODULE_DISABLE Binding removal is outside the narrow contract");
    }
    removal = decoded;
  }
  return {
    disposition: value.disposition,
    plan_digest: value.plan_digest,
    instance_id: value.instance_id,
    precondition_basis: decodeBasis(value.precondition_basis),
    observed_basis: decodeBasis(value.observed_basis),
    candidate_basis: decodeBasis(value.candidate_basis),
    candidate_state: "PROJECTED_NOT_RESERVED",
    ...removal === void 0 ? {} : { binding_removal: removal },
    catalog_change: value.catalog_change
  };
};
const validateDisableProjection = async (context, projection, body, expected) => {
  if (projection.plan_digest !== await planDigestFor(context, body) || projection.instance_id !== body.instance_id || projection.precondition_basis.tenant_id !== context.scope.tenant_id || projection.observed_basis.tenant_id !== context.scope.tenant_id || projection.candidate_basis.tenant_id !== context.scope.tenant_id) {
    throw new Error("MODULE_DISABLE projection does not bind its exact input");
  }
  const preconditionRef = {
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
      if (!sameCanonical(projection.precondition_basis, projection.observed_basis) || !sameCanonical(projection.observed_basis, projection.candidate_basis) || projection.catalog_change !== "NONE" || removal !== void 0) {
        throw new Error("MODULE_DISABLE NO_CHANGE projection is invalid");
      }
      break;
    case "ALREADY_APPLIED":
      if (!nextBasis(projection.precondition_basis, projection.observed_basis) || !sameCanonical(projection.observed_basis, projection.candidate_basis) || projection.catalog_change !== "NONE" || removal !== void 0) {
        throw new Error("MODULE_DISABLE ALREADY_APPLIED projection is invalid");
      }
      break;
    case "WOULD_APPLY":
      if (!sameCanonical(projection.precondition_basis, projection.observed_basis) || !nextBasis(projection.observed_basis, projection.candidate_basis) || projection.catalog_change === "NONE" || removal === void 0 || !sameCanonical(removal.target, body.binding_target) || !sameCanonical(removal.port, body.port)) {
        throw new Error("MODULE_DISABLE WOULD_APPLY projection is invalid");
      }
      break;
  }
};
const confirmationFromRequest = (request) => ({
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
const decodeOperationRequest = async (value, context, inputs) => {
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
  if (!exactKeys(value, inputs.intent === "MUTATE" ? [...commonKeys, ...mutationKeys] : commonKeys) || value.schema_version !== OPERATION_REQUEST_SCHEMA || value.principal_id !== context.principalID || value.capability !== "OPERATE_MODULES" || value.operation !== "MODULE_DISABLE" || value.intent !== inputs.intent || !digest(value.scope_digest) || value.input_digest !== inputs.inputDigest) {
    throw new Error("Control operation Request is outside its exact call input");
  }
  const scope = decodeScope(value.scope);
  const expected = decodeExpectedRef(value.expected_ref);
  if (scopeKey(scope) !== scopeKey(context.scope) || value.scope_digest !== await scopeDigest(context.scope) || !sameRef(expected, inputs.expected)) {
    throw new Error("Control operation Request scope or precondition is invalid");
  }
  const request = {
    schema_version: OPERATION_REQUEST_SCHEMA,
    principal_id: value.principal_id,
    capability: "OPERATE_MODULES",
    scope,
    scope_digest: value.scope_digest,
    operation: "MODULE_DISABLE",
    intent: inputs.intent,
    ...inputs.intent === "MUTATE" ? {
      idempotency_key_digest: value.idempotency_key_digest
    } : {},
    input_digest: value.input_digest,
    ...inputs.intent === "MUTATE" ? {
      operation_evaluation_digest: value.operation_evaluation_digest
    } : {},
    expected_ref: expected,
    ...inputs.intent === "MUTATE" ? { confirmation_digest: value.confirmation_digest } : {}
  };
  if (inputs.intent === "MUTATE") {
    if (value.idempotency_key_digest !== inputs.idempotencyKeyDigest || value.operation_evaluation_digest !== inputs.evaluationDigest || !digest(value.confirmation_digest)) {
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
const decodeDomainReceipt = (value) => {
  if (!isRecord(value) || !exactKeys(value, ["kind", "id", "digest"]) || value.kind !== "MODULE_DISABLE" || !opaque(value.id) || !digest(value.digest)) {
    throw new Error("MODULE_DISABLE domain receipt reference is invalid");
  }
  return value;
};
const decodeOperationReceipt = async (value, request, requestDigest, expected, idempotencyKeyDigest) => {
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
  if (!exactKeys(value, dryRun ? base : mutationRequired, dryRun ? [] : mutationOptional) || value.schema_version !== OPERATION_RECEIPT_SCHEMA || value.request_digest !== requestDigest || value.intent !== request.intent || value.principal_id !== request.principal_id || value.scope_digest !== request.scope_digest || value.operation !== "MODULE_DISABLE" || value.error_code !== "NONE" || !safeInteger(value.completed_at_unix_micros, true)) {
    throw new Error("Control operation Receipt does not bind its Request");
  }
  const preRef = decodeExpectedRef(value.pre_ref);
  if (!sameRef(preRef, expected)) {
    throw new Error("Control operation Receipt precondition is invalid");
  }
  let receipt;
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
    if (value.idempotency_key_digest !== idempotencyKeyDigest || value.status !== "NO_CHANGE" && value.status !== "APPLIED" || value.replay_disposition !== "RETURN_EXACT_RECEIPT") {
      throw new Error("MODULE_DISABLE mutation Receipt outcome is invalid");
    }
    const postRef = decodeExpectedRef(value.post_ref);
    let domainReceipt;
    if (value.status === "NO_CHANGE") {
      if (!sameRef(postRef, preRef) || Object.hasOwn(value, "domain_receipt")) {
        throw new Error("MODULE_DISABLE NO_CHANGE Receipt is invalid");
      }
    } else {
      if (postRef.kind !== preRef.kind || postRef.resource_id !== preRef.resource_id || postRef.revision !== preRef.revision + 1 || postRef.digest === preRef.digest || !Object.hasOwn(value, "domain_receipt")) {
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
      ...domainReceipt === void 0 ? {} : { domain_receipt: domainReceipt },
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
const decodeEvaluation = async (value, context, body, inputDigest, expected) => {
  if (!isRecord(value) || !exactKeys(value, [
    "schema_version",
    "operation",
    "input_digest",
    "expected_ref",
    "projection"
  ]) || value.schema_version !== MODULE_DISABLE_EVALUATION_SCHEMA || value.operation !== "MODULE_DISABLE" || value.input_digest !== inputDigest) {
    throw new Error("MODULE_DISABLE evaluation is invalid");
  }
  const expectedRef = decodeExpectedRef(value.expected_ref);
  if (!sameRef(expectedRef, expected)) {
    throw new Error("MODULE_DISABLE evaluation precondition is invalid");
  }
  const projection = decodeDisableProjection(value.projection);
  await validateDisableProjection(context, projection, body, expected);
  const evaluation = {
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
const decodeStatement = async (value, request) => {
  if (!isRecord(value) || !exactKeys(value, [
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
  ])) {
    throw new Error("Control confirmation Statement is invalid");
  }
  const expected = confirmationFromRequest(request);
  const statement = {
    schema_version: value.schema_version,
    principal_id: value.principal_id,
    capability: value.capability,
    intent: value.intent,
    operation: value.operation,
    scope: decodeScope(value.scope),
    scope_digest: value.scope_digest,
    idempotency_key_digest: value.idempotency_key_digest,
    input_digest: value.input_digest,
    operation_evaluation_digest: value.operation_evaluation_digest,
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
const decodeDryRunResult = async (text, context, body, inputDigest, expected) => {
  const value = parseJSONRecord(text, "MODULE_DISABLE Dry-run response");
  if (!exactKeys(value, [
    "schema_version",
    "request",
    "request_digest",
    "receipt",
    "receipt_digest",
    "projection"
  ]) || value.schema_version !== MODULE_DISABLE_DRY_RUN_RESULT_SCHEMA || !digest(value.request_digest) || !digest(value.receipt_digest)) {
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
  };
};
const decodeConfirmationResult = async (text, context, body, inputDigest, expected, idempotencyKeyDigest) => {
  const value = parseJSONRecord(text, "MODULE_DISABLE confirmation response");
  if (!exactKeys(value, [
    "schema_version",
    "request",
    "request_digest",
    "evaluation",
    "evaluation_digest",
    "statement",
    "statement_digest",
    "confirmation_proof",
    "expires_at_unix_micros"
  ]) || value.schema_version !== MODULE_DISABLE_CONFIRMATION_RESULT_SCHEMA || !digest(value.request_digest) || !digest(value.evaluation_digest) || !digest(value.statement_digest) || !credential(value.confirmation_proof) || !safeInteger(value.expires_at_unix_micros, true)) {
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
  if (value.statement_digest !== statementDigest || request.confirmation_digest !== statementDigest) {
    throw new Error("MODULE_DISABLE confirmation Statement digest is invalid");
  }
  const nowMicros = Date.now() * 1e3;
  if (value.expires_at_unix_micros <= nowMicros) {
    throw new Error("MODULE_DISABLE confirmation is already expired");
  }
  if (value.expires_at_unix_micros > nowMicros + 125e6) {
    throw new Error("MODULE_DISABLE confirmation expiry exceeds its two-minute bound");
  }
  if (context.sessionExpiresAtUnixMicros !== void 0 && value.expires_at_unix_micros > context.sessionExpiresAtUnixMicros) {
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
  };
};
const decodeMutationResult = async (text, context, inputDigest, expected, idempotencyKeyDigest, evaluationDigest) => {
  const value = parseJSONRecord(text, "MODULE_DISABLE mutation response");
  if (!exactKeys(value, [
    "schema_version",
    "request",
    "request_digest",
    "receipt",
    "receipt_digest"
  ]) || value.schema_version !== MODULE_DISABLE_MUTATION_RESULT_SCHEMA || !digest(value.request_digest) || !digest(value.receipt_digest)) {
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
  };
};
const prepareOperation = async (context, body) => {
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
const dryRunModuleDisable = async (context, body, signal, fetcher = fetch) => {
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
const issueModuleDisableConfirmation = async (context, body, key, signal, fetcher = fetch) => {
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
  let result;
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
      error instanceof Error ? error.message : "MODULE_DISABLE confirmation response is invalid",
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
const mutateModuleDisable = async (context, body, key, evaluationDigest, proof, signal, fetcher = fetch) => {
  validateIdempotencyKey(key);
  if (!digest(evaluationDigest) || proof !== void 0 && !credential(proof)) {
    throw new ControlModulesError(
      "MODULE_DISABLE mutation evaluation digest or proof is invalid",
      0,
      "INVALID_CLIENT_INPUT"
    );
  }
  const prepared = await prepareOperation(context, body);
  const idempotencyKeyDigest = await idempotencyDigestFor(key);
  if (proof !== void 0 && !confirmationBindings.has(
    confirmationCacheKey(
      context,
      idempotencyKeyDigest,
      prepared.inputDigest,
      evaluationDigest
    )
  )) {
    throw new ControlModulesError(
      "MODULE_DISABLE proof is not bound to a confirmation issued in this live memory context",
      0,
      "CONFIRMATION_CONTEXT_MISSING"
    );
  }
  const headers = {
    ...prepared.headers,
    "Idempotency-Key": key,
    "X-FreeAgent-Operation-Evaluation-Digest": evaluationDigest
  };
  if (proof !== void 0) headers["X-FreeAgent-Confirmation"] = proof;
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
      error instanceof Error ? error.message : "MODULE_DISABLE mutation response is invalid",
      response.status,
      "INVALID_RESPONSE",
      "",
      0,
      true
    );
  }
};
const RESUME_STORAGE_KEY = "freeagent.control.resume.v1";
const RESUME_STORAGE_SCHEMA = "freeagent.control-resume-storage/v1";
class ControlSessionError extends Error {
  status;
  code;
  correlationID;
  constructor(message, status = 0, code = "TRANSPORT_ERROR", correlationID = "") {
    super(message);
    this.name = "ControlSessionError";
    this.status = status;
    this.code = code;
    this.correlationID = correlationID;
  }
}
const canonicalBootstrapBody = (capability) => `{"capability":${JSON.stringify(capability)},"schema_version":"control-bootstrap-exchange/v1"}`;
const canonicalResumeBody = (resumeCredential) => `{"resume_credential":${JSON.stringify(resumeCredential)},"schema_version":"control-session-resume/v1"}`;
const exactJSONResponse = async (response, label) => {
  if (response.headers.get("Content-Type") !== "application/json") {
    throw new ControlSessionError(`${label} returned an invalid content type`, response.status);
  }
  return response.text();
};
const responseError = async (response) => {
  let text = "";
  try {
    text = await response.text();
  } catch {
    return new ControlSessionError("the control service returned an unreadable error", response.status);
  }
  const decoded = decodeControlError(text);
  if (decoded === null) {
    return new ControlSessionError("the control service returned an invalid error", response.status);
  }
  return new ControlSessionError(
    decoded.message,
    response.status,
    decoded.code,
    decoded.correlation_id
  );
};
const exchangeHandoff = async (handoff, fetcher = fetch) => {
  if (handoff.origin !== window.location.origin) {
    throw new ControlSessionError(
      "handoff origin does not match this control page",
      0,
      "HANDOFF_ORIGIN_MISMATCH"
    );
  }
  const response = await fetcher(`${handoff.origin}/control/bootstrap`, {
    method: "POST",
    credentials: "same-origin",
    redirect: "error",
    headers: { "Content-Type": "application/json" },
    body: canonicalBootstrapBody(handoff.capability)
  });
  if (!response.ok) throw await responseError(response);
  const text = await exactJSONResponse(response, "bootstrap exchange");
  return decodeSessionExchange(text, BOOTSTRAP_RESPONSE_SCHEMA);
};
const resumeSession = async (origin, resumeCredential, fetcher = fetch) => {
  if (!exactLoopbackOrigin(origin) || origin !== window.location.origin || !credential(resumeCredential)) {
    throw new ControlSessionError("stored session metadata is invalid", 0, "STORED_SESSION_INVALID");
  }
  const response = await fetcher(`${origin}/control/session/resume`, {
    method: "POST",
    credentials: "include",
    redirect: "error",
    headers: { "Content-Type": "application/json" },
    body: canonicalResumeBody(resumeCredential)
  });
  if (!response.ok) throw await responseError(response);
  const text = await exactJSONResponse(response, "session resume");
  return decodeSessionExchange(text, RESUME_RESPONSE_SCHEMA);
};
const decodeStoredResume = (text) => {
  let value;
  try {
    value = JSON.parse(text);
  } catch {
    return null;
  }
  if (typeof value !== "object" || value === null || Array.isArray(value) || Object.keys(value).sort().join("\n") !== ["origin", "resume_credential", "schema_version"].sort().join("\n")) return null;
  const record = value;
  if (record.schema_version !== RESUME_STORAGE_SCHEMA || !exactLoopbackOrigin(record.origin) || !credential(record.resume_credential)) return null;
  return record;
};
const readStoredResume = () => {
  try {
    const value = sessionStorage.getItem(RESUME_STORAGE_KEY);
    if (value === null) return null;
    const decoded = decodeStoredResume(value);
    if (decoded === null || decoded.origin !== window.location.origin) {
      sessionStorage.removeItem(RESUME_STORAGE_KEY);
      return null;
    }
    return decoded;
  } catch {
    return null;
  }
};
const storeResume = (origin, value) => {
  if (origin !== window.location.origin || !exactLoopbackOrigin(origin) || !credential(value)) {
    throw new Error("resume session metadata is invalid");
  }
  const encoded = JSON.stringify({
    schema_version: RESUME_STORAGE_SCHEMA,
    origin,
    resume_credential: value
  });
  try {
    sessionStorage.setItem(RESUME_STORAGE_KEY, encoded);
    return true;
  } catch {
    return false;
  }
};
const clearStoredResume = () => {
  try {
    sessionStorage.removeItem(RESUME_STORAGE_KEY);
  } catch {
  }
};
const shouldDiscardResume = (error) => error instanceof ControlSessionError && (error.code === "STORED_SESSION_INVALID" || error.code === "UNAUTHENTICATED" || error.code === "SESSION_EXPIRED" || error.status === 401);
function HandoffPanel({ busy, error, onFile }) {
  return /* @__PURE__ */ jsxRuntimeExports.jsx("main", { className: "entry", "aria-labelledby": "entry-title", children: /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "entry__panel", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "FreeAgent Control" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { id: "entry-title", children: "Open a Control session" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "lede", children: "Select the short-lived handoff JSON created by this exact local Control process. The capability is exchanged once and is never retained by this page." }),
    error !== "" && /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "notice notice--error", role: "alert", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Session could not be opened." }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: error })
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("label", { className: `file-picker${busy ? " file-picker--busy" : ""}`, children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: busy ? "Opening session…" : "Choose handoff JSON" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx(
        "input",
        {
          type: "file",
          accept: "application/json,.json",
          disabled: busy,
          onChange: onFile
        }
      )
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("dl", { className: "security-notes", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Authority" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: "Only server-authorized scopes are selectable." })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Credentials" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: "CSRF stays in memory; resume data stays in this tab session." })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Surface" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: "Overview stays read-only; Modules exposes only confirmed disable operations." })
      ] })
    ] })
  ] }) });
}
function LoadingPanel({ label = "Loading Overview…" }) {
  return /* @__PURE__ */ jsxRuntimeExports.jsx("main", { className: "entry", "aria-busy": "true", "aria-labelledby": "loading-title", children: /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "entry__panel entry__panel--compact", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "FreeAgent Control" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { id: "loading-title", children: label }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("div", { className: "loading-bar", "aria-hidden": "true", children: /* @__PURE__ */ jsxRuntimeExports.jsx("span", {}) }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "muted", children: "Waiting for one bounded, authenticated response." })
  ] }) });
}
function PermissionPanel({ principalID }) {
  return /* @__PURE__ */ jsxRuntimeExports.jsx("main", { className: "entry", "aria-labelledby": "permission-title", children: /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "entry__panel entry__panel--compact", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Read-only access" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { id: "permission-title", children: "Overview permission denied" }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "lede", children: [
      "Session principal ",
      /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: principalID }),
      " does not currently hold the OBSERVE capability for this view. No Overview request was sent."
    ] })
  ] }) });
}
function SessionInvalidBoundary({
  error,
  children
}) {
  if (error !== null && isSessionInvalid(error)) {
    return /* @__PURE__ */ jsxRuntimeExports.jsx(LoadingPanel, { label: "Closing an expired session…" });
  }
  return /* @__PURE__ */ jsxRuntimeExports.jsx(jsxRuntimeExports.Fragment, { children });
}
function FatalOverviewPanel({
  permissionDenied,
  message,
  correlationID,
  onRetry
}) {
  return /* @__PURE__ */ jsxRuntimeExports.jsx("main", { className: "entry", "aria-labelledby": "overview-error-title", children: /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "entry__panel entry__panel--compact", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Read-only Overview" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { id: "overview-error-title", children: permissionDenied ? "Scope permission denied" : "Overview unavailable" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "lede", children: message }),
    correlationID !== "" && /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "correlation", children: [
      "Correlation: ",
      correlationID
    ] }),
    !permissionDenied && /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button button--primary", type: "button", onClick: onRetry, children: "Retry read" })
  ] }) });
}
const formatMicros$1 = (value) => {
  const date = new Date(Math.floor(value / 1e3));
  return Number.isNaN(date.getTime()) ? "Invalid time" : date.toLocaleString();
};
const shortDigest$1 = (value) => `${value.slice(0, 10)}…${value.slice(-8)}`;
const sectionLabels = {
  workspaces: "Workspaces",
  runs: "Recent runs",
  unknown: "Unknown outcomes",
  learning: "Learning proposals",
  "module-candidates": "Module candidates",
  usage: "Usage reconciliation"
};
const sectionTruncated = (overview, section) => {
  switch (section) {
    case "workspaces":
      return overview.workspaces_truncated;
    case "runs":
      return overview.runs_truncated;
    case "unknown":
      return overview.unknown_truncated;
    case "learning":
      return overview.learning_truncated;
    case "module-candidates":
      return overview.module_candidates_truncated;
    case "usage":
      return overview.usage_truncated;
  }
};
function ResultList({ results }) {
  if (results.length === 0) {
    return /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "empty-copy", children: "No items in this current response." });
  }
  return /* @__PURE__ */ jsxRuntimeExports.jsx("ul", { className: "result-list", children: results.map((result) => /* @__PURE__ */ jsxRuntimeExports.jsx("li", { children: /* @__PURE__ */ jsxRuntimeExports.jsxs("a", { href: detailHash(result), className: "result-link", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "result-link__title", children: result.title }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "result-link__summary", children: result.summary }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "result-link__arrow", "aria-hidden": "true", children: "↗" })
  ] }) }, `${result.section}:${result.id}`)) });
}
function DetailDrawer({
  snapshot
}) {
  if (snapshot === null) return null;
  const { result, scope } = snapshot;
  const frozenScope = scope.kind === "WORKSPACE" ? `${scope.tenant_id} / ${scope.workspace_id}` : scope.tenant_id;
  return /* @__PURE__ */ jsxRuntimeExports.jsxs("aside", { className: "drawer", role: "dialog", "aria-modal": "true", "aria-labelledby": "detail-title", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("div", { className: "drawer__scrim", "aria-hidden": "true" }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "drawer__panel", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsxs("header", { className: "drawer__header", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Current response detail" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("h2", { id: "detail-title", children: result?.title ?? "Detail unavailable" })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("a", { className: "drawer__close", href: "#", "aria-label": "Close detail", children: "×" })
      ] }),
      result === null ? /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "empty-copy", children: "This deep link is not present in the current authorized scope response." }) : /* @__PURE__ */ jsxRuntimeExports.jsxs(jsxRuntimeExports.Fragment, { children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "drawer__summary", children: [
          sectionLabels[result.section],
          " · ",
          result.summary
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "drawer__scope", children: [
          "Frozen ",
          scope.kind.toLowerCase(),
          " scope: ",
          frozenScope
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("pre", { children: JSON.stringify(result.value, null, 2) })
      ] })
    ] })
  ] });
}
function OverviewPage({
  session,
  overview,
  scopeChoices,
  selectedScopeKey,
  search,
  stale,
  refreshing,
  backgroundError,
  detailSnapshot,
  onScopeChange,
  onSearchChange,
  onRefresh
}) {
  const allResults = overviewSearchResults(overview, search);
  const unfilteredResults = overviewSearchResults(overview, "");
  const itemCount = unfilteredResults.length;
  const groups = Object.keys(sectionLabels).map((section) => ({
    section,
    results: allResults.filter((result) => result.section === section).slice(0, search.trim() === "" ? 12 : void 0)
  }));
  return /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "control-shell", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsxs("header", { className: "topbar", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsxs("a", { className: "brand", href: "#overview", "aria-label": "FreeAgent Control Overview", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "brand__mark", "aria-hidden": "true", children: "F" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "FreeAgent Control" })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "topbar__status", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: `status-dot${stale ? " status-dot--stale" : ""}`, "aria-hidden": "true" }),
        refreshing ? "Refreshing" : stale ? "Stale" : "Current"
      ] })
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("aside", { className: "sidebar", "aria-label": "Control navigation", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "sidebar__label", children: "Control" }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("nav", { children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("a", { href: "#overview", children: "Status" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("a", { href: "#modules", children: "Modules" }),
        groups.map(({ section }) => /* @__PURE__ */ jsxRuntimeExports.jsx("a", { href: `#section-${section}`, children: sectionLabels[section] }, section))
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "session-card", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Session" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: session.principal_id }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("small", { children: [
          "Expires ",
          formatMicros$1(session.expires_at_unix_micros)
        ] })
      ] })
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("main", { className: "content", id: "overview", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "page-heading", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Authorized read-only surface" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { children: "Overview" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "lede", children: "A bounded projection of the current published basis and recent safe facts." })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: onRefresh, disabled: refreshing, children: refreshing ? "Reading…" : "Refresh read" })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "toolbar", "aria-label": "Overview controls", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("label", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Authorized scope" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("select", { value: selectedScopeKey, onChange: (event) => onScopeChange(event.target.value), children: scopeChoices.map((choice) => /* @__PURE__ */ jsxRuntimeExports.jsx("option", { value: choice.key, children: choice.label }, choice.key)) })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("label", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Search this response" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx(
            "input",
            {
              type: "search",
              value: search,
              onChange: (event) => onSearchChange(event.target.value),
              placeholder: "ID, state, kind, version…",
              autoComplete: "off"
            }
          )
        ] })
      ] }),
      backgroundError !== "" && /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "notice notice--warning", role: "status", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "The last refresh failed." }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("span", { children: [
          backgroundError,
          " The prior verified response remains visible as stale."
        ] })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "metric-grid", "aria-label": "Overview status", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("article", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Pointer revision" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: overview.basis.pointer_revision.toLocaleString() }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("small", { children: overview.published_pointer.kind })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("article", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Control generation" }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("strong", { children: [
            overview.basis.control.id,
            " · r",
            overview.basis.control.revision
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("small", { className: "digest", children: shortDigest$1(overview.basis.control.digest) })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("article", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Catalog generation" }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("strong", { children: [
            overview.basis.catalog.id,
            " · r",
            overview.basis.catalog.revision
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("small", { className: "digest", children: shortDigest$1(overview.basis.catalog.digest) })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("article", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Current response" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: itemCount.toLocaleString() }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("small", { children: "authorized safe items" })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("article", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Observed" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: formatMicros$1(overview.view.observed_at_unix_micros) }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("small", { children: stale ? "stale local representation" : "verified representation" })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("article", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Projection" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { className: "digest", children: shortDigest$1(overview.projection_digest) }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("small", { children: "semantic digest verified" })
        ] })
      ] }),
      itemCount === 0 && search.trim() === "" && /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "empty-state", "aria-labelledby": "empty-title", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Current response" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("h2", { id: "empty-title", children: "No recent items" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("p", { children: "The selected authorized scope returned an empty, verified Overview." })
      ] }),
      search.trim() !== "" && allResults.length === 0 && /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "empty-state", "aria-labelledby": "search-empty-title", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Local search" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("h2", { id: "search-empty-title", children: "No response items match" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("p", { children: "The search examined only the items already present in this authorized response." })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("div", { className: "section-grid", children: groups.map(({ section, results }) => /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "data-section", id: `section-${section}`, children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("header", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
            /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Recent safe facts" }),
            /* @__PURE__ */ jsxRuntimeExports.jsx("h2", { children: sectionLabels[section] })
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "section-meta", children: [
            /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: results.length }),
            sectionTruncated(overview, section) && /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "tag", children: "truncated" })
          ] })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsx(ResultList, { results })
      ] }, section)) }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("footer", { className: "page-footer", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("span", { children: [
          "View ",
          shortDigest$1(overview.view_snapshot_digest)
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("span", { children: [
          "Scope ",
          shortDigest$1(overview.view.scope_digest)
        ] })
      ] })
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsx(DetailDrawer, { snapshot: detailSnapshot })
  ] });
}
const shortDigest = (value) => value.length <= 20 ? value : `${value.slice(0, 10)}...${value.slice(-8)}`;
const formatMicros = (value) => {
  const date = new Date(Math.floor(value / 1e3));
  return Number.isNaN(date.getTime()) ? "Invalid time" : date.toLocaleString();
};
const contextIdentity = (context) => JSON.stringify([
  context.origin,
  context.bootID,
  context.sessionID,
  context.principalID,
  context.authorizationRevision,
  context.scopeSetDigest,
  context.scope.kind,
  context.scope.tenant_id,
  context.scope.workspace_id ?? ""
]);
const publishedIdentity = (source) => JSON.stringify([
  source.published_pointer.kind,
  source.published_pointer.resource_id,
  source.published_pointer.revision,
  source.published_pointer.digest,
  source.basis.tenant_id,
  source.basis.pointer_revision,
  source.basis.control.id,
  source.basis.control.revision,
  source.basis.control.digest,
  source.basis.catalog.id,
  source.basis.catalog.revision,
  source.basis.catalog.digest
]);
const samePublishedBasis = (left, right) => publishedIdentity(left) === publishedIdentity(right);
const bindingTargetLabel = (binding) => binding.target.kind === "PROFILE" ? `Profile ${binding.target.profile_id}` : `Workspace ${binding.target.workspace_id} / endpoint ${binding.target.endpoint_id}`;
const operationTargetLabel = (summary, binding) => `${summary.instance_id} from ${bindingTargetLabel(binding)}`;
const detachBinding = (binding) => ({
  target: binding.target.kind === "PROFILE" ? { kind: "PROFILE", profile_id: binding.target.profile_id } : {
    kind: "WORKSPACE_CHANNEL_ENDPOINT",
    workspace_id: binding.target.workspace_id,
    endpoint_id: binding.target.endpoint_id
  },
  port: { name: binding.port.name, exact_version: binding.port.exact_version },
  port_binding_index: binding.port_binding_index,
  config_ref: binding.config_ref,
  authority_ceiling_ref: binding.authority_ceiling_ref,
  static_context_refs: [...binding.static_context_refs],
  failure_policy: binding.failure_policy
});
const sameBinding = (selected, returned) => {
  if (returned === void 0 || selected.target.kind !== returned.target.kind || selected.port.name !== returned.port.name || selected.port.exact_version !== returned.port.exact_version || selected.port_binding_index !== returned.port_binding_index || selected.config_ref !== returned.config_ref || selected.authority_ceiling_ref !== returned.authority_ceiling_ref || selected.failure_policy !== returned.failure_policy || selected.static_context_refs.length !== returned.static_context_refs.length || selected.static_context_refs.some(
    (reference, index) => reference !== returned.static_context_refs[index]
  )) return false;
  if (selected.target.kind === "PROFILE" && returned.target.kind === "PROFILE") {
    return selected.target.profile_id === returned.target.profile_id;
  }
  return selected.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" && returned.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" && selected.target.workspace_id === returned.target.workspace_id && selected.target.endpoint_id === returned.target.endpoint_id;
};
const sameModuleSummary = (left, right) => left.instance_id === right.instance_id && left.module_id === right.module_id && left.exact_version === right.exact_version && left.artifact_digest === right.artifact_digest && left.execution_class === right.execution_class && left.adapter_identity === right.adapter_identity && left.activation_revision === right.activation_revision && left.visible_binding_count === right.visible_binding_count && left.provides.length === right.provides.length && left.provides.every(
  (port, index) => port.name === right.provides[index].name && port.exact_version === right.provides[index].exact_version
);
const isModuleDisableCandidate = (summary, binding) => summary.execution_class === "DECLARATIVE" && binding.target.kind === "PROFILE" && binding.port.name === "context.provide" && binding.port.exact_version === "v1" && binding.failure_policy === "OPTIONAL";
const sameBindingTarget = (left, right) => {
  if (left.target.kind !== right.target.kind) return false;
  if (left.target.kind === "PROFILE" && right.target.kind === "PROFILE") {
    return left.target.profile_id === right.target.profile_id;
  }
  return left.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" && right.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" && left.target.workspace_id === right.target.workspace_id && left.target.endpoint_id === right.target.endpoint_id;
};
const isLowestDisableCandidate = (summary, bindings, selected) => bindings.some((binding) => sameBinding(selected, binding)) && bindings.filter(
  (binding) => isModuleDisableCandidate(summary, binding) && sameBindingTarget(binding, selected) && binding.port.name === selected.port.name && binding.port.exact_version === selected.port.exact_version
).every(
  (binding) => selected.port_binding_index <= binding.port_binding_index
);
const moduleSearchMatch = (summary, rawSearch) => {
  const search = rawSearch.trim().toLocaleLowerCase("en");
  if (search === "") return true;
  return [
    summary.instance_id,
    summary.module_id,
    summary.exact_version,
    summary.execution_class,
    summary.adapter_identity
  ].join("\n").toLocaleLowerCase("en").includes(search);
};
const failureFromError = (error) => {
  if (isModulesSessionInvalid(error)) {
    return {
      kind: "SESSION",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  if (isModulesPermissionDenied(error)) {
    return {
      kind: "PERMISSION",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  if (isModulesStale(error)) {
    return {
      kind: "STALE",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  if (error instanceof ControlModulesError && (error.code === "INVALID_RESPONSE" || error.code === "INVALID_ERROR_RESPONSE" || error.code === "INVALID_CLIENT_INPUT" || error.code === "INTEGRITY_FAILURE" || error.code === "CONTROL_ORIGIN_MISMATCH" || error.code === "PUBLISHED_BASIS_REQUIRED" || error.code === "CONFIRMATION_CONTEXT_MISSING")) {
    return {
      kind: "INTEGRITY",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  return null;
};
const safeErrorText = (error, pending, fallback) => {
  const message = error instanceof Error ? error.message : fallback;
  const correlationID = error instanceof ControlModulesError ? error.correlationID : "";
  const secrets = pending === null ? [] : [
    pending.idempotencyKey,
    pending.confirmationProof,
    pending.context.csrfToken
  ].filter(
    (value) => value !== ""
  );
  if (secrets.some(
    (secret) => message.includes(secret) || correlationID.includes(secret)
  )) {
    return { message: fallback, correlationID: "" };
  }
  return { message, correlationID };
};
const erasePending = (pending) => {
  if (pending === null) return;
  pending.identity = "";
  pending.idempotencyKey = "";
  pending.evaluationDigest = "";
  pending.confirmationProof = "";
  pending.dryRunProjectionCanonical = "";
  pending.context.csrfToken = "";
};
function FailurePanel({
  failure,
  onRetry
}) {
  const title = {
    PERMISSION: "Modules permission denied",
    SESSION: "Control session unavailable",
    STALE: "Published basis changed",
    INTEGRITY: "Modules response was rejected"
  }[failure.kind];
  return /* @__PURE__ */ jsxRuntimeExports.jsx("main", { className: "entry", "aria-labelledby": "modules-failure-title", children: /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "entry__panel entry__panel--compact", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Modules fail-closed boundary" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { id: "modules-failure-title", children: title }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "lede", children: failure.message }),
    failure.correlationID !== "" && /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "correlation", children: [
      "Correlation: ",
      failure.correlationID
    ] }),
    onRetry !== void 0 && /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button button--primary", type: "button", onClick: onRetry, children: "Reload validated Modules data" })
  ] }) });
}
function ModulesLoading() {
  return /* @__PURE__ */ jsxRuntimeExports.jsx("main", { className: "entry", "aria-busy": "true", "aria-labelledby": "modules-loading-title", children: /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "entry__panel entry__panel--compact", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "FreeAgent Control" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { id: "modules-loading-title", children: "Loading Modules..." }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("div", { className: "loading-bar", "aria-hidden": "true", children: /* @__PURE__ */ jsxRuntimeExports.jsx("span", {}) }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "muted", children: "Waiting for one bounded, authenticated projection." })
  ] }) });
}
function OperationPanel({
  state,
  confirmationAccepted,
  onConfirmationAccepted,
  onRequestConfirmation,
  onAcknowledgeDryResult,
  onExplicitConfirm,
  onExactRetry,
  onRetryStep,
  onReset,
  onReload
}) {
  if (state.phase === "IDLE") return null;
  if (state.phase === "DRY_RUNNING") {
    return /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "module-operation", "aria-busy": "true", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Effect-free evaluation" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: "Running server dry-run" }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
        "No published state is being changed for ",
        state.target,
        "."
      ] })
    ] });
  }
  if (state.phase === "DRY_RESULT") {
    return /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "module-operation", "aria-live": "polite", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Authoritative dry-run result" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: state.disposition.replaceAll("_", " ") }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
        "Target: ",
        state.target,
        ". Catalog effect: ",
        state.catalogChange.replaceAll("_", " "),
        "."
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "digest", children: [
        "Plan ",
        shortDigest(state.planDigest)
      ] }),
      state.disposition === "WOULD_APPLY" ? /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "module-operation__actions", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx(
          "button",
          {
            className: "button button--primary",
            type: "button",
            onClick: onRequestConfirmation,
            children: "Request short-lived confirmation"
          }
        ),
        /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: onReset, children: "Cancel" })
      ] }) : /* @__PURE__ */ jsxRuntimeExports.jsxs(jsxRuntimeExports.Fragment, { children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("p", { children: "No mutation request was sent." }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: onAcknowledgeDryResult, children: "Done and refresh authority" })
      ] })
    ] });
  }
  if (state.phase === "CONFIRMING") {
    return /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "module-operation", "aria-busy": "true", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Confirmation evaluation" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: "Binding the exact request..." }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
        "The server is re-evaluating ",
        state.target,
        "; no mutation is being sent."
      ] })
    ] });
  }
  if (state.phase === "AWAITING_EXPLICIT_CONFIRMATION") {
    return /* @__PURE__ */ jsxRuntimeExports.jsxs(
      "section",
      {
        className: "module-operation module-operation--warning",
        role: "alertdialog",
        "aria-labelledby": "module-confirm-title",
        children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Explicit operator confirmation" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { id: "module-confirm-title", children: "Disable this exact Profile binding?" }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
            "The server evaluated ",
            state.target,
            " as WOULD APPLY. The projected catalog effect is",
            ` ${state.catalogChange.replaceAll("_", " ")}`,
            "."
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
            "Confirmation expires ",
            formatMicros(state.expiresAtUnixMicros),
            "."
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("dl", { className: "module-facts", children: [
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Principal" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: state.review.principalID })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Scope" }),
              /* @__PURE__ */ jsxRuntimeExports.jsxs("dd", { children: [
                state.review.scopeKind,
                " / tenant ",
                state.review.tenantID,
                state.review.workspaceID === "" ? "" : ` / workspace ${state.review.workspaceID}`
              ] })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Published pointer revision" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: state.review.expectedRevision })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Instance" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: state.review.instanceID })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Port / binding index" }),
              /* @__PURE__ */ jsxRuntimeExports.jsxs("dd", { children: [
                state.review.portName,
                "@",
                state.review.portVersion,
                " / ",
                state.review.portBindingIndex
              ] })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Profile target" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: state.review.targetProfileID })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Failure policy" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: state.review.failurePolicy })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Catalog effect" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: state.catalogChange.replaceAll("_", " ") })
            ] })
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "module-operation__digests", children: [
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Scope digest" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.scopeDigest })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Expected ref" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsxs("code", { children: [
                state.review.expectedKind,
                ":",
                state.review.expectedResourceID,
                ":",
                state.review.expectedRevision,
                ":",
                state.review.expectedDigest
              ] })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Config ref" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.configRef })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Authority ceiling" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.authorityCeilingRef })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Static context refs" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.staticContextRefs.length === 0 ? "none" : state.review.staticContextRefs.join(",") })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Input digest" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.inputDigest })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Plan digest" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.planDigest })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Idempotency-key digest" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.idempotencyKeyDigest })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Evaluation digest" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.evaluationDigest })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Statement digest" }),
              " ",
              /* @__PURE__ */ jsxRuntimeExports.jsx("code", { children: state.review.statementDigest })
            ] })
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("label", { className: "module-confirmation-check", children: [
            /* @__PURE__ */ jsxRuntimeExports.jsx(
              "input",
              {
                type: "checkbox",
                checked: confirmationAccepted,
                onChange: (event) => onConfirmationAccepted(event.currentTarget.checked)
              }
            ),
            /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "I understand this sends one governed published-state mutation." })
          ] }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "module-operation__actions", children: [
            /* @__PURE__ */ jsxRuntimeExports.jsx(
              "button",
              {
                className: "button button--primary",
                type: "button",
                disabled: !confirmationAccepted,
                onClick: onExplicitConfirm,
                children: "Confirm and disable binding"
              }
            ),
            /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: onReset, children: "Cancel" })
          ] })
        ]
      }
    );
  }
  if (state.phase === "MUTATING") {
    return /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "module-operation", "aria-busy": "true", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Governed mutation" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: state.exactRetry ? "Replaying the exact request..." : "Waiting for an authoritative receipt..." }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
        "The page will not update local authority optimistically for ",
        state.target,
        "."
      ] })
    ] });
  }
  if (state.phase === "MUTATION_UNCERTAIN") {
    return /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "module-operation module-operation--warning", role: "alert", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Outcome not yet known" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: "Do not construct a replacement mutation" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { children: state.message }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { children: "Only an exact replay of the original body, idempotency key, precondition, and evaluation digest is available. The confirmation proof will not be resent." }),
      state.correlationID !== "" && /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "correlation", children: [
        "Correlation: ",
        state.correlationID
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "module-operation__actions", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button button--primary", type: "button", onClick: onExactRetry, children: "Retry exact request" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: onReload, children: "Discard retry state and reload authority" })
      ] })
    ] });
  }
  if (state.phase === "COMPLETE") {
    return /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "module-operation module-operation--complete", "aria-live": "polite", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Authoritative mutation receipt" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: state.status.replaceAll("_", " ") }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { children: [
        "The server completed ",
        state.target,
        " at ",
        formatMicros(state.completedAtUnixMicros),
        "."
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "digest", children: [
        "Receipt ",
        shortDigest(state.receiptDigest)
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: onReset, children: "Close receipt" })
    ] });
  }
  return /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "module-operation notice notice--error", role: "alert", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "eyebrow", children: [
      state.step.replaceAll("_", " "),
      " stopped"
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: "The operation did not advance" }),
    /* @__PURE__ */ jsxRuntimeExports.jsx("p", { children: state.message }),
    state.correlationID !== "" && /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "correlation", children: [
      "Correlation: ",
      state.correlationID
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "module-operation__actions", children: [
      state.retryable && /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button button--primary", type: "button", onClick: onRetryStep, children: "Retry same step" }),
      /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: onReset, children: state.retryable ? "Cancel" : "Restart review" })
    ] })
  ] });
}
function ModulesPage({
  session,
  context,
  scopeChoices,
  selectedScopeKey,
  navigationKey = "modules",
  onScopeChange,
  onNavigateOverview,
  onFailClosed,
  onMutationComplete
}) {
  const queryClient2 = useQueryClient();
  const [search, setSearch] = reactExports.useState("");
  const [selectedInstanceID, setSelectedInstanceID] = reactExports.useState(null);
  const [operation, setOperation] = reactExports.useState({ phase: "IDLE" });
  const [confirmationAccepted, setConfirmationAccepted] = reactExports.useState(false);
  const [operationFailure, setOperationFailure] = reactExports.useState(null);
  const pendingRef = reactExports.useRef(null);
  const operationAbortRef = reactExports.useRef(null);
  const operationEpochRef = reactExports.useRef(0);
  const operationBusyRef = reactExports.useRef(false);
  const publishedFailureRef = reactExports.useRef("");
  const rawContextIdentity = reactExports.useMemo(() => contextIdentity(context), [context]);
  const sessionIdentity = JSON.stringify([
    session.schema_version,
    session.boot_id,
    session.session_id,
    session.principal_id,
    session.authorization_revision,
    session.scope_set_digest,
    session.issued_at_unix_micros,
    session.expires_at_unix_micros,
    [...session.capabilities].sort()
  ]);
  const selectedChoice = reactExports.useMemo(
    () => scopeChoices.find((choice) => choice.key === selectedScopeKey) ?? null,
    [scopeChoices, selectedScopeKey]
  );
  const sessionExpired = session.expires_at_unix_micros <= Date.now() * 1e3;
  const contextIsValid = session.schema_version === "control-session/v1" && session.boot_id === context.bootID && session.session_id === context.sessionID && session.principal_id === context.principalID && session.authorization_revision === context.authorizationRevision && session.scope_set_digest === context.scopeSetDigest && (context.sessionExpiresAtUnixMicros === void 0 || context.sessionExpiresAtUnixMicros === session.expires_at_unix_micros) && session.capabilities.includes("OBSERVE") && selectedChoice !== null && selectedChoice.key === scopeKey(context.scope) && selectedChoice.key === scopeKey(selectedChoice.scope) && !sessionExpired;
  const modules = useInfiniteQuery({
    queryKey: modulesQueryKey(context),
    queryFn: ({ pageParam, signal }) => fetchModulesPage(context, pageParam === "" ? void 0 : pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.has_more ? lastPage.next_cursor : void 0,
    enabled: contextIsValid,
    staleTime: 3e4,
    retry: false
  });
  const summaries = reactExports.useMemo(
    () => modules.data?.pages.flatMap((page) => page.items) ?? [],
    [modules.data]
  );
  const selectedSummary = reactExports.useMemo(
    () => summaries.find((item) => item.instance_id === selectedInstanceID) ?? null,
    [selectedInstanceID, summaries]
  );
  const detail = useQuery({
    queryKey: selectedInstanceID === null ? ["module-detail", "disabled"] : moduleDetailQueryKey(context, selectedInstanceID),
    queryFn: ({ signal }) => {
      if (selectedInstanceID === null) {
        throw new ControlModulesError(
          "Module detail query is disabled",
          0,
          "INVALID_CLIENT_INPUT"
        );
      }
      return fetchModuleDetail(context, selectedInstanceID, signal);
    },
    enabled: contextIsValid && selectedInstanceID !== null,
    staleTime: 3e4,
    retry: false
  });
  const firstPage = modules.data?.pages[0];
  const pagesAligned = reactExports.useMemo(() => {
    if (firstPage === void 0 || modules.data === void 0) return true;
    return modules.data.pages.every(
      (page) => samePublishedBasis(firstPage, page) && page.source_revision === firstPage.source_revision && page.source_digest === firstPage.source_digest && page.view_snapshot_digest === firstPage.view_snapshot_digest && page.sort_version === firstPage.sort_version && page.filter_digest === firstPage.filter_digest
    );
  }, [firstPage, modules.data]);
  const detailAligned = detail.data === void 0 || firstPage === void 0 || samePublishedBasis(firstPage, detail.data) && detail.data.source_revision === firstPage.source_revision && detail.data.source_digest === firstPage.source_digest && selectedSummary !== null && sameModuleSummary(selectedSummary, detail.data.module.summary);
  const detailBasisIdentity = detail.data === void 0 ? "" : publishedIdentity(detail.data);
  const currentOperationIdentity = JSON.stringify([
    rawContextIdentity,
    context.sessionEpoch,
    sessionIdentity,
    navigationKey,
    selectedInstanceID ?? "",
    detailBasisIdentity
  ]);
  const destroyPending = reactExports.useCallback((clearOperationBindings) => {
    operationEpochRef.current += 1;
    operationBusyRef.current = false;
    operationAbortRef.current?.abort();
    operationAbortRef.current = null;
    erasePending(pendingRef.current);
    pendingRef.current = null;
    setConfirmationAccepted(false);
    if (clearOperationBindings) clearModulesOperationCache();
  }, []);
  const resetOperation = reactExports.useCallback(() => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
  }, [destroyPending]);
  reactExports.useEffect(() => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    return () => destroyPending(true);
  }, [currentOperationIdentity, destroyPending]);
  reactExports.useEffect(() => {
    if (selectedInstanceID !== null && modules.data !== void 0 && !summaries.some((item) => item.instance_id === selectedInstanceID)) {
      setSelectedInstanceID(null);
    }
  }, [modules.data, selectedInstanceID, summaries]);
  reactExports.useEffect(() => {
    const remaining = Math.ceil(session.expires_at_unix_micros / 1e3 - Date.now());
    if (remaining > 2147483647) return;
    const timer = window.setTimeout(() => {
      destroyPending(true);
      setOperation({ phase: "IDLE" });
      setOperationFailure({
        kind: "SESSION",
        message: "The control session expired before this operation completed.",
        correlationID: ""
      });
    }, Math.max(0, remaining));
    return () => window.clearTimeout(timer);
  }, [destroyPending, session.expires_at_unix_micros]);
  reactExports.useEffect(() => {
    if (operation.phase !== "AWAITING_EXPLICIT_CONFIRMATION") return;
    const remaining = Math.ceil(
      operation.expiresAtUnixMicros / 1e3 - Date.now()
    );
    const timer = window.setTimeout(() => {
      destroyPending(true);
      setOperation({
        phase: "ERROR",
        target: operation.target,
        step: "CONFIRMATION",
        retryable: false,
        message: "The short-lived confirmation expired. Start a new dry-run.",
        correlationID: ""
      });
    }, Math.max(0, remaining));
    return () => window.clearTimeout(timer);
  }, [destroyPending, operation]);
  const contextFailure = reactExports.useMemo(() => {
    if (contextIsValid) return null;
    if (sessionExpired) {
      return {
        kind: "SESSION",
        message: "The control session has expired. Open a new handoff.",
        correlationID: ""
      };
    }
    if (!session.capabilities.includes("OBSERVE")) {
      return {
        kind: "PERMISSION",
        message: "This session does not hold OBSERVE for the Modules surface.",
        correlationID: ""
      };
    }
    return {
      kind: "INTEGRITY",
      message: "The Modules session, scope, and transport context do not match exactly.",
      correlationID: ""
    };
  }, [contextIsValid, session.capabilities, sessionExpired]);
  const readFailure = reactExports.useMemo(() => {
    if (!pagesAligned || !detailAligned) {
      return {
        kind: "STALE",
        message: "The Modules list and detail are not bound to one published basis.",
        correlationID: ""
      };
    }
    const error = failureFromError(modules.error) !== null ? modules.error : failureFromError(detail.error) !== null ? detail.error : null;
    const failure = failureFromError(error);
    if (failure === null) return null;
    const safe = safeErrorText(
      error,
      pendingRef.current,
      "The Modules response failed its fail-closed boundary."
    );
    return { ...failure, ...safe };
  }, [detail.error, detailAligned, modules.error, pagesAligned]);
  const displayedFailure = operationFailure ?? contextFailure ?? readFailure;
  reactExports.useEffect(() => {
    if (displayedFailure === null) {
      publishedFailureRef.current = "";
      return;
    }
    const key = JSON.stringify(displayedFailure);
    if (publishedFailureRef.current === key) return;
    publishedFailureRef.current = key;
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    onFailClosed?.(displayedFailure);
  }, [destroyPending, displayedFailure, onFailClosed]);
  const enterCriticalFailure = reactExports.useCallback(
    (failure) => {
      destroyPending(true);
      setOperation({ phase: "IDLE" });
      setOperationFailure(failure);
    },
    [destroyPending]
  );
  const handleStepError = reactExports.useCallback(
    (step, error, epoch, fallback) => {
      if (epoch !== operationEpochRef.current) return;
      const pending = pendingRef.current;
      const safe = step === "MUTATE" ? { message: fallback, correlationID: "" } : safeErrorText(error, pending, fallback);
      if (step === "MUTATE" && error instanceof ControlModulesError && error.outcomeMayBeCommitted) {
        if (pending !== null) pending.confirmationProof = "";
        setOperation({
          phase: "MUTATION_UNCERTAIN",
          target: pending?.target ?? "the selected binding",
          message: safe.message,
          correlationID: safe.correlationID
        });
        return;
      }
      const critical = failureFromError(error);
      if (critical !== null) {
        enterCriticalFailure({
          ...critical,
          message: safe.message,
          correlationID: safe.correlationID
        });
        return;
      }
      if (step === "MUTATE") {
        erasePending(pending);
        pendingRef.current = null;
        clearModulesOperationCache();
      }
      setOperation({
        phase: "ERROR",
        target: pending?.target ?? "the selected binding",
        step,
        retryable: step !== "MUTATE" && pending !== null,
        message: safe.message,
        correlationID: safe.correlationID
      });
    },
    [enterCriticalFailure]
  );
  const runDryRun = reactExports.useCallback(async () => {
    const pending = pendingRef.current;
    if (pending === null || pending.identity !== currentOperationIdentity || operationBusyRef.current) return;
    operationBusyRef.current = true;
    const epoch = operationEpochRef.current;
    const controller = new AbortController();
    operationAbortRef.current = controller;
    setOperation({ phase: "DRY_RUNNING", target: pending.target });
    try {
      const result = await dryRunModuleDisable(
        pending.context,
        pending.body,
        controller.signal
      );
      if (epoch !== operationEpochRef.current) return;
      if (result.projection.disposition === "WOULD_APPLY" && !sameBinding(pending.selectedBinding, result.projection.binding_removal)) {
        enterCriticalFailure({
          kind: "INTEGRITY",
          message: "The dry-run selected a different binding than the exact binding reviewed by the operator.",
          correlationID: ""
        });
        return;
      }
      pending.planDigest = result.projection.plan_digest;
      pending.catalogChange = result.projection.catalog_change;
      pending.dryRunProjectionCanonical = canonicalJSONString(result.projection);
      setOperation({
        phase: "DRY_RESULT",
        target: pending.target,
        disposition: result.projection.disposition,
        planDigest: result.projection.plan_digest,
        catalogChange: result.projection.catalog_change
      });
    } catch (error) {
      handleStepError(
        "DRY_RUN",
        error,
        epoch,
        "The MODULE_DISABLE dry-run did not return a trusted result."
      );
    } finally {
      if (epoch === operationEpochRef.current) {
        operationBusyRef.current = false;
        operationAbortRef.current = null;
      }
    }
  }, [currentOperationIdentity, enterCriticalFailure, handleStepError]);
  const startDryRun = reactExports.useCallback(
    (summary, binding) => {
      if (detail.data === void 0 || !contextIsValid || context.scope.kind !== "TENANT" || !session.capabilities.includes("OPERATE_MODULES") || modules.error !== null || detail.error !== null || modules.isFetching || detail.isFetching || !pagesAligned || !detailAligned || !isModuleDisableCandidate(summary, binding) || !isLowestDisableCandidate(
        summary,
        detail.data.module.bindings,
        binding
      ) || binding.target.kind !== "PROFILE") return;
      destroyPending(true);
      const operationContext = withPublishedModulesBasis(context, detail.data);
      const nextPending = {
        identity: currentOperationIdentity,
        target: operationTargetLabel(summary, binding),
        context: operationContext,
        body: {
          schema_version: "control-module-disable-dry-run-input/v1",
          expected_pointer_revision: detail.data.basis.pointer_revision,
          binding_target: {
            kind: "PROFILE",
            profile_id: binding.target.profile_id
          },
          instance_id: summary.instance_id,
          port: { name: "context.provide", exact_version: "v1" }
        },
        selectedBinding: detachBinding(binding),
        idempotencyKey: "",
        evaluationDigest: "",
        confirmationProof: "",
        expiresAtUnixMicros: 0,
        planDigest: "",
        catalogChange: "NONE",
        dryRunProjectionCanonical: ""
      };
      pendingRef.current = nextPending;
      setOperationFailure(null);
      setConfirmationAccepted(false);
      void runDryRun();
    },
    [
      context,
      contextIsValid,
      currentOperationIdentity,
      destroyPending,
      detail.data,
      detail.error,
      detail.isFetching,
      detailAligned,
      modules.error,
      modules.isFetching,
      pagesAligned,
      runDryRun,
      session.capabilities
    ]
  );
  const runConfirmation = reactExports.useCallback(async () => {
    const pending = pendingRef.current;
    const confirmationStepAllowed = operation.phase === "DRY_RESULT" && operation.disposition === "WOULD_APPLY" || operation.phase === "ERROR" && operation.step === "CONFIRMATION";
    if (pending === null || pending.identity !== currentOperationIdentity || pending.planDigest === "" || pending.catalogChange === "NONE" || !confirmationStepAllowed || operationBusyRef.current) return;
    if (pending.idempotencyKey === "") {
      try {
        pending.idempotencyKey = createIdempotencyKey();
      } catch (error) {
        handleStepError(
          "CONFIRMATION",
          error,
          operationEpochRef.current,
          "A secure confirmation request could not be prepared."
        );
        return;
      }
    }
    operationBusyRef.current = true;
    const epoch = operationEpochRef.current;
    const controller = new AbortController();
    operationAbortRef.current = controller;
    setOperation({ phase: "CONFIRMING", target: pending.target });
    try {
      const result = await issueModuleDisableConfirmation(
        pending.context,
        pending.body,
        pending.idempotencyKey,
        controller.signal
      );
      if (epoch !== operationEpochRef.current) {
        result.confirmation_proof = "";
        return;
      }
      if (result.expires_at_unix_micros <= Date.now() * 1e3) {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "STALE",
          message: "The confirmation expired before it could be presented for explicit approval.",
          correlationID: ""
        });
        return;
      }
      if (result.evaluation.projection.disposition !== "WOULD_APPLY") {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "STALE",
          message: "Confirmation no longer evaluates the exact request as WOULD APPLY.",
          correlationID: ""
        });
        return;
      }
      if (!sameBinding(
        pending.selectedBinding,
        result.evaluation.projection.binding_removal
      )) {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "INTEGRITY",
          message: "Confirmation selected a different binding than the exact binding reviewed by the operator.",
          correlationID: ""
        });
        return;
      }
      if (pending.dryRunProjectionCanonical === "" || canonicalJSONString(result.evaluation.projection) !== pending.dryRunProjectionCanonical) {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "STALE",
          message: "Confirmation drifted from the exact authoritative dry-run projection.",
          correlationID: ""
        });
        return;
      }
      const proof = result.confirmation_proof;
      result.confirmation_proof = "";
      pending.confirmationProof = proof;
      pending.evaluationDigest = result.evaluation_digest;
      pending.expiresAtUnixMicros = result.expires_at_unix_micros;
      pending.planDigest = result.evaluation.projection.plan_digest;
      pending.catalogChange = result.evaluation.projection.catalog_change;
      if (pending.catalogChange !== "RETAIN_INSTANCE" && pending.catalogChange !== "REMOVE_INSTANCE") {
        enterCriticalFailure({
          kind: "INTEGRITY",
          message: "Confirmation returned an invalid MODULE_DISABLE catalog effect.",
          correlationID: ""
        });
        return;
      }
      setConfirmationAccepted(false);
      setOperation({
        phase: "AWAITING_EXPLICIT_CONFIRMATION",
        target: pending.target,
        planDigest: pending.planDigest,
        statementDigest: result.statement_digest,
        catalogChange: pending.catalogChange,
        expiresAtUnixMicros: pending.expiresAtUnixMicros,
        review: {
          principalID: result.statement.principal_id,
          scopeKind: result.statement.scope.kind,
          tenantID: result.statement.scope.tenant_id,
          workspaceID: result.statement.scope.workspace_id ?? "",
          scopeDigest: result.statement.scope_digest,
          expectedKind: result.statement.expected_ref.kind,
          expectedResourceID: result.statement.expected_ref.resource_id,
          expectedRevision: result.statement.expected_ref.revision,
          expectedDigest: result.statement.expected_ref.digest,
          instanceID: result.evaluation.projection.instance_id,
          targetProfileID: pending.selectedBinding.target.kind === "PROFILE" ? pending.selectedBinding.target.profile_id : "",
          portName: pending.selectedBinding.port.name,
          portVersion: pending.selectedBinding.port.exact_version,
          portBindingIndex: pending.selectedBinding.port_binding_index,
          configRef: pending.selectedBinding.config_ref,
          authorityCeilingRef: pending.selectedBinding.authority_ceiling_ref,
          staticContextRefs: [...pending.selectedBinding.static_context_refs],
          failurePolicy: "OPTIONAL",
          inputDigest: result.statement.input_digest,
          idempotencyKeyDigest: result.statement.idempotency_key_digest,
          evaluationDigest: result.evaluation_digest,
          statementDigest: result.statement_digest
        }
      });
    } catch (error) {
      handleStepError(
        "CONFIRMATION",
        error,
        epoch,
        "The confirmation endpoint did not return a trusted exact evaluation."
      );
    } finally {
      if (epoch === operationEpochRef.current) {
        operationBusyRef.current = false;
        operationAbortRef.current = null;
      }
    }
  }, [
    currentOperationIdentity,
    enterCriticalFailure,
    handleStepError,
    operation
  ]);
  const runMutation = reactExports.useCallback(
    async (exactRetry) => {
      const pending = pendingRef.current;
      if (!exactRetry && pending !== null && pending.identity === currentOperationIdentity && operation.phase === "AWAITING_EXPLICIT_CONFIRMATION" && (pending.confirmationProof === "" || pending.expiresAtUnixMicros <= Date.now() * 1e3)) {
        const target = pending.target;
        destroyPending(true);
        setOperation({
          phase: "ERROR",
          target,
          step: "CONFIRMATION",
          retryable: false,
          message: "The short-lived confirmation expired. Start a new dry-run.",
          correlationID: ""
        });
        return;
      }
      if (pending === null || pending.identity !== currentOperationIdentity || pending.idempotencyKey === "" || pending.evaluationDigest === "" || operationBusyRef.current || (exactRetry ? operation.phase !== "MUTATION_UNCERTAIN" : operation.phase !== "AWAITING_EXPLICIT_CONFIRMATION" || !confirmationAccepted) || !exactRetry && (pending.confirmationProof === "" || pending.expiresAtUnixMicros <= Date.now() * 1e3)) return;
      operationBusyRef.current = true;
      const epoch = operationEpochRef.current;
      const controller = new AbortController();
      operationAbortRef.current = controller;
      setConfirmationAccepted(false);
      setOperation({ phase: "MUTATING", target: pending.target, exactRetry });
      try {
        let proof = exactRetry ? void 0 : pending.confirmationProof;
        const request = mutateModuleDisable(
          pending.context,
          pending.body,
          pending.idempotencyKey,
          pending.evaluationDigest,
          proof,
          controller.signal
        );
        proof = void 0;
        pending.confirmationProof = "";
        const result = await request;
        if (epoch !== operationEpochRef.current) return;
        if (result.receipt.status !== "NO_CHANGE" && result.receipt.status !== "APPLIED") {
          enterCriticalFailure({
            kind: "INTEGRITY",
            message: "The MODULE_DISABLE mutation returned a non-mutation receipt status.",
            correlationID: ""
          });
          return;
        }
        const completed = {
          phase: "COMPLETE",
          target: pending.target,
          status: result.receipt.status,
          receiptDigest: result.receipt_digest,
          completedAtUnixMicros: result.receipt.completed_at_unix_micros
        };
        erasePending(pending);
        pendingRef.current = null;
        clearModulesTransportCache();
        setSelectedInstanceID(null);
        setOperation(completed);
        await Promise.all([
          queryClient2.resetQueries({ queryKey: ["modules"] }),
          queryClient2.invalidateQueries({ queryKey: ["overview"] })
        ]);
        queryClient2.removeQueries({ queryKey: ["module-detail"] });
        try {
          await onMutationComplete?.(result);
        } catch {
        }
      } catch (error) {
        handleStepError(
          "MUTATE",
          error,
          epoch,
          "The mutation outcome could not be established from a trusted receipt."
        );
      } finally {
        if (epoch === operationEpochRef.current) {
          operationBusyRef.current = false;
          operationAbortRef.current = null;
        }
      }
    },
    [
      currentOperationIdentity,
      confirmationAccepted,
      destroyPending,
      enterCriticalFailure,
      handleStepError,
      onMutationComplete,
      operation.phase,
      queryClient2
    ]
  );
  const reloadAuthority = reactExports.useCallback(() => {
    destroyPending(true);
    clearModulesTransportCache();
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    setConfirmationAccepted(false);
    setSelectedInstanceID(null);
    queryClient2.removeQueries({ queryKey: ["module-detail"] });
    void queryClient2.resetQueries({ queryKey: ["modules"] });
  }, [destroyPending, queryClient2]);
  const acknowledgeTerminalDryRun = reactExports.useCallback(() => {
    if (operation.phase !== "DRY_RESULT" || operation.disposition === "WOULD_APPLY") return;
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    void Promise.all([
      queryClient2.invalidateQueries({ queryKey: ["modules"] }),
      queryClient2.invalidateQueries({ queryKey: ["module-detail"] }),
      queryClient2.invalidateQueries({ queryKey: ["overview"] })
    ]);
  }, [destroyPending, operation, queryClient2]);
  const retryReads = reactExports.useCallback(() => {
    setOperationFailure(null);
    publishedFailureRef.current = "";
    reloadAuthority();
  }, [reloadAuthority]);
  const retryStep = reactExports.useCallback(() => {
    if (operation.phase !== "ERROR" || !operation.retryable) return;
    if (operation.step === "DRY_RUN") void runDryRun();
    if (operation.step === "CONFIRMATION") void runConfirmation();
  }, [operation, runConfirmation, runDryRun]);
  const onSelectScope = (event) => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    setSelectedInstanceID(null);
    setSearch("");
    onScopeChange(event.currentTarget.value);
  };
  const navigateOverview = (event) => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    if (onNavigateOverview !== void 0) {
      event.preventDefault();
      onNavigateOverview();
    }
  };
  if (displayedFailure !== null) {
    const canRetry = displayedFailure.kind === "STALE" || displayedFailure.kind === "INTEGRITY";
    return /* @__PURE__ */ jsxRuntimeExports.jsx(
      FailurePanel,
      {
        failure: displayedFailure,
        onRetry: canRetry && contextIsValid ? retryReads : void 0
      }
    );
  }
  if (modules.isPending || modules.data === void 0) {
    if (modules.error !== null) {
      const safe = safeErrorText(
        modules.error,
        null,
        "The Modules projection could not be read."
      );
      return /* @__PURE__ */ jsxRuntimeExports.jsx(
        FailurePanel,
        {
          failure: { kind: "INTEGRITY", ...safe },
          onRetry: retryReads
        }
      );
    }
    return /* @__PURE__ */ jsxRuntimeExports.jsx(ModulesLoading, {});
  }
  const filteredSummaries = summaries.filter(
    (summary) => moduleSearchMatch(summary, search)
  );
  const pageBasis = modules.data.pages[0].basis;
  const canOperate = session.capabilities.includes("OPERATE_MODULES") && context.scope.kind === "TENANT" && modules.error === null && !modules.isFetching;
  const statusStale = modules.isFetching || modules.error !== null;
  return /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "control-shell control-shell--modules", children: [
    /* @__PURE__ */ jsxRuntimeExports.jsxs("header", { className: "topbar", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsxs(
        "a",
        {
          className: "brand",
          href: "#overview",
          "aria-label": "FreeAgent Control Overview",
          onClick: navigateOverview,
          children: [
            /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "brand__mark", "aria-hidden": "true", children: "F" }),
            /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "FreeAgent Control" })
          ]
        }
      ),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "topbar__status", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx(
          "span",
          {
            className: `status-dot${statusStale ? " status-dot--stale" : ""}`,
            "aria-hidden": "true"
          }
        ),
        modules.isFetching ? "Refreshing" : modules.error !== null ? "Stale" : "Current"
      ] })
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("aside", { className: "sidebar", "aria-label": "Control navigation", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "sidebar__label", children: "Control" }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("nav", { children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("a", { href: "#overview", onClick: navigateOverview, children: "Overview" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("a", { href: "#modules", "aria-current": "page", children: "Modules" })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "session-card", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Session" }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: session.principal_id }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("small", { children: [
          "Expires ",
          formatMicros(session.expires_at_unix_micros)
        ] })
      ] })
    ] }),
    /* @__PURE__ */ jsxRuntimeExports.jsxs("main", { className: "content modules-content", id: "modules", children: [
      /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "page-heading", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Authorized configuration surface" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("h1", { children: "Modules" }),
          /* @__PURE__ */ jsxRuntimeExports.jsxs("p", { className: "lede", children: [
            "Validated module instances and bindings at published pointer revision",
            ` ${pageBasis.pointer_revision}`,
            "."
          ] })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: reloadAuthority, children: modules.isFetching ? "Refreshing..." : "Refresh exact basis" })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "toolbar modules-toolbar", "aria-label": "Modules filters", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("label", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Authorized scope" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx("select", { value: selectedScopeKey, onChange: onSelectScope, children: scopeChoices.map((choice) => /* @__PURE__ */ jsxRuntimeExports.jsx("option", { value: choice.key, children: choice.label }, choice.key)) })
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("label", { children: [
          /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Search current pages" }),
          /* @__PURE__ */ jsxRuntimeExports.jsx(
            "input",
            {
              type: "search",
              value: search,
              maxLength: 256,
              onChange: (event) => setSearch(event.currentTarget.value),
              placeholder: "Instance, module, version, class"
            }
          )
        ] })
      ] }),
      !session.capabilities.includes("OPERATE_MODULES") && /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "notice notice--warning", role: "status", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Read-only Modules session." }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "OPERATE_MODULES is not present, so no mutation control is rendered." })
      ] }),
      session.capabilities.includes("OPERATE_MODULES") && context.scope.kind !== "TENANT" && /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "notice notice--warning", role: "status", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Workspace scope is read-only for MODULE_DISABLE." }),
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Select an authorized Tenant scope to review a narrow Profile binding." })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "modules-layout", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "data-section modules-list", "aria-labelledby": "modules-list-title", children: [
          /* @__PURE__ */ jsxRuntimeExports.jsxs("header", { children: [
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Current validated pages" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("h2", { id: "modules-list-title", children: "Module instances" })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("span", { className: "section-meta", children: [
              summaries.length,
              " loaded"
            ] })
          ] }),
          modules.error !== null && /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "notice notice--error", role: "alert", children: [
            /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Background refresh failed." }),
            /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: safeErrorText(modules.error, null, "Modules refresh failed.").message }),
            /* @__PURE__ */ jsxRuntimeExports.jsx("button", { className: "button", type: "button", onClick: reloadAuthority, children: "Retry" })
          ] }),
          filteredSummaries.length === 0 ? /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "empty-copy", children: "No loaded module instance matches this search." }) : /* @__PURE__ */ jsxRuntimeExports.jsx("ul", { className: "result-list modules-result-list", children: filteredSummaries.map((summary) => /* @__PURE__ */ jsxRuntimeExports.jsx("li", { children: /* @__PURE__ */ jsxRuntimeExports.jsxs(
            "button",
            {
              className: "result-link module-result",
              type: "button",
              "aria-current": selectedInstanceID === summary.instance_id ? "true" : void 0,
              onClick: () => {
                destroyPending(true);
                setOperation({ phase: "IDLE" });
                setOperationFailure(null);
                setSelectedInstanceID(summary.instance_id);
              },
              children: [
                /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "result-link__title", children: summary.instance_id }),
                /* @__PURE__ */ jsxRuntimeExports.jsxs("span", { className: "result-link__summary", children: [
                  summary.module_id,
                  " @ ",
                  summary.exact_version,
                  " / ",
                  summary.execution_class,
                  ` / ${summary.visible_binding_count} visible bindings`
                ] }),
                /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "result-link__arrow", "aria-hidden": "true", children: ">" })
              ]
            }
          ) }, summary.instance_id)) }),
          modules.hasNextPage && /* @__PURE__ */ jsxRuntimeExports.jsx(
            "button",
            {
              className: "button modules-load-more",
              type: "button",
              disabled: modules.isFetchingNextPage,
              onClick: () => {
                void modules.fetchNextPage();
              },
              children: modules.isFetchingNextPage ? "Loading..." : "Load next validated page"
            }
          )
        ] }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("section", { className: "data-section module-detail", "aria-labelledby": "module-detail-title", children: [
          /* @__PURE__ */ jsxRuntimeExports.jsxs("header", { children: [
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "eyebrow", children: "Exact instance detail" }),
              /* @__PURE__ */ jsxRuntimeExports.jsx("h2", { id: "module-detail-title", children: selectedSummary?.instance_id ?? "Select a module" })
            ] }),
            selectedInstanceID !== null && /* @__PURE__ */ jsxRuntimeExports.jsx(
              "button",
              {
                className: "button",
                type: "button",
                onClick: () => {
                  destroyPending(true);
                  setOperation({ phase: "IDLE" });
                  setOperationFailure(null);
                  setSelectedInstanceID(null);
                },
                children: "Close"
              }
            )
          ] }),
          selectedInstanceID === null ? /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "empty-copy", children: "Select an instance to fetch its independently validated binding detail." }) : detail.isPending ? /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "empty-copy", "aria-busy": "true", children: "Loading exact module detail..." }) : detail.error !== null || detail.data === void 0 ? /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "notice notice--error", role: "alert", children: [
            /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: "Detail unavailable." }),
            /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: safeErrorText(detail.error, null, "Module detail could not be read.").message }),
            /* @__PURE__ */ jsxRuntimeExports.jsx(
              "button",
              {
                className: "button",
                type: "button",
                onClick: () => {
                  resetOperation();
                  void detail.refetch();
                },
                children: "Retry detail"
              }
            )
          ] }) : /* @__PURE__ */ jsxRuntimeExports.jsxs(jsxRuntimeExports.Fragment, { children: [
            /* @__PURE__ */ jsxRuntimeExports.jsxs("dl", { className: "module-facts", children: [
              /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
                /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Module" }),
                /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: detail.data.module.summary.module_id })
              ] }),
              /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
                /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Version" }),
                /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: detail.data.module.summary.exact_version })
              ] }),
              /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
                /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Execution" }),
                /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: detail.data.module.summary.execution_class })
              ] }),
              /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
                /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Adapter" }),
                /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { children: detail.data.module.summary.adapter_identity })
              ] }),
              /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
                /* @__PURE__ */ jsxRuntimeExports.jsx("dt", { children: "Artifact" }),
                /* @__PURE__ */ jsxRuntimeExports.jsx("dd", { className: "digest", children: shortDigest(detail.data.module.summary.artifact_digest) })
              ] })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { className: "module-bindings", children: [
              /* @__PURE__ */ jsxRuntimeExports.jsx("h3", { children: "Visible bindings" }),
              detail.data.module.bindings.length === 0 ? /* @__PURE__ */ jsxRuntimeExports.jsx("p", { className: "empty-copy", children: "No binding is visible in this authorized scope." }) : /* @__PURE__ */ jsxRuntimeExports.jsx("ul", { className: "module-binding-list", children: detail.data.module.bindings.map((binding) => {
                const candidate = isModuleDisableCandidate(
                  detail.data.module.summary,
                  binding
                );
                const lowestCandidate = candidate && isLowestDisableCandidate(
                  detail.data.module.summary,
                  detail.data.module.bindings,
                  binding
                );
                const key = [
                  binding.target.kind,
                  binding.target.kind === "PROFILE" ? binding.target.profile_id : `${binding.target.workspace_id}:${binding.target.endpoint_id}`,
                  binding.port.name,
                  binding.port.exact_version,
                  binding.port_binding_index
                ].join(":");
                return /* @__PURE__ */ jsxRuntimeExports.jsxs("li", { className: "module-binding", children: [
                  /* @__PURE__ */ jsxRuntimeExports.jsxs("div", { children: [
                    /* @__PURE__ */ jsxRuntimeExports.jsx("strong", { children: bindingTargetLabel(binding) }),
                    /* @__PURE__ */ jsxRuntimeExports.jsxs("span", { children: [
                      binding.port.name,
                      "/",
                      binding.port.exact_version,
                      ` / ${binding.failure_policy} / index ${binding.port_binding_index}`
                    ] }),
                    /* @__PURE__ */ jsxRuntimeExports.jsxs("small", { children: [
                      binding.static_context_refs.length,
                      " static context refs"
                    ] })
                  ] }),
                  candidate && lowestCandidate && canOperate ? /* @__PURE__ */ jsxRuntimeExports.jsx(
                    "button",
                    {
                      className: "button",
                      type: "button",
                      disabled: detail.isFetching || operation.phase !== "IDLE",
                      onClick: () => startDryRun(detail.data.module.summary, binding),
                      children: "Review disable"
                    }
                  ) : candidate && !lowestCandidate ? /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "tag", children: "Higher duplicate binding index" }) : candidate ? /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "tag", children: "Tenant OPERATE_MODULES required" }) : /* @__PURE__ */ jsxRuntimeExports.jsx("span", { className: "tag", children: "Outside narrow disable candidate" })
                ] }, key);
              }) })
            ] }),
            /* @__PURE__ */ jsxRuntimeExports.jsx(
              OperationPanel,
              {
                state: operation,
                confirmationAccepted,
                onConfirmationAccepted: setConfirmationAccepted,
                onRequestConfirmation: () => {
                  void runConfirmation();
                },
                onAcknowledgeDryResult: acknowledgeTerminalDryRun,
                onExplicitConfirm: () => {
                  if (confirmationAccepted) void runMutation(false);
                },
                onExactRetry: () => {
                  void runMutation(true);
                },
                onRetryStep: retryStep,
                onReset: resetOperation,
                onReload: reloadAuthority
              }
            )
          ] })
        ] })
      ] }),
      /* @__PURE__ */ jsxRuntimeExports.jsxs("footer", { className: "page-footer", children: [
        /* @__PURE__ */ jsxRuntimeExports.jsx("span", { children: "Reads are bounded and same-origin. MODULE_DISABLE is server-authoritative and non-optimistic." }),
        /* @__PURE__ */ jsxRuntimeExports.jsxs("span", { className: "digest", children: [
          "Pointer ",
          shortDigest(modules.data.pages[0].published_pointer.digest)
        ] })
      ] })
    ] })
  ] });
}
const activeSessionFromExchange = (origin, exchange) => ({
  origin,
  session: exchange.session,
  csrfToken: exchange.csrf_token,
  authorizedScopes: exchange.authorized_scopes
});
const errorMessage = (error) => error instanceof Error ? error.message : "the control session could not be opened";
const currentHash = () => typeof window === "undefined" ? "" : window.location.hash;
function App() {
  const queryClient2 = useQueryClient();
  const [checkingResume, setCheckingResume] = reactExports.useState(true);
  const [openingSession, setOpeningSession] = reactExports.useState(false);
  const [sessionError, setSessionError] = reactExports.useState("");
  const [storageWarning, setStorageWarning] = reactExports.useState("");
  const [active, setActive] = reactExports.useState(null);
  const [sessionEpoch, setSessionEpoch] = reactExports.useState(0);
  const [selectedScopeKey, setSelectedScopeKey] = reactExports.useState("");
  const [workspacesByTenant, setWorkspacesByTenant] = reactExports.useState(/* @__PURE__ */ new Map());
  const [search, setSearch] = reactExports.useState("");
  const [hash, setHash] = reactExports.useState(currentHash);
  const [detailSnapshot, setDetailSnapshot] = reactExports.useState(null);
  const [permissionRevoked, setPermissionRevoked] = reactExports.useState(false);
  const acceptExchange = reactExports.useCallback(
    (origin, exchange) => {
      clearOverviewTransportCache();
      clearModulesTransportCache();
      queryClient2.clear();
      setWorkspacesByTenant(/* @__PURE__ */ new Map());
      setSearch("");
      setDetailSnapshot(null);
      setPermissionRevoked(false);
      const stored = storeResume(origin, exchange.resume_credential);
      if (!stored) {
        clearStoredResume();
        setStorageWarning("This browser could not retain a tab-scoped resume credential.");
      } else {
        setStorageWarning("");
      }
      const next = activeSessionFromExchange(origin, exchange);
      setSessionEpoch((value) => value + 1);
      setActive(next);
      setSelectedScopeKey(scopeKey(next.authorizedScopes[0]));
      setSessionError("");
    },
    [queryClient2]
  );
  const closeActiveSession = reactExports.useCallback(
    (message) => {
      clearStoredResume();
      clearOverviewTransportCache();
      clearModulesTransportCache();
      queryClient2.clear();
      setActive(null);
      setSelectedScopeKey("");
      setWorkspacesByTenant(/* @__PURE__ */ new Map());
      setSearch("");
      setDetailSnapshot(null);
      setPermissionRevoked(false);
      setSessionError(message);
    },
    [queryClient2]
  );
  reactExports.useEffect(() => {
    let cancelled = false;
    const stored = readStoredResume();
    if (stored === null) {
      setCheckingResume(false);
      return () => {
        cancelled = true;
      };
    }
    void resumeSession(stored.origin, stored.resume_credential).then((exchange) => {
      if (!cancelled) acceptExchange(stored.origin, exchange);
    }).catch((error) => {
      if (cancelled) return;
      if (shouldDiscardResume(error)) clearStoredResume();
      setSessionError(`Stored session could not be resumed: ${errorMessage(error)}`);
    }).finally(() => {
      if (!cancelled) setCheckingResume(false);
    });
    return () => {
      cancelled = true;
    };
  }, [acceptExchange]);
  reactExports.useEffect(() => {
    const onHashChange = () => setHash(window.location.hash);
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);
  const detailLink = reactExports.useMemo(() => parseDetailHash(hash), [hash]);
  const onHandoffFile = reactExports.useCallback(
    async (event) => {
      const input = event.currentTarget;
      const file = input.files?.[0];
      input.value = "";
      if (file === void 0) return;
      setOpeningSession(true);
      setSessionError("");
      let text = "";
      let handoff = null;
      try {
        if (file.size > MAX_HANDOFF_BYTES) {
          throw new Error("handoff file exceeds the 4 KiB limit");
        }
        text = await file.text();
        handoff = decodeHandoff(text);
        const exchange = await exchangeHandoff(handoff);
        acceptExchange(handoff.origin, exchange);
      } catch (error) {
        setSessionError(errorMessage(error));
      } finally {
        if (handoff !== null) handoff.capability = "";
        text = "";
        setOpeningSession(false);
      }
    },
    [acceptExchange]
  );
  const observes = active?.session.capabilities.includes("OBSERVE") ?? false;
  const scopeChoices = reactExports.useMemo(
    () => buildScopeChoices(active?.authorizedScopes ?? [], workspacesByTenant),
    [active?.authorizedScopes, workspacesByTenant]
  );
  const selectedScope = reactExports.useMemo(
    () => scopeChoices.find((choice) => choice.key === selectedScopeKey)?.scope ?? null,
    [scopeChoices, selectedScopeKey]
  );
  reactExports.useEffect(() => {
    if (scopeChoices.length === 0) return;
    if (!scopeChoices.some((choice) => choice.key === selectedScopeKey)) {
      setSearch("");
      setSelectedScopeKey(scopeChoices[0].key);
    }
  }, [scopeChoices, selectedScopeKey]);
  const modulesSelected = hash === "#modules";
  const overviewContext = active !== null && observes && selectedScope !== null ? {
    origin: active.origin,
    bootID: active.session.boot_id,
    sessionID: active.session.session_id,
    principalID: active.session.principal_id,
    authorizationRevision: active.session.authorization_revision,
    scopeSetDigest: active.session.scope_set_digest,
    csrfToken: active.csrfToken,
    scope: selectedScope
  } : null;
  const moduleContext = active !== null && overviewContext !== null ? {
    ...overviewContext,
    sessionEpoch,
    sessionExpiresAtUnixMicros: active.session.expires_at_unix_micros
  } : null;
  const overview = useQuery({
    queryKey: overviewContext === null ? ["overview", "disabled"] : overviewQueryKey(overviewContext),
    queryFn: ({ signal }) => {
      if (overviewContext === null) throw new Error("overview query is disabled");
      return fetchOverview(overviewContext, signal);
    },
    enabled: overviewContext !== null,
    staleTime: 3e4,
    retry: false
  });
  reactExports.useEffect(() => {
    if (overview.data === void 0 || selectedScope === null || selectedScope.kind !== "TENANT") return;
    setWorkspacesByTenant((current) => {
      const existing = current.get(selectedScope.tenant_id);
      if (existing === overview.data?.workspaces) return current;
      const next = new Map(current);
      next.set(selectedScope.tenant_id, overview.data.workspaces);
      return next;
    });
  }, [overview.data, selectedScope]);
  reactExports.useEffect(() => {
    if (detailLink === null) {
      setDetailSnapshot(null);
      return;
    }
    if (overview.data === void 0) return;
    setDetailSnapshot((current) => {
      if (current !== null && current.link.section === detailLink.section && current.link.id === detailLink.id) return current;
      return captureDetailSnapshot(overview.data, detailLink);
    });
  }, [detailLink, overview.data]);
  reactExports.useEffect(() => {
    if (overview.error === null || !isSessionInvalid(overview.error)) return;
    closeActiveSession(
      "The control session expired or is no longer authenticated. Open a new handoff."
    );
  }, [closeActiveSession, overview.error]);
  reactExports.useEffect(() => {
    if (overview.error === null || !isPermissionDenied(overview.error)) return;
    setPermissionRevoked(true);
    clearOverviewTransportCache();
    clearModulesTransportCache();
    queryClient2.removeQueries({ queryKey: ["overview"] });
    queryClient2.removeQueries({ queryKey: ["modules"] });
    queryClient2.removeQueries({ queryKey: ["module-detail"] });
  }, [overview.error, queryClient2]);
  const changeScope = reactExports.useCallback(
    (key) => {
      clearModulesTransportCache();
      queryClient2.removeQueries({ queryKey: ["modules"] });
      queryClient2.removeQueries({ queryKey: ["module-detail"] });
      setPermissionRevoked(false);
      setSelectedScopeKey(key);
      setSearch("");
      setDetailSnapshot(null);
    },
    [queryClient2]
  );
  const handleModulesFailure = reactExports.useCallback(
    (event) => {
      if (event.kind === "SESSION") {
        closeActiveSession(
          "The control session expired or is no longer authenticated. Open a new handoff."
        );
        return;
      }
      if (event.kind !== "PERMISSION") return;
      setPermissionRevoked(true);
      clearOverviewTransportCache();
      clearModulesTransportCache();
      queryClient2.removeQueries({ queryKey: ["overview"] });
      queryClient2.removeQueries({ queryKey: ["modules"] });
      queryClient2.removeQueries({ queryKey: ["module-detail"] });
    },
    [closeActiveSession, queryClient2]
  );
  if (checkingResume) return /* @__PURE__ */ jsxRuntimeExports.jsx(LoadingPanel, { label: "Checking this tab session…" });
  if (active === null) {
    return /* @__PURE__ */ jsxRuntimeExports.jsx(HandoffPanel, { busy: openingSession, error: sessionError, onFile: onHandoffFile });
  }
  if (!observes) return /* @__PURE__ */ jsxRuntimeExports.jsx(PermissionPanel, { principalID: active.session.principal_id });
  if (permissionRevoked || overview.error !== null && isPermissionDenied(overview.error)) {
    const denied = overview.error;
    return /* @__PURE__ */ jsxRuntimeExports.jsx(
      FatalOverviewPanel,
      {
        permissionDenied: true,
        message: denied?.message ?? "the selected scope is no longer authorized",
        correlationID: denied?.correlationID ?? "",
        onRetry: () => void 0
      }
    );
  }
  if (modulesSelected && moduleContext !== null) {
    return /* @__PURE__ */ jsxRuntimeExports.jsx(
      ModulesPage,
      {
        session: active.session,
        context: moduleContext,
        scopeChoices,
        selectedScopeKey,
        navigationKey: hash,
        onScopeChange: changeScope,
        onNavigateOverview: () => {
          window.location.hash = "overview";
        },
        onFailClosed: handleModulesFailure
      },
      `${active.session.session_id}:${selectedScopeKey}:modules`
    );
  }
  if (overview.isPending || overview.data === void 0) {
    if (overview.error !== null) {
      return /* @__PURE__ */ jsxRuntimeExports.jsx(
        FatalOverviewPanel,
        {
          permissionDenied: isPermissionDenied(overview.error),
          message: overview.error.message,
          correlationID: overview.error.correlationID,
          onRetry: () => {
            void overview.refetch();
          }
        }
      );
    }
    return /* @__PURE__ */ jsxRuntimeExports.jsx(LoadingPanel, {});
  }
  const backgroundError = overview.error?.message ?? storageWarning;
  return /* @__PURE__ */ jsxRuntimeExports.jsx(SessionInvalidBoundary, { error: overview.error, children: /* @__PURE__ */ jsxRuntimeExports.jsx(
    OverviewPage,
    {
      session: active.session,
      overview: overview.data,
      scopeChoices,
      selectedScopeKey,
      search,
      stale: overview.isStale || overview.error !== null,
      refreshing: overview.isFetching,
      backgroundError,
      detailSnapshot,
      onScopeChange: changeScope,
      onSearchChange: setSearch,
      onRefresh: () => {
        void overview.refetch();
      }
    }
  ) });
}
const rootElement = document.getElementById("root");
if (rootElement === null) {
  throw new Error("control web root element is missing");
}
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnReconnect: false,
      refetchOnWindowFocus: false,
      retry: false
    }
  }
});
clientExports.createRoot(rootElement).render(
  /* @__PURE__ */ jsxRuntimeExports.jsx(QueryClientProvider, { client: queryClient, children: /* @__PURE__ */ jsxRuntimeExports.jsx(App, {}) })
);
