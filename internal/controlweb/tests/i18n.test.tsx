import assert from "node:assert/strict";
import { renderToStaticMarkup } from "react-dom/server";

import {
  FALLBACK_LOCALE,
  I18nProvider,
  LOCALE_PREFERENCE_STORAGE_KEY,
  LocalePreferenceStore,
  LocaleSelector,
  SUPPORTED_LOCALES,
  assertCatalogCompatibility,
  createI18nRuntime,
  i18nResources,
  interpolateMessage,
  interpolationParameterNames,
  matchSupportedLocale,
  resolveLocale,
  syncDocumentLocale,
  useI18n,
  type LocaleStorage,
  type MessageCatalogs,
  type MissingMessageDiagnostic
} from "../src/i18n/index.ts";

assert.deepEqual(SUPPORTED_LOCALES, ["en-US", "zh-CN"]);
assert.equal(FALLBACK_LOCALE, "en-US");
assert.equal(matchSupportedLocale("zh"), "zh-CN");
assert.equal(matchSupportedLocale("zh-Hans-CN"), "zh-CN");
assert.equal(matchSupportedLocale("en"), "en-US");
assert.equal(matchSupportedLocale("en-GB"), "en-US");
assert.equal(matchSupportedLocale("not_a_locale"), null);
assert.equal(resolveLocale(["fr-FR", "zh-SG"]), "zh-CN");
assert.equal(resolveLocale(["fr-FR"]), "en-US");

assert.deepEqual(
  Object.keys(i18nResources["zh-CN"]).sort(),
  Object.keys(i18nResources["en-US"]).sort()
);
assert.doesNotThrow(() => assertCatalogCompatibility(i18nResources));
assert.deepEqual(
  interpolationParameterNames("{{ count }} / {{name}} / {{name}}"),
  ["count", "name"]
);
assert.throws(
  () => assertCatalogCompatibility({
    "en-US": { greeting: "Hello {{name}}" },
    "zh-CN": { greeting: "你好 {{visitor}}" }
  }),
  /i18n interpolation mismatch/u
);
assert.throws(
  () => assertCatalogCompatibility({
    "en-US": { greeting: "Hello" },
    "zh-CN": { farewell: "再见" }
  }),
  /i18n catalog key mismatch/u
);

const fixtureCatalogs: MessageCatalogs = {
  "en-US": {
    fallback: "Fallback for {{name}}",
    items_one: "{{count}} item",
    items_other: "{{count}} items",
    unfilled: "Hello, {{name}}"
  },
  "zh-CN": {
    local: "你好，{{name}}",
    tasks_other: "共 {{count}} 项"
  }
};
const missingMessages: MissingMessageDiagnostic[] = [];
const zh = createI18nRuntime("zh-CN", fixtureCatalogs);
const en = createI18nRuntime(
  "en-US",
  fixtureCatalogs,
  FALLBACK_LOCALE,
  (diagnostic) => missingMessages.push(diagnostic)
);

assert.equal(zh.t("local", { values: { name: "访客" } }), "你好，访客");
assert.equal(zh.t("fallback", { values: { name: "visitor" } }), "Fallback for visitor");
assert.equal(zh.t("tasks", { count: 1 }), "共 1 项");
assert.equal(zh.t("items", { count: 1 }), "1 item");
assert.equal(zh.t("items", { count: 2 }), "2 items");
assert.equal(en.t("unfilled"), "Hello, {{name}}");
assert.equal(en.t("missing.key", { values: { name: "ignored" } }), "missing.key");
assert.deepEqual(missingMessages, [{
  fallbackLocale: "en-US",
  key: "missing.key",
  locale: "en-US"
}]);
assert.equal(interpolateMessage("{{value}} / {{ missing }}", { value: 7 }), "7 / {{ missing }}");

