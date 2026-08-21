export const HANDOFF_SCHEMA = "freeagent.control-bootstrap-handoff/v1";
export const BOOTSTRAP_RESPONSE_SCHEMA = "control-bootstrap-session/v2";
export const RESUME_RESPONSE_SCHEMA = "control-session-resumed/v1";
export const SESSION_SCHEMA = "control-session/v1";
export const SCOPE_SCHEMA = "control-scope/v1";
export const OVERVIEW_SCHEMA = "control-http-overview/v1";

export const MAX_HANDOFF_BYTES = 4 * 1024;
export const MAX_RESPONSE_BYTES = 1 << 20;
export const MAX_AUTHORIZED_SCOPES = 256;
export const MAX_OVERVIEW_WORKSPACES = 256;
export const MAX_OVERVIEW_ITEMS = 12;

const OPAQUE_VALUE_PATTERN = /^[A-Za-z0-9_-]{43}$/u;
const DIGEST_PATTERN = /^[0-9a-f]{64}$/u;
const LOOPBACK_ORIGIN_PATTERN = /^http:\/\/127\.0\.0\.1:([1-9][0-9]{0,4})$/u;
const CAPABILITIES = new Set([
  "OBSERVE",
  "OPERATE_MODULES",
  "REVIEW_LEARNING",
  "RUN_LEARNING"
]);

export type ControlCapability =
  | "OBSERVE"
  | "OPERATE_MODULES"
  | "REVIEW_LEARNING"
  | "RUN_LEARNING";

export type ControlScope = {
  schema_version: "control-scope/v1";
  kind: "TENANT" | "WORKSPACE";
  tenant_id: string;
  workspace_id?: string;
};

export type ControlSession = {
  schema_version: "control-session/v1";
  boot_id: string;
  session_id: string;
  principal_id: string;
  capabilities: ControlCapability[];
  scope_set_digest: string;
  authorization_revision: number;
  issued_at_unix_micros: number;
  expires_at_unix_micros: number;
};

export type BootstrapHandoff = {
  schema_version: "freeagent.control-bootstrap-handoff/v1";
  origin: string;
  capability: string;
  expires_at_unix_micros: number;
};

export type SessionExchange = {
  schema_version:
    | "control-bootstrap-session/v2"
    | "control-session-resumed/v1";
  session: ControlSession;
  authorized_scopes: ControlScope[];
  csrf_token: string;
  resume_credential: string;
};

export type DigestRef = {
  id: string;
  revision: number;
  digest: string;
};

export type PublishedBasis = {
  tenant_id: string;
  pointer_revision: number;
  control: DigestRef;
  catalog: DigestRef;
};

export type ExpectedResourceRef = {
  kind: string;
  resource_id: string;
  revision: number;
  digest: string;
};

export type ViewSection = {
  kind: string;
  source_revision: number;
  source_digest: string;
  item_count: number;
  truncated: boolean;
};

export type ControlView = {
  schema_version: "control-view-snapshot/v1";
  scope: ControlScope;
  scope_digest: string;
  observed_at_unix_micros: number;
  basis: PublishedBasis;
  sections: ViewSection[];
};

export type WorkspaceRef = {
  id: string;
  version: string;
  digest: string;
};

export type RunItem = {
  tenant_id: string;
  workspace_id: string;
  run_id: string;
  state: string;
  disposition?: string;
  revision: number;
  created_at_unix_micros: number;
  updated_at_unix_micros: number;
};

export type UnknownItem = {
  kind: string;
  resource_id: string;
  tenant_id: string;
  workspace_id: string;
  run_id: string;
  revision: number;
  updated_at_unix_micros: number;
};

export type LearningItem = {
  proposal_id: string;
  tenant_id: string;
  workspace_id: string;
  kind: string;
  state: string;
  revision: number;
  created_at_unix_micros: number;
  updated_at_unix_micros: number;
};

export type ModuleCandidateItem = {
  review_id: string;
  candidate_id: string;
  tenant_id: string;
  workspace_id?: string;
  binding_target_kind: string;
  current_instance_id: string;
  target_instance_id: string;
  current_module_id: string;
  current_exact_version: string;
  current_artifact_digest: string;
  target_module_id: string;
  target_exact_version: string;
  target_artifact_digest: string;
  conclusion: string;
  created_at_unix_micros: number;
};

