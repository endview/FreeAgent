import {
  MAX_RESPONSE_BYTES,
  CONTROL_SCOPE_ID_ENCODING,
  CONTROL_SCOPE_ID_ENCODING_HEADER,
  decodeControlError,
  decodeOverview,
  exactLoopbackOrigin,
  encodeControlScopeID,
  scopeKey,
  validateOverviewDigests,
  type ControlScope,
  type OverviewResponse,
  type WorkspaceRef
} from "./contracts.ts";
import { createI18nRuntime, type TranslationOptions } from "./i18n/core.ts";
import { i18nResources } from "./i18n/resources.ts";

export const OVERVIEW_PATH = "/control/api/v1/overview";
const STRONG_ETAG_PATTERN = /^"[0-9a-f]{64}"$/u;

export type OverviewContext = {
  origin: string;
  bootID: string;
  sessionID: string;
  principalID: string;
  authorizationRevision: number;
  scopeSetDigest: string;
  csrfToken: string;
  scope: ControlScope;
};

export type OverviewQueryKey = readonly [
  "overview",
  string,
  string,
  string,
  string,
  number,
  string,
  "TENANT" | "WORKSPACE",
  string,
  string
];

export class ControlOverviewError extends Error {
  readonly status: number;
  readonly code: string;
  readonly correlationID: string;
  readonly retryAfterSeconds: number;

