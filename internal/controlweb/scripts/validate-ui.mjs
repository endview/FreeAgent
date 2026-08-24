import { readFileSync } from "node:fs";

const i18nFiles = [
  "src/i18n/catalogs/en-US.ts",
  "src/i18n/catalogs/zh-CN.ts",
  "src/i18n/core.ts",
  "src/i18n/index.ts",
  "src/i18n/locale-preference-store.ts",
  "src/i18n/locale-selector.tsx",
  "src/i18n/provider.tsx",
  "src/i18n/resources.ts"
];
const files = [
  "src/app.tsx",
  "src/contracts.ts",
  "src/main.tsx",
  "src/modules-ui.tsx",
  "src/modules.ts",
  "src/overview.ts",
  "src/session.ts",
  "src/ui.tsx",
  ...i18nFiles
];
const sources = new Map(files.map((path) => [path, readFileSync(path, "utf8")]));
const source = [...sources.values()].join("\n");

const occurrences = (text, pattern) => [...text.matchAll(pattern)].length;
const requireCount = (path, label, pattern, expected) => {
  const actual = occurrences(sources.get(path) ?? "", pattern);
  if (actual !== expected) {
    throw new Error(`[${label}] ${path} count=${actual} expected=${expected}`);
  }
};

const forbidden = [
  ["EVENT_SOURCE_FORBIDDEN", /\bEventSource\b/u],
  ["WEBSOCKET_FORBIDDEN", /\bWebSocket\b/u],
  ["BROADCAST_CHANNEL_FORBIDDEN", /\bBroadcastChannel\b/u],
  ["SERVICE_WORKER_FORBIDDEN", /\bserviceWorker\b/u],
  ["BEACON_FORBIDDEN", /\bsendBeacon\b/u],
  ["WORKER_FORBIDDEN", /\b(?:Shared)?Worker\b/u],
  ["URL_SEARCH_PARAMS_FORBIDDEN", /\bURLSearchParams\b/u],
  ["MUTATION_METHOD_FORBIDDEN", /method:\s*["'](?:DELETE|PATCH|PUT)["']/u]
];
for (const [code, pattern] of forbidden) {
  if (pattern.test(source)) throw new Error(`[${code}] control-web source violates W6-4 policy`);
}

const localePreferencePath = "src/i18n/locale-preference-store.ts";
for (const [path, text] of sources) {
  if (path !== localePreferencePath && /\blocalStorage\b/u.test(text)) {
    throw new Error(`[LOCAL_STORAGE_SCOPE_INVALID] ${path}`);
  }
}
requireCount(
  localePreferencePath,
  "LOCALE_STORAGE_OWNER_INVALID",
  /\blocalStorage\b/gu,
  1
);
requireCount(
  localePreferencePath,
  "LOCALE_STORAGE_KEY_INVALID",
  /["']freeagent\.ui\.locale\.v1["']/gu,
  1
);
for (const method of ["getItem", "setItem", "removeItem"]) {
  requireCount(
    localePreferencePath,
    "LOCALE_STORAGE_METHOD_INVALID",
    new RegExp(`\\.${method}\\(LOCALE_PREFERENCE_STORAGE_KEY\\b`, "gu"),
    1
  );
}

for (const [path, text] of sources) {
  if (path !== "src/session.ts" && /\bsessionStorage\b/u.test(text)) {
    throw new Error(`[SESSION_STORAGE_SCOPE_INVALID] ${path} must not use sessionStorage`);
  }
}
if (!/\bsessionStorage\b/u.test(sources.get("src/session.ts") ?? "")) {
  throw new Error("[SESSION_STORAGE_MISSING] session resume storage is not present");
}

const expectedAPIPathsByFile = new Map([
  ["src/overview.ts", ["/control/api/v1/overview"]],
  ["src/modules.ts", [
    "/control/api/v1/modules",
    "/control/api/v1/modules/disable/confirmation",
    "/control/api/v1/modules/disable/dry-run",
    "/control/api/v1/modules/disable/mutate"
  ]]
]);
for (const [path, text] of sources) {
  const expected = [...(expectedAPIPathsByFile.get(path) ?? [])].sort();
  const actual = [...text.matchAll(
    /["'](\/control\/api\/v1\/[A-Za-z0-9_./-]+)["']/gu
  )].map((match) => match[1]).sort();
  if (
    actual.length !== expected.length ||
    actual.some((value, index) => value !== expected[index])
  ) {
    throw new Error(`[CONTROL_API_OWNER_INVALID] ${path} actual=${JSON.stringify(actual)}`);
  }
}

const allowedOperations = new Set(["MODULE_DISABLE"]);
const moduleAuthoritySource = [
  sources.get("src/modules.ts") ?? "",
  sources.get("src/modules-ui.tsx") ?? ""
].join("\n");
for (const match of moduleAuthoritySource.matchAll(
  /["']((?:MODULE|LEARNING)_[A-Z0-9_]+)["']/gu
)) {
  if (!allowedOperations.has(match[1])) {
    throw new Error(`[MUTATION_OPERATION_FORBIDDEN] ${match[1]} is outside W6-4`);
  }
}

const methodPolicy = new Map([
  ["src/app.tsx", { GET: 0, POST: 0 }],
  ["src/contracts.ts", { GET: 0, POST: 0 }],
  ["src/main.tsx", { GET: 0, POST: 0 }],
  ["src/modules-ui.tsx", { GET: 0, POST: 0 }],
  ["src/modules.ts", { GET: 2, POST: 3 }],
  ["src/overview.ts", { GET: 1, POST: 0 }],
  ["src/session.ts", { GET: 0, POST: 2 }],
  ["src/ui.tsx", { GET: 0, POST: 0 }]
]);
for (const path of i18nFiles) {
  methodPolicy.set(path, { GET: 0, POST: 0 });
}
for (const [path, policy] of methodPolicy) {
  requireCount(path, "GET_METHOD_POLICY_INVALID", /method:\s*["']GET["']/gu, policy.GET);
  requireCount(path, "POST_METHOD_POLICY_INVALID", /method:\s*["']POST["']/gu, policy.POST);
  requireCount(
    path,
    "TOTAL_METHOD_POLICY_INVALID",
    /\bmethod\s*:/gu,
    policy.GET + policy.POST
  );
}

for (const path of [
  "src/app.tsx",
  "src/contracts.ts",
  "src/main.tsx",
  "src/modules-ui.tsx",
  "src/ui.tsx",
  ...i18nFiles
]) {
  if (/\bfetch(?:er)?\s*\(/u.test(sources.get(path) ?? "")) {
    throw new Error(`[DIRECT_TRANSPORT_FORBIDDEN] ${path}`);
  }
}

for (const path of i18nFiles) {
  const text = sources.get(path) ?? "";
  if (/\b(?:dangerouslySetInnerHTML|innerHTML)\b/u.test(text)) {
    throw new Error(`[I18N_RAW_HTML_FORBIDDEN] ${path}`);
  }
}

const modulesUI = sources.get("src/modules-ui.tsx") ?? "";
for (const [label, pattern] of [
  ["USE_MUTATION_FORBIDDEN", /\buseMutation\b/u],
  ["OPTIMISTIC_SET_QUERY_FORBIDDEN", /\bsetQuer(?:yData|iesData)\b/u],
  ["DIRECT_FETCH_FORBIDDEN", /\bfetch\s*\(/u]
]) {
  if (pattern.test(modulesUI)) throw new Error(`[${label}] Modules UI violates authority policy`);
}
for (const queryKey of ["modules", "module-detail", "overview"]) {
  if (!modulesUI.includes(`invalidateQueries({ queryKey: ["${queryKey}"] })`)) {
    throw new Error(`[AUTHORITATIVE_REFETCH_MISSING] ${queryKey}`);
  }
}

if (
  !source.includes('credentials: "same-origin"') ||
  !source.includes('credentials: "include"')
) {
  throw new Error("[SESSION_CREDENTIAL_MODE_MISSING] exact browser credential modes are not present");
}
if (source.includes("X-FreeAgent-Control-Permission")) {
  throw new Error("[AUTHORITY_HEADER_FORBIDDEN] browser-authored permission authority is forbidden");
}

console.log(
  `PASS control-web static policy: ${files.length} source files preserve exact Overview and MODULE_DISABLE-only authority`
);