export type UsageItem = {
  attempt_id: string;
  run_id: string;
  tenant_id: string;
  workspace_id: string;
  revision: number;
  input_tokens: number | null;
  cached_input_tokens: number | null;
  uncached_input_tokens: number | null;
  output_tokens: number | null;
  reasoning_tokens: number | null;
  reconciliation_status: string;
  updated_at_unix_micros: number;
};

export type OverviewResponse = {
  schema_version: "control-http-overview/v1";
  published_pointer: ExpectedResourceRef;
  basis: PublishedBasis;
  view: ControlView;
  view_snapshot_digest: string;
  workspaces: WorkspaceRef[];
  workspaces_truncated: false;
  runs: RunItem[];
  runs_truncated: boolean;
  unknown: UnknownItem[];
  unknown_truncated: boolean;
  learning: LearningItem[];
  learning_truncated: boolean;
  module_candidates: ModuleCandidateItem[];
  module_candidates_truncated: boolean;
  usage: UsageItem[];
  usage_truncated: boolean;
  projection_digest: string;
};

export type ControlErrorBody = {
  schema_version: "control-error/v1";
  code: string;
  correlation_id: string;
  message: string;
  retry_after_seconds?: number;
};

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
  Number.isSafeInteger(value) &&
  (value as number) >= (positive ? 1 : 0);

const wellFormedUTF8 = (value: string) => {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(
      new TextEncoder().encode(value)
    ) === value;
  } catch {
    return false;
  }
};

const opaque = (value: unknown, maximum = 256): value is string =>
  typeof value === "string" &&
  value.length > 0 &&
  wellFormedUTF8(value) &&
  new TextEncoder().encode(value).length <= maximum &&
  value === value.trim() &&
  value === value.normalize("NFC") &&
  !/[\u0000-\u001f\u007f]/u.test(value);

export const CONTROL_SCOPE_ID_ENCODING = "base64url-utf8-v1";
export const CONTROL_SCOPE_ID_ENCODING_HEADER = "X-FreeAgent-Scope-ID-Encoding";

export const encodeControlScopeID = (value: string) => {
  if (!opaque(value)) throw new Error("Control scope ID is invalid");
  const bytes = new TextEncoder().encode(value);
  const binary = [...bytes].map((byte) => String.fromCharCode(byte)).join("");
  const encoded = btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/u, "");
  if (encoded === "") throw new Error("Control scope ID encoding failed");
  return encoded;
};

const digest = (value: unknown): value is string =>
  typeof value === "string" && DIGEST_PATTERN.test(value);

export const credential = (value: unknown): value is string => {
  if (typeof value !== "string" || !OPAQUE_VALUE_PATTERN.test(value)) return false;
  try {
    const standard = value.replaceAll("-", "+").replaceAll("_", "/") + "=";
    const decoded = atob(standard);
    if (decoded.length !== 32) return false;
    const rebuilt = btoa(decoded)
      .replaceAll("+", "-")
      .replaceAll("/", "_")
      .replace(/=+$/u, "");
    return rebuilt === value;
  } catch {
    return false;
  }
};

export const exactLoopbackOrigin = (value: unknown): value is string => {
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

const rejectDuplicateObjectKeys = (text: string, label: string) => {
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
        return JSON.parse(text.slice(start, index)) as string;
      }
      index += 1;
    }
    throw new Error(`${label} contains an unterminated JSON string`);
  };
  const value = (): void => {
    whitespace();
    if (text[index] === "{") {
      index += 1;
      whitespace();
      const keys = new Set<string>();
      if (text[index] === "}") { index += 1; return; }
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
        if (text[index] === "}") { index += 1; return; }
        if (text[index] !== ",") throw new Error(`${label} contains invalid JSON`);
        index += 1;
        whitespace();
      }
      throw new Error(`${label} contains invalid JSON`);
    }
    if (text[index] === "[") {
      index += 1;
      whitespace();
      if (text[index] === "]") { index += 1; return; }
      while (index < text.length) {
        value();
        whitespace();
        if (text[index] === "]") { index += 1; return; }
        if (text[index] !== ",") throw new Error(`${label} contains invalid JSON`);
        index += 1;
      }
      throw new Error(`${label} contains invalid JSON`);
    }
    if (text[index] === '"') { readStringLexeme(); return; }
    while (index < text.length && !/[\t\n\r ,\]}]/u.test(text[index])) index += 1;
  };
  value();
  whitespace();
  if (index !== text.length) throw new Error(`${label} contains trailing JSON material`);
};