  constructor(
    message: string,
    status = 0,
    code = "TRANSPORT_ERROR",
    correlationID = "",
    retryAfterSeconds = 0
  ) {
    super(message);
    this.name = "ControlOverviewError";
    this.status = status;
    this.code = code;
    this.correlationID = correlationID;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

export const overviewQueryKey = (context: OverviewContext): OverviewQueryKey => [
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

export const overviewHeaders = (
  csrfToken: string,
  scope: ControlScope,
  etag?: string
) => {
  const headers: Record<string, string> = {
    "X-FreeAgent-CSRF": csrfToken,
    "X-FreeAgent-Scope-Kind": scope.kind,
    [CONTROL_SCOPE_ID_ENCODING_HEADER]: CONTROL_SCOPE_ID_ENCODING,
    "X-FreeAgent-Tenant-ID": encodeControlScopeID(scope.tenant_id)
  };
  if (scope.kind === "WORKSPACE") {
    headers["X-FreeAgent-Workspace-ID"] = encodeControlScopeID(scope.workspace_id ?? "");
  }
  if (etag !== undefined) headers["If-None-Match"] = etag;
  return headers;
};

type CachedOverview = { etag: string; data: OverviewResponse };
const etagCache = new Map<string, CachedOverview>();

const cacheKey = (key: OverviewQueryKey) => JSON.stringify(key);

export const clearOverviewTransportCache = () => etagCache.clear();

const readError = async (response: Response) => {
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

export const fetchOverview = async (
  context: OverviewContext,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<OverviewResponse> => {
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
    if (stored === undefined) {
      throw new ControlOverviewError("overview returned 304 without a local representation", 304, "INVALID_304");
    }
    const body = await response.text();
    if (
      response.headers.get("ETag") !== stored.etag ||
      response.headers.get("Content-Type") !== null ||
      body !== ""
    ) {
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
  if (etag === null || !STRONG_ETAG_PATTERN.test(etag)) {
    throw new ControlOverviewError("overview omitted its exact strong ETag", response.status, "INVALID_RESPONSE");
  }
  const text = await response.text();
  if (new TextEncoder().encode(text).length > MAX_RESPONSE_BYTES) {
    throw new ControlOverviewError("overview response exceeds the 1 MiB limit", response.status, "INVALID_RESPONSE");
  }
  let data: OverviewResponse;
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

export const isPermissionDenied = (error: unknown) =>
  error instanceof ControlOverviewError &&
  (error.status === 403 || error.code === "FORBIDDEN" || error.code === "PERMISSION_DENIED");

export const isSessionInvalid = (error: unknown) =>
  error instanceof ControlOverviewError &&
  (error.status === 401 || error.code === "UNAUTHENTICATED" || error.code === "SESSION_EXPIRED");

export type ScopeChoice = {
  key: string;
  label: string;
  scope: ControlScope;
  source: "authorized" | "tenant-workspace";
};

type OverviewTranslator = (
  key: string,
  options?: TranslationOptions
) => string;

const defaultOverviewTranslator = createI18nRuntime(
  "en-US",
  i18nResources
).t;

export const buildScopeChoices = (
  authorizedScopes: readonly ControlScope[],
  workspacesByTenant: ReadonlyMap<string, readonly WorkspaceRef[]>,
  translate: OverviewTranslator = defaultOverviewTranslator
): ScopeChoice[] => {
  const choices = new Map<string, ScopeChoice>();
  for (const scope of authorizedScopes) {
    const key = scopeKey(scope);
    choices.set(key, {
      key,
      label:
        scope.kind === "TENANT"
          ? translate("scope.tenant", { values: { tenant: scope.tenant_id } })
          : translate("scope.workspace", {
              values: {
                tenant: scope.tenant_id,
                workspace: scope.workspace_id ?? ""
              }
            }),
      scope,
      source: "authorized"
    });
    if (scope.kind !== "TENANT") continue;
    const workspaces = workspacesByTenant.get(scope.tenant_id) ?? [];
    for (const workspace of workspaces.slice(0, 256)) {
      const derived: ControlScope = {
        schema_version: "control-scope/v1",
        kind: "WORKSPACE",
        tenant_id: scope.tenant_id,
        workspace_id: workspace.id
      };
      const derivedKey = scopeKey(derived);
      if (!choices.has(derivedKey)) {
        choices.set(derivedKey, {
          key: derivedKey,
          label: translate("scope.workspace", {
            values: { tenant: scope.tenant_id, workspace: workspace.id }
          }),
          scope: derived,
          source: "tenant-workspace"
        });
      }
    }
  }
  return [...choices.values()].sort((left, right) => left.label.localeCompare(right.label, "en"));
};

export type DetailSection =
  | "workspaces"
  | "runs"
  | "unknown"
  | "learning"
  | "module-candidates"
  | "usage";

export type SearchResult = {
  section: DetailSection;
  id: string;
  title: string;
  summary: string;
  value: unknown;
};

export const overviewSearchResults = (
  overview: OverviewResponse,
  rawSearch: string,
  translate: OverviewTranslator = defaultOverviewTranslator
): SearchResult[] => {
  const rows: SearchResult[] = [
    ...overview.workspaces.map((item) => ({
      section: "workspaces" as const,
      id: item.id,
      title: item.id,
      summary: translate("overview.result.workspace", {
        values: { version: item.version }
      }),
      value: item
    })),
    ...overview.runs.map((item) => ({
      section: "runs" as const,
      id: item.run_id,
      title: item.run_id,
      summary: translate("overview.result.run", {
        values: {
          state: item.state,
          disposition: item.disposition ? ` · ${item.disposition}` : ""
        }
      }),
      value: item
    })),
    ...overview.unknown.map((item) => ({
      section: "unknown" as const,
      id: `${item.kind}:${item.resource_id}`,
      title: item.resource_id,
      summary: item.kind,
      value: item
    })),
    ...overview.learning.map((item) => ({
      section: "learning" as const,
      id: item.proposal_id,
      title: item.proposal_id,
      summary: translate("overview.result.learning", {
        values: { kind: item.kind, state: item.state }
      }),
      value: item
    })),
    ...overview.module_candidates.map((item) => ({
      section: "module-candidates" as const,
      id: item.review_id,
      title: item.target_module_id,
      summary: translate("overview.result.moduleCandidate", {
        values: {
          current: item.current_exact_version,
          target: item.target_exact_version,
          conclusion: item.conclusion
        }
      }),
      value: item
    })),
    ...overview.usage.map((item) => ({
      section: "usage" as const,
      id: item.attempt_id,
      title: item.attempt_id,
      summary: translate("overview.result.usage", {
        values: { status: item.reconciliation_status, run: item.run_id }
      }),
      value: item
    }))
  ];
  const search = rawSearch.trim().toLocaleLowerCase("en");
  if (search === "") return rows;
  return rows.filter((row) =>
    `${row.section}\n${row.title}\n${row.summary}\n${JSON.stringify(row.value)}`
      .toLocaleLowerCase("en")
      .includes(search)
  );
};

export type DetailLink = { section: DetailSection; id: string };
export type DetailSnapshot = {
  link: DetailLink;
  result: SearchResult | null;
  scope: ControlScope;
};
const DETAIL_SECTIONS = new Set<DetailSection>([
  "workspaces", "runs", "unknown", "learning", "module-candidates", "usage"
]);

export const detailHash = (detail: DetailLink) =>
  `#detail=${encodeURIComponent(`${detail.section}:${detail.id}`)}`;

export const parseDetailHash = (hash: string): DetailLink | null => {
  if (hash.length > 2048 || !hash.startsWith("#detail=")) return null;
  let decoded: string;
  try {
    decoded = decodeURIComponent(hash.slice("#detail=".length));
  } catch {
    return null;
  }
  const separator = decoded.indexOf(":");
  if (separator < 1) return null;
  const section = decoded.slice(0, separator) as DetailSection;
  const id = decoded.slice(separator + 1);
  if (
    !DETAIL_SECTIONS.has(section) ||
    id === "" ||
    new TextEncoder().encode(id).length > 1024 ||
    /[\u0000-\u001f\u007f]/u.test(id)
  ) return null;
  return { section, id };
};

export const captureDetailSnapshot = (
  overview: OverviewResponse,
  link: DetailLink,
  translate: OverviewTranslator = defaultOverviewTranslator
): DetailSnapshot => ({
  link,
  result: overviewSearchResults(overview, "", translate).find(
    (item) => item.section === link.section && item.id === link.id
  ) ?? null,
  scope: overview.view.scope
});