assert.equal(
  en.formatNumber(1234.5, { minimumFractionDigits: 2, useGrouping: false }),
  "1234.50"
);
assert.equal(en.formatRelativeTime(-1, "day", { numeric: "auto" }), "yesterday");
assert.equal(en.formatList(["A", "B"]), "A and B");
assert.equal(
  en.formatCurrency(1234.5, "USD"),
  new Intl.NumberFormat("en-US", { currency: "USD", style: "currency" }).format(1234.5)
);
assert.equal(
  zh.formatCost(12.5, "CNY"),
  new Intl.NumberFormat("zh-CN", { currency: "CNY", style: "currency" }).format(12.5)
);
assert.equal(
  zh.formatTokenCount(1234.5, { maximumFractionDigits: 1 }),
  new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(1234.5)
);
assert.match(
  en.formatDateTime(Date.UTC(2026, 7, 25), {
    day: "2-digit",
    month: "2-digit",
    timeZone: "UTC",
    year: "numeric"
  }),
  /08\/25\/2026/u
);

const storageValues = new Map<string, string>();
const storageKeys: string[] = [];
const storage: LocaleStorage = {
  getItem: (key) => {
    storageKeys.push(key);
    return storageValues.get(key) ?? null;
  },
  setItem: (key, value) => {
    storageKeys.push(key);
    storageValues.set(key, value);
  },
  removeItem: (key) => {
    storageKeys.push(key);
    storageValues.delete(key);
  }
};

const preference = new LocalePreferenceStore(storage);
assert.equal(preference.read(), null);
assert.equal(preference.write("zh-CN"), true);
assert.equal(preference.read(), "zh-CN");
storageValues.set(LOCALE_PREFERENCE_STORAGE_KEY, "fr-FR");
assert.equal(preference.read(), null);
assert.equal(preference.clear(), true);
assert.ok(storageKeys.length > 0);
assert.ok(storageKeys.every((key) => key === "freeagent.ui.locale.v1"));

storageValues.set(LOCALE_PREFERENCE_STORAGE_KEY, "zh-CN");
const persistedSelectorMarkup = renderToStaticMarkup(
  <I18nProvider initialLocale="en-US" preferenceStore={preference}>
    <LocaleSelector />
  </I18nProvider>
);
assert.match(persistedSelectorMarkup, /value="zh-CN" selected=""/u);
storageValues.delete(LOCALE_PREFERENCE_STORAGE_KEY);

const unavailableStorage: LocaleStorage = {
  getItem: () => { throw new Error("blocked"); },
  setItem: () => { throw new Error("blocked"); },
  removeItem: () => { throw new Error("blocked"); }
};
const unavailablePreference = new LocalePreferenceStore(unavailableStorage);
assert.equal(unavailablePreference.read(), null);
assert.equal(unavailablePreference.write("en-US"), false);
assert.equal(unavailablePreference.clear(), false);

const fakeDocument = { documentElement: { lang: "" }, title: "" };
syncDocumentLocale(fakeDocument, "zh-CN", i18nResources["zh-CN"]["app.title"]);
assert.deepEqual(fakeDocument, {
  documentElement: { lang: "zh-CN" },
  title: "FreeAgent 控制台"
});

const selectorMarkup = renderToStaticMarkup(
  <I18nProvider initialLocale="zh-CN" preferenceStore={preference}>
    <LocaleSelector />
  </I18nProvider>
);
assert.match(selectorMarkup, />语言</u);
assert.match(selectorMarkup, /value="en-US"/u);
assert.match(selectorMarkup, /value="zh-CN" selected=""/u);

function EscapingProbe() {
  const { t } = useI18n();
  return <p>{t("fallback", { values: { name: "<script>alert(1)</script>" } })}</p>;
}

const escapedMarkup = renderToStaticMarkup(
  <I18nProvider
    catalogs={fixtureCatalogs}
    initialLocale="en-US"
    preferenceStore={preference}
  >
    <EscapingProbe />
  </I18nProvider>
);
assert.doesNotMatch(escapedMarkup, /<script>/u);
assert.match(escapedMarkup, /&lt;script&gt;alert\(1\)&lt;\/script&gt;/u);

let capturedSetLocale: ((locale: unknown) => boolean) | null = null;
function LocaleSetterProbe() {
  capturedSetLocale = useI18n().setLocale;
  return null;
}
renderToStaticMarkup(
  <I18nProvider initialLocale="en-US" preferenceStore={preference}>
    <LocaleSetterProbe />
  </I18nProvider>
);
assert.equal(capturedSetLocale?.("fr-FR"), false);
assert.equal(preference.read(), null);

console.log(
  "PASS control-web i18n: locale resolution, fallback, messages, Intl, storage, metadata, selector, and React escaping"
);