const parseJSONRecord = (text: string, label: string) => {
  let decoded: unknown;
  try {
    rejectDuplicateObjectKeys(text, label);
    decoded = JSON.parse(text);
  } catch (error) {
    if (error instanceof Error && error.message.includes("duplicate object key")) {
      throw error;
    }
    throw new Error(`${label} is not valid JSON`);
  }
  if (!isRecord(decoded)) throw new Error(`${label} must be one JSON object`);
  return decoded;
};

export const decodeHandoff = (
  text: string,
  nowUnixMicros = Date.now() * 1000
): BootstrapHandoff => {
  if (new TextEncoder().encode(text).length > MAX_HANDOFF_BYTES) {
    throw new Error("handoff file exceeds the 4 KiB limit");
  }
  const value = parseJSONRecord(text, "handoff file");
  if (
    !exactKeys(value, [
      "schema_version",
      "origin",
      "capability",
      "expires_at_unix_micros"
    ]) ||
    value.schema_version !== HANDOFF_SCHEMA ||
    !exactLoopbackOrigin(value.origin) ||
    !credential(value.capability) ||
    !safeInteger(value.expires_at_unix_micros, true) ||
    value.expires_at_unix_micros <= nowUnixMicros
  ) {
    throw new Error("handoff file is invalid or expired");
  }
  return value as BootstrapHandoff;
};

const decodeScope = (value: unknown): ControlScope => {
  if (!isRecord(value)) throw new Error("authorized scope is not an object");
  if (
    value.schema_version !== SCOPE_SCHEMA ||
    !opaque(value.tenant_id) ||
    (value.kind !== "TENANT" && value.kind !== "WORKSPACE")
  ) {
    throw new Error("authorized scope is invalid");
  }
  if (value.kind === "TENANT") {
    if (!exactKeys(value, ["schema_version", "kind", "tenant_id"], ["workspace_id"])) {
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
  if (
    !exactKeys(value, ["schema_version", "kind", "tenant_id", "workspace_id"]) ||
    !opaque(value.workspace_id)
  ) {
    throw new Error("workspace scope is invalid");
  }
  return value as ControlScope;
};

export const scopeKey = (scope: ControlScope) =>
  `${scope.kind}\u0000${scope.tenant_id}\u0000${scope.workspace_id ?? ""}`;

const scopeOrderKey = (scope: ControlScope) =>
  `${scope.tenant_id}\u0000${scope.kind}\u0000${scope.workspace_id ?? ""}`;

const compareUTF8 = (left: string, right: string) => {
  const leftBytes = new TextEncoder().encode(left);
  const rightBytes = new TextEncoder().encode(right);
  const length = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < length; index += 1) {
    if (leftBytes[index] !== rightBytes[index]) return leftBytes[index] - rightBytes[index];
  }
  return leftBytes.length - rightBytes.length;
};

export const canonicalJSONString = (value: unknown): string => {
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
  if (isRecord(value)) {
    return `{${Object.keys(value)
      .sort()
      .filter((key) => value[key] !== undefined)
      .map((key) => `${JSON.stringify(key)}:${canonicalJSONString(value[key])}`)
      .join(",")}}`;
  }
  throw new Error("canonical JSON contains an unsupported value");
};

export const domainDigest = async (domain: string, canonical: string) => {
  const domainBytes = new TextEncoder().encode(`${domain}\u0000${canonical}`);
  const bytes = new Uint8Array(await crypto.subtle.digest("SHA-256", domainBytes));
  return [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
};

export const authorizedScopeSetDigest = async (scopes: ControlScope[]) =>
  domainDigest(
    "freeagent.control-scope-set/v1",
    canonicalJSONString({ schema_version: "control-scope-set/v1", scopes })
  );

const decodeSession = (value: unknown): ControlSession => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "schema_version",
      "boot_id",
      "session_id",
      "principal_id",
      "capabilities",
      "scope_set_digest",
      "authorization_revision",
      "issued_at_unix_micros",
      "expires_at_unix_micros"
    ]) ||
    value.schema_version !== SESSION_SCHEMA ||
    !opaque(value.boot_id) ||
    !opaque(value.session_id) ||
    !opaque(value.principal_id) ||
    !Array.isArray(value.capabilities) ||
    value.capabilities.length < 1 ||
    value.capabilities.length > 32 ||
    !value.capabilities.every(
      (entry): entry is ControlCapability =>
        typeof entry === "string" && CAPABILITIES.has(entry)
    ) ||
    value.capabilities.some((entry, index, all) => index > 0 && all[index - 1] >= entry) ||
    !digest(value.scope_set_digest) ||
    !safeInteger(value.authorization_revision, true) ||
    !safeInteger(value.issued_at_unix_micros, true) ||
    !safeInteger(value.expires_at_unix_micros, true) ||
    value.expires_at_unix_micros <= value.issued_at_unix_micros ||
    value.expires_at_unix_micros - value.issued_at_unix_micros > 8 * 60 * 60 * 1_000_000
  ) {
    throw new Error("control session metadata is invalid");
  }
  return value as ControlSession;
};

export const decodeSessionExchange = async (
  text: string,
  expectedSchema: SessionExchange["schema_version"]
): Promise<SessionExchange> => {
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) {
    throw new Error("session response exceeds the 1 MiB limit");
  }
  const value = parseJSONRecord(text, "session response");
  if (
    !exactKeys(value, [
      "schema_version",
      "session",
      "authorized_scopes",
      "csrf_token",
      "resume_credential"
    ]) ||
    value.schema_version !== expectedSchema ||
    !credential(value.csrf_token) ||
    !credential(value.resume_credential) ||
    !Array.isArray(value.authorized_scopes) ||
    value.authorized_scopes.length < 1 ||
    value.authorized_scopes.length > MAX_AUTHORIZED_SCOPES
  ) {
    throw new Error("session response envelope is invalid");
  }
  const session = decodeSession(value.session);
  const scopes = value.authorized_scopes.map(decodeScope);
  if (scopes.some((scope, index) =>
    index > 0 && compareUTF8(scopeOrderKey(scopes[index - 1]), scopeOrderKey(scope)) >= 0
  )) {
    throw new Error("authorized scopes are not in canonical server order");
  }
  if ((await authorizedScopeSetDigest(scopes)) !== session.scope_set_digest) {
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

const decodeDigestRef = (value: unknown): DigestRef => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["id", "revision", "digest"]) ||
    !opaque(value.id) ||
    !safeInteger(value.revision) ||
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
    !opaque(value.kind) ||
    !opaque(value.resource_id) ||
    !safeInteger(value.revision, true) ||
    !digest(value.digest)
  ) {
    throw new Error("published pointer reference is invalid");
  }
  return value as ExpectedResourceRef;
};

const decodeViewSection = (value: unknown): ViewSection => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["kind", "source_revision", "source_digest", "item_count", "truncated"]) ||
    !opaque(value.kind) ||
    !safeInteger(value.source_revision, true) ||
    !digest(value.source_digest) ||
    !safeInteger(value.item_count) ||
    typeof value.truncated !== "boolean"
  ) {
    throw new Error("view section is invalid");
  }
  return value as ViewSection;
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
    value.sections.length < 1 ||
    value.sections.length > 32
  ) {
    throw new Error("control view is invalid");
  }
  const sections = value.sections.map(decodeViewSection);
  if (sections.some((section, index) => index > 0 && sections[index - 1].kind >= section.kind)) {
    throw new Error("view sections are not in canonical order");
  }
  return {
    schema_version: "control-view-snapshot/v1",
    scope: decodeScope(value.scope),
    scope_digest: value.scope_digest,
    observed_at_unix_micros: value.observed_at_unix_micros,
    basis: decodeBasis(value.basis),
    sections
  };
};

const decodeWorkspace = (value: unknown): WorkspaceRef => {
  if (
    !isRecord(value) ||
    !exactKeys(value, ["id", "version", "digest"]) ||
    !opaque(value.id) ||
    !opaque(value.version, 64) ||
    !digest(value.digest)
  ) {
    throw new Error("workspace reference is invalid");
  }
  return value as WorkspaceRef;
};

const decodeRun = (value: unknown): RunItem => {
  if (
    !isRecord(value) ||
    !exactKeys(
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
    ) ||
    !opaque(value.tenant_id) ||
    !opaque(value.workspace_id) ||
    !opaque(value.run_id) ||
    !opaque(value.state) ||
    (value.disposition !== undefined && !opaque(value.disposition)) ||
    !safeInteger(value.revision) ||
    !safeInteger(value.created_at_unix_micros, true) ||
    !safeInteger(value.updated_at_unix_micros, true) ||
    value.updated_at_unix_micros < value.created_at_unix_micros
  ) throw new Error("run item is invalid");
  return value as RunItem;
};

const decodeUnknown = (value: unknown): UnknownItem => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "kind", "resource_id", "tenant_id", "workspace_id", "run_id", "revision", "updated_at_unix_micros"
    ]) ||
    !opaque(value.kind) ||
    !opaque(value.resource_id) ||
    !opaque(value.tenant_id) ||
    !opaque(value.workspace_id) ||
    !opaque(value.run_id) ||
    !safeInteger(value.revision) ||
    !safeInteger(value.updated_at_unix_micros, true)
  ) throw new Error("unknown item is invalid");
  return value as UnknownItem;
};

const decodeLearning = (value: unknown): LearningItem => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
      "proposal_id",
      "tenant_id",
      "workspace_id",
      "kind",
      "state",
      "revision",
      "created_at_unix_micros",
      "updated_at_unix_micros"
    ]) ||
    !digest(value.proposal_id) ||
    !opaque(value.tenant_id) ||
    !opaque(value.workspace_id) ||
    !opaque(value.kind) ||
    !opaque(value.state) ||
    !safeInteger(value.revision) ||
    !safeInteger(value.created_at_unix_micros, true) ||
    !safeInteger(value.updated_at_unix_micros, true) ||
    value.updated_at_unix_micros < value.created_at_unix_micros
  ) throw new Error("learning item is invalid");
  return value as LearningItem;
};

const decodeCandidate = (value: unknown): ModuleCandidateItem => {
  if (
    !isRecord(value) ||
    !exactKeys(
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
    ) ||
    !digest(value.review_id) ||
    !digest(value.candidate_id) ||
    !opaque(value.tenant_id) ||
    (value.workspace_id !== undefined && !opaque(value.workspace_id)) ||
    !opaque(value.binding_target_kind) ||
    !opaque(value.current_instance_id) ||
    !opaque(value.target_instance_id) ||
    !opaque(value.current_module_id) ||
    !opaque(value.current_exact_version, 64) ||
    !digest(value.current_artifact_digest) ||
    !opaque(value.target_module_id) ||
    !opaque(value.target_exact_version, 64) ||
    !digest(value.target_artifact_digest) ||
    !opaque(value.conclusion) ||
    !safeInteger(value.created_at_unix_micros, true)
  ) throw new Error("module candidate is invalid");
  return value as ModuleCandidateItem;
};

const nullableTokens = (value: unknown) => value === null || safeInteger(value);

const decodeUsage = (value: unknown): UsageItem => {
  if (
    !isRecord(value) ||
    !exactKeys(value, [
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
    ]) ||
    !opaque(value.attempt_id) ||
    !opaque(value.run_id) ||
    !opaque(value.tenant_id) ||
    !opaque(value.workspace_id) ||
    !safeInteger(value.revision) ||
    !nullableTokens(value.input_tokens) ||
    !nullableTokens(value.cached_input_tokens) ||
    !nullableTokens(value.uncached_input_tokens) ||
    !nullableTokens(value.output_tokens) ||
    !nullableTokens(value.reasoning_tokens) ||
    !opaque(value.reconciliation_status) ||
    !safeInteger(value.updated_at_unix_micros, true)
  ) throw new Error("usage item is invalid");
  return value as UsageItem;
};

const ownsItem = (scope: ControlScope, tenantID: string, workspaceID: string) =>
  tenantID === scope.tenant_id &&
  opaque(workspaceID) &&
  (scope.kind === "TENANT" || workspaceID === scope.workspace_id);

const newerFirst = (
  previousTime: number,
  previousID: string,
  currentTime: number,
  currentID: string
) => previousTime > currentTime ||
  (previousTime === currentTime && compareUTF8(previousID, currentID) > 0);

const validateOverviewSemantics = (overview: OverviewResponse, scope: ControlScope) => {
  const sourceTime = overview.view.observed_at_unix_micros;
  const requireExactTruncationProof = (truncated: boolean, length: number) =>
    !truncated || length === MAX_OVERVIEW_ITEMS;
  if (
    !requireExactTruncationProof(overview.runs_truncated, overview.runs.length) ||
    !requireExactTruncationProof(overview.unknown_truncated, overview.unknown.length) ||
    !requireExactTruncationProof(overview.learning_truncated, overview.learning.length) ||
    !requireExactTruncationProof(
      overview.module_candidates_truncated,
      overview.module_candidates.length
    ) ||
    !requireExactTruncationProof(overview.usage_truncated, overview.usage.length)
  ) throw new Error("overview truncation proof is invalid");
  for (let index = 0; index < overview.workspaces.length; index += 1) {
    const workspace = overview.workspaces[index];
    if (
      (scope.kind === "WORKSPACE" && workspace.id !== scope.workspace_id) ||
      (index > 0 && compareUTF8(overview.workspaces[index - 1].id, workspace.id) >= 0)
    ) throw new Error("overview workspace order or ownership is invalid");
  }
  const validRunProjection = (item: RunItem) =>
    (item.state === "ADMITTED" && (item.disposition === undefined || item.disposition === "WAITING_EXTERNAL")) ||
    (item.revision >= 1 && item.state === "WAITING_RECONCILIATION" &&
      item.disposition === "WAITING_RECONCILIATION") ||
    (item.revision >= 1 && item.state === "TERMINATED" && item.disposition === "TERMINATED");
  const runsByID = new Map<string, RunItem>();
  for (let index = 0; index < overview.runs.length; index += 1) {
    const item = overview.runs[index];
    if (
      runsByID.has(item.run_id) ||
      !ownsItem(scope, item.tenant_id, item.workspace_id) ||
      !validRunProjection(item) ||
      item.updated_at_unix_micros > sourceTime ||
      (index > 0 && !newerFirst(
        overview.runs[index - 1].updated_at_unix_micros,
        overview.runs[index - 1].run_id,
        item.updated_at_unix_micros,
        item.run_id
      ))
    ) throw new Error("overview run semantics, ownership, or order is invalid");
    runsByID.set(item.run_id, item);
  }
  const unknownKinds = new Set([
    "MODEL", "ACTION", "CHANNEL_SEND", "LEARNING_PROPOSAL", "LEARNING_TASK"
  ]);
  const validUnknownRevision = (item: UnknownItem) => {
    switch (item.kind) {
      case "MODEL": return item.revision === 1;
      case "ACTION": return item.revision === 1 || item.revision === 2;
      case "CHANNEL_SEND": return item.revision >= 1;
      case "LEARNING_PROPOSAL":
      case "LEARNING_TASK": return item.revision === 2;
      default: return false;
    }
  };
  const unknownIdentities = new Set<string>();
  const modelUnknownByAttempt = new Map<string, UnknownItem>();
  const proposalUnknownByID = new Map<string, UnknownItem>();
  for (let index = 0; index < overview.unknown.length; index += 1) {
    const item = overview.unknown[index];
    const previous = overview.unknown[index - 1];
    const resourceOrder = index === 0 ? 0 : compareUTF8(previous.resource_id, item.resource_id);
    const resourceIDValid =
      (item.kind === "LEARNING_PROPOSAL" || item.kind === "LEARNING_TASK")
        ? digest(item.resource_id)
        : opaque(item.resource_id);
    const identity = `${item.kind}\u0000${item.resource_id}`;
    const intersectingRun = runsByID.get(item.run_id);
    const ordered = index === 0 ||
      previous.updated_at_unix_micros > item.updated_at_unix_micros ||
      (previous.updated_at_unix_micros === item.updated_at_unix_micros &&
        (resourceOrder > 0 ||
          (resourceOrder === 0 && compareUTF8(previous.kind, item.kind) > 0)));
    if (
      !unknownKinds.has(item.kind) ||
      unknownIdentities.has(identity) ||
      !resourceIDValid ||
      !validUnknownRevision(item) ||
      !ownsItem(scope, item.tenant_id, item.workspace_id) ||
      item.updated_at_unix_micros > sourceTime ||
      (intersectingRun !== undefined && (
        intersectingRun.tenant_id !== item.tenant_id ||
        intersectingRun.workspace_id !== item.workspace_id ||
        intersectingRun.state !== "WAITING_RECONCILIATION" ||
        intersectingRun.disposition !== "WAITING_RECONCILIATION"
      )) ||
      !ordered
    ) {
      throw new Error("overview unknown semantics, ownership, or order is invalid");
    }
    unknownIdentities.add(identity);
    if (item.kind === "MODEL") modelUnknownByAttempt.set(item.resource_id, item);
    if (item.kind === "LEARNING_PROPOSAL") proposalUnknownByID.set(item.resource_id, item);
  }
  const learningKinds = new Set(["KNOWLEDGE", "SKILL"]);
  const validLearningProjection = (item: LearningItem) => {
    switch (item.state) {
      case "SUBMITTED": return item.revision === 0;
      case "REVIEW_PENDING": return item.revision === 1;
      case "REVIEW_UNKNOWN": return item.revision === 2;
      case "APPROVED":
      case "REJECTED":
      case "REVIEW_FAILED": return item.revision === 2 || item.revision === 3;
      default: return false;
    }
  };
  const learningIdentities = new Set<string>();
  for (let index = 0; index < overview.learning.length; index += 1) {
    const item = overview.learning[index];
    const intersectingUnknown = proposalUnknownByID.get(item.proposal_id);
    if (
      learningIdentities.has(item.proposal_id) ||
      !learningKinds.has(item.kind) ||
      !validLearningProjection(item) ||
      !ownsItem(scope, item.tenant_id, item.workspace_id) ||
      item.updated_at_unix_micros > sourceTime ||
      (intersectingUnknown !== undefined && (
        item.state !== "REVIEW_UNKNOWN" ||
        item.revision !== intersectingUnknown.revision ||
        item.tenant_id !== intersectingUnknown.tenant_id ||
        item.workspace_id !== intersectingUnknown.workspace_id ||
        item.updated_at_unix_micros !== intersectingUnknown.updated_at_unix_micros
      )) ||
      (index > 0 && !newerFirst(
        overview.learning[index - 1].updated_at_unix_micros,
        overview.learning[index - 1].proposal_id,
        item.updated_at_unix_micros,
        item.proposal_id
      ))
    ) throw new Error("overview learning semantics, ownership, or order is invalid");
    learningIdentities.add(item.proposal_id);
  }
  const conclusions = new Set(["WOULD_APPLY", "CONFLICT", "UNSUPPORTED"]);
  const moduleReviewIdentities = new Set<string>();
  for (let index = 0; index < overview.module_candidates.length; index += 1) {
    const item = overview.module_candidates[index];
    const validTarget = item.binding_target_kind === "PROFILE"
      ? scope.kind === "TENANT" && item.workspace_id === undefined
      : item.binding_target_kind === "WORKSPACE_CHANNEL_ENDPOINT" &&
        opaque(item.workspace_id) &&
        (scope.kind === "TENANT" || item.workspace_id === scope.workspace_id);
    if (
      moduleReviewIdentities.has(item.review_id) ||
      item.tenant_id !== scope.tenant_id ||
      !validTarget ||
      !conclusions.has(item.conclusion) ||
      item.current_instance_id === item.target_instance_id ||
      item.current_module_id !== item.target_module_id ||
      item.current_exact_version === item.target_exact_version ||
      item.current_artifact_digest === item.target_artifact_digest ||
      item.created_at_unix_micros > sourceTime ||
      (index > 0 && !newerFirst(
        overview.module_candidates[index - 1].created_at_unix_micros,
        overview.module_candidates[index - 1].review_id,
        item.created_at_unix_micros,
        item.review_id
      ))
    ) throw new Error("overview module candidate semantics, ownership, or order is invalid");
    moduleReviewIdentities.add(item.review_id);
  }
  const usageIdentities = new Set<string>();
  for (let index = 0; index < overview.usage.length; index += 1) {
    const item = overview.usage[index];
    const allTokensNull = item.input_tokens === null && item.cached_input_tokens === null &&
      item.uncached_input_tokens === null && item.output_tokens === null &&
      item.reasoning_tokens === null;
    const validUsageProjection =
      (item.reconciliation_status === "PENDING" && item.revision === 0 && allTokensNull) ||
      (item.reconciliation_status === "PENDING_RECONCILIATION" && item.revision === 1) ||
      (item.reconciliation_status === "PROVIDER_REPORTED" &&
        (item.revision === 1 || item.revision === 2)) ||
      (item.reconciliation_status === "NO_USAGE_REPORTED" &&
        item.revision <= 2 && allTokensNull);
    const usageRelationValid = item.input_tokens === null ||
      item.cached_input_tokens === null ||
      item.uncached_input_tokens === null ||
      item.input_tokens === item.cached_input_tokens + item.uncached_input_tokens;
    const intersectingUnknown = modelUnknownByAttempt.get(item.attempt_id);
    if (
      usageIdentities.has(item.attempt_id) ||
      !validUsageProjection ||
      !usageRelationValid ||
      !ownsItem(scope, item.tenant_id, item.workspace_id) ||
      item.updated_at_unix_micros > sourceTime ||
      (intersectingUnknown !== undefined && (
        item.reconciliation_status !== "PENDING_RECONCILIATION" ||
        item.revision !== intersectingUnknown.revision ||
        item.run_id !== intersectingUnknown.run_id ||
        item.tenant_id !== intersectingUnknown.tenant_id ||
        item.workspace_id !== intersectingUnknown.workspace_id ||
        item.updated_at_unix_micros !== intersectingUnknown.updated_at_unix_micros
      )) ||
      (index > 0 && !newerFirst(
        overview.usage[index - 1].updated_at_unix_micros,
        overview.usage[index - 1].attempt_id,
        item.updated_at_unix_micros,
        item.attempt_id
      ))
    ) throw new Error("overview usage semantics, ownership, tokens, or order is invalid");
    usageIdentities.add(item.attempt_id);
  }
};

const decodeBoundedArray = <T>(
  value: unknown,
  maximum: number,
  decoder: (entry: unknown) => T,
  label: string
): T[] => {
  if (!Array.isArray(value) || value.length > maximum) {
    throw new Error(`${label} collection exceeds its bound`);
  }
  return value.map(decoder);
};

export const decodeOverview = (text: string, requestedScope: ControlScope): OverviewResponse => {
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) {
    throw new Error("overview response exceeds the 1 MiB limit");
  }
  const value = parseJSONRecord(text, "overview response");
  const keys = [
    "schema_version", "published_pointer", "basis", "view", "view_snapshot_digest",
    "workspaces", "workspaces_truncated", "runs", "runs_truncated", "unknown",
    "unknown_truncated", "learning", "learning_truncated", "module_candidates",
    "module_candidates_truncated", "usage", "usage_truncated", "projection_digest"
  ];
  if (
    !exactKeys(value, keys) ||
    value.schema_version !== OVERVIEW_SCHEMA ||
    value.workspaces_truncated !== false ||
    typeof value.runs_truncated !== "boolean" ||
    typeof value.unknown_truncated !== "boolean" ||
    typeof value.learning_truncated !== "boolean" ||
    typeof value.module_candidates_truncated !== "boolean" ||
    typeof value.usage_truncated !== "boolean" ||
    !digest(value.view_snapshot_digest) ||
    !digest(value.projection_digest)
  ) throw new Error("overview response envelope is invalid");

  const basis = decodeBasis(value.basis);
  const view = decodeView(value.view);
  if (
    scopeKey(view.scope) !== scopeKey(requestedScope) ||
    basis.tenant_id !== requestedScope.tenant_id ||
    canonicalJSONString(view.basis) !== canonicalJSONString(basis)
  ) throw new Error("overview response is outside the requested scope or basis");

  const overview: OverviewResponse = {
    schema_version: OVERVIEW_SCHEMA,
    published_pointer: decodeExpectedRef(value.published_pointer),
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

const sectionSource = (
  overview: OverviewResponse,
  kind: string
): { items: unknown[]; truncated: boolean } | null => {
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

export const validateOverviewDigests = async (overview: OverviewResponse) => {
  const scopeDigest = await domainDigest(
    "freeagent.control-scope/v1",
    canonicalJSONString(overview.view.scope)
  );
  if (scopeDigest !== overview.view.scope_digest) {
    throw new Error("overview view scope digest is invalid");
  }
  const basisDigest = await domainDigest(
    "freeagent.control-published-pointer-ref/v1",
    canonicalJSONString(overview.basis)
  );
  if (
    overview.published_pointer.kind !== "PUBLISHED_POINTER" ||
    overview.published_pointer.resource_id !== overview.basis.tenant_id ||
    overview.published_pointer.revision !== overview.basis.pointer_revision ||
    overview.published_pointer.digest !== basisDigest
  ) {
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
    if (
      source === null ||
      section.source_revision !== overview.basis.pointer_revision ||
      section.item_count !== source.items.length ||
      section.truncated !== source.truncated
    ) {
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

export const decodeControlError = (text: string): ControlErrorBody | null => {
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) return null;
  let value: Record<string, unknown>;
  try {
    value = parseJSONRecord(text, "control error");
  } catch {
    return null;
  }
  if (
    !exactKeys(
      value,
      ["schema_version", "code", "correlation_id", "message"],
      ["retry_after_seconds"]
    ) ||
    value.schema_version !== "control-error/v1" ||
    !opaque(value.code) ||
    !opaque(value.correlation_id) ||
    !opaque(value.message, 1024) ||
    (value.retry_after_seconds !== undefined && !safeInteger(value.retry_after_seconds))
  ) return null;
  return value as ControlErrorBody;
};
